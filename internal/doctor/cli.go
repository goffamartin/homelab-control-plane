package doctor

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"
)

// CLI returns 0 for reports without failures (warnings/skips are not readiness),
// 1 for diagnostic failures, and 2 for usage/output failures. No token is loaded.
func CLI(args []string, defaultRole string, out io.Writer) int {
	return runCLI(args, defaultRole, out, NativeHost{}, time.Now())
}

func runCLI(args []string, defaultRole string, out io.Writer, host Host, now time.Time) int {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	// Avoid echoing untrusted arguments or flag values on errors.
	flags.SetOutput(io.Discard)
	options := Options{}
	flags.StringVar(&options.Role, "role", defaultRole, "agent or controller")
	flags.StringVar(&options.Config, "config", "/etc/hlcp/agent.yaml", "absolute agent configuration path")
	flags.StringVar(&options.Certificate, "cert", "/etc/hlcp/controller/controller.crt", "absolute controller certificate path")
	flags.StringVar(&options.Key, "key", "/etc/hlcp/controller/controller.key", "absolute controller key path")
	flags.StringVar(&options.CA, "ca", "/etc/hlcp/controller/ca.crt", "absolute controller trust bundle path")
	flags.StringVar(&options.ServerName, "server-name", "", "controller hostname/IP to check")
	flags.BoolVar(&options.HostChecks, "host-checks", false, "read local clock and systemd readiness")
	flags.StringVar(&options.Scope, "scope", "system", "system or current non-root user service")
	flags.StringVar(&options.Unit, "unit", "", "hlcp-agent.service or legacy hlcp-agent-podman.service")
	jsonOutput := flags.Bool("json", false, "JSON report")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			_, err = fmt.Fprintln(out, "Usage: doctor [--role agent|controller] [--config /absolute/agent.yaml]\n  [--cert /absolute/cert] [--key /absolute/key] [--ca /absolute/ca]\n  [--server-name name] [--json] [--host-checks --scope system|user]\n  [--unit hlcp-agent.service|hlcp-agent-podman.service]\nRead-only Linux diagnostics. No configuration, service activation, scripts or remote network operations.")
			if err != nil {
				return 2
			}
			return 0
		}
		fmt.Fprintln(out, "Invalid doctor options. Use doctor --help.")
		return 2
	}
	if flags.NArg() != 0 || (options.Role != "agent" && options.Role != "controller") ||
		(options.Scope != "system" && options.Scope != "user") ||
		(options.Role == "controller" && (options.Scope != "system" || options.Unit != "")) ||
		(options.Unit != "" && options.Unit != "hlcp-agent.service" && options.Unit != "hlcp-agent-podman.service") {
		fmt.Fprintln(out, "Invalid doctor options. Use doctor --help.")
		return 2
	}
	report := Run(host, options, now)
	if *jsonOutput {
		if json.NewEncoder(out).Encode(report) != nil {
			return 2
		}
	} else {
		if _, err := fmt.Fprintln(out, "HLCP doctor (read-only, offline; skips are not verified readiness)"); err != nil {
			return 2
		}
		for _, check := range report.Checks {
			if _, err := fmt.Fprintf(out, "%s %s: %s\n", check.State, check.Code, check.Message); err != nil {
				return 2
			}
		}
	}
	if report.Failed() {
		return 1
	}
	return 0
}
