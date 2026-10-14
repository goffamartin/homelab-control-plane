package onboarding

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"hlcp/internal/localapi"
	"hlcp/internal/setup"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type grantMetadata struct {
	Directory string `json:"directory"`
	Digest    string `json:"digest"`
}
type grantConsent struct {
	Confirmed bool   `json:"confirmed"`
	Token     []byte `json:"token"`
}
type grantAccount struct {
	UID, GID  uint32
	Groups    []uint32
	Directory string
}

func credentialDigest(c ClientCredential) string {
	digest := sha256.Sum256(bytes.TrimSuffix(c.token, []byte("\n")))
	return hex.EncodeToString(digest[:])
}

// Hidden child protocol. Root refuses before any user path access. Only the
// selected user's registered default path is accepted; no home/env inference.
func ClientProvisionChild(input io.Reader, out io.Writer) int {
	emit := func(report clientGrantReport) int {
		if json.NewEncoder(out).Encode(report) != nil {
			return 2
		}
		if report.Status == "failed" {
			return 1
		}
		return 0
	}
	owner, err := (nativeSetupHost{}).Owner()
	if err != nil || owner.UID == 0 {
		return emit(grantFailure("PREVIEW_REFUSED", true))
	}
	decoder := json.NewDecoder(io.LimitReader(input, 4096))
	decoder.DisallowUnknownFields()
	var metadata grantMetadata
	if decoder.Decode(&metadata) != nil || len(metadata.Digest) != 64 {
		return emit(grantFailure("PREVIEW_REFUSED", true))
	}
	if _, err = hex.DecodeString(metadata.Digest); err != nil {
		return emit(grantFailure("PREVIEW_REFUSED", true))
	}
	account, err := user.LookupId(strconv.FormatUint(uint64(owner.UID), 10))
	if err != nil || !safeGrantHome(account.HomeDir) || metadata.Directory != filepath.Join(account.HomeDir, ".config", "hlcp") {
		return emit(grantFailure("PREVIEW_REFUSED", true))
	}
	credential, _ := NewClientCredential([]byte(strings.Repeat("0", 64))) // public schema stand-in, never applied
	planner, err := setup.OpenPlanner(metadata.Directory, owner)
	if err == nil {
		token, e := planner.Snapshot("cli.token", 257)
		planner.Close()
		if e != nil {
			return emit(grantFailure("PREVIEW_REFUSED", true))
		}
		if token.Exists {
			credential, e = NewClientCredential(token.Data)
			if e != nil || credentialDigest(credential) != metadata.Digest {
				return emit(grantFailure("PREVIEW_REFUSED", true))
			}
		}
	} else if !os.IsNotExist(err) && err != setup.ErrMissingDirectory {
		return emit(grantFailure("PREVIEW_REFUSED", true))
	}
	p, err := planGrantClient(context.Background(), metadata.Directory, owner, credential)
	if err != nil {
		return emit(grantFailure("PREVIEW_REFUSED", true))
	}
	defer p.Close()
	preview := p.Preview()
	if emit(clientGrantReport{Status: "preview", ReadOnly: true, Preview: &preview}) != 0 {
		return 2
	}
	var consent grantConsent
	err = decoder.Decode(&consent)
	if err == io.EOF {
		return 0
	} // parent preview/cancellation closes pipe, no writes
	if err != nil || !consent.Confirmed {
		return emit(grantFailure("GRANT_INPUT_INVALID", true))
	}
	actual, err := NewClientCredential(consent.Token)
	if err != nil || credentialDigest(actual) != metadata.Digest {
		return emit(grantFailure("GRANT_INPUT_INVALID", true))
	}
	ctx := context.Background()
	r, err := p.Apply(ctx, true, actual)
	if err != nil {
		return emit(grantFailure("WRITE_INCOMPLETE", false))
	}
	return emit(clientGrantReport{Status: "applied", ReadOnly: false, Result: &r})
}
func safeGrantHome(home string) bool {
	return filepath.IsAbs(home) && filepath.Clean(home) == home && home != "/" && len(home) < 3500 && !strings.ContainsRune(home, 0)
}

