package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type startCLIFake struct {
	*probeCLIFake
	starts      int
	secondFault string
	startError  error
}

func (h *startCLIFake) RunProbe(ctx context.Context, snapshot SandboxProbeSnapshot, inputs SandboxProbeInputs) (SandboxProbeEvidence, error) {
	if h.runs == 1 {
		h.fault = h.secondFault
	}
	return h.probeFake.RunProbe(ctx, snapshot, inputs)
}
func (h *startCLIFake) startVerifiedSandboxAgent(ctx context.Context, snapshot SandboxProbeSnapshot, proof [32]byte, expiresAt time.Time) error {
	h.starts++
	if h.runs != 2 || snapshot != h.snapshot || proof == [32]byte{} || !time.Now().Before(expiresAt) {
		panic("start without fresh matching probes")
	}
	if _, ok := ctx.Deadline(); !ok {
		panic("unbounded CLI start")
	}
	return h.startError
}

func TestSandboxStartCLIOptionsAndPreview(t *testing.T) {
	for _, args := range [][]string{{"--start"}, {"--start", "--apply"}, {"--start", "--yes"}, {"--enable"}, {"--start", "extra"}, {"--start", "--help"}} {
		h := &startCLIFake{probeCLIFake: newProbeCLIFake(t)}
		var out bytes.Buffer
		exit := runSandboxProbeCLI(args, &out, h)
		if len(args) == 1 && args[0] == "--start" {
			if exit != 0 || h.configs != 1 || h.inspects != 1 {
				t.Fatal(exit, out.String())
			}
		} else if len(args) > 1 && args[1] == "--help" {
			if exit != 0 || h.configs != 0 {
				t.Fatal(exit)
			}
		} else {
			if exit != 2 || h.configs != 0 || h.inspects != 0 {
				t.Fatal(exit, out.String())
			}
		}
		if h.runs != 0 || h.starts != 0 {
			t.Fatal("preview/options mutated host")
		}
	}
}

func TestSandboxStartCLIWorkflowReports(t *testing.T) {
	for _, kind := range []string{"success", "initial-failure", "second-failure", "start-failure", "handoff-stale", "root", "config"} {
		t.Run(kind, func(t *testing.T) {
			h := &startCLIFake{probeCLIFake: newProbeCLIFake(t)}
			wantRuns, wantStarts, exit := 2, 1, 1
			readonly, uncertain := false, false
			switch kind {
			case "success":
				exit = 0
			case "initial-failure":
				h.fault = "error"
				wantRuns = 1
				wantStarts = 0
				uncertain = true
			case "second-failure":
				h.secondFault = "dependency"
				wantStarts = 0
				uncertain = true
			case "start-failure":
				h.startError = errors.New("SECRET")
				uncertain = true
			case "handoff-stale":
				h.failInspect = 4
				wantRuns = 1
				wantStarts = 0
			case "root":
				h.callerUID = 1000
				wantRuns = 0
				wantStarts = 0
				readonly = true
			case "config":
				h.err = errors.New("SECRET")
				wantRuns = 0
				wantStarts = 0
				readonly = true
			}
			var out bytes.Buffer
			if code := runSandboxProbeCLI([]string{"--start", "--json", "--apply", "--yes"}, &out, h); code != exit {
				t.Fatal(code, out.String())
			}
			var report sandboxProbeReport
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatal(err, out.String())
			}
			if !report.StartRequested || report.ReadOnly != readonly || h.runs != wantRuns || h.starts != wantStarts {
				t.Fatal(report, h.runs, h.starts)
			}
			if kind == "success" {
				if report.Status != "applied" || report.Start == nil || !report.Start.Started || !report.Start.Probe.Completed || report.Start.Uncertain {
					t.Fatal(report)
				}
			} else if report.Status != "failed" {
				t.Fatal(report)
			}
			if report.Start != nil && (report.Start.Started || report.Start.Uncertain != uncertain) && kind != "success" {
				t.Fatal(report)
			}
			if kind == "second-failure" || kind == "start-failure" {
				if report.Code != "START_INCOMPLETE" {
					t.Fatal(report.Code)
				}
			}
			if kind == "initial-failure" && (report.Result == nil || !report.Result.Uncertain || report.Code != "PROBE_INCOMPLETE") {
				t.Fatal(report)
			}
			if kind == "root" && h.configs != 0 {
				t.Fatal("read config before root refusal")
			}
			for _, secret := range []string{"SECRET", "/opt", h.config.NodeID, h.config.Resources[0].ID, "source_proof", "dependency_proof"} {
				if strings.Contains(out.String(), secret) {
					t.Fatal("private output", secret)
				}
			}
		})
	}
}

func TestSandboxStartCLIUnsupportedHostBeforeMutation(t *testing.T) {
	h := newProbeCLIFake(t)
	var out bytes.Buffer
	if runSandboxProbeCLI([]string{"--start", "--apply", "--yes"}, &out, h) != 1 || h.runs != 0 || h.configs != 0 || h.inspects != 0 {
		t.Fatal(out.String())
	}
}

func TestSandboxStartCLIFailedOutputDoesNotRetry(t *testing.T) {
	h := &startCLIFake{probeCLIFake: newProbeCLIFake(t)}
	if runSandboxProbeCLI([]string{"--start", "--apply", "--yes"}, probeFailWriter{}, h) != 2 || h.runs != 2 || h.starts != 1 {
		t.Fatal(h.runs, h.starts)
	}
}
