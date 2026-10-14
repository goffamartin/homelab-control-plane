package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

type activationReport struct {
	Status   string                       `json:"status"`
	Code     string                       `json:"code,omitempty"`
	ReadOnly bool                         `json:"read_only"`
	Preview  *ControllerActivationPreview `json:"preview,omitempty"`
	Result   *ControllerActivationResult  `json:"result,omitempty"`
}

func ControllerActivationCLI(args []string, out io.Writer) int {
	return runActivationCLI(args, out, nativeControllerActivationHost)
}
func runActivationCLI(args []string, out io.Writer, factory func(string) ControllerActivationHost) int {
	f := flag.NewFlagSet("activate", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	name := f.String("server-name", "", "explicit installed controller certificate DNS/IP")
	enable := f.Bool("enable", false, "also enable persistent boot activation")
	apply := f.Bool("apply", false, "apply the activation plan")
	yes := f.Bool("yes", false, "separate service activation consent")
	jsonOutput := f.Bool("json", false, "JSON report without private evidence")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			if _, err = fmt.Fprintln(out, "Usage: hlcp-controller activate --server-name DNS/IP [--enable] [--json] [--apply --yes]\nDefault: read-only preview. Requires root, installed Debian profile and complete validated setup.\nNo restart, account/config changes, implicit enable, user CLI provisioning or recovery."); err == nil {
				return 0
			}
		}
		fmt.Fprintln(out, "Invalid activation options. Use activate --help.")
		return 2
	}
	emit := func(report activationReport) int {
		var err error
		if *jsonOutput {
			err = json.NewEncoder(out).Encode(report)
		} else {
			_, err = fmt.Fprintf(out, "Controller activation: %s", report.Status)
			if err == nil && report.Code != "" {
				_, err = fmt.Fprintf(out, " (%s)", report.Code)
			}
			if err == nil {
				_, err = fmt.Fprintln(out)
			}
			if err == nil && report.Preview != nil {
				for _, a := range report.Preview.Actions {
					if _, err = fmt.Fprintln(out, a); err != nil {
						break
					}
				}
			}
			if err == nil && report.Result != nil {
				_, err = fmt.Fprintf(out, "Verified completed: %v; uncertain: %t. Inspect actual state before any new attempt.\n", report.Result.Completed, report.Result.Uncertain)
			}
		}
		if err != nil {
			return 2
		}
		return 0
	}
	if f.NArg() != 0 || !validHost(*name) || *apply != *yes {
		if emit(activationReport{Status: "failed", Code: "INVALID_OPTIONS", ReadOnly: true}) != 0 {
			return 2
		}
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	p, err := PlanControllerActivation(ctx, factory(*name), *enable)
	if err != nil {
		if emit(activationReport{Status: "failed", Code: "PREFLIGHT_REFUSED", ReadOnly: true}) != 0 {
			return 2
		}
		return 1
	}
	preview := p.Preview()
	if !*apply {
		return emit(activationReport{Status: "preview", ReadOnly: true, Preview: &preview})
	}
	r, err := p.Apply(ctx, true)
	if err != nil {
		code := "ACTIVATION_INCOMPLETE"
		if errors.Is(err, ErrActivationStale) {
			code = "STALE_PLAN"
		}
		if emit(activationReport{Status: "failed", Code: code, ReadOnly: len(r.Completed) == 0 && !r.Uncertain, Preview: &preview, Result: &r}) != 0 {
			return 2
		}
		return 1
	}
	return emit(activationReport{Status: "applied", ReadOnly: len(r.Completed) == 0, Preview: &preview, Result: &r})
}
