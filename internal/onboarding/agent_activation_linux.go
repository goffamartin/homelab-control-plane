package onboarding

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"hlcp/deploy/systemd"
	"hlcp/internal/agent"
	"hlcp/internal/setup"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type nativeAgentServiceHost struct{}

var nativeAgentActivationHost AgentActivationHost = nativeAgentServiceHost{}

func (nativeAgentServiceHost) Caller(ctx context.Context) (uint32, uint32, error) {
	if ctx.Err() != nil {
		return 0, 0, ErrAgentActivationProfile
	}
	owner, err := (nativeSetupHost{}).Owner()
	return owner.UID, owner.GID, err
}
func (h nativeAgentServiceHost) InspectAgent(ctx context.Context, profile AgentServiceProfile) (AgentActivationSnapshot, error) {
	fail := func() (AgentActivationSnapshot, error) { return AgentActivationSnapshot{}, ErrAgentActivationProfile }
	uid, gid, err := h.Caller(ctx)
	if err != nil || !validAgentServiceProfile(profile) || (profile.Scope == "user" && (uid == 0 || gid == 0)) || (profile.Scope == "system" && (uid != 0 || gid != 0)) {
		return fail()
	}
	self, err := os.Executable()
	if err != nil {
		return fail()
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil || self != "/usr/bin/hlcp-agent" {
		return fail()
	}
	binary, err := activationFile(self, 64<<20, true)
	if err != nil {
		return fail()
	}
	manager, err := activationFile("/usr/bin/systemctl", 64<<20, true)
	if err != nil {
		return fail()
	}
	account, err := user.LookupId(strconv.FormatUint(uint64(uid), 10))
	if err != nil {
		return fail()
	}
	directory := "/etc/hlcp/agent"
	home := ""
	accountProof := ""
	stateOwner := setup.Owner{UID: uid, GID: gid}
	result := AgentActivationSnapshot{CallerUID: uid, CallerGID: gid}
	var runtimeProof []setup.Snapshot
	if profile.Scope == "user" {
		if !safeGrantHome(account.HomeDir) {
			return fail()
		}
		home = account.HomeDir
		directory = filepath.Join(home, ".config", "hlcp")
		runtimeProof, err = agentRuntimeEvidence(uid, gid)
		if err != nil {
			return fail()
		}
		data, err := agentServiceProcess(ctx, profile, "manager", "", 3*time.Second)
		if err != nil {
			return fail()
		}
		properties, err := agentPropertyMap(data, []string{"ActiveState"})
		if err != nil || properties["ActiveState"] != "active" {
			return fail()
		}
		data, err = agentServiceProcess(ctx, profile, "linger", "", 3*time.Second)
		if err != nil {
			return fail()
		}
		properties, err = agentPropertyMap(data, []string{"Linger"})
		if err != nil || (properties["Linger"] != "yes" && properties["Linger"] != "no") {
			return fail()
		}
		result.Linger = properties["Linger"] == "yes"
	} else {
		accountProof, err = serviceActivationAccount("hlcp")
		if err != nil {
			return fail()
		}
		fields := strings.Split(accountProof, ":")
		stateUID, _ := strconv.ParseUint(fields[2], 10, 32)
		stateGID, _ := strconv.ParseUint(fields[3], 10, 32)
		stateOwner = setup.Owner{UID: uint32(stateUID), GID: uint32(stateGID)}
	}
	data, err := agentServiceProcess(ctx, profile, "show", profile.Unit, 3*time.Second)
	if err != nil {
		return fail()
	}
	properties, err := agentPropertyMap(data, strings.Split(activationProperties, ","))
	if err != nil || validateAgentUnitProperties(properties, profile, false) != nil {
		return fail()
	}
	unit, err := activationFile(properties["FragmentPath"], 64*1024, false)
	if err != nil {
		return fail()
	}
	expected, _ := systemd.PackagedAgentUnit(profile.Scope, profile.Unit)
	if strings.ReplaceAll(string(unit.Data), "\r\n", "\n") != expected {
		return fail()
	}
	var peerUnit setup.Snapshot
	if profile.Scope == "user" {
		peer := AgentServiceProfile{Scope: "user", Unit: "hlcp-agent-podman.service"}
		if profile.Unit == peer.Unit {
			peer.Unit = "hlcp-agent.service"
		}
		data, err := agentServiceProcess(ctx, profile, "show", peer.Unit, 3*time.Second)
		if err != nil {
			return fail()
		}
		p, err := agentPropertyMap(data, strings.Split(activationProperties, ","))
		if err != nil || validateAgentUnitProperties(p, peer, true) != nil {
			return fail()
		}
		if p["LoadState"] == "not-found" {
			result.PeerActive = "absent"
			result.PeerEnabled = "absent"
		} else {
			result.PeerActive = p["ActiveState"]
			result.PeerEnabled = p["UnitFileState"]
			peerUnit, err = activationFile(p["FragmentPath"], 64*1024, false)
			if err != nil {
				return fail()
			}
			expected, _ = systemd.PackagedAgentUnit("user", peer.Unit)
			if strings.ReplaceAll(string(peerUnit.Data), "\r\n", "\n") != expected {
				return fail()
			}
		}
	}
	config, err := agentServiceConfig(ctx, directory, setup.Owner{UID: uid, GID: gid}, profile, home, time.Now())
	if errors.Is(err, ErrAgentActivationResources) {
		return AgentActivationSnapshot{}, err
	}
	if err != nil {
		return fail()
	}
	journal := "/var/lib/hlcp/command-journal.json"
	if profile.Scope == "user" {
		journal = filepath.Join(home, ".local", "state", "hlcp", "command-journal.json")
	}
	if agentJournalReadiness(journal, stateOwner) != nil {
		return fail()
	}
	groups, err := os.Getgroups()
	if err != nil {
		return fail()
	}
	evidence := struct {
		Account                     *user.User
		ServiceAccount              string
		Groups                      []int
		Binary, Manager, Unit, Peer setup.Snapshot
		Runtime, Config             []setup.Snapshot
	}{account, accountProof, groups, binary, manager, unit, peerUnit, runtimeProof, config}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return fail()
	}
	result.Service = ControllerActivationSnapshot{Proof: sha256.Sum256(encoded), Active: properties["ActiveState"], Enabled: properties["UnitFileState"] == "enabled"}
	return result, nil
}
func (h nativeAgentServiceHost) ChangeAgent(ctx context.Context, profile AgentServiceProfile, action ControllerServiceAction) error {
	uid, gid, err := h.Caller(ctx)
	if err != nil || !validAgentServiceProfile(profile) || (profile.Scope == "user" && (uid == 0 || gid == 0)) || (profile.Scope == "system" && (uid != 0 || gid != 0)) || (action != ControllerStart && action != ControllerEnable) {
		return ErrAgentActivationProfile
	}
	_, err = agentServiceProcess(ctx, profile, string(action), profile.Unit, 30*time.Second)
	return err
}

