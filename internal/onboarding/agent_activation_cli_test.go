package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type diagnosticAgentHost struct {
	*agentActivationFake
	check string
}

func (h diagnosticAgentHost) InspectAgent(context.Context, AgentServiceProfile) (AgentActivationSnapshot, error) {
	return AgentActivationSnapshot{}, &agentActivationCheckError{check: h.check}
}

func TestAgentActivationRetainsSafeDiagnosticThroughAdapter(t *testing.T) {
	for _, check := range []string{"private_file_agent_yaml", "private_journal_path_and_permissions", "service_manager_reload_required"} {
		host := diagnosticAgentHost{newAgentActivationFake(), check}
		var out bytes.Buffer
		code := runAgentActivationCLI([]string{"--json"}, &out, host)
		var report agentActivationReport
		if code != 1 || json.Unmarshal(out.Bytes(), &report) != nil || report.Check != check || report.SchemaVersion != 1 || !report.ReadOnly || len(host.actions) != 0 || report.Next == "" {
			t.Fatal(code, out.String())
		}
	}
}

func TestAgentActivationCLIHelpOptionsAndPreview(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"--apply"}, {"--yes"}, {"--scope", "remote"}, {"--scope", "system", "--unit", "hlcp-agent-podman.service"}, {"--unknown"}} {
		h := newAgentActivationFake()
		var out bytes.Buffer
		code := runAgentActivationCLI(args, &out, h)
		if h.inspects != 0 || len(h.actions) != 0 || (args[0] == "--help" && code != 0) || (args[0] != "--help" && code != 2) {
			t.Fatal(args, code)
		}
	}
	h := newAgentActivationFake()
	var out bytes.Buffer
	if runAgentActivationCLI([]string{"--json", "--enable"}, &out, h) != 0 || len(h.actions) != 0 {
		t.Fatal(out.String())
	}
	var report agentActivationReport
	if json.Unmarshal(out.Bytes(), &report) != nil || report.Status != "preview" || !report.ReadOnly || report.Preview.Scope != "user" || len(report.Preview.Actions) != 2 {
		t.Fatal(out.String())
	}
}
func TestAgentActivationCLIConsentAndControlledErrors(t *testing.T) {
	for _, fail := range []ControllerServiceAction{"", ControllerStart} {
		h := newAgentActivationFake()
		h.fail = fail
		var out bytes.Buffer
		code := runAgentActivationCLI([]string{"--apply", "--yes", "--enable", "--json"}, &out, h)
		var report agentActivationReport
		if json.Unmarshal(out.Bytes(), &report) != nil || strings.Contains(out.String(), "SECRET") || len(h.actions) != 2 || report.ReadOnly {
			t.Fatal(out.String())
		}
		if fail == "" && (code != 0 || report.Status != "applied") {
			t.Fatal(out.String())
		}
		if fail != "" && (code != 1 || report.Code != "ACTIVATION_INCOMPLETE" || !report.Result.Uncertain || len(report.Result.Completed) != 1) {
			t.Fatal(out.String())
		}
	}
	for _, kind := range []string{"linger", "peer"} {
		h := newAgentActivationFake()
		expected := "LINGER_REQUIRED"
		if kind == "linger" {
			h.snapshot.Linger = false
		} else {
			h.snapshot.PeerActive = "active"
			expected = "PEER_SERVICE_CONFLICT"
		}
		var out bytes.Buffer
		if runAgentActivationCLI([]string{"--enable", "--json"}, &out, h) != 1 || !strings.Contains(out.String(), expected) || len(h.actions) != 0 {
			t.Fatal(out.String())
		}
	}
}
