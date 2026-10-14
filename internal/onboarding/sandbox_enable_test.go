package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type enableCLIFake struct {
	*startCLIFake
	checks, enables int
	enableError     error
}

func (h *enableCLIFake) enableVerifiedSandboxAgent(ctx context.Context, snapshot SandboxProbeSnapshot, proof [32]byte, expiresAt time.Time) error {
	h.checks++
	expected := h.snapshot
	expected.AgentInactive = false
	if h.starts != 1 || h.runs != 2 || snapshot != expected || proof == [32]byte{} || !time.Now().Before(expiresAt) {
		panic("enable without verified start")
	}
	if _, ok := ctx.Deadline(); !ok {
		panic("unbounded enable")
	}
	if !snapshot.AgentEnabled {
		h.enables++
	}
	return h.enableError
}
func freshEnableCLIFake(t *testing.T) *enableCLIFake {
	return &enableCLIFake{startCLIFake: &startCLIFake{probeCLIFake: newProbeCLIFake(t)}}
}
func TestSandboxEnableCLIRequiresExplicitStartAndConsent(t *testing.T) {
	for _, args := range [][]string{{"--enable"}, {"--enable", "--apply", "--yes"}, {"--start", "--enable"}, {"--start", "--enable", "--apply"}, {"--start", "--enable", "--yes"}} {
		h := freshEnableCLIFake(t)
		var out bytes.Buffer
		code := runSandboxProbeCLI(args, &out, h)
		if len(args) == 2 && args[0] == "--start" {
			if code != 0 || h.configs != 1 {
				t.Fatal(code, out.String())
			}
		} else {
			if code != 2 || h.configs != 0 {
				t.Fatal(code, out.String())
			}
		}
		if h.runs != 0 || h.starts != 0 || h.enables != 0 {
			t.Fatal("preview/invalid consent mutated host")
		}
	}
}
func TestSandboxEnableCLIReportsPartialResults(t *testing.T) {
	for _, kind := range []string{"success", "already-enabled", "no-enable", "start-error", "enable-error", "probe-error"} {
		t.Run(kind, func(t *testing.T) {
			h := freshEnableCLIFake(t)
			args := []string{"--start", "--enable", "--apply", "--yes", "--json"}
			exit := 0
			wantStarts, wantEnables := 1, 1
			switch kind {
			case "already-enabled":
				h.snapshot.AgentEnabled = true
				wantEnables = 0
			case "no-enable":
				args = []string{"--start", "--apply", "--yes", "--json"}
				wantEnables = 0
			case "start-error":
				h.startError = errors.New("SECRET")
				exit = 1
				wantEnables = 0
			case "enable-error":
				h.enableError = errors.New("SECRET")
				exit = 1
			case "probe-error":
				h.secondFault = "error"
				exit = 1
				wantStarts = 0
				wantEnables = 0
			}
			var out bytes.Buffer
			if code := runSandboxProbeCLI(args, &out, h); code != exit {
				t.Fatal(code, out.String())
			}
			var report sandboxProbeReport
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.ReadOnly || report.Start == nil || h.starts != wantStarts || h.enables != wantEnables {
				t.Fatal(report, h.starts, h.enables)
			}
			if report.EnableRequested != (kind != "no-enable") {
				t.Fatal(report)
			}
			if kind == "enable-error" && (!report.Start.Started || report.Start.Enabled || !report.Start.Uncertain || report.Code != "START_INCOMPLETE") {
				t.Fatal("lost verified start", report)
			}
			if exit == 0 && (!report.Start.Started || report.Start.Uncertain || report.Start.Enabled != (kind != "no-enable")) {
				t.Fatal(report)
			}
		})
	}
}
func TestSandboxEnableUnsupportedHostBeforeProbe(t *testing.T) {
	h := &startCLIFake{probeCLIFake: newProbeCLIFake(t)}
	var out bytes.Buffer
	if runSandboxProbeCLI([]string{"--start", "--enable", "--apply", "--yes", "--json"}, &out, h) != 1 || h.runs != 0 || h.configs != 0 {
		t.Fatal(out.String())
	}
	var report sandboxProbeReport
	if json.Unmarshal(out.Bytes(), &report) != nil || report.Code != "ENABLE_UNAVAILABLE" || !report.ReadOnly {
		t.Fatal(report)
	}
}
func TestSandboxEnabledSourceMatchesOnlyEnabledTransition(t *testing.T) {
	before := freshProbeFake(t).snapshot
	before.AgentInactive = false
	after := before
	after.AgentEnabled = true
	if !sandboxEnabledSourceMatches(before, after) {
		t.Fatal("enable transition refused")
	}
	for _, kind := range []string{"source", "config", "uid", "inactive", "probe", "disabled"} {
		modified := after
		switch kind {
		case "source":
			modified.SourceProof = [32]byte{2}
		case "config":
			modified.ConfigProof = [32]byte{2}
		case "uid":
			modified.UID++
		case "inactive":
			modified.AgentInactive = true
		case "probe":
			modified.ProbeAbsent = false
		case "disabled":
			modified.AgentEnabled = false
		}
		if sandboxEnabledSourceMatches(before, modified) {
			t.Fatal(kind)
		}
	}
	if sandboxEnabledSourceMatches(SandboxProbeSnapshot{}, SandboxProbeSnapshot{}) {
		t.Fatal("zero source")
	}
}

func TestSandboxPersistentStartIsSingleUse(t *testing.T) {
	p, h, _ := completedSandboxProbe(t)
	p.clock = time.Now
	p.completedAt = time.Now()
	host := &enableCLIFake{startCLIFake: &startCLIFake{probeCLIFake: &probeCLIFake{probeFake: h, config: probeConfig()}}}
	p.host = host
	a, err := PlanSandboxReadiness(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	copyPlan := *a
	if _, err := a.StartPersistent(context.Background(), false); !errors.Is(err, ErrSandboxProbeConsent) || host.starts != 0 || host.enables != 0 {
		t.Fatal(err)
	}
	r, err := a.StartPersistent(context.Background(), true)
	if err != nil || !r.Started || !r.Enabled || r.Uncertain {
		t.Fatal(r, err)
	}
	if _, err := copyPlan.StartPersistent(context.Background(), true); !errors.Is(err, ErrSandboxProbeStale) || host.starts != 1 || host.enables != 1 {
		t.Fatal(err)
	}
}
