package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"hlcp/internal/agent"
	"strings"
	"testing"
)

type probeCLIFake struct {
	*probeFake
	configs int
	config  agent.Config
	err     error
}

func (h *probeCLIFake) Config(ctx context.Context) (agent.Config, error) {
	h.configs++
	if _, ok := ctx.Deadline(); !ok {
		panic("unbounded config inspection")
	}
	return h.config, h.err
}
func newProbeCLIFake(t *testing.T) *probeCLIFake {
	return &probeCLIFake{probeFake: freshProbeFake(t), config: probeConfig()}
}
func TestSandboxProbeCLIOptionsHaveNoHostIO(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"--apply"}, {"--yes"}, {"--scope", "user"}, {"--config", "SECRET"}, {"extra"}, {"--json", "--apply"}} {
		h := newProbeCLIFake(t)
		var out bytes.Buffer
		code := runSandboxProbeCLI(args, &out, h)
		if args[0] == "--help" && code != 0 {
			t.Fatal(code, out.String())
		}
		if args[0] != "--help" && code != 2 {
			t.Fatal(code, out.String())
		}
		if h.configs != 0 || h.inspects != 0 || h.runs != 0 {
			t.Fatal("options accessed host")
		}
	}
}
func TestSandboxProbeCLIReports(t *testing.T) {
	for _, test := range []struct {
		name                string
		args                []string
		alter               func(*probeCLIFake)
		status, code        string
		exit                int
		readonly, uncertain bool
		runs                int
	}{
		{"preview", nil, nil, "preview", "", 0, true, false, 0},
		{"apply", []string{"--apply", "--yes"}, nil, "applied", "", 0, false, false, 1},
		{"root", nil, func(h *probeCLIFake) { h.callerUID = 1000 }, "failed", "PREFLIGHT_REFUSED", 1, true, false, 0},
		{"config", nil, func(h *probeCLIFake) { h.err = errors.New("SECRET") }, "failed", "PREFLIGHT_REFUSED", 1, true, false, 0},
		{"unsupported", nil, func(h *probeCLIFake) { h.err = ErrSandboxProbeUnsupported }, "failed", "UNSUPPORTED_RESOURCES", 1, true, false, 0},
		{"binding", nil, func(h *probeCLIFake) { h.snapshot.ConfigProof = [32]byte{9} }, "failed", "PREFLIGHT_REFUSED", 1, true, false, 0},
		{"active", nil, func(h *probeCLIFake) { h.snapshot.AgentInactive = false }, "failed", "PREFLIGHT_REFUSED", 1, true, false, 0},
		{"existing", nil, func(h *probeCLIFake) { h.snapshot.ProbeAbsent = false }, "failed", "PREFLIGHT_REFUSED", 1, true, false, 0},
		{"stale", []string{"--apply", "--yes"}, func(h *probeCLIFake) { h.failInspect = 2 }, "failed", "STALE_PLAN", 1, true, false, 0},
		{"uncertain", []string{"--apply", "--yes"}, func(h *probeCLIFake) { h.fault = "error" }, "failed", "PROBE_INCOMPLETE", 1, false, true, 1},
		{"post", []string{"--apply", "--yes"}, func(h *probeCLIFake) { h.fault = "post" }, "failed", "PROBE_INCOMPLETE", 1, false, true, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newProbeCLIFake(t)
			if test.alter != nil {
				test.alter(h)
			}
			var out bytes.Buffer
			args := append([]string{"--json"}, test.args...)
			if code := runSandboxProbeCLI(args, &out, h); code != test.exit {
				t.Fatalf("exit %d: %s", code, &out)
			}
			var report sandboxProbeReport
			if json.Unmarshal(out.Bytes(), &report) != nil {
				t.Fatal(out.String())
			}
			if report.Status != test.status || report.Code != test.code || report.ReadOnly != test.readonly || h.runs != test.runs {
				t.Fatalf("report %+v runs %d", report, h.runs)
			}
			if test.uncertain && (report.Result == nil || !report.Result.Uncertain) {
				t.Fatal(report)
			}
			if test.name == "root" && h.configs != 0 {
				t.Fatal("read config before root refusal")
			}
			for _, secret := range []string{"SECRET", h.config.NodeID, h.config.Resources[0].ID, "source_proof", "dependency_proof", "executable"} {
				if secret != "" && strings.Contains(out.String(), secret) {
					t.Fatalf("private output %q", secret)
				}
			}
		})
	}
}

type probeFailWriter struct{}

func (probeFailWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }
func TestSandboxProbeCLIWriterFailure(t *testing.T) {
	h := newProbeCLIFake(t)
	if runSandboxProbeCLI(nil, probeFailWriter{}, h) != 2 || h.runs != 0 {
		t.Fatal("writer failure")
	}
}
