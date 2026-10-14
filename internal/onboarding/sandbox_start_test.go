package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

type startProbeFake struct {
	*probeFake
	starts int
	err    error
	cancel context.CancelFunc
}

func (h *startProbeFake) startVerifiedSandboxAgent(ctx context.Context, snapshot SandboxProbeSnapshot, proof [32]byte, expiresAt time.Time) error {
	h.starts++
	if _, ok := ctx.Deadline(); !ok {
		panic("unbounded start")
	}
	if h.runs != 2 || snapshot != h.snapshot || proof == [32]byte{} || expiresAt.IsZero() {
		panic("start without revalidated proof")
	}
	if h.cancel != nil {
		h.cancel()
	}
	return h.err
}
func startReadinessPlan(t *testing.T) (*SandboxReadiness, *startProbeFake) {
	t.Helper()
	p, h, _ := completedSandboxProbe(t)
	s := &startProbeFake{probeFake: h}
	p.host = s
	a, err := PlanSandboxReadiness(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	return a, s
}
func TestSandboxStartRequiresConsentAndSingleUse(t *testing.T) {
	p, h := startReadinessPlan(t)
	copyPlan := *p
	if _, err := p.Start(context.Background(), false); !errors.Is(err, ErrSandboxProbeConsent) || h.runs != 1 || h.starts != 0 {
		t.Fatal(err)
	}
	r, err := p.Start(context.Background(), true)
	if err != nil || !r.Started || !r.Probe.Completed || r.Uncertain || h.starts != 1 || h.runs != 2 {
		t.Fatal(r, err)
	}
	if _, err := copyPlan.Start(context.Background(), true); !errors.Is(err, ErrSandboxProbeStale) || h.starts != 1 || h.runs != 2 {
		t.Fatal(err)
	}
}
func TestSandboxStartDoesNotAdoptAppliedOrUntrustedEvidence(t *testing.T) {
	p, h := startReadinessPlan(t)
	if _, err := p.Apply(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Start(context.Background(), true); !errors.Is(err, ErrSandboxProbeStale) || h.starts != 0 {
		t.Fatal(err)
	}
	original, fake, _ := completedSandboxProbe(t)
	a, err := PlanSandboxReadiness(context.Background(), original)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Start(context.Background(), true); !errors.Is(err, ErrSandboxProbeUnsupported) || fake.runs != 1 {
		t.Fatal(err)
	}
	var forged SandboxReadiness
	_ = json.Unmarshal([]byte(`{"proof":[1]}`), &forged)
	if _, err := forged.Start(context.Background(), true); !errors.Is(err, ErrSandboxProbeInvalid) {
		t.Fatal(err)
	}
}
func TestSandboxStartRefusalsAndUncertainOutcomes(t *testing.T) {
	for _, kind := range []string{"stale", "probe-failed", "start-error", "cancel-before", "cancel-start"} {
		t.Run(kind, func(t *testing.T) {
			p, h := startReadinessPlan(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wantStarts := 0
			wantUncertain := false
			switch kind {
			case "stale":
				h.snapshot.SourceProof = [32]byte{2}
			case "probe-failed":
				h.fault = "dependency"
				wantUncertain = true
			case "start-error":
				h.err = errors.New("SECRET")
				wantStarts = 1
				wantUncertain = true
			case "cancel-before":
				cancel()
			case "cancel-start":
				h.cancel = cancel
				wantStarts = 1
				wantUncertain = true
			}
			r, err := p.Start(ctx, true)
			if err == nil || r.Started || r.Uncertain != wantUncertain || h.starts != wantStarts {
				t.Fatal(r, err, h.starts)
			}
			if wantUncertain {
				if _, err := p.Start(context.Background(), true); !errors.Is(err, ErrSandboxProbeStale) || h.starts != wantStarts {
					t.Fatal("automatic replay", err)
				}
			}
		})
	}
}
func TestSandboxStartedSourceMatchesOnlyActiveTransition(t *testing.T) {
	before := freshProbeFake(t).snapshot
	after := before
	after.AgentInactive = false
	if !sandboxStartedSourceMatches(before, after) {
		t.Fatal("active transition refused")
	}
	for _, kind := range []string{"source", "config", "uid", "gid", "probe", "inactive"} {
		modified := after
		switch kind {
		case "source":
			modified.SourceProof = [32]byte{2}
		case "config":
			modified.ConfigProof = [32]byte{2}
		case "uid":
			modified.UID++
		case "gid":
			modified.GID++
		case "probe":
			modified.ProbeAbsent = false
		case "inactive":
			modified.AgentInactive = true
		}
		if sandboxStartedSourceMatches(before, modified) {
			t.Fatal(kind)
		}
	}
	if sandboxStartedSourceMatches(SandboxProbeSnapshot{}, SandboxProbeSnapshot{}) {
		t.Fatal("zero evidence")
	}
}
func TestSandboxStartConcurrentPlansOnlyStartOnce(t *testing.T) {
	a, h := startReadinessPlan(t)
	b, err := PlanSandboxReadiness(context.Background(), &SandboxProbe{a.probe})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, p := range []*SandboxReadiness{a, b} {
		wg.Add(1)
		go func(p *SandboxReadiness) { defer wg.Done(); _, err := p.Start(context.Background(), true); errs <- err }(p)
	}
	wg.Wait()
	close(errs)
	success, stale := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, ErrSandboxProbeStale) {
			stale++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || stale != 1 || h.starts != 1 || h.runs != 2 {
		t.Fatal(success, stale, h.starts, h.runs)
	}
}

type delayedStartSourceHost struct {
	*startProbeFake
	advance func()
	at      int
}

func (h *delayedStartSourceHost) InspectProbe(ctx context.Context) (SandboxProbeSnapshot, error) {
	s, err := h.probeFake.InspectProbe(ctx)
	if h.inspects == h.at {
		h.advance()
	}
	return s, err
}
func TestSandboxStartChecksExpiryAfterFinalSourceRead(t *testing.T) {
	p, h, now := completedSandboxProbe(t)
	s := &startProbeFake{probeFake: h}
	delayed := &delayedStartSourceHost{startProbeFake: s, advance: func() { *now = now.Add(sandboxReadinessMaximumAge) }}
	p.host = delayed
	a, err := PlanSandboxReadiness(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	delayed.at = h.inspects + 3 // revalidation pre/post, then final start precheck
	r, err := a.Start(context.Background(), true)
	if !errors.Is(err, ErrSandboxProbeIncomplete) || !r.Uncertain || !r.Probe.Completed || r.Started || s.starts != 0 {
		t.Fatal(r, err, s.starts)
	}
}
