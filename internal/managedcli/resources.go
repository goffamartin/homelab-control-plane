package managedcli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go.yaml.in/yaml/v4"
	"hlcp/internal/agent"
	"hlcp/internal/managed"
	"hlcp/internal/onboarding"
	"io"
	"os"
	"strings"
	"time"
)

func activateConfiguredAgent(scope, unit string) int {
	if scope == "system" {
		return onboarding.SandboxProbeCLI([]string{"--start", "--apply", "--yes"}, io.Discard)
	}
	return onboarding.AgentActivationCLI([]string{"--scope", scope, "--unit", unit, "--apply", "--yes"}, io.Discard)
}

type arguments []string

func (a *arguments) String() string         { return strings.Join(*a, ",") }
func (a *arguments) Set(value string) error { *a = append(*a, value); return nil }

// Resources changes only an agent's explicit local allowlist. Discovery never
// imports resources automatically, and validation never executes an action.
func Resources(args []string, input io.Reader, out io.Writer) int {
	action := "configure"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = args[0]
		args = args[1:]
	}
	f := flag.NewFlagSet("resources", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	defaultScope := "user"
	if os.Geteuid() == 0 {
		defaultScope = "system"
	}
	scope := f.String("scope", defaultScope, "user or system")
	unit := f.String("unit", "hlcp-agent.service", "packaged generic or user compatibility profile")
	id := f.String("id", "", "resource ID")
	adapter := f.String("adapter", "", "docker, podman, systemd or script")
	target := f.String("target", "", "container name or service unit")
	actions := f.String("actions", "status", "comma-separated explicit allowed actions")
	executable := f.String("executable", "", "absolute local executable for script")
	cwd := f.String("working-directory", "/", "script working directory")
	timeout := f.Duration("timeout", 10*time.Second, "script action timeout up to 20s")
	var argv arguments
	f.Var(&argv, "arg", "one literal script argument; repeatable, no shell parsing")
	replace := f.Bool("replace", false, "replace an existing resource")
	restart := f.Bool("restart", false, "explicit stop/configure/validated start")
	apply := f.Bool("apply", false, "write config")
	yes := f.Bool("yes", false, "consent")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(out, "Usage: hlcp-agent resources [configure|list|discover|remove] [--scope user|system]\n  --id NAME --adapter docker|podman|systemd|script [--target NAME] [--actions status,restart]\n  Script: --executable /absolute/program [--arg literal] [--working-directory /path] [--timeout 10s]\n  [--replace] [--restart] [--apply --yes]\nNo arguments starts a local wizard. Preview never runs actions. Discovery lists container names only. Changes retain a private backup and never touch the journal.")
			return 0
		}
		return 2
	}
	if f.NArg() != 0 || !validAgentProfile(*scope, *unit) || *apply != *yes {
		return 2
	}
	p, err := pathsFor(*scope)
	if err != nil {
		return 1
	}
	ctx, cancel := boundedContext()
	defer cancel()
	fail := func(code string) int { fmt.Fprintln(out, "Resource configuration refused:", code); return 1 }
	if action == "discover" {
		if *apply || *restart || (*adapter != "docker" && *adapter != "podman") {
			return 2
		}
		if os.Geteuid() == 0 {
			return fail("RUN_CONTAINER_DISCOVERY_AS_RESOURCE_OWNER")
		}
		data, e := discoverContainers(ctx, *adapter)
		if e != nil {
			return fail("RUNTIME_UNAVAILABLE")
		}
		_, _ = out.Write(data)
		return 0
	}
	if action != "configure" && action != "remove" && action != "list" {
		return 2
	}
	raw, err := readPrivate(p.directory+"/agent.yaml", p.owner)
	if err != nil {
		return fail("CONFIG_UNAVAILABLE")
	}
	config, err := agent.Parse(raw)
	if err != nil {
		return fail("CONFIG_INVALID")
	}
	if action == "list" {
		if *apply || *restart {
			return 2
		}
		_ = json.NewEncoder(out).Encode(config.Resources)
		return 0
	}
	interactive := len(args) == 0 && action == "configure"
	if interactive {
		ui := newTerminal(input, out)
		if !ui.interactive {
			return fail("TERMINAL_REQUIRED")
		}
		*id, err = ui.line("Resource name: ")
		if err != nil {
			return 1
		}
		*adapter, err = ui.line("Adapter [docker/podman/systemd/script]: ")
		if err != nil {
			return 1
		}
		if *adapter == "script" {
			*executable, err = ui.line("Absolute executable path (no shell command): ")
			if err != nil {
				return 1
			}
			value, e := ui.line("Action name [status]: ")
			if e != nil {
				return 1
			}
			if value != "" {
				*actions = value
			}
			for {
				value, e = ui.line("One literal argument (blank to finish): ")
				if e != nil {
					return 1
				}
				if value == "" {
					break
				}
				argv = append(argv, value)
				if len(argv) > 32 {
					return 1
				}
			}
		} else {
			*target, err = ui.line("Exact container name or systemd unit: ")
			if err != nil {
				return 1
			}
			value, e := ui.line("Allowed actions [status] (comma separated, e.g. status,restart): ")
			if e != nil {
				return 1
			}
			if value != "" {
				*actions = value
			}
		}
		value, e := ui.line("Stop/restart the agent to apply this resource now? [yes/no]: ")
		if e != nil {
			return 1
		}
		*restart = affirmative(value)
		value, e = ui.line("Save this explicit resource/action allowlist? [yes/no]: ")
		if e != nil || !affirmative(value) {
			fmt.Fprintln(out, "No changes applied.")
			return 0
		}
		*apply = true
		*yes = true
	}
	resource := agent.ResourceConfig{ID: *id, Adapter: *adapter, Target: *target, Actions: strings.Split(*actions, ",")}
	switch *adapter {
	case "docker", "podman":
		resource.Type = "container"
	case "systemd":
		resource.Type = "service"
	case "script":
		resource.Type = "task"
		resource.Target = ""
		if len(resource.Actions) != 1 {
			return fail("ONE_SCRIPT_ACTION_PER_CONFIGURATION_REQUIRED")
		}
		resource.Commands = map[string]agent.ScriptCommandConfig{resource.Actions[0]: {Executable: *executable, Argv: argv, WorkingDirectory: *cwd, Timeout: *timeout, Environment: map[string]string{}}}
	}
	found := false
	next := make([]agent.ResourceConfig, 0, len(config.Resources)+1)
	for _, r := range config.Resources {
		if r.ID == *id {
			found = true
			if action != "remove" {
				if !*replace {
					return fail("RESOURCE_EXISTS_USE_REPLACE")
				}
				next = append(next, resource)
			}
		} else {
			next = append(next, r)
		}
	}
	if action == "remove" {
		if !found {
			return fail("RESOURCE_NOT_FOUND")
		}
	} else if !found {
		next = append(next, resource)
	}
	config.Resources = next
	if action != "remove" {
		for _, capability := range []string{"resource.control", resource.Adapter} {
			present := false
			for _, c := range config.Capabilities {
				present = present || c == capability
			}
			if !present {
				config.Capabilities = append(config.Capabilities, capability)
			}
		}
	}
	if config.Validate() != nil {
		return fail("RESOURCE_OR_ACTION_INVALID")
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		return 1
	}
	if !*apply {
		_ = json.NewEncoder(out).Encode(map[string]any{"status": "preview", "read_only": true, "resource": *id, "action": action, "restart": *restart})
		return 0
	}
	if bytes.Equal(raw, data) {
		fmt.Fprintln(out, "Resource configuration unchanged.")
		return 0
	}
	if *restart {
		if reloadManager(ctx, *scope) != nil {
			return fail("SERVICE_MANAGER_RELOAD_FAILED")
		}
		state, e := inspectService(ctx, *scope, *unit)
		if e != nil {
			return fail("SERVICE_PREFLIGHT_REFUSED")
		}
		if state.active == "active" {
			if _, e = fixedProcess(ctx, "/usr/bin/systemctl", []string{"--" + *scope, "--no-ask-password", "stop", *unit}, *scope); e != nil {
				return fail("STOP_UNCERTAIN")
			}
		}
	}
	s, err := managed.OpenStore(p.directory)
	if err != nil {
		return fail("CONFIG_UNSAFE_OR_LOCKED")
	}
	defer s.Close()
	current, err := s.Read("agent.yaml")
	if err != nil || !bytes.Equal(current, raw) {
		return fail("CONFIG_CHANGED_RETRY")
	}
	if s.Update(ctx, "resources-backup.yaml", func([]byte) ([]byte, error) { return raw, nil }) != nil {
		return fail("BACKUP_FAILED")
	}
	if s.Update(ctx, "agent.yaml", func(before []byte) ([]byte, error) {
		if !bytes.Equal(before, raw) {
			return nil, ErrSetup
		}
		return data, nil
	}) != nil {
		return fail("WRITE_FAILED")
	}
	_ = s.Close()
	if *restart && activateConfiguredAgent(*scope, *unit) != 0 {
		return fail("CONFIGURED_SERVICE_NOT_STARTED_CHECK_READINESS")
	}
	fmt.Fprintln(out, "Resource configuration saved. Journal and identity unchanged.")
	if !*restart {
		fmt.Fprintln(out, "Restart the agent explicitly when ready.")
	}
	return 0
}
