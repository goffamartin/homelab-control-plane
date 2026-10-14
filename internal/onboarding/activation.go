package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

var (
	ErrActivationInvalid    = errors.New("controller activation evidence is invalid")
	ErrActivationConsent    = errors.New("controller activation requires consent")
	ErrActivationStale      = errors.New("controller activation snapshot changed")
	ErrActivationIncomplete = errors.New("controller activation outcome requires inspection")
)

// ControllerActivationSnapshot is supplied by a trusted host adapter, not CLI
// inputs. Proof binds validated installed binaries/unit, service account and
// configuration/TLS snapshots. It excludes dynamic Active/Enabled properties.
// Inspect must validate them afresh, without writing, opening the ledger,
// generating credentials, or starting a listener. A zero proof is invalid.
type ControllerActivationSnapshot struct {
	Proof   [32]byte
	Active  string // exactly inactive or active; failed/transitional states refuse
	Enabled bool   // persistent enabled; runtime-only/masked/unknown states refuse
}

type ControllerServiceAction string

const (
	ControllerEnable ControllerServiceAction = "enable"
	ControllerStart  ControllerServiceAction = "start"
)

// ControllerActivationHost has no unit, scope, path or command arguments.
// A native adapter may operate only the installed hlcp-controller.service.
// Change must be bounded/cancellable and must not restart or reset a failure.
type ControllerActivationHost interface {
	Inspect(context.Context) (ControllerActivationSnapshot, error)
	Change(context.Context, ControllerServiceAction) error
}

type ControllerActivationPreview struct {
	ReadOnly bool                      `json:"read_only"`
	Actions  []ControllerServiceAction `json:"actions"`
}

type ControllerActivationResult struct {
	Completed []ControllerServiceAction `json:"completed"`
	Uncertain bool                      `json:"uncertain"`
}

// The wrapper protects copies from copying a mutex or disclosing host evidence.
type ControllerActivation struct{ *controllerActivationState }
type controllerActivationState struct {
	mutex    sync.Mutex
	host     ControllerActivationHost
	snapshot ControllerActivationSnapshot
	actions  []ControllerServiceAction
	used     bool
}

// PlanControllerActivation is read-only. Enable is separately requested;
// default activation starts an inactive service but never restarts an active one.
func PlanControllerActivation(ctx context.Context, host ControllerActivationHost, enable bool) (*ControllerActivation, error) {
	if host == nil {
		return nil, ErrActivationInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snapshot, err := host.Inspect(ctx)
	if err != nil || !validActivationSnapshot(snapshot) {
		return nil, ErrActivationInvalid
	}
	actions := []ControllerServiceAction{}
	if enable && !snapshot.Enabled {
		actions = append(actions, ControllerEnable)
	}
	if snapshot.Active == "inactive" {
		actions = append(actions, ControllerStart)
	}
	return &ControllerActivation{&controllerActivationState{host: host, snapshot: snapshot, actions: actions}}, nil
}

func validActivationSnapshot(s ControllerActivationSnapshot) bool {
	return s.Proof != [32]byte{} && (s.Active == "active" || s.Active == "inactive")
}

func (p ControllerActivation) Preview() ControllerActivationPreview {
	if p.controllerActivationState == nil {
		return ControllerActivationPreview{ReadOnly: true, Actions: []ControllerServiceAction{}}
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return ControllerActivationPreview{ReadOnly: true, Actions: append([]ControllerServiceAction{}, p.actions...)}
}
func (p ControllerActivation) MarshalJSON() ([]byte, error) { return json.Marshal(p.Preview()) }
func (p ControllerActivation) String() string               { data, _ := p.MarshalJSON(); return string(data) }
func (p ControllerActivation) GoString() string             { return p.String() }

// Apply is deliberately not a file transaction. An enable/start failure or
// failed post-check can have changed systemd state; never auto-disable/stop,
// roll back files, retry a mutation or claim non-execution in that case.
func (p *ControllerActivation) Apply(ctx context.Context, confirmed bool) (ControllerActivationResult, error) {
	result := ControllerActivationResult{Completed: []ControllerServiceAction{}}
	if p == nil || p.controllerActivationState == nil {
		return result, ErrActivationInvalid
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if !confirmed {
		return result, ErrActivationConsent
	}
	if p.used {
		return result, ErrActivationStale
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	current, err := p.host.Inspect(ctx)
	if err != nil || !validActivationSnapshot(current) || current != p.snapshot {
		return result, ErrActivationStale
	}
	// Even an unchanged confirmed plan is single-use. After any dispatched
	// mutation, cancellation/error requires a new read-only inspection.
	p.used = true
	for _, action := range p.actions {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := p.host.Change(ctx, action); err != nil {
			result.Uncertain = true
			return result, ErrActivationIncomplete
		}
		expected := current
		if action == ControllerEnable {
			expected.Enabled = true
		} else {
			expected.Active = "active"
		}
		current, err = p.host.Inspect(ctx)
		if err != nil || !validActivationSnapshot(current) || current != expected {
			result.Uncertain = true
			return result, ErrActivationIncomplete
		}
		result.Completed = append(result.Completed, action)
	}
	return result, nil
}
