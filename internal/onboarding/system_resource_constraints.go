package onboarding

import (
	"encoding/json"
	"hlcp/deploy/systemd"
	"hlcp/internal/agent"
	"sort"
	"strings"
)

// A controlled refusal, not sandbox readiness or authorization. Codes are fixed
// by this analyzer; config paths, resource names and command data stay private.
type systemResourceRefusal struct{ constraints []string }

func (e *systemResourceRefusal) Error() string                { return ErrAgentActivationResources.Error() }
func (e *systemResourceRefusal) Unwrap() error                { return ErrAgentActivationResources }
func (e *systemResourceRefusal) Constraints() []string        { return append([]string{}, e.constraints...) }
func (e *systemResourceRefusal) MarshalJSON() ([]byte, error) { return json.Marshal(e.Constraints()) }
func (e *systemResourceRefusal) String() string               { data, _ := e.MarshalJSON(); return string(data) }
func (e *systemResourceRefusal) GoString() string             { return e.String() }

// Analyze only the already-validated config against the embedded fixed system
// profile. No filesystem reads, host probes, grants or service calls are allowed.
// Even a script with no detectable path conflict needs a real sandbox probe.
func systemResourceConstraints(config agent.Config) error {
	unit, ok := systemd.PackagedAgentUnit("system", "hlcp-agent.service")
	if !ok || !systemResourceProfileMatches(unit) {
		return &systemResourceRefusal{[]string{"system.profile_contract_changed"}}
	}
	codes := map[string]bool{"system.sandbox_probe_required": true}
	for _, resource := range config.Resources {
		switch resource.Adapter {
		case "docker":
			codes["system.docker_group_not_provisioned"] = true
		case "podman":
			codes["system.podman_privileged_helpers_blocked"] = true
		case "systemd":
			codes["system.systemd_authorization_unverified"] = true
		}
		if len(resource.Commands) > 0 {
			codes["system.script_dependencies_unverified"] = true
			for _, action := range resource.ScriptActions() {
				command := resource.Commands[action]
				for _, path := range []string{command.Executable, command.WorkingDirectory} {
					if underSystemPath(path, "/home") || underSystemPath(path, "/root") || underSystemPath(path, "/run/user") {
						codes["system.script_protected_home"] = true
					}
					if underSystemPath(path, "/tmp") || underSystemPath(path, "/var/tmp") {
						codes["system.script_private_tmp"] = true
					}
				}
				if !underSystemPath(command.WorkingDirectory, "/var/lib/hlcp") && !underSystemPath(command.WorkingDirectory, "/tmp") && !underSystemPath(command.WorkingDirectory, "/var/tmp") {
					codes["system.script_read_only_workdir"] = true
				}
			}
		}
	}
	ordered := make([]string, 0, len(codes))
	for code := range codes {
		ordered = append(ordered, code)
	}
	sort.Strings(ordered)
	return &systemResourceRefusal{ordered}
}
func underSystemPath(value, root string) bool {
	return value == root || strings.HasPrefix(value, root+"/")
}

// Fail closed if the distributed profile changes. Only service-section values
// count; duplicates and supplementary groups invalidate these assumptions.
func systemResourceProfileMatches(unit string) bool {
	properties := map[string]string{}
	service := false
	for _, line := range strings.Split(strings.ReplaceAll(unit, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			service = line == "[Service]"
			continue
		}
		if !service {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return false
		}
		switch key {
		case "User", "Group", "NoNewPrivileges", "PrivateTmp", "ProtectHome", "ProtectSystem", "StateDirectory", "SupplementaryGroups":
			if _, exists := properties[key]; exists {
				return false
			}
			properties[key] = value
		case "Type", "LoadCredential", "ExecStartPre", "ExecStart", "Restart", "RestartSec", "StateDirectoryMode", "RestrictAddressFamilies", "SystemCallArchitectures", "UMask":
			// New sandbox/mount/environment directives require explicit analysis.
		default:
			return false
		}
	}
	for key, want := range map[string]string{"User": "hlcp", "Group": "hlcp", "NoNewPrivileges": "true", "PrivateTmp": "true", "ProtectHome": "true", "ProtectSystem": "strict", "StateDirectory": "hlcp"} {
		if properties[key] != want {
			return false
		}
	}
	_, groups := properties["SupplementaryGroups"]
	return !groups
}
