package onboarding

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"hlcp/deploy/systemd"
	"hlcp/internal/setup"
	"hlcp/internal/transport"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const controllerUnitName = "hlcp-controller.service"
const controllerConfigDirectory = "/etc/hlcp/controller"
const activationProperties = "LoadState,ActiveState,UnitFileState,FragmentPath,DropInPaths,NeedDaemonReload,User,Group"

type nativeActivationHost struct{ serverName string }

func nativeControllerActivationHost(name string) ControllerActivationHost {
	return nativeActivationHost{name}
}

func (h nativeActivationHost) Inspect(ctx context.Context) (ControllerActivationSnapshot, error) {
	fail := func() (ControllerActivationSnapshot, error) {
		return ControllerActivationSnapshot{}, ErrActivationInvalid
	}
	if os.Geteuid() != 0 || os.Getegid() != 0 || ctx.Err() != nil {
		return fail()
	}
	self, err := os.Executable()
	if err != nil {
		return fail()
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil || self != "/usr/bin/hlcp-controller" {
		return fail()
	}
	account, err := activationAccount()
	if err != nil {
		return fail()
	}
	data, err := runActivationProcess(ctx, []string{"show", controllerUnitName, "--property=" + activationProperties}, 3*time.Second)
	if err != nil {
		return fail()
	}
	properties, err := parseActivationProperties(data)
	if err != nil {
		return fail()
	}
	unit, err := activationFile(properties["FragmentPath"], 64*1024, false)
	if err != nil {
		return fail()
	}
	if strings.ReplaceAll(string(unit.Data), "\r\n", "\n") != systemd.PackagedControllerUnit() {
		return fail()
	}
	controller, err := activationFile("/usr/bin/hlcp-controller", 64<<20, true)
	if err != nil {
		return fail()
	}
	manager, err := activationFile("/usr/bin/systemctl", 64<<20, true)
	if err != nil {
		return fail()
	}
	config, err := activationConfig(ctx, controllerConfigDirectory, h.serverName, time.Now())
	if err != nil {
		return fail()
	}
	// Framed serialization avoids concatenation ambiguity. No evidence is logged.
	evidence := struct {
		Account                   string
		Unit, Controller, Manager setup.Snapshot
		Config                    []setup.Snapshot
	}{account, unit, controller, manager, config}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return fail()
	}
	return ControllerActivationSnapshot{Proof: sha256.Sum256(encoded), Active: properties["ActiveState"], Enabled: properties["UnitFileState"] == "enabled"}, nil
}

func (nativeActivationHost) Change(ctx context.Context, action ControllerServiceAction) error {
	if os.Geteuid() != 0 || (action != ControllerEnable && action != ControllerStart) {
		return ErrActivationInvalid
	}
	_, err := runActivationProcess(ctx, []string{string(action), controllerUnitName}, 30*time.Second)
	return err
}

func parseActivationProperties(data []byte) (map[string]string, error) {
	if len(data) > 16*1024 {
		return nil, ErrActivationInvalid
	}
	expected := strings.Split(activationProperties, ",")
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, ErrActivationInvalid
		}
		allowed := false
		for _, name := range expected {
			if key == name {
				allowed = true
			}
		}
		if _, duplicate := values[key]; !allowed || duplicate {
			return nil, ErrActivationInvalid
		}
		values[key] = value
	}
	if len(values) != len(expected) || values["LoadState"] != "loaded" || values["NeedDaemonReload"] != "no" || values["DropInPaths"] != "" ||
		values["FragmentPath"] != "/usr/lib/systemd/system/hlcp-controller.service" || values["User"] != "hlcp-controller" || values["Group"] != "hlcp-controller" ||
		(values["ActiveState"] != "active" && values["ActiveState"] != "inactive") || (values["UnitFileState"] != "enabled" && values["UnitFileState"] != "disabled") {
		return nil, ErrActivationInvalid
	}
	return values, nil
}

