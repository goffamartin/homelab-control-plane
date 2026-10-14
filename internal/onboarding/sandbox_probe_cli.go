package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hlcp/internal/agent"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type sandboxProbeCLIHost interface {
	SandboxProbeHost
	Config(context.Context) (agent.Config, error)
}

type sandboxProbeReport struct {
	Status   string               `json:"status"`
	Code     string               `json:"code,omitempty"`
	ReadOnly bool                 `json:"read_only"`
	Preview  *SandboxProbePreview `json:"preview,omitempty"`
	Result   *SandboxProbeResult  `json:"result,omitempty"`
}

func SandboxProbeCLI(args []string, out io.Writer) int {
	return runSandboxProbeCLI(args, out, nativeProbeCLIHost)
}

func runSandboxProbeCLI(args []string, out io.Writer, host sandboxProbeCLIHost) int {
	f := flag.NewFlagSet("probe-resources", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	apply := f.Bool("apply", false, "launch fixed metadata inspector")
	yes := f.Bool("yes", false, "separate probe consent")
	jsonOutput := f.Bool("json", false, "redacted JSON report")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			if _, e := fmt.Fprintln(out, "Usage: hlcp-agent probe-resources [--json] [--apply --yes]\nDefault: read-only preview. Requires root, installed package and local booted Linux systemd.\nFixed system profile and /etc/hlcp/agent configuration; inactive agent, absent probe.\nOnly script executable/workdir metadata is inspected; no configured actions run.\nApply creates a transient sandbox and may create its StateDirectory.\nNo main-agent start/enable, persisted readiness receipt or activation-gate bypass.\nAfter uncertain outcome inspect actual state before retry."); e == nil {
				return 0
			}
		}
		fmt.Fprintln(out, "Invalid probe options. Use probe-resources --help.")
		return 2
	}
	emit := func(report sandboxProbeReport) int {
		var err error
		if *jsonOutput {
			err = json.NewEncoder(out).Encode(report)
		} else {
			_, err = fmt.Fprintf(out, "Resource sandbox probe: %s (%s); read-only: %t\n", report.Status, report.Code, report.ReadOnly)
			if err == nil && report.Preview != nil {
				_, err = fmt.Fprintf(out, "Fixed unit: %s; file checks: %d; explicit consent required.\n", report.Preview.Unit, report.Preview.FileChecks)
			}
			if err == nil && report.Result != nil {
				_, err = fmt.Fprintf(out, "Completed: %t; uncertain: %t. No activation receipt stored.\n", report.Result.Completed, report.Result.Uncertain)
			}
		}
		if err != nil {
			return 2
		}
		return 0
	}
	if f.NArg() != 0 || *apply != *yes {
		emit(sandboxProbeReport{Status: "failed", Code: "INVALID_OPTIONS", ReadOnly: true})
		return 2
	}
	failure := func(err error, readonly bool, preview *SandboxProbePreview, result *SandboxProbeResult) int {
		code := "PREFLIGHT_REFUSED"
		switch {
		case errors.Is(err, ErrSandboxProbeUnsupported):
			code = "UNSUPPORTED_RESOURCES"
		case errors.Is(err, ErrSandboxProbeStale):
			code = "STALE_PLAN"
		case errors.Is(err, ErrSandboxProbeIncomplete):
			code = "PROBE_INCOMPLETE"
		}
		if emit(sandboxProbeReport{Status: "failed", Code: code, ReadOnly: readonly, Preview: preview, Result: result}) != 0 {
			return 2
		}
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Bound the entire read-only phase, including native config/TLS inspection.
	preflight, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if host == nil {
		return failure(ErrSandboxProbeInvalid, true, nil, nil)
	}
	uid, gid, err := host.Caller(preflight)
	if err != nil || uid != 0 || gid != 0 {
		return failure(ErrSandboxProbeInvalid, true, nil, nil)
	}
	config, err := host.Config(preflight)
	if err != nil {
		return failure(err, true, nil, nil)
	}
	plan, err := PlanSandboxProbe(preflight, host, config)
	if err != nil {
		return failure(err, true, nil, nil)
	}
	preview := plan.Preview()
	if !*apply {
		return emit(sandboxProbeReport{Status: "preview", ReadOnly: true, Preview: &preview})
	}
	result, err := plan.Apply(ctx, true)
	if err != nil {
		return failure(err, !result.Completed && !result.Uncertain, &preview, &result)
	}
	return emit(sandboxProbeReport{Status: "applied", ReadOnly: false, Preview: &preview, Result: &result})
}
