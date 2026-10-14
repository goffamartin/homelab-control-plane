// Package systemd contains the authoritative distributed service profiles.
package systemd

import (
	_ "embed"
	"strings"
)

//go:embed hlcp-controller.service
var controllerUnit string

// PackagedControllerUnit applies the same binary path substitution as packaging.
func PackagedControllerUnit() string {
	return strings.ReplaceAll(strings.ReplaceAll(controllerUnit, "\r\n", "\n"), "/usr/local/bin/hlcp-controller", "/usr/bin/hlcp-controller")
}
