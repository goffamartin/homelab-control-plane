//go:build linux

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.yaml.in/yaml/v4"
	"hlcp/internal/command"
	"hlcp/internal/protocol"
	"hlcp/internal/telemetry"
)

type scriptHTTPResult struct {
	status int
	result protocol.CommandResultPayload
	body   string
}

func scriptRequest(t *testing.T, api, route string, payload protocol.CommandPayload) scriptHTTPResult {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "POST", api+"/v1/nodes/debian-01/"+route, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+e2eToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Error(err)
		return scriptHTTPResult{}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Error(err)
	}
	result := scriptHTTPResult{status: response.StatusCode, body: string(body)}
	_ = json.Unmarshal(body, &result.result)
	return result
}

func TestScriptMTLSRecoveryAndNonblockingFlood(t *testing.T) {
	serverFiles, clientFiles := e2ePKI(t)
	directory := t.TempDir()
	marker := filepath.Join(directory, "effects")
	release := filepath.Join(directory, "release")
	filename := filepath.Join(directory, "diagnose.sh")
	body := `printf x >> "$1"
while [ ! -f "$2" ]; do
  for ((i=0;i<100;i++)); do printf 'output-must-not-enter-audit'; done
  sleep 0.01
done
printf done
`
	if err := os.WriteFile(filename, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(directory, "ledger.json")
	diagnostics := telemetry.New(nil)
	defer diagnostics.Close(context.Background())
	fixture := startE2EController(t, serverFiles, ledger, diagnostics)
	runner := &noRuntimeRunner{}
	config := Config{ControllerAddress: fixture.server.Address().String(), ServerName: clientFiles.ServerName,
		CertificateFile: clientFiles.CertificateFile, PrivateKeyFile: clientFiles.PrivateKeyFile, CAFile: clientFiles.CAFile,
		NodeID: "debian-01", AgentVersion: "script-test", Capabilities: []string{"resource.control", "script"},
		JournalFile: filepath.Join(directory, "journal.json"), ObservationInterval: 5 * time.Minute, runner: runner, Telemetry: diagnostics,
		Resources: []ResourceConfig{{ID: "diagnostics", Type: "task", Adapter: "script", Actions: []string{"diagnose", "status"}, Commands: map[string]ScriptCommandConfig{
			"diagnose": {Executable: "/bin/bash", Argv: []string{filename, marker, release}, WorkingDirectory: directory, Timeout: 10 * time.Second},
			"status":   {Executable: "/bin/bash", Argv: []string{filename, marker, release}, WorkingDirectory: directory, Timeout: 10 * time.Second},
		}}}}
	stopAgent := startE2EAgent(t, config)
	e2eEventually(t, "script node online", func() bool {
		node, ok := fixture.server.Registry().Get(config.NodeID)
		return ok && node.State == "online"
	})
	payload := protocol.CommandPayload{CommandID: "script-1", ResourceID: "diagnostics", Action: "diagnose"}
	before, _ := os.ReadFile(config.JournalFile)
	resolved := scriptRequest(t, fixture.api.URL, "resolve", payload)
	if resolved.status != 200 || !strings.Contains(resolved.body, `"read_only":false`) {
		t.Fatalf("resolve=%+v", resolved)
	}
	after, _ := os.ReadFile(config.JournalFile)
	if !bytes.Equal(before, after) {
		t.Fatal("dry-run changed script journal")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("dry-run executed script")
	}
	completed := make(chan scriptHTTPResult, 1)
	go func() { completed <- scriptRequest(t, fixture.api.URL, "commands", payload) }()
	e2eEventually(t, "script side effect", func() bool { data, _ := os.ReadFile(marker); return string(data) == "x" })
	e2eEventually(t, "script ACK", func() bool {
		progress, ok := e2eOperation(fixture.api.URL, payload.CommandID)
		return ok && progress.AckStatus == "accepted" && progress.Result == nil
	})
	// After ACK there are no further results/observations (interval 5m).
	// The next correlated activity must therefore be a heartbeat during flood.
	initial, _ := fixture.server.Registry().Get(config.NodeID)
	e2eEventually(t, "heartbeat during script flood", func() bool {
		node, ok := fixture.server.Registry().Get(config.NodeID)
		return ok && node.State == "online" && node.SessionID == initial.SessionID && node.LastActivity.After(initial.LastActivity)
	})
	node, _ := fixture.server.Registry().Get(config.NodeID)
	if len(node.Resources.Resources) != 1 || node.Resources.Resources[0].State != "unknown" || node.Resources.Resources[0].Code != "NOT_OBSERVED" || runner.calls.Load() != 0 {
		t.Fatalf("script observation=%+v runtime calls=%d", node.Resources, runner.calls.Load())
	}
	fixture.stop(t) // Lose the result after ACK, not the execution.
	if err := os.WriteFile(release, []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	e2eEventually(t, "offline durable script result", func() bool {
		data, _ := os.ReadFile(config.JournalFile)
		var document struct {
			Entries []command.Entry `json:"entries"`
		}
		_ = json.Unmarshal(data, &document)
		return len(document.Entries) == 1 && document.Entries[0].Result != nil && document.Entries[0].Result.Success
	})
	stopAgent()
	select {
	case response := <-completed:
		if response.status == 200 {
			t.Fatalf("lost result falsely succeeded: %+v", response)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("request did not stop")
	}
	fixture = startE2EController(t, serverFiles, ledger)
	config.ControllerAddress = fixture.server.Address().String()
	config.Telemetry = nil
	startE2EAgent(t, config)
	e2eEventually(t, "script result reconciled after both restarts", func() bool {
		progress, ok := e2eOperation(fixture.api.URL, payload.CommandID)
		return ok && progress.Result != nil && progress.Result.Success && progress.Attempts == 1
	})
	for i := 0; i < 3; i++ {
		response := scriptRequest(t, fixture.api.URL, "commands", payload)
		if response.status != 200 || !response.result.Success || !strings.Contains(response.result.Message, "[output truncated]") {
			t.Fatalf("duplicate=%+v", response)
		}
	}
	payload.Action = "status"
	if response := scriptRequest(t, fixture.api.URL, "commands", payload); response.status != 409 {
		t.Fatalf("conflicting payload=%+v", response)
	}
	data, _ := os.ReadFile(marker)
	if string(data) != "x" {
		t.Fatalf("script replayed: %q", data)
	}
	for _, event := range diagnostics.Events() {
		encoded, _ := json.Marshal(event)
		if bytes.Contains(encoded, []byte("output-must-not-enter-audit")) {
			t.Fatal("script output leaked into audit")
		}
	}
}

func TestScriptShutdownAndCrashNeverReplay(t *testing.T) {
	if os.Getenv("HLCP_SCRIPT_AGENT_HELPER") == "1" {
		config, err := Load(os.Getenv("HLCP_SCRIPT_AGENT_CONFIG"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := Run(context.Background(), config); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	for _, crash := range []bool{false, true} {
		t.Run(fmt.Sprintf("crash=%v", crash), func(t *testing.T) {
			serverFiles, clientFiles := e2ePKI(t)
			directory := t.TempDir()
			marker := filepath.Join(directory, "effects")
			pidFile := filepath.Join(directory, "pid")
			filename := filepath.Join(directory, "status.sh")
			if err := os.WriteFile(filename, []byte("printf x >> \"$1\"\necho $$ > \"$2\"\ntrap '' TERM\nwhile :; do sleep 0.05; done\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			fixture := startE2EController(t, serverFiles, filepath.Join(directory, "ledger.json"))
			config := Config{ControllerAddress: fixture.server.Address().String(), ServerName: clientFiles.ServerName, CertificateFile: clientFiles.CertificateFile, PrivateKeyFile: clientFiles.PrivateKeyFile, CAFile: clientFiles.CAFile,
				NodeID: "debian-01", AgentVersion: "script-recovery", Capabilities: []string{"resource.control", "script"}, JournalFile: filepath.Join(directory, "journal.json"),
				Resources: []ResourceConfig{{ID: "diagnostics", Type: "task", Adapter: "script", Actions: []string{"status"}, Commands: map[string]ScriptCommandConfig{"status": {Executable: "/bin/bash", Argv: []string{filename, marker, pidFile}, WorkingDirectory: directory, Timeout: 20 * time.Second}}}}}
			var stopAgent func()
			if crash {
				path := filepath.Join(directory, "agent.yaml")
				data, err := yaml.Marshal(config)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
				process := exec.Command(os.Args[0], "-test.run=^TestScriptShutdownAndCrashNeverReplay$")
				process.Env = append(os.Environ(), "HLCP_SCRIPT_AGENT_HELPER=1", "HLCP_SCRIPT_AGENT_CONFIG="+path)
				if err := process.Start(); err != nil {
					t.Fatal(err)
				}
				stopped := false
				stopAgent = func() {
					if !stopped {
						stopped = true
						_ = process.Process.Kill()
						_ = process.Wait()
					}
				}
				t.Cleanup(stopAgent)
				// A hard agent crash is not a containment mechanism. systemd's cgroup
				// normally owns cleanup; this bare subprocess fixture cleans explicitly.
				t.Cleanup(func() {
					data, _ := os.ReadFile(pidFile)
					pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
					if pid > 0 {
						_ = syscall.Kill(-pid, syscall.SIGKILL)
					}
				})
			} else {
				stopAgent = startE2EAgent(t, config)
			}
			e2eEventually(t, "script recovery node online", func() bool {
				node, ok := fixture.server.Registry().Get(config.NodeID)
				return ok && node.State == "online"
			})
			payload := protocol.CommandPayload{CommandID: "status-1", ResourceID: "diagnostics", Action: "status"}
			completed := make(chan scriptHTTPResult, 1)
			go func() { completed <- scriptRequest(t, fixture.api.URL, "commands", payload) }()
			e2eEventually(t, "script recovery side effect", func() bool { data, _ := os.ReadFile(marker); return string(data) == "x" })
			e2eEventually(t, "script recovery ACK", func() bool {
				progress, ok := e2eOperation(fixture.api.URL, payload.CommandID)
				return ok && progress.AckStatus == "accepted"
			})
			start := time.Now()
			stopAgent()
			if time.Since(start) > 2*time.Second {
				t.Fatal("script shutdown was not bounded")
			}
			startE2EAgent(t, config)
			e2eEventually(t, "interrupted script reconciled as unknown", func() bool {
				progress, ok := e2eOperation(fixture.api.URL, payload.CommandID)
				return ok && progress.Result != nil && progress.Result.Code == "EXECUTION_UNKNOWN"
			})
			select {
			case <-completed:
			case <-time.After(6 * time.Second):
				t.Fatal("interrupted request did not stop")
			}
			response := scriptRequest(t, fixture.api.URL, "commands", payload)
			if response.status != 200 || response.result.Code != "EXECUTION_UNKNOWN" {
				t.Fatalf("unknown duplicate=%+v", response)
			}
			data, _ := os.ReadFile(marker)
			if string(data) != "x" {
				t.Fatalf("interrupted script replayed: %q", data)
			}
		})
	}
}

func TestScriptDispatcherTimeoutDedupAndNoPolling(t *testing.T) {
	directory := t.TempDir()
	marker := filepath.Join(directory, "effects")
	filename := filepath.Join(directory, "status.sh")
	if err := os.WriteFile(filename, []byte("printf x >> \"$1\"\ntrap '' TERM\nwhile :; do sleep 0.05; done\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := Config{NodeID: "script-test", JournalFile: filepath.Join(directory, "journal.json"), Resources: []ResourceConfig{{ID: "diagnostics", Type: "task", Adapter: "script", Actions: []string{"status"}, Commands: map[string]ScriptCommandConfig{
		"status": {Executable: "/bin/bash", Argv: []string{filename, marker}, WorkingDirectory: directory, Timeout: 100 * time.Millisecond},
	}}}}
	for i := 0; i < 3; i++ {
		state := config.observe(context.Background(), config.Resources[0])
		if state.State != "unknown" || state.Code != "NOT_OBSERVED" || !state.ObservedAt.IsZero() {
			t.Fatalf("state=%+v", state)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("status was automatically polled")
	}
	dispatcher, err := config.NewDispatcher()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dispatcher.Close() })
	for _, payload := range []protocol.CommandPayload{
		{CommandID: "unknown", ResourceID: "diagnostics", Action: "remove"},
		{CommandID: "injection", ResourceID: "diagnostics", Action: "status;touch"},
		{CommandID: "injection-id", ResourceID: "diagnostics;touch", Action: "status"},
	} {
		if ack, _ := dispatcher.Execute(payload); ack.Status != "rejected" {
			t.Fatalf("accepted invalid command: %+v", payload)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("unknown/injection-looking action executed")
	}
	payload := protocol.CommandPayload{CommandID: "same-id", ResourceID: "diagnostics", Action: "status"}
	ack, result := dispatcher.Execute(payload)
	if ack.Status != "accepted" || result.Code != "EXECUTION_UNKNOWN" {
		t.Fatalf("result=%+v ack=%+v", result, ack)
	}
	ack, duplicate := dispatcher.Execute(payload)
	if ack.Status != "completed" || duplicate != result {
		t.Fatalf("duplicate=%+v", duplicate)
	}
	conflict := payload
	conflict.Action = "remove"
	if _, result := dispatcher.Execute(conflict); result.Code != "DUPLICATE_CONFLICT" {
		t.Fatalf("conflict=%+v", result)
	}
	if err := dispatcher.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := config.NewDispatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	ack, duplicate = restarted.Execute(payload)
	if ack.Status != "completed" || duplicate != result {
		t.Fatalf("restart=%+v", duplicate)
	}
	data, _ := os.ReadFile(marker)
	if string(data) != "x" {
		t.Fatalf("timeout repeated side effect: %q", data)
	}
}
