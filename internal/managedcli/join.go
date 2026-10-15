package managedcli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go.yaml.in/yaml/v4"
	"hlcp/internal/agent"
	"hlcp/internal/buildinfo"
	"hlcp/internal/managed"
	"hlcp/internal/onboarding"
	"hlcp/internal/setup"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type joinPending struct {
	Version    int                       `json:"version"`
	Invitation string                    `json:"invitation"`
	Node       string                    `json:"node"`
	RequestID  string                    `json:"request_id"`
	Key        []byte                    `json:"key"`
	CSR        []byte                    `json:"csr"`
	Result     *managed.EnrollmentResult `json:"result,omitempty"`
	Completed  bool                      `json:"completed"`
}
type agentPaths struct {
	directory, state, journal string
	uid, gid                  int
	owner                     setup.Owner
}

func pathsFor(scope string) (agentPaths, error) {
	u, uid, gid, err := caller()
	if err != nil {
		return agentPaths{}, err
	}
	p := agentPaths{uid: uid, gid: gid, owner: setup.Owner{UID: uint32(uid), GID: uint32(gid)}}
	switch scope {
	case "user":
		if uid == 0 || !filepath.IsAbs(u.HomeDir) || u.HomeDir == "/" || u.HomeDir == "/nonexistent" {
			return p, ErrSetup
		}
		p.directory = filepath.Join(u.HomeDir, ".config/hlcp")
		p.state = filepath.Join(u.HomeDir, ".local/state/hlcp/managed")
		p.journal = filepath.Join(u.HomeDir, ".local/state/hlcp/command-journal.json")
	case "system":
		if uid != 0 || gid != 0 {
			return p, ErrSetup
		}
		p.directory = "/etc/hlcp/agent"
		p.state = "/var/lib/hlcp/managed"
		p.journal = "/var/lib/hlcp/command-journal.json"
	default:
		return p, ErrSetup
	}
	return p, nil
}
func Join(args []string, input io.Reader, out io.Writer) int {
	f := flag.NewFlagSet("join", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	defaultScope := "user"
	if os.Geteuid() == 0 {
		defaultScope = "system"
	}
	scope := f.String("scope", defaultScope, "user or system")
	unit := f.String("unit", "hlcp-agent.service", "packaged generic or user Podman compatibility unit")
	codeFile := f.String("pairing-file", "", "private pairing code file")
	codeStdin := f.Bool("pairing-stdin", false, "read pairing code from stdin")
	resume := f.Bool("resume", false, "resume saved join without generating another key")
	repair := f.Bool("re-pair", false, "explicit replacement preserving resources and journal")
	offline := f.Bool("offline-stopped", false, "explicit stopped-service confirmation for non-booted images only")
	start := f.Bool("start", false, "start packaged service")
	enable := f.Bool("enable", false, "enable after verified start")
	apply := f.Bool("apply", false, "apply pairing")
	yes := f.Bool("yes", false, "confirm changes")
	jsonOut := f.Bool("json", false, "redacted JSON")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(out, "Profiles: --unit hlcp-agent.service (default) or user --unit hlcp-agent-podman.service. Non-booted stopped images alone can use --offline-stopped; never live activation.")
			fmt.Fprintln(out, "Usage: hlcp-agent join [--scope user|system] [--pairing-stdin|--pairing-file /private/code|--resume] [--re-pair] [--start [--enable]] [--json] [--apply --yes]\nNo arguments opens an interactive wizard. Noninteractive defaults to read-only preview. Pairing codes are never accepted in argv. Re-pair preserves resources and journal; stop the old service first.")
			return 0
		}
		return 2
	}
	ui := newTerminal(input, out)
	interactive := len(args) == 0
	fail := func(code string) int {
		if *jsonOut {
			_ = json.NewEncoder(out).Encode(map[string]any{"status": "failed", "code": code, "retry": "inspect state; use --resume for interrupted join"})
		} else {
			fmt.Fprintln(out, "Join refused:", code, "No journal or resource was reset.")
		}
		return 1
	}
	if f.NArg() != 0 || !validAgentProfile(*scope, *unit) || *apply != *yes || (*enable && !*start) || (*codeFile != "" && *codeStdin) || (*resume && (*codeFile != "" || *codeStdin)) || (*resume && *repair) {
		return 2
	}
	if runtime.GOOS != "linux" {
		return fail("LINUX_REQUIRED")
	}
	p, err := pathsFor(*scope)
	if err != nil {
		return fail("SCOPE_OR_ACCOUNT_INVALID")
	}
	ctx, cancel := boundedContext()
	defer cancel()
	if *offline && (*start || !offlineImage()) {
		return fail("OFFLINE_MODE_NOT_AVAILABLE")
	}
	code := ""
	if interactive {
		if !ui.interactive {
			return fail("TERMINAL_REQUIRED")
		}
		code, err = ui.secret(ctx, "Paste single-use pairing code: ")
		if err != nil {
			return fail("PAIRING_REQUIRED")
		}
		answer, e := ui.line("Start agent and enable boot startup? [yes/no]: ")
		if e != nil {
			return fail("INPUT_REQUIRED")
		}
		*start = affirmative(answer)
		*enable = *start
		answer, e = ui.line("Pair this machine with the controller? [yes/no]: ")
		if e != nil || !affirmative(answer) {
			fmt.Fprintln(out, "No changes applied.")
			return 0
		}
		*apply = true
		*yes = true
	} else if *codeStdin {
		code, err = newTerminal(input, io.Discard).secret(ctx, "")
		if err != nil {
			return fail("PAIRING_REQUIRED")
		}
	} else if *codeFile != "" {
		data, e := readPrivate(*codeFile, p.owner)
		if e != nil || len(data) > 4096 {
			return fail("PAIRING_FILE_UNSAFE")
		}
		code = strings.TrimSpace(string(data))
	}
	var invitation managed.Invitation
	if !*resume {
		invitation, err = managed.ParseInvitation(code)
		if err != nil {
			return fail("INVALID_PAIRING_CODE")
		}
	}
	if !*apply {
		_ = json.NewEncoder(out).Encode(map[string]any{"status": "preview", "read_only": true, "scope": *scope, "node": invitation.NodeID, "controller": invitation.URL, "resume": *resume})
		return 0
	}
	// Refuse changing a live service before consuming the one-use invitation.
	if (*start || *repair) && !*offline {
		if reloadManager(ctx, *scope) != nil {
			return fail("SERVICE_MANAGER_RELOAD_FAILED")
		}
		current, e := inspectService(ctx, *scope, *unit)
		if e != nil {
			return fail("SERVICE_PREFLIGHT_REFUSED")
		}
		if current.active != "inactive" {
			return fail("STOP_AGENT_BEFORE_JOIN")
		}
		if *scope == "user" {
			peer := "hlcp-agent-podman.service"
			if *unit == peer {
				peer = "hlcp-agent.service"
			}
			other, e := inspectService(ctx, *scope, peer)
			if e != nil || other.active != "inactive" {
				return fail("STOP_PEER_AGENT_PROFILE_FIRST")
			}
		}
		if *enable && *scope == "user" && checkLinger(ctx) != nil {
			fmt.Fprintln(out, "Before boot startup run as yourself: sudo hlcp-agent prepare-user --user \"$(id -un)\" --apply --yes")
			return fail("RUN_PREPARE_USER_FIRST")
		}
	}
	if *scope == "system" {
		p.uid, p.gid, err = ensureAccount(ctx, "hlcp")
		if err != nil {
			return fail("SERVICE_ACCOUNT_REFUSED")
		}
	}
	if ensurePrivateDirectory(p.directory, int(p.owner.UID), int(p.owner.GID)) != nil || ensurePrivateDirectory(p.state, p.uid, p.gid) != nil {
		return fail("PRIVATE_DIRECTORY_UNSAFE")
	}
	configStore, err := managed.OpenStore(p.directory)
	if err != nil {
		return fail("CONFIG_LOCKED_OR_UNSAFE")
	}
	defer configStore.Close()
	state, err := managed.OpenStoreAsOwner(p.state, p.uid, p.gid)
	if err != nil {
		return fail("STOP_AGENT_OR_INSPECT_STATE")
	}
	defer state.Close()
	var pending joinPending
	before, pendingErr := state.Read("join-pending.json")
	if pendingErr == nil {
		if managed.DecodeJSON(before, &pending, 256<<10) != nil || pending.Version != 1 {
			return fail("PENDING_STATE_INVALID")
		}
		if *resume {
			if pending.Completed {
				fmt.Fprintln(out, "Join already completed; use hlcp-agent activate to start the service.")
				return 0
			}
			invitation, err = managed.ParseInvitation(pending.Invitation)
			if err != nil {
				return fail("PENDING_INVITATION_INVALID")
			}
		} else if !*repair {
			return fail("EXISTING_JOIN_USE_RESUME_OR_RE_PAIR")
		}
	} else if !os.IsNotExist(pendingErr) {
		return fail("PENDING_STATE_UNREADABLE")
	} else if *resume {
		return fail("NO_PENDING_JOIN")
	}
	var old agent.Config
	oldBytes, oldErr := configStore.Read("agent.yaml")
	if oldErr == nil {
		old, err = agent.Parse(oldBytes)
		if err != nil || old.NodeID != invitation.NodeID {
			return fail("EXISTING_NODE_CONFLICT")
		}
		if !*repair && !*resume {
			return fail("EXPLICIT_RE_PAIR_REQUIRED")
		}
	} else if !os.IsNotExist(oldErr) {
		return fail("EXISTING_CONFIG_UNREADABLE")
	}
	if !*resume {
		if pendingErr == nil {
			if state.Update(ctx, "join-before-repair.json", func([]byte) ([]byte, error) { return before, nil }) != nil {
				return fail("BACKUP_FAILED")
			}
		}
		key, csr, e := managed.NewAgentRequest(invitation.NodeID)
		if e != nil {
			return fail("KEY_GENERATION_FAILED")
		}
		var request [16]byte
		if _, e = rand.Read(request[:]); e != nil {
			return fail("REQUEST_ID_FAILED")
		}
		pending = joinPending{Version: 1, Invitation: code, Node: invitation.NodeID, RequestID: hex.EncodeToString(request[:]), Key: key, CSR: csr}
		if savePending(ctx, state, pending) != nil {
			return fail("PENDING_PERSIST_FAILED")
		}
	}
	if pending.Node != invitation.NodeID || len(pending.Key) == 0 || len(pending.CSR) == 0 {
		return fail("PENDING_STATE_INVALID")
	}
	if pending.Result == nil {
		result, e := managed.EnrollAgent(ctx, invitation, pending.RequestID, pending.Key, pending.CSR)
		if e != nil {
			return fail("ENROLLMENT_FAILED_RESUME_SUPPORTED")
		}
		pending.Result = &result
		if savePending(ctx, state, pending) != nil {
			return fail("ENROLLMENT_RESPONSE_PERSIST_FAILED")
		}
	}
	result := *pending.Result
	if managed.VerifyAgentMaterial(pending.Node, result.Certificate, pending.Key, result.CA, time.Now()) != nil {
		return fail("ENROLLED_IDENTITY_INVALID")
	}
	// Prepare and validate the entire config before installing the identity.
	config := old
	if oldErr != nil {
		config = agent.Config{JournalFile: p.journal, AgentVersion: buildinfo.Version, Capabilities: []string{}, Resources: []agent.ResourceConfig{}}
	}
	config.NodeID = pending.Node
	config.ControllerAddress = result.Controller
	config.ServerName = result.ServerName
	config.ManagedDirectory = p.state
	config.CertificateFile = filepath.Join(p.directory, "agent.crt")
	config.PrivateKeyFile = filepath.Join(p.directory, "agent.key")
	config.CAFile = filepath.Join(p.directory, "ca.crt")
	if *scope == "system" {
		config.CertificateFile = "/run/credentials/hlcp-agent.service/agent.crt"
		config.PrivateKeyFile = "/run/credentials/hlcp-agent.service/agent.key"
		config.CAFile = "/run/credentials/hlcp-agent.service/ca.crt"
	}
	if config.Validate() != nil {
		return fail("GENERATED_CONFIG_INVALID")
	}
	data, e := yaml.Marshal(config)
	if e != nil {
		return fail("GENERATED_CONFIG_INVALID")
	}
	files := map[string][]byte{"agent.yaml": data, "agent.crt": result.Certificate, "agent.key": pending.Key, "ca.crt": result.CA, "issuer-ca.crt": result.CA}
	if oldErr == nil && (*repair || *resume) {
		if e = backupJoin(ctx, configStore, pending.RequestID, files); e != nil {
			return fail("CONFIG_BACKUP_FAILED")
		}
	}
	if e = managed.ProvisionAgentIdentity(ctx, state, pending.Node, invitation.URL, pending.Key, pending.CSR, result, *repair || *resume); e != nil {
		return fail("IDENTITY_PERSIST_FAILED")
	}
	for _, name := range []string{"agent.crt", "agent.key", "ca.crt", "issuer-ca.crt", "agent.yaml"} {
		existing, e := configStore.Read(name)
		if e == nil && bytes.Equal(existing, files[name]) {
			continue
		}
		if e == nil && oldErr != nil {
			return fail("UNEXPECTED_EXISTING_CONFIG_FILE")
		}
		if e != nil && !os.IsNotExist(e) {
			return fail("CONFIG_UNREADABLE")
		}
		if e = configStore.Update(ctx, name, func([]byte) ([]byte, error) { return files[name], nil }); e != nil {
			return fail("CONFIG_PERSIST_FAILED")
		}
	}
	pending.Completed = true
	pending.Invitation = ""
	pending.Key = nil
	pending.CSR = nil
	pending.Result = nil
	if savePending(ctx, state, pending) != nil {
		return fail("FINALIZE_FAILED_RESUME_SUPPORTED")
	}
	_ = state.Close()
	_ = configStore.Close()
	if *start {
		activate := []string{"--scope", *scope, "--unit", *unit, "--apply", "--yes"}
		if *enable {
			activate = append(activate, "--enable")
		}
		if onboarding.AgentActivationCLI(activate, io.Discard) != 0 {
			return fail("CONFIGURED_ACTIVATION_INCOMPLETE")
		}
	}
	_ = json.NewEncoder(out).Encode(map[string]any{"status": "configured", "node": pending.Node, "scope": *scope, "managed": true, "started": *start, "enabled": *enable, "resources_preserved": oldErr == nil, "journal_preserved": true})
	return 0
}
func savePending(ctx context.Context, s *managed.Store, p joinPending) error {
	data, e := json.Marshal(p)
	if e != nil {
		return e
	}
	return s.Update(ctx, "join-pending.json", func([]byte) ([]byte, error) { return data, nil })
}

type joinBackup struct {
	Version int               `json:"version"`
	Request string            `json:"request"`
	Files   map[string][]byte `json:"files"`
}

func backupJoin(ctx context.Context, s *managed.Store, request string, files map[string][]byte) error {
	if data, e := s.Read("join-config-backup.json"); e == nil {
		var old joinBackup
		if managed.DecodeJSON(data, &old, 256<<10) != nil {
			return ErrSetup
		}
		if old.Request == request {
			return nil
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	b := joinBackup{Version: 1, Request: request, Files: map[string][]byte{}}
	for name := range files {
		data, e := s.Read(name)
		if e == nil {
			b.Files[name] = data
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	data, e := json.Marshal(b)
	if e != nil {
		return e
	}
	return s.Update(ctx, "join-config-backup.json", func([]byte) ([]byte, error) { return data, nil })
}