func agentPropertyMap(data []byte, fields []string) (map[string]string, error) {
	if len(data) > 16*1024 {
		return nil, ErrAgentActivationProfile
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, ErrAgentActivationProfile
		}
		allowed := false
		for _, field := range fields {
			if field == key {
				allowed = true
			}
		}
		if _, duplicate := values[key]; !allowed || duplicate {
			return nil, ErrAgentActivationProfile
		}
		values[key] = value
	}
	if len(values) != len(fields) {
		return nil, ErrAgentActivationProfile
	}
	return values, nil
}
func validateAgentUnitProperties(p map[string]string, profile AgentServiceProfile, peer bool) error {
	if !validAgentServiceProfile(profile) || p["DropInPaths"] != "" || p["NeedDaemonReload"] != "no" {
		return ErrAgentActivationProfile
	}
	if peer && p["LoadState"] == "not-found" && p["FragmentPath"] == "" && p["UnitFileState"] == "" && p["ActiveState"] == "inactive" && p["User"] == "" && p["Group"] == "" {
		return nil
	}
	path := "/usr/lib/systemd/" + profile.Scope + "/" + profile.Unit
	serviceUser := ""
	if profile.Scope == "system" {
		serviceUser = "hlcp"
	}
	if p["LoadState"] != "loaded" || p["FragmentPath"] != path || p["User"] != serviceUser || p["Group"] != serviceUser {
		return ErrAgentActivationProfile
	}
	if !peer && ((p["ActiveState"] != "active" && p["ActiveState"] != "inactive") || (p["UnitFileState"] != "enabled" && p["UnitFileState"] != "disabled")) {
		return ErrAgentActivationProfile
	}
	return nil
}