func lookupGrantAccount(name string) (grantAccount, error) {
	if name == "" || len(name) > 128 || strings.ContainsRune(name, 0) {
		return grantAccount{}, ErrClientOptions
	}
	account, err := user.Lookup(name)
	if err != nil || account.Username != name || !safeGrantHome(account.HomeDir) {
		return grantAccount{}, ErrClientOptions
	}
	uid, e1 := strconv.ParseUint(account.Uid, 10, 32)
	gid, e2 := strconv.ParseUint(account.Gid, 10, 32)
	if e1 != nil || e2 != nil || uid == 0 || gid == 0 {
		return grantAccount{}, ErrClientOptions
	}
	reverse, err := user.LookupId(account.Uid)
	if err != nil || reverse.Username != name || reverse.Gid != account.Gid || reverse.HomeDir != account.HomeDir {
		return grantAccount{}, ErrClientOptions
	}
	ids, err := account.GroupIds()
	if err != nil || len(ids) > 128 {
		return grantAccount{}, ErrClientOptions
	}
	result := grantAccount{UID: uint32(uid), GID: uint32(gid), Directory: filepath.Join(account.HomeDir, ".config", "hlcp"), Groups: []uint32{uint32(gid)}}
	seen := map[uint32]bool{uint32(gid): true}
	for _, text := range ids {
		id, err := strconv.ParseUint(text, 10, 32)
		if err != nil || id == 0 {
			return grantAccount{}, ErrClientOptions
		}
		if !seen[uint32(id)] {
			result.Groups = append(result.Groups, uint32(id))
			seen[uint32(id)] = true
		}
	}
	sort.Slice(result.Groups, func(i, j int) bool { return result.Groups[i] < result.Groups[j] })
	return result, nil
}

func controllerGrantSource(directory, role string) (*setup.Planner, *setup.Plan, ClientCredential, error) {
	fail := func(p *setup.Planner) (*setup.Planner, *setup.Plan, ClientCredential, error) {
		if p != nil {
			p.Close()
		}
		return nil, nil, ClientCredential{}, ErrClientOptions
	}
	if role != "operator" && role != "observer" {
		return fail(nil)
	}
	p, err := setup.OpenPlanner(directory, setup.Owner{})
	if err != nil {
		return fail(nil)
	}
	status, err := p.Inspect(context.Background())
	if err != nil || (status.State != "none" && status.State != "committed") {
		return fail(p)
	}
	names := []string{"api-policy.json", "api.token", "observer.token"}
	files := make([]setup.Snapshot, 3)
	for index, name := range names {
		limit := int64(257)
		if index == 0 {
			limit = 64 * 1024
		}
		files[index], err = p.Snapshot(name, limit)
		if err != nil || !files[index].Exists || files[index].Mode != 0600 {
			return fail(p)
		}
	}
	credentials, err := NewControllerCredentials(files[1].Data, files[2].Data)
	if err != nil {
		return fail(p)
	}
	policy, err := localapi.ParsePolicy(files[0].Data, func(path string) (string, error) {
		switch path {
		case "api.token":
			return localapi.ParseToken(credentials.operator)
		case "observer.token":
			return localapi.ParseToken(credentials.observer)
		}
		return "", ErrClientOptions
	})
	if err != nil {
		return fail(p)
	}
	data := credentials.operator
	if role == "observer" {
		data = credentials.observer
	}
	value, _ := localapi.ParseToken(data)
	actual, err := policy.RoleForToken(value)
	if err != nil || actual != role {
		return fail(p)
	}
	intents := make([]setup.Intent, 3)
	for index, name := range names {
		validate := func(data []byte) error { _, err := localapi.ParseToken(data); return err }
		if index == 0 {
			validate = func(data []byte) error {
				_, err := localapi.ParsePolicy(data, func(path string) (string, error) {
					switch path {
					case "api.token":
						return localapi.ParseToken(credentials.operator)
					case "observer.token":
						return localapi.ParseToken(credentials.observer)
					}
					return "", ErrClientOptions
				})
				return err
			}
		}
		intents[index] = setup.Intent{Name: name, Data: files[index].Data, Mode: 0600, Validate: validate}
	}
	plan, err := p.Build(context.Background(), intents)
	if err != nil {
		return fail(p)
	}
	for _, change := range plan.Preview().Changes {
		if change.Action != "unchanged" {
			return fail(p)
		}
	}
	if p.Recheck(context.Background(), plan) != nil {
		return fail(p)
	}
	credential, _ := NewClientCredential(data)
	return p, plan, credential, nil
}

func validGrantPreview(report clientGrantReport) bool {
	if report.Status != "preview" || !report.ReadOnly || report.Code != "" || report.Result != nil || report.Preview == nil || !report.Preview.ReadOnly {
		return false
	}
	changes := report.Preview.Changes
	if len(changes) < 2 || len(changes) > 4 {
		return false
	}
	seen := map[string]bool{}
	for _, c := range changes {
		if seen[c.Name] {
			return false
		}
		seen[c.Name] = true
		if c.Name == "configuration-directory" || c.Name == "user-config-directory" {
			if c.Action != "create-directory" || c.Mode != 0700 {
				return false
			}
			continue
		}
		if (c.Name != "cli.json" && c.Name != "cli.token") || (c.Action != "create" && c.Action != "unchanged") || c.Mode != 0600 {
			return false
		}
	}
	if !seen["cli.json"] || !seen["cli.token"] {
		return false
	}
	actions := map[string]string{}
	for _, change := range changes {
		actions[change.Name] = change.Action
	}
	return actions["cli.json"] == actions["cli.token"] && (!seen["configuration-directory"] || actions["cli.json"] == "create") && (!seen["user-config-directory"] || seen["configuration-directory"])
}

