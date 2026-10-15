package managedcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hlcp/internal/managed"
	"hlcp/internal/onboarding"
	"hlcp/internal/setup"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const controllerDirectory = "/etc/hlcp/controller"
const controllerState = "/var/lib/hlcp-controller/managed"

type controllerBootstrap struct {
	Version     int                   `json:"version"`
	Config      managed.RuntimeConfig `json:"config"`
	Seed        []byte                `json:"seed"`
	BackupPath  string                `json:"backup_path"`
	Provisioned bool                  `json:"provisioned,omitempty"`
}

func readPrivate(path string, owner setup.Owner) ([]byte, error) {
	if _, err := os.Lstat(filepath.Dir(path)); os.IsNotExist(err) {
		return nil, os.ErrNotExist
	}
	p, err := setup.OpenPlanner(filepath.Dir(path), owner)
	if err != nil {
		return nil, ErrSetup
	}
	defer p.Close()
	snapshot, err := p.Snapshot(filepath.Base(path), setup.MaximumFileBytes)
	if err != nil {
		return nil, ErrSetup
	}
	if !snapshot.Exists {
		return nil, os.ErrNotExist
	}
	if snapshot.Mode != 0600 {
		return nil, ErrSetup
	}
	return snapshot.Data, nil
}
func putSame(ctx context.Context, s *managed.Store, name string, data []byte) error {
	if before, err := s.Read(name); err == nil {
		if !bytes.Equal(before, data) {
			return ErrSetup
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return s.Create(ctx, name, data)
}
func recoverSetup(ctx context.Context, directory string, owner setup.Owner, resume bool) error {
	p, err := setup.OpenPlanner(directory, owner)
	if err != nil {
		return err
	}
	defer p.Close()
	status, err := p.Inspect(ctx)
	if err != nil {
		return err
	}
	if status.State == "none" || status.State == "committed" {
		return nil
	}
	if !resume {
		return ErrSetup
	}
	_, err = p.Recover(ctx, true)
	return err
}
func ControllerSetup(args []string, input io.Reader, out io.Writer) int {
	f := flag.NewFlagSet("setup", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	managedMode := f.Bool("managed", true, "managed PKI")
	migrate := f.Bool("migrate", false, "explicit imported-to-managed trust overlap; preserve old server credentials and node IDs")
	offline := f.Bool("offline-stopped", false, "explicit stopped-service confirmation for non-booted images only")
	interactive := f.Bool("interactive", false, "interactive managed setup")
	listen := f.String("listen", "", "explicit controller IP:port")
	name := f.String("server-name", "", "controller DNS/IP")
	enrollment := f.String("enrollment-listen", "", "private enrollment IP:port (default management IP:9445)")
	address := f.String("enrollment-url", "", "agent-visible HTTPS enrollment URL")
	directory := f.String("directory", controllerDirectory, "private root config directory")
	state := f.String("state-directory", controllerState, "private runtime-managed directory")
	backup := f.String("backup-file", "", "encrypted root backup file (private root-owned parent)")
	passwordStdin := f.Bool("backup-password-stdin", false, "read one backup password line, never argv")
	backupConfirmed := f.Bool("backup-confirmed", false, "explicit acknowledgement that backup must be saved off-host")
	cliUser := f.String("cli-user", "", "explicit existing non-root CLI account")
	start := f.Bool("start", false, "start controller after configuration")
	enable := f.Bool("enable", false, "enable boot startup after verified start")
	resume := f.Bool("resume", false, "explicitly recover interrupted local setup transaction")
	apply := f.Bool("apply", false, "write managed setup")
	yes := f.Bool("yes", false, "explicit write consent")
	jsonOutput := f.Bool("json", false, "redacted JSON result")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(out, "Migration: setup --migrate --interactive (stopped imported controller, explicit trust overlap). Non-booted images alone may use --offline-stopped without activation.")
			fmt.Fprintln(out, "Usage: hlcp-controller setup [--managed] --interactive\nOr: --listen IP:9443 --server-name DNS/IP [--enrollment-listen IP:9445] [--enrollment-url https://DNS:9445]\n  [--cli-user account] [--backup-file /private/root-backup.json] [--backup-password-stdin --backup-confirmed]\n  [--start [--enable]] [--resume] [--json] [--apply --yes]\nDefault noninteractive mode is read-only preview. Managed setup creates CA/certificates automatically.\nEncrypted root backup needs a >=16-character password and off-host retention. No CA key in argv.\nImported mode remains: setup --imported with explicit certificate options.")
			return 0
		}
		fmt.Fprintln(out, "Invalid managed setup options.")
		return 2
	}
	ui := newTerminal(input, out)
	if len(args) == 0 {
		*interactive = true
	}
	if f.NArg() != 0 || !*managedMode || (*interactive && (!ui.interactive || *apply || *yes || *jsonOutput)) || (*apply != *yes) || (*enable && !*start) {
		fmt.Fprintln(out, "Invalid setup options or interactive terminal unavailable. Use --help.")
		return 2
	}
	u, uid, gid, err := caller()
	if err != nil || uid != 0 || gid != 0 || runtime.GOOS != "linux" {
		fmt.Fprintln(out, "Managed controller setup requires local Linux root.")
		return 1
	}
	ctx, cancel := boundedContext()
	defer cancel()
	if *offline && (*start || !offlineImage()) {
		fmt.Fprintln(out, "Offline mode is only for stopped non-booted images, never service activation.")
		return 2
	}
	fail := func(code string) int {
		if *jsonOutput {
			_ = json.NewEncoder(out).Encode(map[string]any{"status": "failed", "code": code, "preserved": true})
		} else {
			fmt.Fprintln(out, "Managed setup refused:", code, "Existing state preserved; inspect before retry.")
		}
		return 1
	}
	if *interactive {
		if *listen == "" {
			*listen, err = ui.line("Controller IP:port (for example 192.168.0.15:9443): ")
			if err != nil {
				return fail("INPUT_REQUIRED")
			}
		}
		host, _, _ := net.SplitHostPort(*listen)
		if *name == "" {
			value, e := ui.line("Controller DNS/IP [" + host + "]: ")
			if e != nil {
				return fail("INPUT_REQUIRED")
			}
			*name = value
			if *name == "" {
				*name = host
			}
		}
		if *cliUser == "" {
			*cliUser, err = ui.line("Existing non-root account for hlcp CLI (blank to skip): ")
			if err != nil {
				return fail("INPUT_REQUIRED")
			}
		}
		value, e := ui.line("Start controller after setup? [yes/no]: ")
		if e != nil {
			return fail("INPUT_REQUIRED")
		}
		*start = affirmative(value)
		if *start {
			value, e = ui.line("Enable controller after successful start? [yes/no]: ")
			if e != nil {
				return fail("INPUT_REQUIRED")
			}
			*enable = affirmative(value)
		}
	}
	host, _, err := net.SplitHostPort(*listen)
	if err != nil {
		return fail("INVALID_LISTENER")
	}
	if *enrollment == "" {
		*enrollment = net.JoinHostPort(host, "9445")
	}
	if *address == "" {
		*address = "https://" + net.JoinHostPort(*name, "9445")
	}
	config := managed.RuntimeConfig{Version: 1, Listen: *listen, ServerName: *name, EnrollmentListen: *enrollment, EnrollmentURL: *address}
	var importedIDs []string
	if *migrate {
		owner := setup.Owner{UID: 0, GID: 0}
		chain, e1 := readPrivate(filepath.Join(*directory, "controller.crt"), owner)
		key, e2 := readPrivate(filepath.Join(*directory, "controller.key"), owner)
		trust, e3 := readPrivate(filepath.Join(*directory, "ca.crt"), owner)
		nodes, e4 := readPrivate(filepath.Join(*directory, "nodes.json"), owner)
		environment, e5 := readPrivate(filepath.Join(*directory, "controller.env"), owner)
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil || managed.VerifyLegacyServer(chain, key, trust, *name, time.Now()) != nil || managed.DecodeJSON(nodes, &importedIDs, 64<<10) != nil || importedIDs == nil || environmentListener(environment) != *listen {
			return fail("IMPORTED_MIGRATION_PREFLIGHT_REFUSED")
		}
		config.LegacyServer = true
		config.LegacyTrust = string(trust)
	}
	if config.Validate() != nil || !filepath.IsAbs(*directory) || filepath.Clean(*directory) != *directory || !filepath.IsAbs(*state) || filepath.Clean(*state) != *state || strings.ContainsAny(*state, "\r\n\x00") {
		return fail("INVALID_CONFIGURATION")
	}
	if *start && (*directory != controllerDirectory || *state != controllerState) {
		return fail("SERVICE_REQUIRES_PACKAGED_PATHS")
	}
	if *backup == "" {
		*backup = filepath.Join(u.HomeDir, "hlcp-root-backup.json")
	}
	if !filepath.IsAbs(*backup) || filepath.Clean(*backup) != *backup {
		return fail("INVALID_BACKUP_PATH")
	}
	if *interactive {
		fmt.Fprintf(out, "Plan: managed PKI, controller %s, enrollment %s, encrypted backup %s, start=%t, boot=%t.\n", *listen, *address, *backup, *start, *enable)
		answer, e := ui.line("Apply this configuration? [yes/no]: ")
		if e != nil || !affirmative(answer) {
			fmt.Fprintln(out, "No changes applied.")
			return 0
		}
		*apply = true
		*yes = true
	}
	if !*apply {
		if *jsonOutput {
			_ = json.NewEncoder(out).Encode(map[string]any{"status": "preview", "read_only": true, "managed": true, "start": *start, "enable": *enable})
		} else {
			fmt.Fprintln(out, "Read-only managed setup preview. No CA, keys, tokens or files generated.")
		}
		return 0
	}
	var serviceBefore serviceState
	if (*start || *migrate) && !*offline {
		var err error
		if serviceBefore, err = inspectService(ctx, "system", "hlcp-controller.service"); err != nil {
			return fail("SERVICE_PREFLIGHT_REFUSED")
		}
	}
	if err := ensurePrivateDirectory(*directory, 0, 0); err != nil {
		return fail("CONFIG_DIRECTORY_UNSAFE")
	}
	rootStore, err := managed.OpenStore(*directory)
	if err != nil {
		return fail("CONFIG_STORE_UNAVAILABLE")
	}
	defer rootStore.Close()
	// Completed setup does not re-run seed provisioning or reset live registry.
	mode, modeErr := rootStore.Read("managed.env")
	if modeErr == nil {
		data, err := rootStore.Read("managed-bootstrap.json")
		var saved controllerBootstrap
		if err != nil || managed.DecodeJSON(data, &saved, 512<<10) != nil || saved.Config != config {
			return fail("EXISTING_CONFIGURATION_CONFLICT")
		}
		if !bytes.Equal(mode, []byte("HLCP_MANAGED_STATE="+*state+"\n")) {
			return fail("EXISTING_MODE_CONFLICT")
		}
		if *start && activateService(ctx, "system", "hlcp-controller.service", *enable) != nil {
			return fail("ACTIVATION_UNCERTAIN")
		}
		if *cliUser != "" && onboarding.ClientGrantCLI([]string{"--user", *cliUser, "--apply", "--yes"}, io.Discard) != 0 {
			return fail("CLIENT_SETUP_INCOMPLETE")
		}
		if *jsonOutput {
			_ = json.NewEncoder(out).Encode(map[string]any{"status": "configured", "unchanged": true, "managed": true})
		} else {
			fmt.Fprintln(out, "Managed controller already configured; identity and registry preserved.")
		}
		return 0
	}
	if !os.IsNotExist(modeErr) {
		return fail("MANAGED_MODE_INVALID")
	}
	if (*start || *migrate) && !*offline && serviceBefore.active != "inactive" {
		return fail("STOP_CONTROLLER_BEFORE_PROVISIONING")
	}
	password := ""
	if *interactive {
		password, err = ui.secret(ctx, "Root backup password (at least 16 characters): ")
		if err != nil {
			return fail("PASSWORD_REQUIRED")
		}
		again, e := ui.secret(ctx, "Repeat root backup password: ")
		if e != nil || again != password {
			return fail("PASSWORD_MISMATCH")
		}
	} else {
		if !*passwordStdin || !*backupConfirmed {
			return fail("BACKUP_PASSWORD_AND_ACK_REQUIRED")
		}
		quiet := newTerminal(input, io.Discard)
		password, err = quiet.secret(ctx, "")
		if err != nil {
			return fail("PASSWORD_REQUIRED")
		}
	}
	if len(password) < 16 || len(password) > 1024 {
		return fail("PASSWORD_LENGTH")
	}
	var seed *managed.Seed
	provisioned := false
	if data, err := rootStore.Read("managed-bootstrap.json"); err == nil {
		var saved controllerBootstrap
		if managed.DecodeJSON(data, &saved, 512<<10) != nil || saved.Version != 1 || saved.Config != config || saved.BackupPath != *backup {
			return fail("BOOTSTRAP_CONFLICT")
		}
		seed, err = managed.DecodeSeed(saved.Seed, password, time.Now())
		provisioned = saved.Provisioned
		if err != nil {
			return fail("BOOTSTRAP_VALIDATION_FAILED")
		}
	} else if !os.IsNotExist(err) {
		return fail("BOOTSTRAP_READ_FAILED")
	} else {
		if !*migrate {
			for _, file := range []string{"controller.key", "controller.crt", "api.token", "nodes.json"} {
				if _, err := rootStore.Read(file); !os.IsNotExist(err) {
					return fail("EXISTING_IMPORTED_SETUP")
				}
			}
		}
		if *migrate {
			seed, err = managed.NewMigrationSeed(config, password, importedIDs, time.Now())
			if err == nil {
				backupData := joinBackup{Version: 1, Request: "imported-to-managed", Files: map[string][]byte{}}
				for _, name := range []string{"controller.crt", "controller.key", "ca.crt", "issuer-ca.crt", "controller.env", "api-policy.json", "api.token", "observer.token", "nodes.json"} {
					data, e := rootStore.Read(name)
					if e != nil {
						return fail("IMPORTED_BACKUP_REFUSED")
					}
					backupData.Files[name] = data
				}
				data, _ := json.Marshal(backupData)
				if putSame(ctx, rootStore, "migration-imported-backup.json", data) != nil {
					return fail("IMPORTED_BACKUP_PERSIST_FAILED")
				}
			}
		} else {
			seed, err = managed.NewSeed(config, password, time.Now())
		}
		if err != nil {
			return fail("PKI_GENERATION_FAILED")
		}
		data, _ := json.Marshal(controllerBootstrap{Version: 1, Config: config, Seed: seed.Encode(), BackupPath: *backup})
		if rootStore.Create(ctx, "managed-bootstrap.json", data) != nil {
			return fail("BOOTSTRAP_PERSIST_FAILED")
		}
	}
	if ensurePrivateDirectory(filepath.Dir(*backup), 0, 0) != nil {
		return fail("BACKUP_DIRECTORY_UNSAFE")
	}
	backupStore, err := managed.OpenStore(filepath.Dir(*backup))
	if err != nil {
		return fail("BACKUP_STORE_UNAVAILABLE")
	}
	err = putSame(ctx, backupStore, filepath.Base(*backup), seed.Backup())
	_ = backupStore.Close()
	if err != nil {
		return fail("BACKUP_WRITE_FAILED")
	}
	if *interactive {
		answer, e := ui.line("Encrypted backup saved. Retain it and its password OFF this VM. Acknowledge? [yes/no]: ")
		if e != nil || !affirmative(answer) {
			return fail("BACKUP_ACK_REQUIRED")
		}
	}
	serviceUID, serviceGID, err := ensureAccount(ctx, "hlcp-controller")
	if err != nil {
		return fail("SERVICE_ACCOUNT_REFUSED")
	}
	if ensurePrivateDirectory(*state, serviceUID, serviceGID) != nil {
		return fail("STATE_DIRECTORY_UNSAFE")
	}
	store, err := managed.OpenStoreAsOwner(*state, serviceUID, serviceGID)
	if err != nil {
		return fail("STATE_STORE_UNAVAILABLE")
	}
	if provisioned {
		if _, e := store.Read("nodes-managed.json"); e != nil {
			store.Close()
			return fail("REGISTRY_LOSS_REFUSED")
		}
	}
	err = seed.Provision(ctx, store)
	_ = store.Close()
	if err != nil {
		return fail("STATE_PROVISION_FAILED")
	}
	bootstrapData, _ := json.Marshal(controllerBootstrap{Version: 1, Config: config, Seed: seed.Encode(), BackupPath: *backup, Provisioned: true})
	if rootStore.Update(ctx, "managed-bootstrap.json", func([]byte) ([]byte, error) { return bootstrapData, nil }) != nil {
		return fail("BOOTSTRAP_CHECKPOINT_FAILED")
	}
	if !*migrate {
		if recoverSetup(ctx, *directory, setup.Owner{UID: 0, GID: 0}, *resume) != nil {
			return fail("SETUP_RECOVERY_REQUIRED")
		}
		chain, key, root := seed.InitialTLS()
		material, err := onboarding.NewImportedTLS(chain, key, root, root)
		if err != nil {
			return fail("INITIAL_TLS_INVALID")
		}
		plan, err := onboarding.PlanControllerAuto(ctx, *directory, setup.Owner{UID: 0, GID: 0}, onboarding.ControllerOptions{ListenAddress: *listen, ServerName: *name}, material, time.Now())
		if err != nil {
			return fail("CONTROLLER_PLAN_REFUSED")
		}
		_, err = plan.Apply(ctx, true, time.Now())
		_ = plan.Close()
		if err != nil {
			return fail("CONTROLLER_CONFIG_INCOMPLETE")
		}
	}
	if putSame(ctx, rootStore, "managed.env", []byte("HLCP_MANAGED_STATE="+*state+"\n")) != nil {
		return fail("MANAGED_MODE_COMMIT_FAILED")
	}
	if *cliUser != "" && onboarding.ClientGrantCLI([]string{"--user", *cliUser, "--apply", "--yes"}, io.Discard) != 0 {
		return fail("CLIENT_SETUP_INCOMPLETE")
	}
	if *start && activateService(ctx, "system", "hlcp-controller.service", *enable) != nil {
		return fail("ACTIVATION_UNCERTAIN")
	}
	if *jsonOutput {
		_ = json.NewEncoder(out).Encode(map[string]any{"status": "configured", "managed": true, "started": *start, "enabled": *enable, "backup_file": *backup})
	} else {
		fmt.Fprintln(out, "Managed controller configured. PKI created, encrypted backup retained. Next: hlcp pairing create --node NODE.")
	}
	return 0
}
func affirmative(value string) bool {
	return strings.EqualFold(value, "yes") || strings.EqualFold(value, "ano")
}
func environmentListener(data []byte) string {
	result := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key != "HLCP_AGENT_LISTEN" || result != "" || value == "" {
			return ""
		}
		result = value
	}
	return result
}
