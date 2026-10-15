package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hlcp/internal/protocol"
)

type mixedCommandRunner struct{ calls []string }

func (r *mixedCommandRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	r.calls = append(r.calls, fmt.Sprint(name, args))
	if args[0] == "container" {
		return "running", nil
	}
	if args[0] == "show" {
		return "LoadState=loaded\nActiveState=active\n", nil
	}
	return "native action", nil
}

type mixedE2ERunner struct{ nativeMutations atomic.Int32 }

func (r *mixedE2ERunner) Run(_ context.Context, _ string, args ...string) (string, error) {
	switch args[0] {
	case "container":
		return "running", nil
	case "show":
		return "LoadState=loaded\nActiveState=active\n", nil
	case "restart":
		r.nativeMutations.Add(1)
		return "native restart", nil
	default:
		return "", fmt.Errorf("unexpected native invocation")
	}
}

func TestMixedResourceCommandsThroughMTLSController(t *testing.T) {
	for _, name := range []string{"docker", "podman", "systemd"} {
		t.Run(name, func(t *testing.T) {
			serverFiles, clientFiles := e2ePKI(t)
			dir := t.TempDir()
			marker := filepath.Join(dir, "effects")
			filename := filepath.Join(dir, "backup.sh")
			if err := os.WriteFile(filename, []byte("printf x >> \"$1\"\nprintf backup-completed\n"), 0700); err != nil {
				t.Fatal(err)
			}
			fixture := startE2EController(t, serverFiles, filepath.Join(dir, "ledger.json"))
			c := mixedConfig(t, name)
			c.ControllerAddress = fixture.server.Address().String()
			c.ServerName = clientFiles.ServerName
			c.CertificateFile = clientFiles.CertificateFile
			c.PrivateKeyFile = clientFiles.PrivateKeyFile
			c.CAFile = clientFiles.CAFile
			c.JournalFile = filepath.Join(dir, "journal.json")
			c.ObservationInterval = 5 * time.Minute
			r := &c.Resources[0]
			r.Actions = append(r.Actions, "backup")
			r.Commands = map[string]ScriptCommandConfig{"backup": {Executable: "/bin/sh", Argv: []string{filename, marker}, WorkingDirectory: dir}}
			runner := new(mixedE2ERunner)
			c.runner = runner
			if err := c.Validate(); err != nil {
				t.Fatal(err)
			}
			stop := startE2EAgent(t, c)
			e2eEventually(t, "mixed node online", func() bool {
				record, ok := fixture.server.Registry().Get(c.NodeID)
				return ok && record.State == "online" && len(record.Inventory.Resources) == 1
			})
			record, _ := fixture.server.Registry().Get(c.NodeID)
			inventory, _ := json.Marshal(record.Inventory)
			if record.Inventory.Resources[0].Type != r.Type || !strings.Contains(string(inventory), "backup") || strings.Contains(string(inventory), "argv") || strings.Contains(string(inventory), filename) {
				t.Fatal("inventory contract changed", string(inventory))
			}
			payload := protocol.CommandPayload{CommandID: "mixed-backup", ResourceID: r.ID, Action: "backup"}
			preview := scriptRequest(t, fixture.api.URL, "resolve", payload)
			if preview.status != 200 || !strings.Contains(preview.body, `"read_only":false`) {
				t.Fatal(preview)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("observation/preview executed custom command")
			}
			result := scriptRequest(t, fixture.api.URL, "commands", payload)
			if result.status != 200 || !result.result.Success || result.result.Message != "backup-completed" {
				t.Fatal(result)
			}
			duplicate := scriptRequest(t, fixture.api.URL, "commands", payload)
			if duplicate.result != result.result || duplicate.status != 200 {
				t.Fatal(duplicate)
			}
			native := scriptRequest(t, fixture.api.URL, "commands", protocol.CommandPayload{CommandID: "mixed-native-restart", ResourceID: r.ID, Action: "restart"})
			if native.status != 200 || !native.result.Success || runner.nativeMutations.Load() != 1 {
				t.Fatal("native restart routing", native)
			}
			oldSession := record.SessionID
			stop()
			stop = startE2EAgent(t, c)
			defer stop()
			e2eEventually(t, "mixed node reconnected", func() bool {
				record, ok := fixture.server.Registry().Get(c.NodeID)
				return ok && record.State == "online" && record.SessionID != oldSession
			})
			duplicate = scriptRequest(t, fixture.api.URL, "commands", payload)
			if duplicate.status != 200 || duplicate.result != result.result {
				t.Fatal("durable duplicate", duplicate)
			}
			data, _ := os.ReadFile(marker)
			if string(data) != "x" {
				t.Fatal("custom action replayed", string(data))
			}
		})
	}
}

