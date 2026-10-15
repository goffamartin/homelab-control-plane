package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hlcp/internal/setup"
	"io"
	"os"
	"os/signal"
	"syscall"
)

type clientGrantReport struct {
	Status   string             `json:"status"`
	Code     string             `json:"code,omitempty"`
	ReadOnly bool               `json:"read_only"`
	Preview  *setup.Preview     `json:"preview,omitempty"`
	Result   *setup.ApplyResult `json:"result,omitempty"`
	Path     string             `json:"path,omitempty"`
	Group    uint32             `json:"group,omitempty"`
}

type ClientAccessReport = clientGrantReport

// ClientPreflight runs the normal user-side profile preview without requiring
// a CA/token to exist and never exports a bearer or mutates user paths.
func ClientPreflight(ctx context.Context, name string, trust bool) ClientAccessReport {
	return nativeClientGrantWithOptions(ctx, name, "operator", false, trust, true)
}

func ClientGrantCLI(args []string, out io.Writer) int {
	return runClientGrantCLI(args, out, func(ctx context.Context, name, role string, apply, trust bool) clientGrantReport {
		return nativeClientGrantWithOptions(ctx, name, role, apply, trust, false)
	})
}
func runClientGrantCLI(args []string, out io.Writer, grant func(context.Context, string, string, bool, bool) clientGrantReport) int {
	f := flag.NewFlagSet("configure-client", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	user := f.String("user", "", "explicit local non-root account")
	role := f.String("role", "operator", "existing operator or observer access")
	apply := f.Bool("apply", false, "apply user CLI plan")
	yes := f.Bool("yes", false, "explicit access export and file-write consent")
	trust := f.Bool("trust-user-group", false, "explicitly trust group-writable ancestors owned by this user and their primary group; no chmod")
	jsonOutput := f.Bool("json", false, "redacted JSON report")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(out, "Optional: --trust-user-group explicitly accepts primary-group-writable user-owned ancestors only; no chmod and no relaxation of private profile/file permissions.")
			if _, err = fmt.Fprintln(out, "Usage: hlcp-controller configure-client --user account [--role operator|observer] [--json] [--apply --yes]\nDefault: preview without exporting the bearer; role defaults to operator. Writes run as the selected user only.\nRequires installed controller and an existing safe user home. Missing .config/hlcp directories are created only after consent. No overwrite, rotation, policy or service changes."); err == nil {
				return 0
			}
		}
		fmt.Fprintln(out, "Invalid client setup options. Use configure-client --help.")
		return 2
	}
	if f.NArg() != 0 || *user == "" || len(*user) > 128 || *apply != *yes || (*role != "operator" && *role != "observer") {
		fmt.Fprintln(out, "Invalid client setup options. Use configure-client --help.")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if !*jsonOutput {
		if _, err := fmt.Fprintf(out, "Selected account %q; existing %s access.\n", *user, *role); err != nil {
			return 2
		}
	}
	report := grant(ctx, *user, *role, *apply, *trust)
	var err error
	if *jsonOutput {
		err = json.NewEncoder(out).Encode(report)
	} else {
		_, err = fmt.Fprintf(out, "User CLI setup: %s", report.Status)
		if err == nil && report.Code != "" {
			_, err = fmt.Fprintf(out, " (%s)", report.Code)
		}
		if err == nil {
			_, err = fmt.Fprintln(out)
		}
		if err == nil && report.Path != "" {
			_, err = fmt.Fprintf(out, "Path: %s (group ID %d).\n", report.Path, report.Group)
		}
		if err == nil && report.Code == "GROUP_WRITABLE_PARENT" {
			_, err = fmt.Fprintln(out, "Selected user's primary group can modify this parent. If you trust its members, retry with --trust-user-group; no permissions will be changed.")
		}
		if err == nil && report.Preview != nil {
			for _, c := range report.Preview.Changes {
				if _, err = fmt.Fprintf(out, "%s %s %04o\n", c.Action, c.Name, c.Mode); err != nil {
					break
				}
			}
		}
		if err == nil && report.Status == "applied" {
			_, err = fmt.Fprintln(out, "Client configured. As the selected user, try: hlcp -n")
		}
		if err == nil && report.Status == "failed" && !report.ReadOnly {
			_, err = fmt.Fprintln(out, "Access may already have been exported or files changed. Inspect actual state before retry; no rollback was performed.")
		}
	}
	if err != nil {
		return 2
	}
	if report.Status == "failed" {
		return 1
	}
	return 0
}

func grantFailure(code string, readOnly bool) clientGrantReport {
	return clientGrantReport{Status: "failed", Code: code, ReadOnly: readOnly}
}
