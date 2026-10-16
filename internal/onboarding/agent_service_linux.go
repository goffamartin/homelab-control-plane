package onboarding

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"hlcp/deploy/systemd"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (h nativeAgentServiceHost) InspectAgentService(ctx context.Context, profile AgentServiceProfile) (AgentServiceState, error) {
	fail := func() (AgentServiceState, error) { return AgentServiceState{}, ErrAgentActivationProfile }
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
	data, err := agentServiceProcess(ctx, profile, "show", profile.Unit, 3*time.Second)
	if err != nil {
		return fail()
	}
	props, err := agentPropertyMap(data, strings.Split(activationProperties, ","))
	if err != nil {
		return fail()
	}
	// Inspect failed/transitional states too, without relaxing unit identity checks.
	identity := make(map[string]string, len(props))
	for k, v := range props {
		identity[k] = v
	}
	identity["ActiveState"] = "inactive"
	identity["UnitFileState"] = "disabled"
	identity["NeedDaemonReload"] = "no"
	if validateAgentUnitProperties(identity, profile, false) != nil {
		return fail()
	}
	switch props["ActiveState"] {
	case "active", "inactive", "failed", "activating", "deactivating", "reloading", "maintenance":
	default:
		return fail()
	}
	switch props["UnitFileState"] {
	case "enabled", "disabled", "masked", "enabled-runtime", "linked", "linked-runtime", "static", "indirect":
	default:
		return fail()
	}
	if props["NeedDaemonReload"] != "yes" && props["NeedDaemonReload"] != "no" {
		return fail()
	}
	unit, err := activationFile(props["FragmentPath"], 64<<10, false)
	if err != nil {
		return fail()
	}
	expected, _ := systemd.PackagedAgentUnit(profile.Scope, profile.Unit)
	if strings.ReplaceAll(string(unit.Data), "\r\n", "\n") != expected {
		return fail()
	}
	// Identity/file evidence, never config/TLS/journal contents or dynamic state.
	proofData, err := json.Marshal(struct {
		UID, GID              uint32
		Binary, Manager, Unit [32]byte
		Properties            map[string]string
	}{uid, gid, sha256.Sum256(binary.Data), sha256.Sum256(manager.Data), sha256.Sum256(unit.Data), identity})
	if err != nil {
		return fail()
	}
	return AgentServiceState{Active: props["ActiveState"], Enabled: props["UnitFileState"], ReloadRequired: props["NeedDaemonReload"] == "yes", Proof: sha256.Sum256(proofData)}, nil
}
