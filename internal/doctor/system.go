package doctor

import (
	"errors"
	"strconv"
	"strings"
)

type Account struct{ Name, UID, GID string }
type Query struct{ Kind, Scope, Unit, UID string }

// SystemHost allows only predefined property queries, not arbitrary commands.
type SystemHost interface {
	ClockSynced() (bool, error)
	Account() (Account, error)
	Properties(Query) (map[string]string, error)
}

func checkSystem(host Host, options Options, podman bool, add func(string, string, string)) {
	native, ok := host.(SystemHost)
	if !ok {
		add("host.integration", "skip", "Local system diagnostics are unavailable on this host.")
		return
	}
	if synced, err := native.ClockSynced(); err != nil {
		add("host.clock", "warn", "Kernel clock synchronization status is unavailable.")
	} else if synced {
		add("host.clock", "ok", "Kernel reports a synchronized clock.")
	} else {
		add("host.clock", "warn", "Kernel does not report a synchronized clock; verify time before provisioning TLS.")
	}
	account, err := native.Account()
	uid, uidErr := strconv.ParseUint(account.UID, 10, 32)
	_, gidErr := strconv.ParseUint(account.GID, 10, 32)
	if err != nil || uidErr != nil || gidErr != nil || account.Name == "" {
		add("host.account", "fail", "Effective local account cannot be validated.")
		return
	}
	add("host.account", "ok", "Effective account and primary IDs were resolved without HOME or SUDO_USER inference.")
	if options.Scope == "user" && uid == 0 {
		add("host.user_scope", "fail", "Run user-scope diagnostics directly as the non-root runtime account, without sudo.")
		return
	}
	unit := options.Unit
	if options.Role == "controller" {
		unit = "hlcp-controller.service"
	} else if unit == "" {
		unit = "hlcp-agent.service"
	}
	properties, err := native.Properties(Query{Kind: "unit", Scope: options.Scope, Unit: unit, UID: account.UID})
	if err != nil {
		add("host.unit", "warn", "Service properties are unavailable; manager, permissions or probe limits may prevent inspection.")
	} else {
		checkUnit(properties, options.Scope, add)
	}
	if options.Scope == "user" {
		properties, err = native.Properties(Query{Kind: "linger", UID: account.UID})
		switch {
		case err != nil:
			add("host.linger", "warn", "Linger status is unavailable.")
		case properties["Linger"] == "yes":
			add("host.linger", "ok", "Linger is enabled for the effective user.")
		case properties["Linger"] == "no":
			add("host.linger", "warn", "Linger is disabled; user services may stop after logout.")
		default:
			add("host.linger", "warn", "Linger status was not recognized.")
		}
		properties, err = native.Properties(Query{Kind: "manager", UID: account.UID})
		if err != nil || properties["ActiveState"] != "active" {
			add("host.user_manager", "warn", "User manager is unavailable or not active.")
		} else {
			add("host.user_manager", "ok", "User manager is active.")
		}
	}
	if podman && options.Scope == "user" {
		for _, spec := range []struct{ path, code string }{{"/etc/subuid", "host.subuid"}, {"/etc/subgid", "host.subgid"}} {
			data, err := host.ReadFile(spec.path, false)
			if err != nil || len(data) > MaximumFileSize {
				add(spec.code, "fail", "Subordinate ID configuration is unreadable or unsafe.")
			} else if validSubordinateRange(data, account) {
				add(spec.code, "ok", "A non-overflowing subordinate range of at least 65536 IDs is configured.")
			} else {
				add(spec.code, "fail", "No valid subordinate range of at least 65536 IDs was found for this account.")
			}
		}
		add("host.rootless", "skip", "ID ranges do not establish mapping-helper, storage, cgroup or container runtime health.")
	} else if podman {
		add("host.rootless", "skip", "System scope does not select a rootless runtime account; subordinate ranges were not checked.")
	}
}

func checkUnit(p map[string]string, scope string, add func(string, string, string)) {
	switch p["LoadState"] {
	case "loaded":
		add("host.unit.loaded", "ok", "Selected service unit is loaded.")
	case "masked":
		add("host.unit.loaded", "fail", "Selected service unit is masked.")
	case "not-found":
		add("host.unit.loaded", "warn", "Selected service unit is not installed.")
	default:
		add("host.unit.loaded", "warn", "Selected service unit is not in a recognized loaded state.")
	}
	switch p["ActiveState"] {
	case "active":
		add("host.unit.active", "ok", "Selected service is active.")
	case "failed":
		add("host.unit.active", "fail", "Selected service is failed; inspect its journal separately.")
	default:
		add("host.unit.active", "warn", "Selected service is not active.")
	}
	switch p["UnitFileState"] {
	case "enabled":
		add("host.unit.enabled", "ok", "Selected service is persistently enabled.")
	case "enabled-runtime":
		add("host.unit.enabled", "warn", "Selected service is enabled only until reboot.")
	case "masked", "masked-runtime":
		add("host.unit.enabled", "fail", "Selected service activation is masked.")
	default:
		add("host.unit.enabled", "warn", "Persistent service enablement was not established.")
	}
	fragment := p["FragmentPath"]
	prefix := "/usr/lib/systemd/system/"
	if scope == "user" {
		prefix = "/usr/lib/systemd/user/"
	}
	if p["DropInPaths"] != "" || (fragment != "" && !strings.HasPrefix(fragment, prefix) && !strings.HasPrefix(fragment, strings.Replace(prefix, "/usr/lib/", "/lib/", 1))) {
		add("host.unit.overrides", "warn", "A local unit fragment or drop-in is present; review it before migration.")
	} else if fragment == "" {
		add("host.unit.overrides", "skip", "No unit fragment was reported.")
	} else {
		add("host.unit.overrides", "ok", "Vendor fragment selected and no drop-ins reported; content was not compared with the package.")
	}
}

func validSubordinateRange(data []byte, account Account) bool {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) != 3 || (fields[0] != account.Name && fields[0] != account.UID) {
			continue
		}
		start, e1 := strconv.ParseUint(fields[1], 10, 32)
		count, e2 := strconv.ParseUint(fields[2], 10, 32)
		if e1 == nil && e2 == nil && start > 0 && count >= 65536 && start+count <= 1<<32 {
			return true
		}
	}
	return false
}

// Parse only selected property output, rejecting duplicate/extra/malformed keys.
func parseProperties(data []byte, allowed []string) (map[string]string, error) {
	if len(data) > maximumProbeOutput {
		return nil, errProbe
	}
	result := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		valid := false
		for _, field := range allowed {
			if key == field {
				valid = true
			}
		}
		if !ok || !valid {
			return nil, errProbe
		}
		if _, exists := result[key]; exists {
			return nil, errProbe
		}
		result[key] = value
	}
	for _, field := range allowed {
		if _, ok := result[field]; !ok {
			return nil, errProbe
		}
	}
	return result, nil
}

const maximumProbeOutput = 4096

var errProbe = errors.New("local diagnostic query unavailable")
