package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type serviceFake struct {
	*agentActivationFake
	invalidConfig bool
	changedProof  bool
	stuck         bool
	serviceReads  int
}

func (h *serviceFake) InspectAgent(ctx context.Context, p AgentServiceProfile) (AgentActivationSnapshot, error) {
	if h.invalidConfig {
		return AgentActivationSnapshot{}, &agentActivationCheckError{check: "yaml_syntax_and_schema"}
	}
	return h.agentActivationFake.InspectAgent(ctx, p)
}
func (h *serviceFake) InspectAgentService(ctx context.Context, p AgentServiceProfile) (AgentServiceState, error) {
	h.serviceReads++
	s := AgentServiceState{Active: h.snapshot.Service.Active, Enabled: "disabled", Proof: [32]byte{1}}
	if h.snapshot.Service.Enabled {
		s.Enabled = "enabled"
	}
	if h.changedProof && h.serviceReads > 1 {
		s.Proof[0] = 2
	}
	return s, nil
}
func (h *serviceFake) ChangeAgent(ctx context.Context, p AgentServiceProfile, a ControllerServiceAction) error {
	if h.stuck {
		h.actions = append(h.actions, a)
		return errors.New("SECRET")
	}
	return h.agentActivationFake.ChangeAgent(ctx, p, a)
}
func serviceRun(t *testing.T, h *serviceFake, op string, args ...string) (agentActivationReport, int) {
	t.Helper()
	var out bytes.Buffer
	args = append([]string{"--scope", "user", "--json"}, args...)
	code := runAgentServiceCLI(op, args, &out, h)
	var r agentActivationReport
	if json.Unmarshal(out.Bytes(), &r) != nil || r.SchemaVersion != 1 || r.Operation != "agent."+op || strings.Contains(out.String(), "SECRET") || strings.Contains(out.String(), "Proof") {
		t.Fatal(out.String())
	}
	return r, code
}
func TestAgentServiceDirectCommandsPreviewAndIdempotency(t *testing.T) {
	for _, op := range []string{"start", "restart", "stop", "status"} {
		h := &serviceFake{agentActivationFake: newAgentActivationFake()}
		h.snapshot.Service.Active = "active"
		r, code := serviceRun(t, h, op)
		if code != 0 {
			t.Fatal(op, r)
		}
		want := []ControllerServiceAction{}
		if op == "restart" {
			want = append(want, AgentRestart)
		}
		if op == "stop" {
			want = append(want, AgentStop)
		}
		if !reflect.DeepEqual(r.Completed, want) {
			t.Fatal(op, r)
		}
		if op != "status" {
			h = &serviceFake{agentActivationFake: newAgentActivationFake()}
			r, code = serviceRun(t, h, op, "--dry-run")
			if code != 0 || r.Status != "preview" || !r.ReadOnly || len(h.actions) != 0 {
				t.Fatal(op, r)
			}
		}
	}
	h := &serviceFake{agentActivationFake: newAgentActivationFake()}
	r, code := serviceRun(t, h, "start", "--enable")
	if code != 0 || !reflect.DeepEqual(h.actions, []ControllerServiceAction{ControllerEnable, ControllerStart}) || !r.Changed {
		t.Fatal(r)
	}
	r, code = serviceRun(t, h, "start")
	if code != 0 || !r.ReadOnly || len(r.Completed) != 0 {
		t.Fatal(r)
	}
}
func TestInvalidYAMLNeverStopsRunningAgent(t *testing.T) {
	for _, op := range []string{"start", "restart"} {
		h := &serviceFake{agentActivationFake: newAgentActivationFake(), invalidConfig: true}
		h.snapshot.Service.Active = "active"
		r, code := serviceRun(t, h, op)
		if code != 1 || !r.ReadOnly || r.Check != "yaml_syntax_and_schema" || len(h.actions) != 0 || h.snapshot.Service.Active != "active" {
			t.Fatal(op, r)
		}
	}
	for _, op := range []string{"status", "stop"} {
		h := &serviceFake{agentActivationFake: newAgentActivationFake(), invalidConfig: true}
		h.snapshot.Service.Active = "active"
		_, code := serviceRun(t, h, op)
		if code != 0 || h.inspects != 0 {
			t.Fatal(op, code)
		}
	}
}
func TestServiceStaleProofAndMutationFailures(t *testing.T) {
	h := &serviceFake{agentActivationFake: newAgentActivationFake(), changedProof: true}
	h.snapshot.Service.Active = "active"
	r, code := serviceRun(t, h, "stop")
	if code != 1 || len(h.actions) != 0 || !r.ReadOnly {
		t.Fatal(r)
	}
	for _, op := range []string{"start", "restart", "stop"} {
		h = &serviceFake{agentActivationFake: newAgentActivationFake(), stuck: true}
		h.snapshot.Service.Active = "active"
		if op == "start" {
			h.snapshot.Service.Active = "inactive"
		}
		r, code = serviceRun(t, h, op)
		if code != 1 || !r.Uncertain || r.ReadOnly || r.Code != "SERVICE_OPERATION_INCOMPLETE" {
			t.Fatal(op, r)
		}
	}
}
func TestServiceBadOptionsDoNotInspect(t *testing.T) {
	for _, op := range []string{"restart", "stop", "status"} {
		h := &serviceFake{agentActivationFake: newAgentActivationFake()}
		_, code := serviceRun(t, h, op, "--enable")
		if code != 2 || h.inspects != 0 || h.serviceReads != 0 {
			t.Fatal(op, code)
		}
	}
}
