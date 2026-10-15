package doctor

import (
	"bytes"
	"errors"
	"hlcp/internal/setup"
	"strings"
	"testing"
)

type localFixture struct {
	*fakeHost
	script       ScriptStatus
	transaction  setup.TransactionStatus
	err          error
	scriptCalls  int
	inspectCalls int
}

func (h *localFixture) Script(string, string) ScriptStatus { h.scriptCalls++; return h.script }
func (h *localFixture) InspectSetup(string) (setup.TransactionStatus, error) {
	h.inspectCalls++
	return h.transaction, h.err
}

func TestTransactionReports(t *testing.T) {
	for _, row := range []struct {
		state, want string
		err         error
	}{
		{"none", "ok", nil}, {"committed", "warn", nil}, {"pending", "fail", nil}, {"discarding", "fail", nil},
		{"SECRET", "fail", nil}, {"", "warn", setup.ErrMissingDirectory}, {"", "skip", setup.ErrUnsupportedPlatform},
		{"invalid", "fail", errors.New("SECRET metadata")},
	} {
		h := &localFixture{transaction: setup.TransactionStatus{State: row.state}, err: row.err}
		report := Report{}
		checkTransaction(h, "/SECRET", func(code, state, message string) { report.Checks = append(report.Checks, Check{code, state, message}) })
		if stateFor(report, "setup.transaction") != row.want || h.inspectCalls != 1 {
			t.Fatal(row)
		}
		for _, check := range report.Checks {
			if strings.Contains(check.Message, "SECRET") {
				t.Fatal("leak")
			}
		}
	}
}

func TestScriptReports(t *testing.T) {
	for _, row := range []struct{ format, want string }{{"interpreter", "ok"}, {"indirect", "warn"}, {"unknown", "fail"}} {
		h := &localFixture{script: ScriptStatus{Executable: true, Directory: true, Format: row.format}}
		r := Report{}
		checkScript(h, "/SECRET", "/SECRET", func(code, state, message string) { r.Checks = append(r.Checks, Check{code, state, message}) })
		if stateFor(r, "script.interpreter") != row.want {
			t.Fatal(row)
		}
	}
}

func TestScriptAgentUsesOnlyInspection(t *testing.T) {
	h := &localFixture{fakeHost: fixture(t, true, testNow.AddDate(1, 0, 0)),
		script:      ScriptStatus{Executable: true, Directory: true, Format: "interpreter"},
		transaction: setup.TransactionStatus{State: "none"}}
	config := strings.ReplaceAll(minimalAgent, "capabilities: []", "capabilities: [resource.control, script]")
	config = strings.ReplaceAll(config, "resources: []", "resources:\n  - id: task\n    type: task\n    adapter: script\n    actions: [status]\n    commands:\n      status:\n        executable: /SECRET/script\n        working_directory: /SECRET/cwd\n        environment:\n          SECRET: TOP_SECRET_VALUE")
	h.files["/config"] = []byte(config)
	var out bytes.Buffer
	if code := runCLI([]string{"--config", "/config", "--setup-dir", "/SECRET/setup", "--json"}, "agent", &out, h, testNow); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if h.scriptCalls != 1 || h.inspectCalls != 1 || len(h.executables) != 0 {
		t.Fatal("inspection selection")
	}
	if strings.Contains(out.String(), "SECRET") {
		t.Fatal("secret leaked")
	}
	// Transaction inspection is explicit; neither agent nor controller guesses a directory.
	h.inspectCalls = 0
	out.Reset()
	runCLI([]string{"--config", "/config"}, "agent", &out, h, testNow)
	if h.inspectCalls != 0 {
		t.Fatal("implicit transaction read")
	}
}

func TestDoctorInspectsCustomCommandsOnNativeResources(t *testing.T) {
	for _, name := range []string{"docker", "podman", "systemd"} {
		t.Run(name, func(t *testing.T) {
			h := &localFixture{fakeHost: fixture(t, true, testNow.AddDate(1, 0, 0)), script: ScriptStatus{Executable: true, Directory: true, Format: "interpreter"}}
			kind, target := "container", "app"
			if name == "systemd" {
				kind, target = "service", "app.service"
			}
			config := strings.ReplaceAll(minimalAgent, "capabilities: []", "capabilities: [resource.control, script, "+name+"]")
			config = strings.ReplaceAll(config, "resources: []", "resources:\n  - id: app\n    type: "+kind+"\n    adapter: "+name+"\n    target: "+target+"\n    actions: [status, backup]\n    commands:\n      backup:\n        executable: /SECRET/script\n        working_directory: /SECRET/cwd\n        environment: {SECRET: TOP_SECRET_VALUE}")
			h.files["/config"] = []byte(config)
			var out bytes.Buffer
			if code := runCLI([]string{"--config", "/config", "--json"}, "agent", &out, h, testNow); code != 0 {
				t.Fatal(code, out.String())
			}
			if h.scriptCalls != 1 || len(h.executables) != 1 {
				t.Fatal("custom command inspection skipped", h.scriptCalls, h.executables)
			}
			if strings.Contains(out.String(), "SECRET") {
				t.Fatal("secret exposed")
			}
		})
	}
}
