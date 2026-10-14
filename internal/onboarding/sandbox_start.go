package onboarding

import (
	"context"
	"time"
)

// Package-private: never adopt an external callback or a client-supplied proof
// as authority to start a service. Only the same trusted probe host may start.
type sandboxStartHost interface {
	startVerifiedSandboxAgent(context.Context, SandboxProbeSnapshot, [32]byte, time.Time) error
}

type SandboxStartResult struct {
	Probe     SandboxProbeResult `json:"probe"`
	Started   bool               `json:"started"`
	Uncertain bool               `json:"uncertain"`
}

// Start is a one-shot composite operation, not a way to consume a previously
// serialized or applied readiness proof. It repeats the sandbox check under
// consent, then starts only the inactive system agent. No enable/restart/reset.
func (p *SandboxReadiness) Start(ctx context.Context, confirmed bool) (SandboxStartResult, error) {
	r := SandboxStartResult{}
	if p == nil || p.sandboxReadinessState == nil || p.probe == nil {
		return r, ErrSandboxProbeInvalid
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if !confirmed {
		return r, ErrSandboxProbeConsent
	}
	host, ok := p.probe.host.(sandboxStartHost)
	if !ok {
		return r, ErrSandboxProbeUnsupported
	}
	var err error
	r.Probe, err = p.applyLocked(ctx, true)
	if err != nil {
		r.Uncertain = r.Probe.Uncertain
		return r, err
	}
	// A source/expiry failure here occurs after a probe mutation, but before
	// start dispatch. Do not report a wholly read-only failed operation.
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	source := p.probe
	source.mutex.Lock()
	defer source.mutex.Unlock()
	if p.proof == [32]byte{} || !sameSandboxSource(bounded, source) {
		r.Uncertain = true
		return r, ErrSandboxProbeIncomplete
	}
	age := source.clock().Sub(source.completedAt)
	if age < 0 || age >= sandboxReadinessMaximumAge {
		r.Uncertain = true
		return r, ErrSandboxProbeIncomplete
	}
	// Native adapter rechecks source immediately before systemctl and confirms
	// the active state with unchanged source/profile/config after command exit.
	if host.startVerifiedSandboxAgent(bounded, source.snapshot, p.proof, source.completedAt.Add(sandboxReadinessMaximumAge)) != nil || bounded.Err() != nil {
		r.Uncertain = true
		return r, ErrSandboxProbeIncomplete
	}
	r.Started = true
	return r, nil
}

func sandboxStartedSourceMatches(before, after SandboxProbeSnapshot) bool {
	if !validSandboxProbeSnapshot(before) {
		return false
	}
	expected := before
	expected.AgentInactive = false
	return after == expected
}
