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
	f := flag.NewFlagSet("certificates", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	defaultScope := "user"
	if os.Geteuid() == 0 {
		defaultScope = "system"
	}
	scope := f.String("scope", defaultScope, "user or system")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(out, "Usage: hlcp-agent certificates [--scope user|system]\nRead-only expiry, renewal and retry diagnostics; works while the agent runs.")
			return 0
		}
		return 2
	}
	if f.NArg() != 0 {
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
	if len(args) == 0 || args[0] != "renew-issuer" {
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
	ctx, cancel := boundedContext()
	defer cancel()
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
	}
	password, err := ui.secret(ctx, "Root backup password: ")
	if err != nil {
		return 1
	}
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
