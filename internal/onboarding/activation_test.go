package onboarding

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type activationHost struct {
	snapshot    ControllerActivationSnapshot
	changes     []ControllerServiceAction
	inspects    int
	failAction  ControllerServiceAction
	failInspect int
	changeProof bool
	cancel      context.CancelFunc
}

func (h *activationHost) Inspect(context.Context) (ControllerActivationSnapshot, error) {
	h.inspects++
	if h.inspects == h.failInspect {
		return ControllerActivationSnapshot{}, errors.New("SECRET")
	}
	return h.snapshot, nil
}
func (h *activationHost) Change(_ context.Context, a ControllerServiceAction) error {
	h.changes = append(h.changes, a)
	if a == ControllerEnable {
		h.snapshot.Enabled = true
	} else if a == ControllerStart {
		h.snapshot.Active = "active"
	} else {
		panic("unexpected action")
	}
	if h.changeProof {
		h.snapshot.Proof[0]++
	}
	if h.cancel != nil {
		h.cancel()
	}
	if a == h.failAction {
		return errors.New("SECRET")
	}
	return nil
}
func freshActivationHost() *activationHost {
	return &activationHost{snapshot: ControllerActivationSnapshot{Proof: [32]byte{1}, Active: "inactive"}}
}
func TestActivationPreviewConsentAndCopies(t *testing.T) {
	h := freshActivationHost()
	p, err := PlanControllerActivation(context.Background(), h, true)
	if err != nil {
		t.Fatal(err)
	}
	preview := p.Preview()
	if len(preview.Actions) != 2 || preview.Actions[0] != ControllerEnable || preview.Actions[1] != ControllerStart {
		t.Fatal(preview)
	}
	preview.Actions[0] = "SECRET"
	copyPlan := *p
	if strings.Contains(fmt.Sprintf("%#v", copyPlan), "SECRET") || strings.Contains(copyPlan.String(), "Proof") {
		t.Fatal("plan leaked private evidence")
	}
	if _, err = p.Apply(context.Background(), false); !errors.Is(err, ErrActivationConsent) || len(h.changes) != 0 {
		t.Fatal(err)
	}
	r, err := p.Apply(context.Background(), true)
	if err != nil || r.Uncertain || len(r.Completed) != 2 || len(h.changes) != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err = copyPlan.Apply(context.Background(), true); !errors.Is(err, ErrActivationStale) {
		t.Fatal(err)
	}
}
func TestActivationNoImplicitEnableOrRestart(t *testing.T) {
	for _, active := range []string{"active", "inactive"} {
		h := freshActivationHost()
		h.snapshot.Active = active
		p, err := PlanControllerActivation(context.Background(), h, false)
		if err != nil {
			t.Fatal(err)
		}
		r, err := p.Apply(context.Background(), true)
		if err != nil || h.snapshot.Enabled || (active == "active" && len(r.Completed) != 0) || (active == "inactive" && len(r.Completed) != 1) {
			t.Fatalf("%+v %v", r, err)
		}
	}
}
func TestActivationInvalidAndStale(t *testing.T) {
	for _, state := range []string{"failed", "activating", "deactivating", "unknown"} {
		h := freshActivationHost()
		h.snapshot.Active = state
		if _, err := PlanControllerActivation(context.Background(), h, true); !errors.Is(err, ErrActivationInvalid) || len(h.changes) != 0 {
			t.Fatal(state, err)
		}
	}
	h := freshActivationHost()
	h.snapshot.Proof = [32]byte{}
	if _, err := PlanControllerActivation(context.Background(), h, true); !errors.Is(err, ErrActivationInvalid) {
		t.Fatal(err)
	}
	for _, field := range []string{"proof", "active", "enabled"} {
		h := freshActivationHost()
		p, _ := PlanControllerActivation(context.Background(), h, true)
		switch field {
		case "proof":
			h.snapshot.Proof[0]++
		case "active":
			h.snapshot.Active = "active"
		case "enabled":
			h.snapshot.Enabled = true
		}
		if _, err := p.Apply(context.Background(), true); !errors.Is(err, ErrActivationStale) || len(h.changes) != 0 {
			t.Fatal(field, err)
		}
	}
}
func TestActivationPartialFailureNeverRollsBackOrRetries(t *testing.T) {
	for _, kind := range []string{"enable", "start", "postcheck", "proof"} {
		h := freshActivationHost()
		p, _ := PlanControllerActivation(context.Background(), h, true)
		switch kind {
		case "enable":
			h.failAction = ControllerEnable
		case "start":
			h.failAction = ControllerStart
		case "postcheck":
			h.failInspect = 3
		case "proof":
			h.changeProof = true
		}
		r, err := p.Apply(context.Background(), true)
		if !errors.Is(err, ErrActivationIncomplete) || !r.Uncertain || !h.snapshot.Enabled || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("%s %+v %v", kind, r, err)
		}
		count := len(h.changes)
		if _, err = p.Apply(context.Background(), true); !errors.Is(err, ErrActivationStale) || len(h.changes) != count {
			t.Fatal(err)
		}
		if kind == "start" && len(r.Completed) != 1 {
			t.Fatal(r)
		}
	}
}
func TestActivationCancellationAndInspectionFailure(t *testing.T) {
	h := freshActivationHost()
	p, _ := PlanControllerActivation(context.Background(), h, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Apply(ctx, true); !errors.Is(err, context.Canceled) || len(h.changes) != 0 {
		t.Fatal(err)
	}
	h.failInspect = 2
	if _, err := p.Apply(context.Background(), true); !errors.Is(err, ErrActivationStale) || len(h.changes) != 0 {
		t.Fatal(err)
	}
	h = freshActivationHost()
	p, _ = PlanControllerActivation(context.Background(), h, true)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	h.cancel = cancel
	r, err := p.Apply(ctx, true)
	if !errors.Is(err, context.Canceled) || len(r.Completed) != 1 || len(h.changes) != 1 || !h.snapshot.Enabled || h.snapshot.Active != "inactive" {
		t.Fatalf("%+v %v", r, err)
	}
}