func activationConfig(ctx context.Context, directory, name string, now time.Time) ([]setup.Snapshot, error) {
	planner, err := setup.OpenPlanner(directory, setup.Owner{UID: 0, GID: 0})
	if err != nil {
		return nil, ErrActivationInvalid
	}
	defer planner.Close()
	status, err := planner.Inspect(ctx)
	if err != nil || (status.State != "none" && status.State != "committed") {
		return nil, ErrActivationInvalid
	}
	names := []string{"controller.crt", "controller.key", "ca.crt", "issuer-ca.crt", "controller.env", "api-policy.json", "api.token", "observer.token", "nodes.json"}
	files := make([]setup.Snapshot, len(names))
	for index, file := range names {
		limit := int64(setup.MaximumFileBytes)
		if file == "api.token" || file == "observer.token" {
			limit = 257
		}
		if file == "nodes.json" || file == "api-policy.json" {
			limit = 64 * 1024
		}
		if file == "controller.env" {
			limit = 4096
		}
		files[index], err = planner.Snapshot(file, limit)
		if err != nil || !files[index].Exists || files[index].Mode != 0600 {
			return nil, ErrActivationInvalid
		}
	}
	material, err := NewImportedTLS(files[0].Data, files[1].Data, files[2].Data, files[3].Data)
	if err != nil {
		return nil, ErrActivationInvalid
	}
	credentials, err := NewControllerCredentials(files[6].Data, files[7].Data)
	if err != nil {
		return nil, ErrActivationInvalid
	}
	if validateControllerEnv(files[4].Data) != nil {
		return nil, ErrActivationInvalid
	}
	listen := ""
	for _, line := range strings.Split(string(files[4].Data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "HLCP_AGENT_LISTEN=") {
			listen = strings.TrimPrefix(strings.TrimSpace(line), "HLCP_AGENT_LISTEN=")
		}
	}
	nodes, err := transport.ParseAllowedNodes(files[8].Data)
	if err != nil {
		return nil, ErrActivationInvalid
	}
	admitted := []string{}
	for node := range nodes {
		admitted = append(admitted, node)
	}
	intents, err := controllerIntents(directory, ControllerOptions{ListenAddress: listen, ServerName: name, AdmittedNodes: admitted}, material, credentials, now)
	if err != nil {
		return nil, ErrActivationInvalid
	}
	// Validate actual snapshots, preserving valid manual env/policy formatting.
	for index, intent := range intents {
		if intent.Validate(files[index].Data) != nil {
			return nil, ErrActivationInvalid
		}
		intent.Data = files[index].Data
		intents[index] = intent
	}
	p, err := planner.Build(ctx, intents)
	if err != nil || planner.Recheck(ctx, p) != nil {
		return nil, ErrActivationInvalid
	}
	for _, change := range p.Preview().Changes {
		if change.Action != "unchanged" {
			return nil, ErrActivationInvalid
		}
	}
	return files, nil
}

func activationAccount() (string, error) {
	account, err := user.Lookup("hlcp-controller")
	if err != nil {
		return "", ErrActivationInvalid
	}
	uid, e1 := strconv.ParseUint(account.Uid, 10, 32)
	gid, e2 := strconv.ParseUint(account.Gid, 10, 32)
	if e1 != nil || e2 != nil || uid == 0 || gid == 0 || account.HomeDir != "/nonexistent" {
		return "", ErrActivationInvalid
	}
	reverse, err := user.LookupId(account.Uid)
	if err != nil || reverse.Username != account.Username || reverse.Gid != account.Gid {
		return "", ErrActivationInvalid
	}
	group, err := user.LookupGroupId(account.Gid)
	if err != nil || group.Name != "hlcp-controller" {
		return "", ErrActivationInvalid
	}
	groups, err := account.GroupIds()
	if err != nil || len(groups) != 1 || groups[0] != account.Gid {
		return "", ErrActivationInvalid
	}
	passwd, err := activationFile("/etc/passwd", 1<<20, false)
	if err != nil {
		return "", ErrActivationInvalid
	}
	found := ""
	for _, line := range strings.Split(string(passwd.Data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) > 0 && fields[0] == "hlcp-controller" {
			if found != "" || len(fields) != 7 || fields[2] != account.Uid || fields[3] != account.Gid || fields[5] != "/nonexistent" || (fields[6] != "/usr/sbin/nologin" && fields[6] != "/bin/false") {
				return "", ErrActivationInvalid
			}
			found = line
		}
	}
	if found == "" {
		return "", ErrActivationInvalid
	}
	return found, nil
}