func agentServiceConfig(ctx context.Context, directory string, owner setup.Owner, profile AgentServiceProfile, home string, now time.Time) ([]setup.Snapshot, error) {
	if !validAgentServiceProfile(profile) {
		return nil, ErrAgentActivationProfile
	}
	p, err := setup.OpenPlanner(directory, owner)
	if err != nil {
		return nil, ErrAgentActivationProfile
	}
	defer p.Close()
	status, err := p.Inspect(ctx)
	if err != nil || (status.State != "none" && status.State != "committed") {
		return nil, ErrAgentActivationProfile
	}
	names := []string{"agent.yaml", "agent.crt", "agent.key", "ca.crt", "issuer-ca.crt"}
	files := make([]setup.Snapshot, 5)
	intents := make([]setup.Intent, 5)
	for index, name := range names {
		files[index], err = p.Snapshot(name, setup.MaximumFileBytes)
		if err != nil || !files[index].Exists || files[index].Mode != 0600 {
			return nil, ErrAgentActivationProfile
		}
		captured := files[index].Data
		intents[index] = setup.Intent{Name: name, Data: captured, Mode: 0600, Validate: func(data []byte) error {
			if !bytes.Equal(data, captured) {
				return ErrAgentActivationProfile
			}
			return nil
		}}
	}
	config, err := agent.Parse(files[0].Data)
	if err != nil {
		return nil, ErrAgentActivationProfile
	}
	address, port, e1 := net.SplitHostPort(config.ControllerAddress)
	number, e2 := strconv.Atoi(port)
	if e1 != nil || e2 != nil || !validHost(address) || !validHost(config.ServerName) || number < 1 || number > 65535 {
		return nil, ErrAgentActivationProfile
	}
	certDir := directory
	journal := filepath.Join(home, ".local", "state", "hlcp", "command-journal.json")
	if profile.Scope == "system" {
		certDir = "/run/credentials/hlcp-agent.service"
		journal = "/var/lib/hlcp/command-journal.json"
	}
	if config.CertificateFile != filepath.Join(certDir, "agent.crt") || config.PrivateKeyFile != filepath.Join(certDir, "agent.key") || config.CAFile != filepath.Join(certDir, "ca.crt") || config.JournalFile != journal {
		return nil, ErrAgentActivationProfile
	}
	material, err := NewImportedTLS(files[1].Data, files[2].Data, files[3].Data, files[4].Data)
	if err != nil {
		return nil, ErrAgentActivationProfile
	}
	if _, err = material.Validate(Identity{Role: "agent", NodeID: config.NodeID}, now); err != nil {
		return nil, ErrAgentActivationProfile
	}
	// This initial service bridge deliberately accepts only fresh imported
	// agents. Adapter/action readiness under the actual service account follows.
	if len(config.Resources) != 0 || len(config.Capabilities) != 0 {
		return nil, ErrAgentActivationResources
	}
	plan, err := p.Build(ctx, intents)
	if err != nil || p.Recheck(ctx, plan) != nil {
		return nil, ErrAgentActivationProfile
	}
	for _, change := range plan.Preview().Changes {
		if change.Action != "unchanged" {
			return nil, ErrAgentActivationProfile
		}
	}
	return files, nil
}

