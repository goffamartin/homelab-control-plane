package onboarding

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func launcherProperties(ready bool) []byte {
	values := map[string]string{}
	for _, key := range strings.Split(sandboxLaunchProperties, ",") {
		values[key] = ""
	}
	values["LoadState"] = "not-found"
	values["ActiveState"] = "inactive"
	values["Transient"] = "no"
	values["MainPID"] = "0"
	if ready {
		values["TimeoutStartUSec"] = "3s"
		for key, value := range map[string]string{"LoadState": "loaded", "ActiveState": "active", "Transient": "yes", "FragmentPath": "/run/systemd/transient/" + sandboxProbeUnit, "MainPID": "123", "Type": "exec", "User": "hlcp", "Group": "hlcp", "NoNewPrivileges": "yes", "PrivateTmp": "yes", "ProtectHome": "yes", "ProtectSystem": "strict", "StateDirectory": "hlcp", "StateDirectoryMode": "0700", "UMask": "0077", "RestrictAddressFamilies": "AF_UNIX AF_INET AF_INET6", "SystemCallArchitectures": "native", "Restart": "no", "RuntimeMaxUSec": "15s", "TimeoutStopUSec": "2s", "KillMode": "control-group", "SendSIGKILL": "yes", "CollectMode": "inactive-or-failed"} {
			values[key] = value
		}
	}
	var out strings.Builder
	for _, key := range strings.Split(sandboxLaunchProperties, ",") {
		out.WriteString(key + "=" + values[key] + "\n")
	}
	return []byte(out.String())
}

type launchFake struct {
	calls, begins, exchanges, closes int
	fault                            string
	cancel                           context.CancelFunc
}

func (h *launchFake) Unit(context.Context) ([]byte, error) {
	h.calls++
	if h.fault == "inspect" || (h.fault == "cleanup-inspect" && h.calls > 2) {
		return nil, errors.New("SECRET")
	}
	if h.calls == 1 {
		if h.fault == "conflict" {
			return launcherProperties(true), nil
		}
		return launcherProperties(false), nil
	}
	if h.calls == 2 {
		if h.fault == "cancel-before-input" {
			h.cancel()
		}
		data := launcherProperties(true)
		if h.fault == "sandbox" {
			data = bytes.Replace(data, []byte("ProtectHome=yes"), []byte("ProtectHome=no"), 1)
		}
		if h.fault == "malformed" {
			data = append(data, []byte("SECRET=value\n")...)
		}
		return data, nil
	}
	if h.fault == "cleanup" {
		return launcherProperties(true), nil
	}
	return launcherProperties(false), nil
}
func (h *launchFake) Begin(context.Context) (sandboxLaunchProcess, error) {
	h.begins++
	if h.fault == "start" {
		return nil, errors.New("SECRET")
	}
	return h, nil
}
func (h *launchFake) Exchange(ctx context.Context, input []byte) ([]byte, error) {
	h.exchanges++
	if _, ok := ctx.Deadline(); !ok {
		panic("unbounded launch")
	}
	if h.fault == "exit" {
		return nil, errors.New("SECRET")
	}
	var out bytes.Buffer
	if runSandboxInspector(ctx, nil, bytes.NewReader(input), &out, &inspectorFake{}) != 0 {
		return nil, errors.New("SECRET")
	}
	if h.fault == "response" {
		return []byte("SECRET"), nil
	}
	if h.fault == "cancel-after-exit" {
		h.cancel()
	}
	return out.Bytes(), nil
}
func (h *launchFake) Close() { h.closes++ }

func TestSandboxLauncherReadinessBeforeInputAndVerifiedCollection(t *testing.T) {
	_, snapshot, inputs := inspectorFixture(t)
	h := &launchFake{}
	evidence, err := launchSandboxInspector(context.Background(), h, snapshot, inputs)
	if err != nil || !evidence.InspectorExited || !evidence.UnitCollected || h.begins != 1 || h.exchanges != 1 || h.closes != 1 || h.calls != 3 {
		t.Fatal(evidence, err, h)
	}
}
func TestSandboxLauncherRefusalsNeverLeakOrRetry(t *testing.T) {
	for _, fault := range []string{"inspect", "conflict", "start", "sandbox", "malformed", "exit", "response", "cleanup-inspect", "cleanup", "cancel-before-input", "cancel-after-exit"} {
		t.Run(fault, func(t *testing.T) {
			_, snapshot, inputs := inspectorFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h := &launchFake{fault: fault, cancel: cancel}
			evidence, err := launchSandboxInspector(ctx, h, snapshot, inputs)
			if err == nil || evidence.UnitCollected || strings.Contains(err.Error(), "SECRET") || h.begins > 1 || h.exchanges > 1 {
				t.Fatal(fault, evidence, err, h)
			}
			if (fault == "inspect" || fault == "conflict") && h.begins != 0 {
				t.Fatal("preexisting unit was touched")
			}
			if (fault == "sandbox" || fault == "malformed" || fault == "cancel-before-input") && h.exchanges != 0 {
				t.Fatal("input before sandbox validation")
			}
			if h.begins == 1 && fault != "start" && h.closes != 1 {
				t.Fatal("client not closed")
			}
		})
	}
}
func TestSandboxLauncherFixedArgumentsAndPropertyRefusals(t *testing.T) {
	args := sandboxLaunchArgs()
	joined := strings.Join(args, "|")
	for _, required := range []string{"--collect", "--wait", "--pipe", "--property=RuntimeMaxSec=15s", "--property=TimeoutStopSec=2s", "--property=Restart=no", "/usr/bin/hlcp-agent", "--internal-sandbox-inspector"} {
		if !strings.Contains(joined, required) {
			t.Fatal(required)
		}
	}
	for _, forbidden := range []string{"--user", "--shell", "--scope", "--ignore-failure", "--remain-after-exit", "--config", "SupplementaryGroups=docker"} {
		if strings.Contains(joined, forbidden) {
			t.Fatal("unexpected execution option", forbidden)
		}
	}
	args[0] = "SECRET"
	if sandboxLaunchArgs()[0] == "SECRET" {
		t.Fatal("mutable command template")
	}
	base := launcherProperties(true)
	p, err := sandboxUnitProperties(base)
	if err != nil || !sandboxUnitReady(p) {
		t.Fatal(err)
	}
	for key := range p {
		q := map[string]string{}
		for k, v := range p {
			q[k] = v
		}
		q[key] = "UNSUPPORTED"
		if sandboxUnitReady(q) {
			t.Fatal("unchecked unit property", key)
		}
	}
	if _, err := sandboxUnitProperties(append(base, []byte("User=hlcp\n")...)); err == nil {
		t.Fatal("duplicate properties accepted")
	}
	for _, path := range []string{"/run/credentials/agent.key", "/var/lib/hlcp/command-journal.json", "/var/lib/hlcp/command-journal.json.lock"} {
		if validInspectorPath(path) {
			t.Fatal("reserved file accepted", path)
		}
	}
}

func TestSandboxProbeReservedSourcePathsAreNotForwarded(t *testing.T) {
	c := probeConfig()
	for _, path := range []string{c.CertificateFile, c.PrivateKeyFile, c.CAFile, c.JournalFile, c.JournalFile + ".lock", "/run/credentials/agent.key", "/var/lib/hlcp/command-journal.json"} {
		changed := probeConfig()
		command := changed.Resources[0].Commands["status"]
		command.Executable = path
		changed.Resources[0].Commands["status"] = command
		if _, err := sandboxProbeInputs(changed); !errors.Is(err, ErrSandboxProbeUnsupported) {
			t.Fatal("reserved source forwarded", err)
		}
	}
}
