package onboarding

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"sync"
	"time"
)

const sandboxReadinessMaximumAge = 2 * time.Minute

// A process-local handoff, not a persistable receipt or an activation grant.
// Only a completed probe can produce a plan; Apply must repeat the sandbox
// inspection and match dependency evidence before a future start adapter may
// consider it. Start couples revalidation and the fixed native start adapter;
// no CLI path consumes this evidence yet.
type SandboxReadiness struct{ *sandboxReadinessState }

type sandboxReadinessState struct {
	mutex sync.Mutex
	probe *sandboxProbeState
	proof [32]byte
}

type SandboxReadinessPreview struct {
	ReadOnly             bool `json:"read_only"`
	RequiresConsent      bool `json:"requires_consent"`
	RechecksDependencies bool `json:"rechecks_dependencies"`
	MaximumAgeSeconds    int  `json:"maximum_age_seconds"`
}

// No arbitrary evidence JSON, external host, timestamp or proof is accepted.
// Copies of the initial probe and all readiness plans share the same one-shot
// handoff marker, so concurrent plans cannot each launch a revalidation.
func PlanSandboxReadiness(ctx context.Context, probe *SandboxProbe) (*SandboxReadiness, error) {
	if probe == nil || probe.sandboxProbeState == nil {
		return nil, ErrSandboxProbeInvalid
	}
	p := probe.sandboxProbeState
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !freshSandboxHandoff(p) {
		return nil, ErrSandboxProbeStale
	}
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if !sameSandboxSource(bounded, p) || !freshSandboxHandoff(p) {
		return nil, ErrSandboxProbeStale
	}
	return &SandboxReadiness{&sandboxReadinessState{probe: p}}, nil
}

func freshSandboxHandoff(p *sandboxProbeState) bool {
	if p == nil || p.host == nil || p.clock == nil || !p.used || p.handoffUsed || p.dependencyProof == [32]byte{} || p.completedAt.IsZero() {
		return false
	}
	age := p.clock().Sub(p.completedAt)
	return age >= 0 && age < sandboxReadinessMaximumAge
}

func sameSandboxSource(ctx context.Context, p *sandboxProbeState) bool {
	uid, gid, err := p.host.Caller(ctx)
	if err != nil || uid != 0 || gid != 0 || ctx.Err() != nil {
		return false
	}
	current, err := p.host.InspectProbe(ctx)
	return err == nil && ctx.Err() == nil && current == p.snapshot && validSandboxProbeSnapshot(current)
}

func (p SandboxReadiness) Preview() SandboxReadinessPreview {
	return SandboxReadinessPreview{ReadOnly: true, RequiresConsent: true, RechecksDependencies: true, MaximumAgeSeconds: int(sandboxReadinessMaximumAge / time.Second)}
}
func (p SandboxReadiness) MarshalJSON() ([]byte, error) { return json.Marshal(p.Preview()) }
func (p SandboxReadiness) String() string               { data, _ := p.MarshalJSON(); return string(data) }
func (p SandboxReadiness) GoString() string             { return p.String() }

// Apply only repeats the fixed metadata probe. It never starts/enables the
// agent. A dispatched failure consumes the handoff and is uncertain; no retry.
func (p *SandboxReadiness) Apply(ctx context.Context, confirmed bool) (SandboxProbeResult, error) {
	if p == nil || p.sandboxReadinessState == nil || p.probe == nil {
		return SandboxProbeResult{}, ErrSandboxProbeInvalid
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.applyLocked(ctx, confirmed)
}

func (p *SandboxReadiness) applyLocked(ctx context.Context, confirmed bool) (SandboxProbeResult, error) {
	r := SandboxProbeResult{}
	source := p.probe
	source.mutex.Lock()
	defer source.mutex.Unlock()
	if !confirmed {
		return r, ErrSandboxProbeConsent
	}
	if err := ctx.Err(); err != nil {
		return r, err
	}
	if !freshSandboxHandoff(source) {
		return r, ErrSandboxProbeStale
	}
	bounded, cancel := context.WithTimeout(ctx, sandboxProbeTimeout)
	defer cancel()
	if !sameSandboxSource(bounded, source) || !freshSandboxHandoff(source) {
		return r, ErrSandboxProbeStale
	}
	// Consume immediately before dispatch, including across independent plans.
	source.handoffUsed = true
	inputs := SandboxProbeInputs{append([]sandboxProbeFile{}, source.inputs.files...), source.inputs.binding, source.inputs.configProof}
	evidence, err := source.host.RunProbe(bounded, source.snapshot, inputs)
	if err != nil || bounded.Err() != nil || !evidence.InspectorExited || !evidence.UnitCollected || evidence.SourceProof != source.snapshot.SourceProof || evidence.RequestProof != source.inputs.binding || evidence.UID != source.snapshot.UID || evidence.GID != source.snapshot.GID || evidence.DependencyProof != source.dependencyProof {
		r.Uncertain = true
		return r, ErrSandboxProbeIncomplete
	}
	// Check expiry again: slow work does not extend the original evidence age.
	if !sameSandboxSource(bounded, source) {
		r.Uncertain = true
		return r, ErrSandboxProbeIncomplete
	}
	encoded, err := json.Marshal(struct {
		Source                SandboxProbeSnapshot
		Request, Dependencies [32]byte
	}{source.snapshot, source.inputs.binding, evidence.DependencyProof})
	age := source.clock().Sub(source.completedAt)
	if err != nil || bounded.Err() != nil || age < 0 || age >= sandboxReadinessMaximumAge {
		r.Uncertain = true
		return r, ErrSandboxProbeIncomplete
	}
	p.proof = sha256.Sum256(encoded)
	r.Completed = true
	return r, nil
}
