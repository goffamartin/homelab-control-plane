package managedcli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hlcp/internal/agent"
	"hlcp/internal/managed"
	"hlcp/internal/setup"
	"io"
	"os"
	"time"
)

func AgentCertificates(args []string, out io.Writer) int {
	refresh := len(args) > 0 && args[0] == "refresh-trust"
	if refresh {
		args = args[1:]
	}
	f := flag.NewFlagSet("certificates", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	defaultScope := "user"
	if os.Geteuid() == 0 {
		defaultScope = "system"
	}
	scope := f.String("scope", defaultScope, "user or system")
	apply := f.Bool("apply", false, "refresh verified controller trust without changing identity")
	yes := f.Bool("yes", false, "consent")
	jsonOut := f.Bool("json", has(args, "--json"), "versioned JSON diagnostics")
	phase := "options"
	fail := func(code string) int {
		return resultError(out, *jsonOut, "agent.certificates", phase, code, "Certificate operation did not complete.", "Inspect configuration and managed state; do not reset pairing or journal.")
	}
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			if *jsonOut {
				return emitResult(out, true, Result{Operation: "agent.certificates", Status: "ok", Code: "HELP", Phase: "options", Message: "Usage: hlcp-agent certificates [refresh-trust] [--scope user|system] [--json] [--apply --yes]. Status is read-only; stop the agent before applying trust refresh."})
			}
			fmt.Fprintln(out, "Trust cleanup: certificates refresh-trust [--scope user|system] --apply --yes, with agent stopped.")
			fmt.Fprintln(out, "Usage: hlcp-agent certificates [--scope user|system]\nRead-only expiry, renewal and retry diagnostics; works while the agent runs.")
			return 0
		}
		return fail("INVALID_OPTIONS")
	}
	if f.NArg() != 0 || *apply != *yes || (!refresh && *apply) {
		return fail("INVALID_OPTIONS")
	}
	phase = "account"
	p, err := pathsFor(*scope)
	if err != nil {
		return fail("SCOPE_OR_ACCOUNT_INVALID")
	}
	phase = "config_read"
	raw, err := readPrivate(p.directory+"/agent.yaml", p.owner)
	if err != nil {
		return fail("CONFIG_UNAVAILABLE")
	}
	c, err := agent.Parse(raw)
	if err != nil {
		return fail("CONFIG_INVALID")
	}
	if c.ManagedDirectory == "" {
		return emitResult(out, *jsonOut, Result{Operation: "agent.certificates", Status: "ok", Phase: "inspect", Code: "EXTERNAL_PKI", Message: "Imported certificates: renewal is managed externally."})
	}
	owner := p.owner
	if *scope == "system" {
		uid, gid, e := account("hlcp")
		if e != nil {
			return fail("SERVICE_ACCOUNT_UNAVAILABLE")
		}
		owner.UID = uint32(uid)
		owner.GID = uint32(gid)
	}
	if refresh {
		if !*apply {
			return emitResult(out, *jsonOut, Result{Operation: "agent.certificates", Status: "preview", Phase: "inspect", Code: "TRUST_REFRESH_PREVIEW", Message: "Read-only preview: refresh managed peer trust. Stop agent before apply; key, resources and journal remain unchanged."})
		}
		ctx, cancel := boundedContext()
		defer cancel()
		store, e := managed.OpenStoreAsOwner(c.ManagedDirectory, int(owner.UID), int(owner.GID))
		if e != nil {
			return fail("STATE_LOCKED_OR_UNSAFE")
		}
		defer store.Close()
		identity, e := managed.OpenAgentIdentity(store, c.NodeID)
		if e != nil {
			return fail("IDENTITY_INVALID")
		}
		if identity.RefreshTrust(ctx) != nil {
			return emitResult(out, *jsonOut, Result{Operation: "agent.certificates", Status: "uncertain", Code: "TRUST_REFRESH_INCOMPLETE", Phase: "refresh_trust", Uncertain: true, Message: "Trust refresh did not complete. Inspect retained identity state before retrying; no rollback was performed."})
		}
		return emitResult(out, *jsonOut, Result{Operation: "agent.certificates", Status: "ok", Code: "TRUST_REFRESHED", Phase: "refresh_trust", Changed: true, Completed: []string{"trust_refreshed"}, Message: "Verified peer trust refreshed. Start agent explicitly."})
	}
	phase = "identity_status"
	raw, err = readPrivate(c.ManagedDirectory+"/identity.json", owner)
	if err != nil {
		return fail("IDENTITY_UNAVAILABLE")
	}
	status, err := managed.IdentityStatus(raw, c.NodeID, time.Now())
	if err != nil {
		return fail("IDENTITY_STATUS_INVALID")
	}
	if !*jsonOut {
		fmt.Fprintf(out, "Managed certificates\n  Agent expires:  %s\n  Renewal from:   %s\n  Issuer expires: %s\n  Root expires:   %s\n  Retry failures: %d\n", status.Expires.UTC().Format(time.RFC3339), status.RenewAt.UTC().Format(time.RFC3339), status.IssuerExpires.UTC().Format(time.RFC3339), status.RootExpires.UTC().Format(time.RFC3339), status.Failures)
		if !status.NextAttempt.IsZero() {
			fmt.Fprintln(out, "  Next attempt:  ", status.NextAttempt.UTC().Format(time.RFC3339))
		}
		for _, warning := range []string{status.Warning, status.IssuerWarning, status.RootWarning} {
			if warning != "" {
				fmt.Fprintln(out, "  Warning:", warning)
			}
		}
		return 0
	}
	return emitResult(out, true, Result{Operation: "agent.certificates", Status: "ok", Code: "OK", Phase: "inspect", Message: "Managed certificate renewal status inspected.", Data: status})
}