func nativeClientGrant(parent context.Context, name, role string, apply bool) clientGrantReport {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		return grantFailure("ROOT_REQUIRED", true)
	}
	self, err := os.Executable()
	if err != nil {
		return grantFailure("INSTALLED_PROFILE_REQUIRED", true)
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil || self != "/usr/bin/hlcp-controller" {
		return grantFailure("INSTALLED_PROFILE_REQUIRED", true)
	}
	binary, err := activationFile(self, 64<<20, true)
	if err != nil {
		return grantFailure("INSTALLED_PROFILE_REQUIRED", true)
	}
	account, err := lookupGrantAccount(name)
	if err != nil {
		return grantFailure("ACCOUNT_REFUSED", true)
	}
	planner, plan, credential, err := controllerGrantSource(controllerConfigDirectory, role)
	if err != nil {
		return grantFailure("SOURCE_REFUSED", true)
	}
	defer planner.Close()
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, self, "__client-provision")
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}
	command.Dir = "/"
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: account.UID, Gid: account.GID, Groups: account.Groups}, Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	command.WaitDelay = 250 * time.Millisecond
	stderr := &activationOutput{}
	command.Stderr = stderr
	stdin, err := command.StdinPipe()
	if err != nil {
		return grantFailure("CHILD_REFUSED", true)
	}
	defer stdin.Close()
	stdout, err := command.StdoutPipe()
	if err != nil {
		return grantFailure("CHILD_REFUSED", true)
	}
	defer stdout.Close()
	if command.Start() != nil {
		return grantFailure("CHILD_REFUSED", true)
	}
	waited := false
	defer func() {
		if !waited {
			command.Cancel()
			command.Wait()
		}
	}()
	encoder := json.NewEncoder(stdin)
	if encoder.Encode(grantMetadata{Directory: account.Directory, Digest: credentialDigest(credential)}) != nil {
		return grantFailure("CHILD_REFUSED", true)
	}
	decoder := json.NewDecoder(io.LimitReader(stdout, 16*1024))
	decoder.DisallowUnknownFields()
	var preview clientGrantReport
	if decoder.Decode(&preview) != nil || !validGrantPreview(preview) {
		return grantFailure("USER_PROFILE_REFUSED", true)
	}
	if !apply {
		stdin.Close()
		err = command.Wait()
		waited = true
		if err != nil || ctx.Err() != nil || stderr.overflow {
			return grantFailure("CHILD_REFUSED", true)
		}
		return preview
	}
	// Account/source/binary rechecks happen before any bearer export. Root never
	// opens or writes user paths; only the credential-dropped child can do that.
	current, e1 := lookupGrantAccount(name)
	changedBinary, e2 := activationFile(self, 64<<20, true)
	a, _ := json.Marshal(account)
	b, _ := json.Marshal(current)
	if e1 != nil || e2 != nil || !bytes.Equal(a, b) || !bytes.Equal(binary.Data, changedBinary.Data) || binary.Identity != changedBinary.Identity || binary.Mode != changedBinary.Mode || binary.Owner != changedBinary.Owner || planner.Recheck(ctx, plan) != nil || ctx.Err() != nil {
		return grantFailure("STALE_SOURCE", true)
	}
	if encoder.Encode(grantConsent{Confirmed: true, Token: credential.token}) != nil {
		return grantFailure("TRANSFER_INCOMPLETE", false)
	}
	stdin.Close()
	var result clientGrantReport
	if decoder.Decode(&result) != nil {
		return grantFailure("TRANSFER_INCOMPLETE", false)
	}
	// Never relay arbitrary child strings or a second claimed preview.
	if result.Status != "applied" || result.Code != "" || result.ReadOnly || result.Preview != nil || result.Result == nil || result.Result.Changed < 0 || result.Result.Changed > 2 {
		return grantFailure("TRANSFER_INCOMPLETE", false)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return grantFailure("TRANSFER_INCOMPLETE", false)
	}
	err = command.Wait()
	waited = true
	if err != nil || ctx.Err() != nil || stderr.overflow {
		return grantFailure("TRANSFER_INCOMPLETE", false)
	}
	return result
}
