// Package systemd contains the authoritative distributed service profiles.
package systemd

import (
	_ "embed"
	"strings"
)

//go:embed hlcp-controller.service
var controllerUnit string

//go:embed hlcp-agent.service
var agentSystemUnit string

//go:embed user/hlcp-agent.service
var agentUserUnit string

//go:embed user/hlcp-agent-podman.service
var agentPodmanUnit string

// PackagedAgentUnit selects only distributed profiles; no arbitrary unit paths.
func PackagedAgentUnit(scope, unit string) (string, bool) {
	var template string
	switch {
	case scope == "system" && unit == "hlcp-agent.service":
		template = agentSystemUnit
	case scope == "user" && unit == "hlcp-agent.service":
		template = agentUserUnit
	case scope == "user" && unit == "hlcp-agent-podman.service":
		template = agentPodmanUnit
	default:
		return "", false
	}
	template = strings.ReplaceAll(template, "\r\n", "\n")
	template = strings.ReplaceAll(template, "%h/.local/bin/hlcp-agent", "/usr/bin/hlcp-agent")
	return strings.ReplaceAll(template, "/usr/local/bin/hlcp-agent", "/usr/bin/hlcp-agent"), true
}

// PackagedControllerUnit applies the same binary path substitution as packaging.
func PackagedControllerUnit() string {
	return strings.ReplaceAll(strings.ReplaceAll(controllerUnit, "\r\n", "\n"), "/usr/local/bin/hlcp-controller", "/usr/bin/hlcp-controller")
}
