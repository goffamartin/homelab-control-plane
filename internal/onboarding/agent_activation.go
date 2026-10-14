package onboarding

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
)

var (
	ErrAgentActivationProfile = errors.New("agent activation profile is invalid")
	ErrAgentActivationPeer    = errors.New("other agent service must be inactive and disabled")
	ErrAgentActivationLinger  = errors.New("persistent user agent activation requires explicit linger provisioning")
)

type AgentServiceProfile struct{ Scope, Unit string }

// Proof must bind native installed profile/account/config/TLS, caller identity,
// user manager and linger/peer unit properties. It must not read a journal.
type AgentActivationSnapshot struct {
	Service                 ControllerActivationSnapshot
	CallerUID, CallerGID    uint32
	Linger                  bool
	PeerActive, PeerEnabled string // user peer: absent/absent or inactive/disabled
}
type AgentActivationHost interface {
	Caller(context.Context) (uint32, uint32, error)
	InspectAgent(context.Context, AgentServiceProfile) (AgentActivationSnapshot, error)
	ChangeAgent(context.Context, AgentServiceProfile, ControllerServiceAction) error
}
type AgentActivation struct {
	lifecycle *ControllerActivation
	profile   AgentServiceProfile
}
type AgentActivationPreview struct {
	ControllerActivationPreview
	Scope string `json:"scope"`
	Unit  string `json:"unit"`
}

func validAgentServiceProfile(profile AgentServiceProfile) bool {
	return (profile.Scope == "system" && profile.Unit == "hlcp-agent.service") ||
		(profile.Scope == "user" && (profile.Unit == "hlcp-agent.service" || profile.Unit == "hlcp-agent-podman.service"))
}

// Only predefined system/user lifecycle profiles. Run directly as the real
// non-root owner for user scope, never root impersonation or bus/env inference.
func PlanAgentActivation(ctx context.Context, host AgentActivationHost, profile AgentServiceProfile, enable bool) (*AgentActivation, error) {
	if host == nil || !validAgentServiceProfile(profile) {
		return nil, ErrAgentActivationProfile
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	uid, gid, err := host.Caller(ctx)
	if err != nil || (profile.Scope == "system" && (uid != 0 || gid != 0)) || (profile.Scope == "user" && (uid == 0 || gid == 0)) {
		return nil, ErrAgentActivationProfile
	}
	adapter := agentActivationAdapter{host: host, profile: profile, enable: enable, uid: uid, gid: gid}
	// Inspect first to retain a useful controlled refusal (peer/linger), then
	// pass the same snapshot to the shared consent/stale/partial-outcome engine.
	snapshot, err := adapter.Inspect(ctx)
	if err != nil {
		return nil, err
	}
	lifecycle, err := PlanControllerActivation(ctx, &firstAgentSnapshot{agentActivationAdapter: adapter, first: &snapshot}, enable)
	if err != nil {
		return nil, err
	}
	return &AgentActivation{lifecycle: lifecycle, profile: profile}, nil
}

type agentActivationAdapter struct {
	host     AgentActivationHost
	profile  AgentServiceProfile
	enable   bool
	uid, gid uint32
}

func (a agentActivationAdapter) Inspect(ctx context.Context) (ControllerActivationSnapshot, error) {
	uid, gid, err := a.host.Caller(ctx)
	if err != nil || uid != a.uid || gid != a.gid {
		return ControllerActivationSnapshot{}, ErrAgentActivationProfile
	}
	snapshot, err := a.host.InspectAgent(ctx, a.profile)
	if err != nil || snapshot.CallerUID != uid || snapshot.CallerGID != gid || !validActivationSnapshot(snapshot.Service) {
		return ControllerActivationSnapshot{}, ErrAgentActivationProfile
	}
	if a.profile.Scope == "user" {
		if !((snapshot.PeerActive == "absent" && snapshot.PeerEnabled == "absent") || (snapshot.PeerActive == "inactive" && snapshot.PeerEnabled == "disabled")) {
			return ControllerActivationSnapshot{}, ErrAgentActivationPeer
		}
		if a.enable && !snapshot.Linger {
			return ControllerActivationSnapshot{}, ErrAgentActivationLinger
		}
	}
	// Bind surrounding user lifecycle evidence even if a host supplied a proof
	// excluding it. A changed peer/linger/caller cannot slip through stale checks.
	evidence := snapshot
	evidence.Service.Active = ""
	evidence.Service.Enabled = false
	data, _ := json.Marshal(evidence)
	snapshot.Service.Proof = sha256.Sum256(data)
	return snapshot.Service, nil
}
func (a agentActivationAdapter) Change(ctx context.Context, action ControllerServiceAction) error {
	return a.host.ChangeAgent(ctx, a.profile, action)
}

// Called once by planning; subsequent inspections always query the actual host.
type firstAgentSnapshot struct {
	agentActivationAdapter
	first *ControllerActivationSnapshot
}

func (a *firstAgentSnapshot) Inspect(ctx context.Context) (ControllerActivationSnapshot, error) {
	if a.first != nil {
		value := *a.first
		a.first = nil
		return value, nil
	}
	return a.agentActivationAdapter.Inspect(ctx)
}

func (p AgentActivation) Preview() AgentActivationPreview {
	if p.lifecycle == nil {
		return AgentActivationPreview{ControllerActivationPreview: ControllerActivationPreview{ReadOnly: true, Actions: []ControllerServiceAction{}}}
	}
	return AgentActivationPreview{ControllerActivationPreview: p.lifecycle.Preview(), Scope: p.profile.Scope, Unit: p.profile.Unit}
}
func (p AgentActivation) MarshalJSON() ([]byte, error) { return json.Marshal(p.Preview()) }
func (p AgentActivation) String() string               { data, _ := p.MarshalJSON(); return string(data) }
func (p AgentActivation) GoString() string             { return p.String() }
func (p *AgentActivation) Apply(ctx context.Context, confirmed bool) (ControllerActivationResult, error) {
	if p == nil || p.lifecycle == nil {
		return ControllerActivationResult{}, ErrAgentActivationProfile
	}
	return p.lifecycle.Apply(ctx, confirmed)
}
