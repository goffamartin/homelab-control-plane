package managedcli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"hlcp/internal/managed"
	"io"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var ErrSetup = errors.New("managed setup operation did not complete; inspect actual state before retrying")

func validAgentProfile(scope, unit string) bool {
	return (scope == "user" && (unit == "hlcp-agent.service" || unit == "hlcp-agent-podman.service")) || (scope == "system" && unit == "hlcp-agent.service")
}

type terminal struct {
	reader      *bufio.Reader
	input       io.Reader
	out         io.Writer
	interactive bool
}

func newTerminal(input io.Reader, out io.Writer) *terminal {
	return &terminal{reader: bufio.NewReader(input), input: input, out: out, interactive: isTerminal(input)}
}
func (t *terminal) lineContext(ctx context.Context, prompt string) (string, error) {
	if _, err := fmt.Fprint(t.out, prompt); err != nil {
		return "", err
	}
	type result struct {
		data string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		chunk, err := t.reader.ReadSlice('\n')
		done <- result{string(chunk), err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case value := <-done:
		if len(value.data) == 0 || len(value.data) > 4096 {
			return "", ErrSetup
		}
		if value.err != nil && !errors.Is(value.err, io.EOF) {
			return "", value.err
		}
		return strings.TrimSpace(value.data), nil
	}
}
func (t *terminal) secret(ctx context.Context, prompt string) (string, error) {
	if _, err := fmt.Fprint(t.out, prompt); err != nil {
		return "", err
	}
	restore, err := hideEcho(t.input)
	if err != nil {
		return "", err
	}
	defer restore()
	type result struct {
		value string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		chunk, err := t.reader.ReadSlice('\n')
		data := string(chunk)
		if len(data) > 4096 {
			err = ErrSetup
		}
		done <- result{strings.TrimSuffix(strings.TrimSuffix(data, "\n"), "\r"), err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case value := <-done:
		fmt.Fprintln(t.out)
		if value.err != nil && !errors.Is(value.err, io.EOF) {
			return "", value.err
		}
		if value.value == "" {
			return "", ErrSetup
		}
		return value.value, nil
	}
}
func caller() (*user.User, int, int, error) {
	uid, gid := os.Geteuid(), os.Getegid()
	u, err := user.LookupId(strconv.Itoa(uid))
	if err != nil || u.Uid != strconv.Itoa(uid) || u.Gid != strconv.Itoa(gid) {
		return nil, 0, 0, ErrSetup
	}
	return u, uid, gid, nil
}
func account(name string) (int, int, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, 0, ErrSetup
	}
	uid, e1 := strconv.Atoi(u.Uid)
	gid, e2 := strconv.Atoi(u.Gid)
	if e1 != nil || e2 != nil || uid <= 0 || gid <= 0 || u.HomeDir != "/nonexistent" {
		return 0, 0, ErrSetup
	}
	group, err := user.LookupGroupId(u.Gid)
	if err != nil || group.Name != name {
		return 0, 0, ErrSetup
	}
	groups, err := u.GroupIds()
	if err != nil || len(groups) != 1 || groups[0] != u.Gid {
		return 0, 0, ErrSetup
	}
	if !serviceAccountLocked(name, u.Uid, u.Gid) {
		return 0, 0, ErrSetup
	}
	return uid, gid, nil
}
func ensurePrivateDirectory(path string, uid, gid int) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(filepath.Separator) {
		return ErrSetup
	}
	if info, err := os.Lstat(path); err == nil {
		return checkDirectory(path, info, uid, gid)
	} else if !os.IsNotExist(err) {
		return ErrSetup
	}
	parent := filepath.Dir(path)
	if _, err := os.Lstat(parent); os.IsNotExist(err) {
		if err := ensurePrivateDirectory(parent, uid, gid); err != nil {
			return err
		}
	}
	if err := safeParent(parent, uid); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0700); err != nil {
		return ErrSetup
	}
	if os.Geteuid() == 0 && (uid != 0 || gid != 0) {
		if os.Chown(path, uid, gid) != nil {
			return ErrSetup
		}
	}
	return nil
}
func boundedContext() (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	bounded, cancel := context.WithTimeout(ctx, 2*time.Minute)
	return bounded, func() { cancel(); stop() }
}

func has(args []string, name string) bool {
	for _, arg := range args {
		if arg == name || strings.HasPrefix(arg, name+"=") {
			return true
		}
	}
	return false
}
func ManagedSetup(args []string) bool {
	if has(args, "--imported") {
		return false
	}
	return !has(args, "--cert") && !has(args, "--key") && !has(args, "--peer-ca")
}
func WithoutImported(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if arg != "--imported" {
			out = append(out, arg)
		}
	}
	return out
}

func WritePrivateFile(ctx context.Context, path string, data []byte) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrSetup
	}
	store, err := managed.OpenStore(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer store.Close()
	return store.Create(ctx, filepath.Base(path), data)
}