func TestMixedResourceDispatchNativeCustomDedupAndNoPolling(t *testing.T) {
	for _, name := range []string{"docker", "podman", "systemd"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "effects")
			script := filepath.Join(dir, "backup.sh")
			if err := os.WriteFile(script, []byte("printf x >> \"$1\"\nprintf '%s' \"$OUTPUT\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			spec := func() ScriptCommandConfig {
				return ScriptCommandConfig{Executable: "/bin/sh", Argv: []string{script, marker}, WorkingDirectory: dir, Environment: map[string]string{"OUTPUT": "custom action"}}
			}
			c := mixedConfig(t, name)
			c.JournalFile = filepath.Join(dir, "journal.json")
			c.refresh = make(chan struct{}, 1)
			r := &c.Resources[0]
			r.Actions = append(r.Actions, "backup")
			r.Commands = map[string]ScriptCommandConfig{"backup": spec()}
			runner := new(mixedCommandRunner)
			c.runner = runner
			for i := 0; i < 2; i++ {
				if state := c.observe(context.Background(), *r); state.State != "running" {
					t.Fatal(state)
				}
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("custom action polled")
			}
			d, err := c.NewDispatcher()
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			for _, action := range []string{"status", "restart"} {
				ack, result := d.Execute(protocol.CommandPayload{CommandID: "native-" + action, ResourceID: r.ID, Action: action})
				if ack.Status != "accepted" || !result.Success {
					t.Fatal(ack, result)
				}
			}
			if len(runner.calls) != 4 {
				t.Fatal("native routing", runner.calls)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("native action invoked script")
			}
			// Dispatcher owns a defensive copy of fixed argv/environment/command map.
			command := r.Commands["backup"]
			command.Argv[1] = filepath.Join(dir, "wrong")
			command.Environment["OUTPUT"] = "mutated"
			command.Executable = "/missing"
			r.Commands["backup"] = command
			for _, action := range []string{"unknown", "backup;touch"} {
				ack, _ := d.Execute(protocol.CommandPayload{CommandID: "bad-" + action, ResourceID: r.ID, Action: action})
				if ack.Status != "rejected" {
					t.Fatal("non-allowlisted action executed", ack)
				}
			}
			payload := protocol.CommandPayload{CommandID: "custom-once", ResourceID: r.ID, Action: "backup"}
			ack, result := d.Execute(payload)
			if ack.Status != "accepted" || !result.Success || result.Message != "custom action" {
				t.Fatal(ack, result)
			}
			if len(runner.calls) != 4 {
				t.Fatal("custom command sent to native runner", runner.calls)
			}
			ack, duplicate := d.Execute(payload)
			if ack.Status != "completed" || duplicate != result {
				t.Fatal(ack, duplicate)
			}
			if err := d.Close(); err != nil {
				t.Fatal(err)
			}
			r.Commands = map[string]ScriptCommandConfig{"backup": spec()}
			restarted, err := c.NewDispatcher()
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			ack, duplicate = restarted.Execute(payload)
			if ack.Status != "completed" || duplicate != result {
				t.Fatal(ack, duplicate)
			}
			data, err := os.ReadFile(marker)
			if err != nil || string(data) != "x" {
				t.Fatal("custom action repeated or did not execute", string(data), err)
			}
			if _, err := os.Stat(filepath.Join(dir, "wrong")); !os.IsNotExist(err) {
				t.Fatal("mutable argv affected execution")
			}
		})
	}
}

func TestMixedResourceTimeoutIsMutatingAndNeverAutomaticallyRetried(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "effects")
	script := filepath.Join(dir, "slow.sh")
	if err := os.WriteFile(script, []byte("printf x >> \"$1\"\nwhile :;do sleep 0.05;done\n"), 0700); err != nil {
		t.Fatal(err)
	}
	c := mixedConfig(t, "podman")
	c.JournalFile = filepath.Join(dir, "journal.json")
	r := &c.Resources[0]
	r.Actions = []string{"backup"}
	r.Commands = map[string]ScriptCommandConfig{"backup": {Executable: "/bin/sh", Argv: []string{script, marker}, WorkingDirectory: dir, Timeout: 150 * time.Millisecond}}
	d, err := c.NewDispatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	payload := protocol.CommandPayload{CommandID: "timeout-once", ResourceID: r.ID, Action: "backup"}
	ack, result := d.Execute(payload)
	if ack.Status != "accepted" || result.Code != "EXECUTION_UNKNOWN" {
		t.Fatal(ack, result)
	}
	ack, duplicate := d.Execute(payload)
	if ack.Status != "completed" || duplicate != result {
		t.Fatal(ack, duplicate)
	}
	d.Close()
	d, err = c.NewDispatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ack, duplicate = d.Execute(payload)
	if ack.Status != "completed" || duplicate != result {
		t.Fatal(ack, duplicate)
	}
	data, _ := os.ReadFile(marker)
	if strings.TrimSpace(string(data)) != "x" {
		t.Fatal("timed out action repeated", string(data))
	}
}
