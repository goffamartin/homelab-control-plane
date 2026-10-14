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
	Status         string               `json:"status"`
	Code           string               `json:"code,omitempty"`
	ReadOnly       bool                 `json:"read_only"`
	Preview        *SandboxProbePreview `json:"preview,omitempty"`
	Result         *SandboxProbeResult  `json:"result,omitempty"`
	StartRequested bool                 `json:"start_requested,omitempty"`
	Start          *SandboxStartResult  `json:"start,omitempty"`
}

func SandboxProbeCLI(args []string, out io.Writer) int {
	return runSandboxProbeCLI(args, out, nativeProbeCLIHost)
}

func runSandboxProbeCLI(args []string, out io.Writer, host sandboxProbeCLIHost) int {
	f := flag.NewFlagSet("probe-resources", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	apply := f.Bool("apply", false, "launch fixed metadata inspector")
	yes := f.Bool("yes", false, "separate probe consent")
	start := f.Bool("start", false, "after a matching fresh probe, start the inactive system agent")
	jsonOutput := f.Bool("json", false, "redacted JSON report")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			if _, e := fmt.Fprintln(out, "Usage: hlcp-agent probe-resources [--start] [--json] [--apply --yes]\nDefault: read-only preview. Requires root, installed package and local booted Linux systemd.\nFixed system profile and /etc/hlcp/agent configuration; inactive agent, absent probe.\nOnly script executable/workdir metadata is inspected; no configured actions run by probes.\nApply creates a transient sandbox and may create its StateDirectory.\n--start with --apply --yes repeats the sandbox check and starts the inactive agent.\nAgent start opens its normal journal/runtime and outbound controller connection.\nWithout --start, no main-agent start. No enable/restart/reset or persisted receipt.\n--start alone is still read-only preview. Live Debian acceptance remains pending.\nAfter uncertain outcome inspect actual state before retry."); e == nil {
				return 0
			}
		}
		fmt.Fprintln(out, "Invalid probe options. Use probe-resources --help.")
		return 2
	}
	var startResult *SandboxStartResult
	emit := func(report sandboxProbeReport) int {
		report.StartRequested = *start
		report.Start = startResult
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
			if err == nil && *start {
				_, err = fmt.Fprintln(out, "Planned: fresh matching sandbox check, then start inactive system agent (no enable/restart).")
			}
			if err == nil && report.Start != nil {
				_, err = fmt.Fprintf(out, "Verified started: %t; uncertain: %t. Active state is not controller registration/health.\n", report.Start.Started, report.Start.Uncertain)
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
			if startResult != nil {
				code = "START_INCOMPLETE"
			}
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
	if *start {
		if _, ok := host.(sandboxStartHost); !ok {
			if emit(sandboxProbeReport{Status: "failed", Code: "START_UNAVAILABLE", ReadOnly: true}) != 0 {
				return 2
			}
			return 1
		}
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
	workflow, cancelWorkflow := context.WithTimeout(ctx, 90*time.Second)
	defer cancelWorkflow()
	result, err := plan.Apply(workflow, true)
	if err != nil {
		return failure(err, !result.Completed && !result.Uncertain, &preview, &result)
	}
	if *start {
		ready, err := PlanSandboxReadiness(workflow, plan)
		if err != nil {
			return failure(err, false, &preview, &result)
		}
		r, err := ready.Start(workflow, true)
		startResult = &r
		if err != nil {
			return failure(err, false, &preview, &result)
		}
	}
	return emit(sandboxProbeReport{Status: "applied", ReadOnly: false, Preview: &preview, Result: &result})
}
