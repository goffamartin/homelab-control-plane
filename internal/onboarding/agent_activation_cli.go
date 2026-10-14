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

type agentActivationReport struct {
	Status      string                      `json:"status"`
	Code        string                      `json:"code,omitempty"`
	ReadOnly    bool                        `json:"read_only"`
	Preview     *AgentActivationPreview     `json:"preview,omitempty"`
	Result      *ControllerActivationResult `json:"result,omitempty"`
	Constraints []string                    `json:"constraints,omitempty"`
}

func AgentActivationCLI(args []string, out io.Writer) int {
	return runAgentActivationCLI(args, out, nativeAgentActivationHost)
}
func runAgentActivationCLI(args []string, out io.Writer, host AgentActivationHost) int {
	f := flag.NewFlagSet("activate", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	profile := AgentServiceProfile{}
	f.StringVar(&profile.Scope, "scope", "user", "user or system manager")
	f.StringVar(&profile.Unit, "unit", "hlcp-agent.service", "generic or user compatibility agent unit")
	enable := f.Bool("enable", false, "explicit persistent service enable; user linger must already exist")
	apply := f.Bool("apply", false, "apply service activation plan")
	yes := f.Bool("yes", false, "separate activation consent")
	jsonOutput := f.Bool("json", false, "redacted JSON report")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			if _, err = fmt.Fprintln(out, "Usage: hlcp-agent activate [--scope user|system] [--unit hlcp-agent.service|hlcp-agent-podman.service]\n  [--enable] [--json] [--apply --yes]\nDefault: user-scope read-only preview; run directly as the non-root resource owner.\nSystem scope requires root. Installed package profiles and validated imported setup only.\nUser resources: bounded Docker/Podman/script metadata checks; systemd status only.\nSystem-scope resources still require separate service-sandbox readiness review.\nMetadata checks do not establish runtime health, service environment or resource execution success.\nNo restart, implicit linger, account/config changes, recovery or resource execution during preview."); err == nil {
				return 0
			}
		}
		fmt.Fprintln(out, "Invalid agent activation options. Use activate --help.")
		return 2
	}
	emit := func(report agentActivationReport) int {
		var err error
		if *jsonOutput {
			err = json.NewEncoder(out).Encode(report)
		} else {
			_, err = fmt.Fprintf(out, "Agent activation (%s, %s): %s", profile.Scope, profile.Unit, report.Status)
			if err == nil && report.Code != "" {
				_, err = fmt.Fprintf(out, " (%s)", report.Code)
			}
			if err == nil {
				_, err = fmt.Fprintln(out)
			}
			if err == nil && report.Preview != nil {
				for _, action := range report.Preview.Actions {
					if _, err = fmt.Fprintln(out, action); err != nil {
						break
					}
				}
			}
			if err == nil && report.Result != nil {
				_, err = fmt.Fprintf(out, "Verified completed: %v; uncertain: %t. Inspect actual state before retry.\n", report.Result.Completed, report.Result.Uncertain)
			}
			if err == nil {
				for _, code := range report.Constraints {
					if _, err = fmt.Fprintln(out, "Constraint:", code); err != nil {
						break
					}
				}
			}
		}
		if err != nil {
			return 2
		}
		return 0
	}
	if f.NArg() != 0 || !validAgentServiceProfile(profile) || *apply != *yes {
		if *jsonOutput {
			emit(agentActivationReport{Status: "failed", Code: "INVALID_OPTIONS", ReadOnly: true})
		} else {
			fmt.Fprintln(out, "Invalid agent activation options. Use activate --help.")
		}
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	p, err := PlanAgentActivation(ctx, host, profile, *enable)
	if err != nil {
		code := "PREFLIGHT_REFUSED"
		if errors.Is(err, ErrAgentActivationPeer) {
			code = "PEER_SERVICE_CONFLICT"
		}
		if errors.Is(err, ErrAgentActivationLinger) {
			code = "LINGER_REQUIRED"
		}
		if errors.Is(err, ErrAgentActivationResources) {
			code = "RESOURCE_PREFLIGHT_REQUIRED"
		}
		var constraints []string
		var refusal *systemResourceRefusal
		if errors.As(err, &refusal) {
			constraints = refusal.Constraints()
		}
		if emit(agentActivationReport{Status: "failed", Code: code, ReadOnly: true, Constraints: constraints}) != 0 {
			return 2
		}
		return 1
	}
	preview := p.Preview()
	if !*apply {
		return emit(agentActivationReport{Status: "preview", ReadOnly: true, Preview: &preview})
	}
	r, err := p.Apply(ctx, true)
	if err != nil {
		code := "ACTIVATION_INCOMPLETE"
		if errors.Is(err, ErrActivationStale) {
			code = "STALE_PLAN"
		}
		if emit(agentActivationReport{Status: "failed", Code: code, ReadOnly: len(r.Completed) == 0 && !r.Uncertain, Preview: &preview, Result: &r}) != 0 {
			return 2
		}
		return 1
	}
	return emit(agentActivationReport{Status: "applied", ReadOnly: len(r.Completed) == 0, Preview: &preview, Result: &r})
}
