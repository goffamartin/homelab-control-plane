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
	"time"
)

const (
	AgentRestart ControllerServiceAction = "restart"
	AgentStop    ControllerServiceAction = "stop"
)

// Stop/status must not depend on valid YAML, TLS, or opening a journal.
type AgentServiceState struct {
	Active         string   `json:"active"`
	Enabled        string   `json:"enabled"`
	ReloadRequired bool     `json:"reload_required"`
	Proof          [32]byte `json:"-"`
}
type agentServiceInspector interface {
	InspectAgentService(context.Context, AgentServiceProfile) (AgentServiceState, error)
}
type agentActivationReport struct {
	SchemaVersion int                         `json:"schema_version"`
	Operation     string                      `json:"operation"`
	Status        string                      `json:"status"`
	Code          string                      `json:"code"`
	Message       string                      `json:"message"`
	ReadOnly      bool                        `json:"read_only"`
	Changed       bool                        `json:"changed"`
	Uncertain     bool                        `json:"uncertain"`
	Completed     []ControllerServiceAction   `json:"completed"`
	Check         string                      `json:"check,omitempty"`
	Next          string                      `json:"next,omitempty"`
	Preview       *AgentActivationPreview     `json:"preview,omitempty"`
	Result        *ControllerActivationResult `json:"result,omitempty"`
	State         *AgentServiceState          `json:"state,omitempty"`
	Constraints   []string                    `json:"constraints,omitempty"`
}

