package onboarding

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"time"
)

type nativeSandboxLaunchTransport struct{}

func nativeSandboxLauncherReady(ctx context.Context) error {
	if ctx.Err() != nil || os.Geteuid() != 0 || os.Getegid() != 0 {
		return ErrSandboxProbeInvalid
	}
	owner, err := (nativeSetupHost{}).Owner()
	if err != nil || owner.UID != 0 || owner.GID != 0 {
		return ErrSandboxProbeInvalid
	}
	// Never attempt unit launch in a non-booted build container or remote manager.
	pid1, err := os.ReadFile("/proc/1/comm")
	if err != nil || strings.TrimSpace(string(pid1)) != "systemd" {
		return ErrSandboxProbeInvalid
	}
	info, err := os.Lstat("/run/systemd/system")
	if err != nil || !info.IsDir() {
		return ErrSandboxProbeInvalid
	}
	if _, err := serviceActivationAccount("hlcp"); err != nil {
		return ErrSandboxProbeInvalid
	}
	for _, path := range []string{"/usr/bin/systemctl", "/usr/bin/systemd-run", "/usr/bin/hlcp-agent"} {
		if _, err := activationFile(path, 64<<20, true); err != nil {
			return ErrSandboxProbeInvalid
		}
	}
	return nil
}
func sandboxProcessEnvironment() []string {
	return []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "SYSTEMD_PAGER=cat", "SYSTEMD_COLORS=0"}
}
func (nativeSandboxLaunchTransport) Unit(parent context.Context) ([]byte, error) {
	if nativeSandboxLauncherReady(parent) != nil {
		return nil, ErrSandboxProbeInvalid
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/systemctl", "--system", "--no-pager", "--no-ask-password", "--all", "show", sandboxProbeUnit, "--property="+sandboxLaunchProperties)
	command.Env = sandboxProcessEnvironment()
	command.Dir = "/"
	command.WaitDelay = 250 * time.Millisecond
	out, stderr := &activationOutput{}, &activationOutput{}
	command.Stdout = out
	command.Stderr = stderr
	// systemctl show may return nonzero for a missing unit while still emitting
	// the complete not-found property set. No other exit/output is adopted.
	err := command.Run()
	if ctx.Err() != nil || out.overflow || stderr.overflow {
		return nil, ErrSandboxProbeInvalid
	}
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return nil, ErrSandboxProbeInvalid
		}
		parsed, e := sandboxUnitProperties(out.data)
		if e != nil || !sandboxUnitAbsent(parsed) {
			return nil, ErrSandboxProbeInvalid
		}
	}
	return out.data, nil
}
func (nativeSandboxLaunchTransport) Begin(ctx context.Context) (sandboxLaunchProcess, error) {
	if nativeSandboxLauncherReady(ctx) != nil {
		return nil, ErrSandboxProbeInvalid
	}
	return startSandboxClient(ctx, "/usr/bin/systemd-run", sandboxLaunchArgs())
}

type sandboxOutput struct {
	limit    int
	data     []byte
	overflow bool
}

func (w *sandboxOutput) Write(data []byte) (int, error) {
	n := len(data)
	available := w.limit - len(w.data)
	if len(data) > available {
		w.overflow = true
		data = data[:available]
	}
	w.data = append(w.data, data...)
	return n, nil
}

type nativeSandboxLaunchProcess struct {
	command      *exec.Cmd
	cancel       context.CancelFunc
	input        *io.PipeWriter
	done         chan error
	out, stderr  *sandboxOutput
	mutex        sync.Mutex
	used, reaped bool
}

func startSandboxClient(parent context.Context, path string, args []string) (sandboxLaunchProcess, error) {
	if path != "/usr/bin/systemd-run" || !reflect.DeepEqual(args, sandboxLaunchArgs()) || parent.Err() != nil {
		return nil, ErrSandboxProbeInvalid
	}
	return spawnSandboxClient(parent, path, args)
}

// Private process mechanics: the production caller above has already enforced
// fixed path/argv. Test subprocesses exercise pipes/timeouts without systemd.
func spawnSandboxClient(parent context.Context, path string, args []string) (sandboxLaunchProcess, error) {
	ctx, cancel := context.WithCancel(parent)
	reader, writer := io.Pipe()
	command := exec.CommandContext(ctx, path, args...)
	command.Env = sandboxProcessEnvironment()
	command.Dir = "/"
	command.WaitDelay = 250 * time.Millisecond
	command.Stdin = reader
	out, stderr := &sandboxOutput{limit: inspectorOutputLimit}, &sandboxOutput{limit: 4096}
	command.Stdout = out
	command.Stderr = stderr
	if command.Start() != nil {
		cancel()
		reader.Close()
		writer.Close()
		return nil, ErrSandboxProbeIncomplete
	}
	process := &nativeSandboxLaunchProcess{command: command, cancel: cancel, input: writer, done: make(chan error, 1), out: out, stderr: stderr}
	go func() { err := command.Wait(); reader.Close(); process.done <- err }()
	return process, nil
}
func (p *nativeSandboxLaunchProcess) Exchange(ctx context.Context, data []byte) ([]byte, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.used || p.reaped || len(data) > inspectorInputLimit || ctx.Err() != nil {
		return nil, ErrSandboxProbeIncomplete
	}
	p.used = true
	// Write can block if the unit never consumes stdin; cancellation also closes
	// our pipe and kills/reaps the client. Server-side RuntimeMaxSec limits the unit.
	writing := make(chan error, 1)
	go func() { _, err := p.input.Write(data); p.input.Close(); writing <- err }()
	select {
	case err := <-writing:
		if err != nil {
			return nil, ErrSandboxProbeIncomplete
		}
	case <-ctx.Done():
		p.cancel()
		p.input.CloseWithError(ctx.Err())
		return nil, ErrSandboxProbeIncomplete
	}
	select {
	case err := <-p.done:
		p.reaped = true
		if err != nil || p.out.overflow || p.stderr.overflow || ctx.Err() != nil {
			return nil, ErrSandboxProbeIncomplete
		}
		return append([]byte{}, p.out.data...), nil
	case <-ctx.Done():
		p.cancel()
		p.input.CloseWithError(ctx.Err())
		return nil, ErrSandboxProbeIncomplete
	}
}
func (p *nativeSandboxLaunchProcess) Close() {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.input.Close()
	p.cancel()
	if !p.reaped {
		<-p.done
		p.reaped = true
	}
}
