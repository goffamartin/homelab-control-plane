package managedcli

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/user"
	"strconv"
	"strings"
)

func checkLinger(ctxContext context.Context) error {
	data, err := fixedProcess(ctxContext, "/usr/bin/loginctl", []string{"show-user", strconv.Itoa(os.Geteuid()), "--property=Linger", "--value"}, "")
	if err != nil || strings.TrimSpace(string(data)) != "yes" {
		return ErrSetup
	}
	return nil
}
func PrepareUser(args []string, out io.Writer) int {
	f := flag.NewFlagSet("prepare-user", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	name := f.String("user", "", "explicit local non-root user")
	apply := f.Bool("apply", false, "enable linger")
	yes := f.Bool("yes", false, "confirm")
	jsonOut := f.Bool("json", has(args, "--json"), "versioned JSON")
	fail := func(code string) int {
		return resultError(out, *jsonOut, "agent.prepare-user", "preflight", code, "User-manager preparation did not proceed.", "Inspect the selected account and loginctl; no implicit user impersonation is performed.")
	}
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return emitResult(out, *jsonOut, Result{Operation: "agent.prepare-user", Status: "ok", Code: "HELP", Phase: "options", Message: "Usage: sudo hlcp-agent prepare-user --user ACCOUNT [--apply --yes] [--json]. Enables linger; never impersonates the user or creates containers/keys."})
		}
		return fail("INVALID_OPTIONS")
	}
	if f.NArg() != 0 || *apply != *yes || os.Geteuid() != 0 || *name == "" {
		return fail("INVALID_OPTIONS_OR_ACCOUNT")
	}
	u, err := user.Lookup(*name)
	if err != nil || u.Uid == "0" || u.HomeDir == "/nonexistent" || u.Username != *name {
		return fail("ACCOUNT_INVALID")
	}
	if !*apply {
		return emitResult(out, *jsonOut, Result{Operation: "agent.prepare-user", Status: "preview", Code: "LINGER_PREVIEW", Phase: "preflight", Message: "Read-only preview: enable the selected user's manager at boot."})
	}
	ctx, cancel := boundedContext()
	defer cancel()
	if _, err = fixedProcess(ctx, "/usr/bin/loginctl", []string{"enable-linger", *name}, ""); err != nil {
		return emitResult(out, *jsonOut, Result{Operation: "agent.prepare-user", Status: "uncertain", Code: "LINGER_ENABLE_UNVERIFIED", Phase: "enable_linger", Uncertain: true, Message: "Linger enable did not complete cleanly. Inspect loginctl before retrying."})
	}
	data, err := fixedProcess(ctx, "/usr/bin/loginctl", []string{"show-user", *name, "--property=Linger", "--value"}, "")
	if err != nil || strings.TrimSpace(string(data)) != "yes" {
		return emitResult(out, *jsonOut, Result{Operation: "agent.prepare-user", Status: "uncertain", Code: "LINGER_VERIFICATION_FAILED", Phase: "verify_linger", Changed: true, Uncertain: true, Completed: []string{"enable_linger_returned_success"}, Message: "Linger enable returned success, but the resulting state could not be verified. Inspect loginctl."})
	}
	return emitResult(out, *jsonOut, Result{Operation: "agent.prepare-user", Status: "ok", Code: "LINGER_ENABLED", Phase: "verify_linger", Changed: true, Completed: []string{"linger_enabled_and_verified"}, Message: "User manager enabled at boot."})
}
