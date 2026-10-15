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
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(out, "Trust cleanup: certificates refresh-trust [--scope user|system] --apply --yes, with agent stopped.")
			fmt.Fprintln(out, "Usage: hlcp-agent certificates [--scope user|system]\nRead-only expiry, renewal and retry diagnostics; works while the agent runs.")
			return 0
		}
		return 2
	}
	if f.NArg() != 0 || *apply != *yes || (!refresh && *apply) {
		return 2
	}
	p, err := pathsFor(*scope)
	if err != nil {
		return 1
	}
	raw, err := readPrivate(p.directory+"/agent.yaml", p.owner)
	if err != nil {
		return 1
	}
	c, err := agent.Parse(raw)
	if err != nil {
		return 1
	}
	if c.ManagedDirectory == "" {
		fmt.Fprintln(out, "Imported certificates: renewal is managed externally.")
		return 0
	}
	owner := p.owner
	if *scope == "system" {
		uid, gid, e := account("hlcp")
		if e != nil {
			return 1
		}
		owner.UID = uint32(uid)
		owner.GID = uint32(gid)
	}
	if refresh {
		if !*apply {
			fmt.Fprintln(out, "Read-only preview: refresh managed peer trust. Stop agent before apply; key, resources and journal remain unchanged.")
			return 0
		}
		ctx, cancel := boundedContext()
		defer cancel()
		store, e := managed.OpenStoreAsOwner(c.ManagedDirectory, int(owner.UID), int(owner.GID))
		if e != nil {
			fmt.Fprintln(out, "Stop agent first; managed state must be private and unlocked.")
			return 1
		}
		defer store.Close()
		identity, e := managed.OpenAgentIdentity(store, c.NodeID)
		if e != nil || identity.RefreshTrust(ctx) != nil {
			return 1
		}
		fmt.Fprintln(out, "Verified peer trust refreshed. Start agent explicitly.")
		return 0
	}
	raw, err = readPrivate(c.ManagedDirectory+"/identity.json", owner)
	if err != nil {
		return 1
	}
	status, err := managed.IdentityStatus(raw, c.NodeID, time.Now())
	if err != nil {
		return 1
	}
	if json.NewEncoder(out).Encode(status) != nil {
		return 1
	}
	return 0
}

func ControllerPKI(args []string, input io.Reader, out io.Writer) int {
	if len(args) > 0 && args[0] == "finish-migration" {
		return finishMigrationCLI(args[1:], out)
	}
	if len(args) == 0 || args[0] != "renew-issuer" {
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
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(out, "Usage: hlcp-controller pki renew-issuer --backup-file /private/backup.json [--password-stdin] [--apply --yes]")
			return 0
		}
		return 2
	}
	if f.NArg() != 0 || *apply != *yes || os.Geteuid() != 0 {
		return 2
	}
	if !*apply {
		fmt.Fprintln(out, "Read-only preview. Issuer renewal needs the encrypted root backup and stopped controller.")
		return 0
	}
	phases := newWorkflowContext()
	defer phases.close()
	ctx := phases.operation()
	uid, gid, err := account("hlcp-controller")
	if err != nil {
		return 1
	}
	s, err := managed.OpenStoreAsOwner(controllerState, uid, gid)
	if err != nil {
		fmt.Fprintln(out, "Stop controller first; managed state must be private and unlocked.")
		return 1
	}
	defer s.Close()
	raw, err := s.Read("authority.json")
	if err != nil {
		return 1
	}
	var material managed.AuthorityMaterial
	if managed.DecodeJSON(raw, &material, 64<<10) != nil {
		return 1
	}
	archived, err := readPrivate(*backup, setup.Owner{UID: 0, GID: 0})
	if err != nil {
		return 1
	}
	ui := newTerminal(input, out)
	if !*stdin && !ui.interactive {
		fmt.Fprintln(out, "Interactive terminal or --password-stdin required.")
		return 1
	}
	if *stdin {
		ui = newTerminal(input, io.Discard)
	} else {
		ctx = phases.input()
	}
	password, err := ui.secret(ctx, "Root backup password: ")
	if err != nil {
		return 1
	}
	ctx = phases.operation()
	a, err := managed.RenewIssuer(material, archived, password, time.Now())
	if err != nil {
		fmt.Fprintln(out, "Issuer renewal refused; root backup/password/validity must match.")
		return 1
	}
	if s.Update(ctx, "authority-before-renewal.json", func([]byte) ([]byte, error) { return raw, nil }) != nil {
		return 1
	}
	root, issuer, key := a.Material()
	next, _ := json.Marshal(managed.AuthorityMaterial{Root: root, Issuer: issuer, Key: key})
	if s.Update(ctx, "authority.json", func([]byte) ([]byte, error) { return next, nil }) != nil {
		return 1
	}
	fmt.Fprintln(out, "Issuer renewed. Root, registry and history preserved. Start controller explicitly.")
	return 0
}

func finishMigrationCLI(args []string, out io.Writer) int {
	f := flag.NewFlagSet("finish-migration", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	apply := f.Bool("apply", false, "remove explicit legacy trust after all nodes migrate or are revoked")
	yes := f.Bool("yes", false, "consent")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(out, "Usage: hlcp-controller pki finish-migration [--apply --yes]\nStop controller first. Refuses remaining active legacy nodes or pending joins. Retains backup, root, registry and ledger; restart explicitly.")
			return 0
		}
		return 2
	}
	if f.NArg() != 0 || *apply != *yes || os.Geteuid() != 0 {
		return 2
	}
	if !*apply {
		fmt.Fprintln(out, "Read-only preview: finish imported-to-managed migration, then refresh agent trust explicitly.")
		return 0
	}
	uid, gid, err := account("hlcp-controller")
	if err != nil {
		return 1
	}
	store, err := managed.OpenStoreAsOwner(controllerState, uid, gid)
	if err != nil {
		fmt.Fprintln(out, "Stop controller first; state must be private and unlocked.")
		return 1
	}
	defer store.Close()
	ctx, cancel := boundedContext()
	defer cancel()
	if managed.FinishMigration(ctx, store) != nil {
		fmt.Fprintln(out, "Migration finish refused: legacy nodes/pending join remain or state invalid.")
		return 1
	}
	fmt.Fprintln(out, "Legacy server trust removed. Start controller, then stop/refresh-trust/start each managed agent.")
	return 0
}
