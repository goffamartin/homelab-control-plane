package onboarding

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type agentActivationFake struct {
	snapshot AgentActivationSnapshot
	profiles []AgentServiceProfile
	actions  []ControllerServiceAction
	inspects int
	fail     ControllerServiceAction
}

func newAgentActivationFake() *agentActivationFake {
	return &agentActivationFake{snapshot: AgentActivationSnapshot{Service: ControllerActivationSnapshot{Proof: [32]byte{1}, Active: "inactive"}, CallerUID: 1000, CallerGID: 1000, Linger: true, PeerActive: "inactive", PeerEnabled: "disabled"}}
}
func (h *agentActivationFake) Caller(context.Context) (uint32, uint32, error) {
	return h.snapshot.CallerUID, h.snapshot.CallerGID, nil
}
func (h *agentActivationFake) InspectAgent(_ context.Context, profile AgentServiceProfile) (AgentActivationSnapshot, error) {
	h.inspects++
	h.profiles = append(h.profiles, profile)
	return h.snapshot, nil
}
func (h *agentActivationFake) ChangeAgent(_ context.Context, profile AgentServiceProfile, action ControllerServiceAction) error {
	h.profiles = append(h.profiles, profile)
	h.actions = append(h.actions, action)
	if action == ControllerEnable {
		h.snapshot.Service.Enabled = true
	} else if action == ControllerStart || action == AgentRestart {
		h.snapshot.Service.Active = "active"
	} else if action == AgentStop {
		h.snapshot.Service.Active = "inactive"
	} else {
		panic("unexpected service mutation")
	}
	if action == h.fail {
		return errors.New("SECRET")
	}
	return nil
}
func TestAgentActivationProfileConsentAndLifecycle(t *testing.T) {
	ctx := context.Background()
	for _, profile := range []AgentServiceProfile{{Scope: "system", Unit: "hlcp-agent.service"}, {Scope: "user", Unit: "hlcp-agent.service"}, {Scope: "user", Unit: "hlcp-agent-podman.service"}} {
		h := newAgentActivationFake()
		if profile.Scope == "system" {
			h.snapshot.CallerUID = 0
			h.snapshot.CallerGID = 0
		}
		p, err := PlanAgentActivation(ctx, h, profile, true)
		if err != nil {
			t.Fatal(err)
		}
		if h.inspects != 1 || len(p.Preview().Actions) != 2 || p.Preview().Unit != profile.Unit || p.Preview().Scope != profile.Scope {
			t.Fatal("wrong preview")
		}
		if strings.Contains(fmt.Sprintf("%#v", *p), "Proof") {
			t.Fatal("proof exposed")
		}
		if _, err = p.Apply(ctx, false); !errors.Is(err, ErrActivationConsent) || len(h.actions) != 0 {
			t.Fatal(err)
		}
		r, err := p.Apply(ctx, true)
		if err != nil || r.Uncertain || len(r.Completed) != 2 {
			t.Fatal(r, err)
		}
		for _, actual := range h.profiles {
			if actual != profile {
				t.Fatal("wrong manager or unit")
			}
		}
		if _, err = p.Apply(ctx, true); !errors.Is(err, ErrActivationStale) {
			t.Fatal("automatic repeat", err)
		}
	}
}
func TestAgentActivationRejectsWrongScopeBeforeInspection(t *testing.T) {
	for _, profile := range []AgentServiceProfile{{Scope: "system", Unit: "hlcp-agent-podman.service"}, {Scope: "user", Unit: "OTHER.service"}, {Scope: "remote", Unit: "hlcp-agent.service"}} {
		h := newAgentActivationFake()
		if _, err := PlanAgentActivation(context.Background(), h, profile, true); !errors.Is(err, ErrAgentActivationProfile) || h.inspects != 0 {
			t.Fatal(profile, err)
		}
	}
	h := newAgentActivationFake()
	h.snapshot.CallerUID = 0
	h.snapshot.CallerGID = 0
	if _, err := PlanAgentActivation(context.Background(), h, AgentServiceProfile{"user", "hlcp-agent.service"}, false); !errors.Is(err, ErrAgentActivationProfile) || h.inspects != 0 {
		t.Fatal(err)
	}
	h = newAgentActivationFake()
	if _, err := PlanAgentActivation(context.Background(), h, AgentServiceProfile{"system", "hlcp-agent.service"}, false); !errors.Is(err, ErrAgentActivationProfile) || h.inspects != 0 {
		t.Fatal(err)
	}
}
func TestAgentActivationPeerAndLingerGates(t *testing.T) {
	profile := AgentServiceProfile{"user", "hlcp-agent.service"}
	for _, pair := range [][2]string{{"active", "disabled"}, {"inactive", "enabled"}, {"failed", "disabled"}, {"activating", "disabled"}, {"absent", "disabled"}, {"inactive", "enabled-runtime"}} {
		h := newAgentActivationFake()
		h.snapshot.PeerActive = pair[0]
		h.snapshot.PeerEnabled = pair[1]
		if _, err := PlanAgentActivation(context.Background(), h, profile, false); !errors.Is(err, ErrAgentActivationPeer) || len(h.actions) != 0 {
			t.Fatal(pair, err)
		}
	}
	h := newAgentActivationFake()
	h.snapshot.Linger = false
	if _, err := PlanAgentActivation(context.Background(), h, profile, true); !errors.Is(err, ErrAgentActivationLinger) {
		t.Fatal(err)
	}
	p, err := PlanAgentActivation(context.Background(), h, profile, false)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := p.Apply(context.Background(), true); err != nil || len(r.Completed) != 1 || h.snapshot.Service.Enabled || h.snapshot.Linger {
		t.Fatal(r, err)
	}
	h = newAgentActivationFake()
	h.snapshot.PeerActive = "absent"
	h.snapshot.PeerEnabled = "absent"
	if _, err := PlanAgentActivation(context.Background(), h, profile, true); err != nil {
		t.Fatal(err)
	}
}
func TestAgentActivationStaleAndPartialOutcomes(t *testing.T) {
	profile := AgentServiceProfile{"user", "hlcp-agent.service"}
	for _, field := range []string{"peer", "linger", "proof", "caller"} {
		h := newAgentActivationFake()
		p, err := PlanAgentActivation(context.Background(), h, profile, false)
		if err != nil {
			t.Fatal(err)
		}
		switch field {
		case "peer":
			h.snapshot.PeerActive = "active"
		case "linger":
			h.snapshot.Linger = false
		case "proof":
			h.snapshot.Service.Proof[0]++
		case "caller":
			h.snapshot.CallerUID++
		}
		if _, err = p.Apply(context.Background(), true); !errors.Is(err, ErrActivationStale) || len(h.actions) != 0 {
			t.Fatal(field, err)
		}
	}
	h := newAgentActivationFake()
	h.fail = ControllerStart
	p, _ := PlanAgentActivation(context.Background(), h, profile, true)
	r, err := p.Apply(context.Background(), true)
	if !errors.Is(err, ErrActivationIncomplete) || !r.Uncertain || len(r.Completed) != 1 || !h.snapshot.Service.Enabled || strings.Contains(err.Error(), "SECRET") {
		t.Fatal(r, err)
	}
	count := len(h.actions)
	if _, err = p.Apply(context.Background(), true); !errors.Is(err, ErrActivationStale) || len(h.actions) != count {
		t.Fatal("retry occurred")
	}
}