// Metadata only: never open the journal, acquire its lock or read its content.
// Absent journal/state parents may be created by the runtime/systemd on start.
// Dynamic state inode existence is intentionally not hashed into startup proof.
func agentJournalReadiness(path string, owner setup.Owner) error {
	for current := filepath.Dir(path); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !os.IsNotExist(err) {
			return ErrAgentActivationProfile
		}
		if err == nil {
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok || !info.IsDir() || (stat.Uid != 0 && stat.Uid != owner.UID) || info.Mode().Perm()&0022 != 0 {
				return ErrAgentActivationProfile
			}
			if current == filepath.Dir(path) && (stat.Uid != owner.UID || stat.Gid != owner.GID || info.Mode().Perm() != 0700) {
				return ErrAgentActivationProfile
			}
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return ErrAgentActivationProfile
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Nlink != 1 || stat.Uid != owner.UID || stat.Gid != owner.GID || info.Mode().Perm() != 0600 {
		return ErrAgentActivationProfile
	}
	return nil
}

func agentRuntimeEvidence(uid, gid uint32) ([]setup.Snapshot, error) {
	path := "/run/user/" + strconv.FormatUint(uint64(uid), 10)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return nil, ErrAgentActivationProfile
	}
	for _, parent := range []string{"/run", "/run/user"} {
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0022 != 0 {
			return nil, ErrAgentActivationProfile
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 {
			return nil, ErrAgentActivationProfile
		}
	}
	result := []setup.Snapshot{}
	for index, name := range []string{path, filepath.Join(path, "bus")} {
		info, err := os.Lstat(name)
		if err != nil {
			return nil, ErrAgentActivationProfile
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uid || stat.Gid != gid || (index == 0 && (!info.IsDir() || info.Mode().Perm() != 0700)) || (index == 1 && info.Mode()&os.ModeSocket == 0) {
			return nil, ErrAgentActivationProfile
		}
		result = append(result, setup.Snapshot{Exists: true, Mode: info.Mode(), Owner: setup.Owner{UID: uid, GID: gid}, Identity: setup.FileIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}})
	}
	return result, nil
}

func agentServiceProcess(parent context.Context, profile AgentServiceProfile, operation, unit string, timeout time.Duration) ([]byte, error) {
	if !validAgentServiceProfile(profile) || parent.Err() != nil {
		return nil, ErrAgentActivationProfile
	}
	owner, err := (nativeSetupHost{}).Owner()
	if err != nil || (profile.Scope == "user" && (owner.UID == 0 || owner.GID == 0)) || (profile.Scope == "system" && (owner.UID != 0 || owner.GID != 0)) {
		return nil, ErrAgentActivationProfile
	}
	path := "/usr/bin/systemctl"
	args := []string{"--no-pager", "--no-ask-password", "--all", "--no-reload"}
	env := []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "SYSTEMD_PAGER=cat", "SYSTEMD_COLORS=0"}
	if profile.Scope == "user" {
		if _, err = agentRuntimeEvidence(owner.UID, owner.GID); err != nil {
			return nil, err
		}
		runtime := "/run/user/" + strconv.FormatUint(uint64(owner.UID), 10)
		env = append(env, "XDG_RUNTIME_DIR="+runtime, "DBUS_SESSION_BUS_ADDRESS=unix:path="+runtime+"/bus")
	}
	switch operation {
	case "show":
		if unit != profile.Unit && !(profile.Scope == "user" && (unit == "hlcp-agent.service" || unit == "hlcp-agent-podman.service")) {
			return nil, ErrAgentActivationProfile
		}
		if profile.Scope == "user" {
			args = append(args, "--user")
		}
		args = append(args, "show", unit, "--property="+activationProperties)
	case "enable", "start":
		if unit != profile.Unit {
			return nil, ErrAgentActivationProfile
		}
		if profile.Scope == "user" {
			args = append(args, "--user")
		}
		args = append(args, operation, unit)
	case "manager":
		if profile.Scope != "user" || unit != "" {
			return nil, ErrAgentActivationProfile
		}
		args = append(args, "show", "user@"+strconv.FormatUint(uint64(owner.UID), 10)+".service", "--property=ActiveState")
	case "linger":
		if profile.Scope != "user" || unit != "" {
			return nil, ErrAgentActivationProfile
		}
		path = "/usr/bin/loginctl"
		args = []string{"--no-pager", "--no-ask-password", "--all", "show-user", strconv.FormatUint(uint64(owner.UID), 10), "--property=Linger"}
	default:
		return nil, ErrAgentActivationProfile
	}
	if _, err = activationFile(path, 64<<20, true); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	command := exec.CommandContext(ctx, path, args...)
	command.Env = env
	command.Dir = "/"
	command.WaitDelay = 250 * time.Millisecond
	out, stderr := &activationOutput{}, &activationOutput{}
	command.Stdout = out
	command.Stderr = stderr
	if command.Run() != nil || ctx.Err() != nil || out.overflow || stderr.overflow {
		return nil, ErrActivationIncomplete
	}
	return out.data, nil
}
