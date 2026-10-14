package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func completedSandboxProbe(t *testing.T) (*SandboxProbe, *probeFake, *time.Time) {
	t.Helper()
	h := freshProbeFake(t)
	p, err := PlanSandboxProbe(context.Background(), h, probeConfig())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	p.clock = func() time.Time { return now }
	if r, err := p.Apply(context.Background(), true); err != nil || !r.Completed {
		t.Fatal(r, err)
	}
	return p, h, &now
}

func TestSandboxReadinessConsentRevalidationAndRedaction(t *testing.T) {
	p, h, _ := completedSandboxProbe(t)
	r, err := PlanSandboxReadiness(context.Background(), p)
	if err != nil || h.runs != 1 {
		t.Fatal(err)
	}
	for _, out := range []string{r.String(), fmt.Sprintf("%#v", *r)} {
		for _, secret := range []string{"/opt", "Proof", "proof", "host", "dependency"} {
			if strings.Contains(out, secret) {
				t.Fatal("private readiness output", out)
			}
		}
	}
	data, err := json.Marshal(r)
	if err != nil || !strings.Contains(string(data), "rechecks_dependencies") {
		t.Fatal(err, string(data))
	}
	if _, err := r.Apply(context.Background(), false); !errors.Is(err, ErrSandboxProbeConsent) || h.runs != 1 {
		t.Fatal(err)
	}
	copyPlan := *r
	result, err := r.Apply(context.Background(), true)
	if err != nil || !result.Completed || result.Uncertain || h.runs != 2 || r.proof == [32]byte{} {
		t.Fatal(result, err)
	}
	if _, err := copyPlan.Apply(context.Background(), true); !errors.Is(err, ErrSandboxProbeStale) || h.runs != 2 {
		t.Fatal(err)
	}
	if _, err := PlanSandboxReadiness(context.Background(), p); !errors.Is(err, ErrSandboxProbeStale) {
		t.Fatal(err)
	}
}

func TestSandboxReadinessRequiresSuccessfulFreshProbe(t *testing.T) {
	if _, err := PlanSandboxReadiness(context.Background(), nil); !errors.Is(err, ErrSandboxProbeInvalid) {
		t.Fatal(err)
	}
	for _, kind := range []string{"unrun", "failed", "zero-proof", "expired", "backwards", "caller", "source", "active", "probe"} {
		t.Run(kind, func(t *testing.T) {
			p, h, now := completedSandboxProbe(t)
			switch kind {
			case "unrun":
				p.used = false
			case "failed":
				p.completedAt = time.Time{}
			case "zero-proof":
				p.dependencyProof = [32]byte{}
			case "expired":
				*now = now.Add(sandboxReadinessMaximumAge)
			case "backwards":
				*now = now.Add(-time.Nanosecond)
			case "caller":
				h.callerUID = 1000
			case "source":
				h.snapshot.SourceProof = [32]byte{2}
			case "active":
				h.snapshot.AgentInactive = false
			case "probe":
				h.snapshot.ProbeAbsent = false
			}
			if _, err := PlanSandboxReadiness(context.Background(), p); !errors.Is(err, ErrSandboxProbeStale) || h.runs != 1 {
				t.Fatal(err)
			}
		})
	}
}

func TestSandboxReadinessFailureIsConsumedAndUncertain(t *testing.T) {
	for _, fault := range []string{"error", "source", "request", "dependency", "uid", "gid", "exit", "cleanup", "post"} {
		t.Run(fault, func(t *testing.T) {
			p, h, _ := completedSandboxProbe(t)
			a, err := PlanSandboxReadiness(context.Background(), p)
			if err != nil {
				t.Fatal(err)
			}
			b, err := PlanSandboxReadiness(context.Background(), p)
			if err != nil {
				t.Fatal(err)
			}
			h.fault = fault
			r, err := a.Apply(context.Background(), true)
			if !errors.Is(err, ErrSandboxProbeIncomplete) || !r.Uncertain || r.Completed || a.proof != [32]byte{} || h.runs != 2 {
				t.Fatal(r, err)
			}
			if _, err := b.Apply(context.Background(), true); !errors.Is(err, ErrSandboxProbeStale) || h.runs != 2 {
				t.Fatal(err)
			}
		})
	}
}