// Fixed installed paths only. /usr-merge resolution is permitted, followed by
// no-follow regular-file and root-owned non-writable ancestor validation.
func activationFile(path string, limit int64, executable bool) (setup.Snapshot, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return setup.Snapshot{}, ErrActivationInvalid
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return setup.Snapshot{}, ErrActivationInvalid
	}
	// Unit/config files may not be redirected through symlinks.
	if !executable && resolved != path {
		return setup.Snapshot{}, ErrActivationInvalid
	}
	for parent := filepath.Dir(resolved); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0022 != 0 {
			return setup.Snapshot{}, ErrActivationInvalid
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 {
			return setup.Snapshot{}, ErrActivationInvalid
		}
		if parent == filepath.Dir(parent) {
			break
		}
	}
	fd, err := syscall.Open(resolved, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return setup.Snapshot{}, ErrActivationInvalid
	}
	file := os.NewFile(uintptr(fd), "activation input")
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return setup.Snapshot{}, ErrActivationInvalid
	}
	stat, ok := before.Sys().(*syscall.Stat_t)
	if !ok || !before.Mode().IsRegular() || stat.Uid != 0 || stat.Gid != 0 || stat.Nlink != 1 || before.Mode().Perm()&0022 != 0 || before.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 || before.Size() > limit || (executable && before.Mode().Perm()&0111 == 0) {
		return setup.Snapshot{}, ErrActivationInvalid
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return setup.Snapshot{}, ErrActivationInvalid
	}
	after, err := file.Stat()
	if err != nil || after.Size() != before.Size() || after.ModTime() != before.ModTime() || after.Mode() != before.Mode() {
		return setup.Snapshot{}, ErrActivationInvalid
	}
	afterStat, ok := after.Sys().(*syscall.Stat_t)
	if !ok || afterStat.Uid != stat.Uid || afterStat.Gid != stat.Gid || afterStat.Nlink != 1 || afterStat.Ctim != stat.Ctim {
		return setup.Snapshot{}, ErrActivationInvalid
	}
	bound, err := os.Lstat(resolved)
	if err != nil || !os.SameFile(before, bound) {
		return setup.Snapshot{}, ErrActivationInvalid
	}
	if executable && !bytes.HasPrefix(data, []byte{0x7f, 'E', 'L', 'F'}) {
		return setup.Snapshot{}, ErrActivationInvalid
	}
	// Retain only executable digests, never entire binaries in the evidence.
	if executable {
		digest := sha256.Sum256(data)
		data = digest[:]
	}
	return setup.Snapshot{Exists: true, Data: data, Mode: before.Mode().Perm(), Owner: setup.Owner{UID: stat.Uid, GID: stat.Gid}, Identity: setup.FileIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}}, nil
}

type activationOutput struct {
	data     []byte
	overflow bool
}

func (w *activationOutput) Write(data []byte) (int, error) {
	n := len(data)
	remaining := 16*1024 - len(w.data)
	if len(data) > remaining {
		w.overflow = true
		data = data[:remaining]
	}
	w.data = append(w.data, data...)
	return n, nil
}
func runActivationProcess(parent context.Context, args []string, timeout time.Duration) ([]byte, error) {
	valid := len(args) == 2 && args[1] == controllerUnitName && (args[0] == "enable" || args[0] == "start")
	valid = valid || (len(args) == 3 && args[0] == "show" && args[1] == controllerUnitName && args[2] == "--property="+activationProperties)
	if !valid || parent.Err() != nil {
		return nil, ErrActivationInvalid
	}
	if _, err := activationFile("/usr/bin/systemctl", 64<<20, true); err != nil {
		return nil, ErrActivationInvalid
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	// enable otherwise performs an implicit daemon-reload, outside this plan.
	command := exec.CommandContext(ctx, "/usr/bin/systemctl", append([]string{"--no-pager", "--no-ask-password", "--no-reload"}, args...)...)
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "SYSTEMD_PAGER=cat", "SYSTEMD_COLORS=0"}
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