func ControllerPKI(args []string, input io.Reader, out io.Writer) int {
	if len(args) > 0 && args[0] == "finish-migration" {
		return finishMigrationCLI(args[1:], out)
	}
	if len(args) == 0 || args[0] != "renew-issuer" {
		if has(args, "--json") {
			return resultError(out, true, "controller.pki", "options", "INVALID_OPTIONS", "Select renew-issuer or finish-migration. Nothing changed.", "Use the selected PKI command with --help --json.")
		}
		if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
			fmt.Fprintln(out, "Usage: hlcp-controller pki renew-issuer|finish-migration --help")
			return 0
		}
		fmt.Fprintln(out, "Usage: hlcp-controller pki renew-issuer --backup-file /private/root-backup.json [--password-stdin] [--apply --yes]\nStop the controller first. Retains root, issuer key, node registry and command history. Not a compromised-key rotation.")
		return 2
	}
	f := flag.NewFlagSet("renew-issuer", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	backup := f.String("backup-file", "", "encrypted root backup")
	stdin := f.Bool("password-stdin", false, "read backup password from stdin")
	apply := f.Bool("apply", false, "renew issuer")
	yes := f.Bool("yes", false, "consent")
	jsonOut := f.Bool("json", has(args, "--json"), "versioned JSON")
	progress := Result{Operation: "controller.pki.renew-issuer", Status: "failed", Phase: "preflight", Message: "Issuer renewal did not complete.", Next: "Inspect retained state and encrypted backup; do not replace CA or registry."}
	fail := func(code string) int {
		progress.Code = code
		if progress.Changed {
			progress.Status = "partial"
		}
		if progress.Uncertain {
			progress.Status = "uncertain"
		}
		return emitResult(out, *jsonOut, progress)
	}
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return emitResult(out, *jsonOut, Result{Operation: progress.Operation, Status: "ok", Code: "HELP", Phase: "options", Message: "Usage: hlcp-controller pki renew-issuer --backup-file /private/backup.json [--password-stdin] [--json] [--apply --yes]. Stop controller first."})
		}
		return fail("INVALID_OPTIONS")
	}
	if f.NArg() != 0 || *apply != *yes || os.Geteuid() != 0 || (*jsonOut && *apply && !*stdin) {
		return fail("INVALID_OPTIONS_OR_ACCOUNT")
	}
	if !*apply {
		return emitResult(out, *jsonOut, Result{Operation: progress.Operation, Status: "preview", Code: "RENEWAL_PREVIEW", Phase: "preflight", Message: "Read-only preview. Issuer renewal needs the encrypted root backup and stopped controller."})
	}
	phases := newWorkflowContext()
	defer phases.close()
	ctx := phases.operation()
	uid, gid, err := account("hlcp-controller")
	if err != nil {
		return fail("SERVICE_ACCOUNT_UNAVAILABLE")
	}
	s, err := managed.OpenStoreAsOwner(controllerState, uid, gid)
	if err != nil {
		return fail("STATE_LOCKED_OR_UNSAFE")
	}
	defer s.Close()
	raw, err := s.Read("authority.json")
	if err != nil {
		return fail("AUTHORITY_UNAVAILABLE")
	}
	var material managed.AuthorityMaterial
	if managed.DecodeJSON(raw, &material, 64<<10) != nil {
		return fail("AUTHORITY_INVALID")
	}
	archived, err := readPrivate(*backup, setup.Owner{UID: 0, GID: 0})
	if err != nil {
		return fail("BACKUP_UNAVAILABLE")
	}
	ui := newTerminal(input, out)
	if !*stdin && !ui.interactive {
		return fail("PASSWORD_INPUT_REQUIRED")
	}
	if *stdin {
		ui = newTerminal(input, io.Discard)
	} else {
		ctx = phases.input()
	}
	password, err := ui.secret(ctx, "Root backup password: ")
	if err != nil {
		return fail("PASSWORD_INPUT_FAILED")
	}
	ctx = phases.operation()
	a, err := managed.RenewIssuer(material, archived, password, time.Now())
	if err != nil {
		return fail("BACKUP_PASSWORD_OR_VALIDITY_MISMATCH")
	}
	progress.Phase = "save_recovery_authority"
	progress.Uncertain = true
	if s.Update(ctx, "authority-before-renewal.json", func([]byte) ([]byte, error) { return raw, nil }) != nil {
		return fail("RECOVERY_BACKUP_UNVERIFIED")
	}
	progress.Changed = true
	progress.Uncertain = false
	progress.Completed = []string{"previous_authority_saved"}
	root, issuer, key := a.Material()
	next, _ := json.Marshal(managed.AuthorityMaterial{Root: root, Issuer: issuer, Key: key})
	progress.Phase = "save_renewed_authority"
	progress.Uncertain = true
	if s.Update(ctx, "authority.json", func([]byte) ([]byte, error) { return next, nil }) != nil {
		return fail("AUTHORITY_WRITE_UNVERIFIED")
	}
	progress.Uncertain = false
	progress.Status = "ok"
	progress.Code = "ISSUER_RENEWED"
	progress.Completed = append(progress.Completed, "issuer_renewed")
	progress.Message = "Issuer renewed. Root, registry and history preserved. Start controller explicitly."
	progress.Next = ""
	return emitResult(out, *jsonOut, progress)
}