func TestSandboxReadinessStaleBeforeDispatchAndCancellation(t *testing.T) {
	for _, kind := range []string{"expiry", "source", "caller", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			p, h, now := completedSandboxProbe(t)
			a, err := PlanSandboxReadiness(context.Background(), p)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "expiry":
				*now = now.Add(sandboxReadinessMaximumAge)
			case "source":
				h.snapshot.ConfigProof = [32]byte{3}
			case "caller":
				h.callerGID = 1000
			case "cancel":
				cancel()
			}
			r, err := a.Apply(ctx, true)
			if err == nil || r.Uncertain || h.runs != 1 || p.handoffUsed {
				t.Fatal(r, err)
			}
		})
	}
}

func TestSandboxReadinessConcurrentPlansShareOneDispatch(t *testing.T) {
	p, h, _ := completedSandboxProbe(t)
	a, err := PlanSandboxReadiness(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := PlanSandboxReadiness(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errorsOut := make(chan error, 2)
	for _, plan := range []*SandboxReadiness{a, b} {
		wg.Add(1)
		go func(plan *SandboxReadiness) {
			defer wg.Done()
			_, err := plan.Apply(context.Background(), true)
			errorsOut <- err
		}(plan)
	}
	wg.Wait()
	close(errorsOut)
	success, stale := 0, 0
	for err := range errorsOut {
		if err == nil {
			success++
		} else if errors.Is(err, ErrSandboxProbeStale) {
			stale++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || stale != 1 || h.runs != 2 {
		t.Fatal(success, stale, h.runs)
	}
}

type expiryProbeHost struct {
	*probeFake
	advance func()
}

func (h expiryProbeHost) RunProbe(ctx context.Context, s SandboxProbeSnapshot, i SandboxProbeInputs) (SandboxProbeEvidence, error) {
	e, err := h.probeFake.RunProbe(ctx, s, i)
	h.advance()
	return e, err
}
func TestSandboxReadinessExpiryDuringProbeDoesNotExtendLifetime(t *testing.T) {
	p, h, now := completedSandboxProbe(t)
	p.host = expiryProbeHost{h, func() { *now = now.Add(sandboxReadinessMaximumAge) }}
	a, err := PlanSandboxReadiness(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	r, err := a.Apply(context.Background(), true)
	if !errors.Is(err, ErrSandboxProbeIncomplete) || !r.Uncertain || a.proof != [32]byte{} {
		t.Fatal(r, err)
	}
}

type changedDependencyHost struct{ *probeFake }

func (h changedDependencyHost) RunProbe(ctx context.Context, s SandboxProbeSnapshot, i SandboxProbeInputs) (SandboxProbeEvidence, error) {
	e, err := h.probeFake.RunProbe(ctx, s, i)
	e.DependencyProof = [32]byte{10}
	return e, err
}

func TestSandboxReadinessRejectsChangedNonzeroDependencies(t *testing.T) {
	p, h, _ := completedSandboxProbe(t)
	p.host = changedDependencyHost{h}
	a, err := PlanSandboxReadiness(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	r, err := a.Apply(context.Background(), true)
	if !errors.Is(err, ErrSandboxProbeIncomplete) || !r.Uncertain || a.proof != [32]byte{} {
		t.Fatal(r, err)
	}
}

func TestSandboxReadinessCancelledDispatchIsUncertain(t *testing.T) {
	p, h, _ := completedSandboxProbe(t)
	a, err := PlanSandboxReadiness(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.cancel = cancel
	r, err := a.Apply(ctx, true)
	if !errors.Is(err, ErrSandboxProbeIncomplete) || !r.Uncertain || !p.handoffUsed || a.proof != [32]byte{} {
		t.Fatal(r, err)
	}
}

func TestSandboxReadinessJSONCannotCreateEvidence(t *testing.T) {
	var forged SandboxReadiness
	_ = json.Unmarshal([]byte(`{"proof":[1],"probe":{"completedAt":"2026-10-14T12:00:00Z"}}`), &forged)
	if _, err := forged.Apply(context.Background(), true); !errors.Is(err, ErrSandboxProbeInvalid) {
		t.Fatal("JSON minted readiness", err)
	}
	var nilPlan *SandboxReadiness
	if _, err := nilPlan.Apply(context.Background(), true); !errors.Is(err, ErrSandboxProbeInvalid) {
		t.Fatal(err)
	}
}
