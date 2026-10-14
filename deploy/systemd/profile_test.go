package systemd

import (
	"strings"
	"testing"
)

func TestPackagedAgentProfilesMatchBinarySubstitution(t *testing.T) {
	for _, profile := range [][2]string{{"system", "hlcp-agent.service"}, {"user", "hlcp-agent.service"}, {"user", "hlcp-agent-podman.service"}} {
		unit, ok := PackagedAgentUnit(profile[0], profile[1])
		if !ok || strings.Contains(unit, "/usr/local/bin/") || strings.Contains(unit, "%h/.local/bin/") || !strings.Contains(unit, "ExecStart=/usr/bin/hlcp-agent") {
			t.Fatal(profile)
		}
		if profile[0] == "system" && !strings.Contains(unit, "LoadCredential=agent.key:") {
			t.Fatal("system credentials missing")
		}
		if profile[0] == "user" && strings.Contains(unit, "NoNewPrivileges=true") {
			t.Fatal("user mapping helper profile restricted")
		}
	}
	for _, profile := range [][2]string{{"system", "hlcp-agent-podman.service"}, {"user", "other.service"}, {"remote", "hlcp-agent.service"}} {
		if _, ok := PackagedAgentUnit(profile[0], profile[1]); ok {
			t.Fatal(profile)
		}
	}
}