func finishMigrationCLI(args []string, out io.Writer) int {
	f := flag.NewFlagSet("finish-migration", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	apply := f.Bool("apply", false, "remove explicit legacy trust after all nodes migrate or are revoked")
	yes := f.Bool("yes", false, "consent")
	jsonOut := f.Bool("json", has(args, "--json"), "versioned JSON")
	fail := func(code string) int {
		return resultError(out, *jsonOut, "controller.pki.finish-migration", "preflight", code, "Migration finalization did not proceed.", "Stop controller and inspect state; do not delete history or CA.")
	}
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return emitResult(out, *jsonOut, Result{Operation: "controller.pki.finish-migration", Status: "ok", Code: "HELP", Phase: "options", Message: "Usage: hlcp-controller pki finish-migration [--json] [--apply --yes]. Stop controller first; refuses remaining legacy nodes/pending joins. Retains history, registry and root."})
		}
		return fail("INVALID_OPTIONS")
	}
	if f.NArg() != 0 || *apply != *yes || os.Geteuid() != 0 {
		return fail("INVALID_OPTIONS_OR_ACCOUNT")
	}
	if !*apply {
		return emitResult(out, *jsonOut, Result{Operation: "controller.pki.finish-migration", Status: "preview", Code: "MIGRATION_FINISH_PREVIEW", Phase: "preflight", Message: "Read-only preview: finish migration, then refresh agent trust explicitly."})
	}
	uid, gid, err := account("hlcp-controller")
	if err != nil {
		return fail("SERVICE_ACCOUNT_UNAVAILABLE")
	}
	store, err := managed.OpenStoreAsOwner(controllerState, uid, gid)
	if err != nil {
		return fail("STATE_LOCKED_OR_UNSAFE")
	}
	defer store.Close()
	ctx, cancel := boundedContext()
	defer cancel()
	if managed.FinishMigration(ctx, store) != nil {
		return emitResult(out, *jsonOut, Result{Operation: "controller.pki.finish-migration", Status: "uncertain", Code: "MIGRATION_FINISH_UNVERIFIED", Phase: "finish_migration", Uncertain: true, Message: "Migration finalization did not complete. Legacy nodes/pending joins, invalid state or a write failure may be responsible. Inspect retained state before retrying."})
	}
	return emitResult(out, *jsonOut, Result{Operation: "controller.pki.finish-migration", Status: "ok", Code: "MIGRATION_FINISHED", Phase: "finish_migration", Changed: true, Completed: []string{"legacy_trust_removed"}, Message: "Legacy server trust removed. Start controller, then stop/refresh-trust/start each managed agent."})
}