func AgentServiceCLI(operation string, args []string, out io.Writer) int {
	return runAgentServiceCLI(operation, args, out, nativeAgentActivationHost)
}
func runAgentServiceCLI(operation string, args []string, out io.Writer, host AgentActivationHost) int {
	jsonMode := false
	for _, arg := range args {
		if arg == "--json" {
			jsonMode = true
		}
	}
	scope := "user"
	if os.Geteuid() == 0 {
		scope = "system"
	}
	f := flag.NewFlagSet(operation, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	profile := AgentServiceProfile{}
	f.StringVar(&profile.Scope, "scope", scope, "user or system service manager")
	f.StringVar(&profile.Unit, "unit", "hlcp-agent.service", "installed agent unit")
	enable := f.Bool("enable", false, "also enable boot startup (start only)")
	dry := f.Bool("dry-run", false, "read-only service plan")
	apply := f.Bool("apply", false, "explicit apply (optional with --yes)")
	yes := f.Bool("yes", false, "confirmation paired with --apply")
	f.BoolVar(&jsonMode, "json", jsonMode, "versioned JSON")
	emit := func(r agentActivationReport, code int) int {
		r.SchemaVersion = 1
		r.Operation = "agent." + operation
		if r.Completed == nil {
			r.Completed = []ControllerServiceAction{}
		}
		if jsonMode {
			if json.NewEncoder(out).Encode(r) != nil {
				return 2
			}
			return code
		}
		if _, err := fmt.Fprintln(out, r.Message); err != nil {
			return 2
		}
		if r.State != nil {
			fmt.Fprintf(out, "Service: %s (%s); state: %s; boot: %s.\n", profile.Unit, profile.Scope, r.State.Active, r.State.Enabled)
		}
		if r.Status == "failed" {
			fmt.Fprintf(out, "Reason: %s", r.Code)
			if r.Check != "" {
				fmt.Fprintf(out, " (check: %s)", r.Check)
			}
			fmt.Fprintln(out, ".")
		}
		if r.Preview != nil && r.Status == "preview" {
			fmt.Fprintln(out, "Planned:", r.Preview.Actions)
		}
		for _, c := range r.Constraints {
			fmt.Fprintln(out, "Constraint:", c)
		}
		if r.Uncertain {
			fmt.Fprintln(out, "The service outcome is unverified. No rollback was performed; inspect status before retrying.")
		}
		if r.Next != "" {
			fmt.Fprintln(out, "Next:", r.Next)
		}
		return code
	}
	invalid := func() int {
		return emit(agentActivationReport{Status: "failed", Code: "INVALID_OPTIONS", ReadOnly: true, Message: "Invalid service command options. Nothing changed.", Next: "Use hlcp-agent " + operation + " --help."}, 2)
	}
	help := "Usage: hlcp-agent start|restart|stop|status [--scope user|system] [--unit hlcp-agent.service|hlcp-agent-podman.service] [--json]\nStart also accepts --enable for boot startup. Start/restart/stop perform the named operation by default; use --dry-run for preview.\nUser scope runs as the resource owner without sudo; root defaults to system scope.\nStart/restart validate the installed profile, YAML, TLS and readiness before changing service state. Stop/status do not require valid YAML.\nRestart does not imply enable. Stop does not disable boot startup. Service state is not controller connectivity or application health."
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return emit(agentActivationReport{Status: "ok", Code: "HELP", ReadOnly: true, Message: help}, 0)
		}
		return invalid()
	}
	if f.NArg() != 0 || !validAgentServiceProfile(profile) || *apply != *yes || (*dry && *apply) || (*enable && operation != "start") || (operation != "start" && operation != "restart" && operation != "stop" && operation != "status") || (operation == "status" && (*dry || *apply)) {
		return invalid()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	failure := func(err error, r agentActivationReport) int {
		r.Status = "failed"
		r.Code = "PREFLIGHT_REFUSED"
		r.Message = "Service preflight failed. No service operation was dispatched."
		r.ReadOnly = true
		r.Next = "Inspect the selected service/configuration. Do not reset pairing or journal state."
		if errors.Is(err, ErrAgentActivationPeer) {
			r.Code = "PEER_SERVICE_CONFLICT"
		}
		if errors.Is(err, ErrAgentActivationLinger) {
			r.Code = "LINGER_REQUIRED"
		}
		if errors.Is(err, ErrAgentActivationResources) {
			r.Code = "RESOURCE_PREFLIGHT_REQUIRED"
		}
		var check *agentActivationCheckError
		if errors.As(err, &check) {
			r.Check = check.check
		}
		var refusal *systemResourceRefusal
		if errors.As(err, &refusal) {
			r.Constraints = refusal.Constraints()
		}
		if r.Check == "service_manager_reload_required" {
			r.Next = "Reload the selected systemd manager after a package/unit update, then retry. Running service was not stopped."
		}
		return emit(r, 1)
	}
	if operation == "start" || operation == "restart" {
		plan, err := PlanAgentActivation(ctx, host, profile, *enable)
		if err != nil {
			return failure(err, agentActivationReport{})
		}
		preview := plan.Preview()
		if operation == "restart" {
			preview.Actions = []ControllerServiceAction{AgentRestart}
		}
		if *dry {
			return emit(agentActivationReport{Status: "preview", Code: "SERVICE_PLAN", ReadOnly: true, Message: "Read-only service plan. No service change was made.", Preview: &preview}, 0)
		}
		var result ControllerActivationResult
		if operation == "start" {
			result, err = plan.Apply(ctx, true)
		} else {
			result.Completed = []ControllerServiceAction{}
			current, e := plan.lifecycle.host.Inspect(ctx)
			if e != nil || current != plan.lifecycle.snapshot {
				return failure(ErrActivationStale, agentActivationReport{})
			}
			err = host.ChangeAgent(ctx, profile, AgentRestart)
			if err == nil {
				after, e := plan.lifecycle.host.Inspect(ctx)
				expected := current
				expected.Active = "active"
				if e != nil || after != expected {
					err = ErrActivationIncomplete
				}
			}
			if err != nil {
				result.Uncertain = true
			} else {
				result.Completed = append(result.Completed, AgentRestart)
			}
		}
		r := agentActivationReport{Status: "applied", Code: "SERVICE_RUNNING", ReadOnly: len(result.Completed) == 0 && !result.Uncertain, Changed: len(result.Completed) > 0, Uncertain: result.Uncertain, Completed: result.Completed, Preview: &preview, Result: &result, Message: "Agent service is running. Controller connectivity and resource health were not verified."}
		if err != nil {
			r.Status = "failed"
			r.Code = "SERVICE_OPERATION_INCOMPLETE"
			r.Message = "Service operation did not finish with a verified outcome. Completed changes are retained."
			if errors.Is(err, ErrActivationStale) {
				r.Code = "STALE_PLAN"
			}
			return emit(r, 1)
		}
		return emit(r, 0)
	}
	inspector, ok := host.(agentServiceInspector)
	if !ok {
		return failure(ErrAgentActivationProfile, agentActivationReport{})
	}
	state, err := inspector.InspectAgentService(ctx, profile)
	if err != nil {
		return failure(err, agentActivationReport{})
	}
	if operation == "status" {
		return emit(agentActivationReport{Status: "ok", Code: "SERVICE_STATUS", ReadOnly: true, State: &state, Message: "Agent service state inspected. Configuration and controller connectivity were not checked."}, 0)
	}
	if state.Active != "active" && state.Active != "inactive" && state.Active != "failed" {
		return failure(ErrAgentActivationProfile, agentActivationReport{})
	}
	preview := AgentActivationPreview{Scope: profile.Scope, Unit: profile.Unit, ControllerActivationPreview: ControllerActivationPreview{ReadOnly: true, Actions: []ControllerServiceAction{}}}
	if state.Active != "inactive" {
		preview.Actions = append(preview.Actions, AgentStop)
	}
	if *dry {
		return emit(agentActivationReport{Status: "preview", Code: "SERVICE_PLAN", ReadOnly: true, Preview: &preview, State: &state, Message: "Read-only stop plan. No service change was made."}, 0)
	}
	if state.Active == "inactive" {
		return emit(agentActivationReport{Status: "ok", Code: "SERVICE_ALREADY_STOPPED", ReadOnly: true, State: &state, Message: "Agent service is already stopped. Boot startup was not changed."}, 0)
	}
	current, err := inspector.InspectAgentService(ctx, profile)
	if err != nil || current != state {
		return failure(ErrActivationStale, agentActivationReport{})
	}
	err = host.ChangeAgent(ctx, profile, AgentStop)
	if err == nil {
		current, err = inspector.InspectAgentService(ctx, profile)
		if err == nil && (current.Active != "inactive" || current.Enabled != state.Enabled || current.Proof != state.Proof) {
			err = ErrActivationIncomplete
		}
	}
	if err != nil {
		return emit(agentActivationReport{Status: "failed", Code: "SERVICE_OPERATION_INCOMPLETE", Uncertain: true, Message: "Stop was requested, but the stopped state could not be verified."}, 1)
	}
	return emit(agentActivationReport{Status: "applied", Code: "SERVICE_STOPPED", Changed: true, Completed: []ControllerServiceAction{AgentStop}, State: &current, Message: "Agent service is stopped. Boot startup was not changed."}, 0)
}
