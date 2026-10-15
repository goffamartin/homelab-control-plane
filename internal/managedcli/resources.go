package managedcli

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"hlcp/internal/agent"
	"io"
	"os"
	"strings"
)

// Resources is read-only. YAML is the single source of truth; mutation flags
// from older previews are explicitly rejected, never silently ignored.
func Resources(args []string, _ io.Reader, out io.Writer) int {
	jsonMode := has(args, "--json")
	action := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = args[0]
		args = args[1:]
	}
	if action == "configure" || action == "remove" {
		return resultError(out, jsonMode, "agent.resources", "options", "COMMAND_REMOVED", "Resource configuration was not changed. Edit the agent YAML instead.", "hlcp-agent configure; hlcp-agent check-config; then restart the selected systemd service explicitly.")
	}
	f := flag.NewFlagSet("resources", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	defaultScope := "user"
	if os.Geteuid() == 0 {
		defaultScope = "system"
	}
	scope := f.String("scope", defaultScope, "user or system")
	adapter := f.String("adapter", "", "podman or docker for discovery")
	f.BoolVar(&jsonMode, "json", jsonMode, "one versioned JSON result; no interactive prompts")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return emitResult(out, jsonMode, Result{Operation: "agent.resources", Status: "ok", Code: "HELP", Phase: "options", Message: "Usage: hlcp-agent resources [list|discover --adapter podman|docker] [--scope user|system] [--json]. Read-only; edit YAML with hlcp-agent configure."})
		}
		return resultError(out, jsonMode, "agent.resources", "options", "INVALID_OPTIONS", "Invalid resource command. No configuration changed.", "hlcp-agent resources --help")
	}
	if f.NArg() != 0 || (action != "list" && action != "discover") || (action == "list" && *adapter != "") {
		return resultError(out, jsonMode, "agent.resources", "options", "INVALID_OPTIONS", "Use list or discover. No configuration changed.", "hlcp-agent resources --help")
	}
	p, err := pathsFor(*scope)
	if err != nil {
		return resultError(out, jsonMode, "agent.resources", "account", "SCOPE_OR_ACCOUNT_INVALID", "Cannot inspect resources for the selected account/scope.", "Run user-scope commands as the actual container owner, without sudo.")
	}
	if action == "discover" {
		if os.Geteuid() == 0 || (*adapter != "podman" && *adapter != "docker") {
			return resultError(out, jsonMode, "agent.resources.discover", "options", "RESOURCE_OWNER_REQUIRED", "Discovery requires podman/docker and the actual non-root container owner.", "Run as the container owner without sudo; specify --adapter podman or docker.")
		}
		ctx, cancel := boundedContext()
		defer cancel()
		data, err := discoverContainers(ctx, *adapter)
		if err != nil {
			return resultError(out, jsonMode, "agent.resources.discover", "runtime", "RUNTIME_UNAVAILABLE", "Cannot inspect the selected container runtime. No configuration or containers changed.", "Check that the runtime is installed and accessible to this user.")
		}
		names := []string{}
		for _, line := range bytes.Split(data, []byte{'\n'}) {
			if name := strings.TrimSpace(string(line)); name != "" {
				names = append(names, name)
			}
		}
		if !jsonMode {
			for _, name := range names {
				fmt.Fprintln(out, name)
			}
			if len(names) == 0 {
				fmt.Fprintln(out, "No containers found.")
			}
			return 0
		}
		return emitResult(out, true, Result{Operation: "agent.resources.discover", Status: "ok", Code: "OK", Phase: "inspect", Message: "Container names inspected. Nothing was enabled or executed.", Data: map[string]any{"adapter": *adapter, "containers": names}})
	}
	raw, err := readPrivate(p.directory+"/agent.yaml", p.owner)
	if err != nil {
		return resultError(out, jsonMode, "agent.resources.list", "read", "CONFIG_UNAVAILABLE", "Cannot read the private agent YAML.", "Check the configuration path and ownership; do not delete identity/state.")
	}
	c, err := agent.Parse(raw)
	if err != nil {
		return resultError(out, jsonMode, "agent.resources.list", "validate", "CONFIG_INVALID", "Agent YAML is invalid. No configuration changed.", "hlcp-agent check-config")
	}
	type resource struct {
		ID      string   `json:"id"`
		Type    string   `json:"type"`
		Adapter string   `json:"adapter"`
		Target  string   `json:"target"`
		Actions []string `json:"actions"`
	}
	items := []resource{}
	for _, r := range c.Resources {
		items = append(items, resource{r.ID, r.Type, r.Adapter, r.Target, r.Actions})
	}
	if !jsonMode {
		if len(items) == 0 {
			fmt.Fprintln(out, "No resources configured.")
		}
		for _, r := range items {
			fmt.Fprintf(out, "%s  %s  %v\n", r.ID, r.Adapter, r.Actions)
		}
		return 0
	}
	return emitResult(out, true, Result{Operation: "agent.resources.list", Status: "ok", Code: "OK", Phase: "inspect", Message: "Configured resource metadata inspected.", Data: map[string]any{"resources": items}})
}
