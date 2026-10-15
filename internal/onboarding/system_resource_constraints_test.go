package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hlcp/deploy/systemd"
	"hlcp/internal/agent"
	"reflect"
	"strings"
	"testing"
)

func TestSystemResourceConstraintsProfileAndPaths(t *testing.T) {
	unit, _ := systemd.PackagedAgentUnit("system", "hlcp-agent.service")
	if !systemResourceProfileMatches(unit) {
		t.Fatal("distributed profile contract changed")
	}
	for _, directive := range []string{"ReadWritePaths=/", "Environment=SECRET=value"} {
		if systemResourceProfileMatches(unit + "\n[Service]\n" + directive + "\n") {
			t.Fatal("new profile directive silently adopted")
		}
	}
	for _, change := range []string{
		strings.Replace(unit, "ProtectHome=true", "ProtectHome=false", 1),
		strings.Replace(unit, "NoNewPrivileges=true", "NoNewPrivileges=false", 1),
		strings.Replace(unit, "Group=hlcp", "Group=docker", 1),
		strings.Replace(unit, "[Service]", "[Service]\nSupplementaryGroups=docker", 1),
		strings.Replace(unit, "[Service]", "[Service]\nPrivateTmp=true", 1),
	} {
		if systemResourceProfileMatches(change) {
			t.Fatal("unsafe profile assumption accepted")
		}
	}
	for _, row := range []struct {
		path, root string
		want       bool
	}{
		{"/home/user/tool", "/home", true}, {"/home", "/home", true}, {"/home-other/tool", "/home", false},
		{"/tmp-other", "/tmp", false}, {"/var/lib/hlcp-other", "/var/lib/hlcp", false},
	} {
		if underSystemPath(row.path, row.root) != row.want {
			t.Fatal(row)
		}
	}
}
func TestSystemResourceConstraintsRedactionAndNoInspection(t *testing.T) {
	c := readinessConfig()
	command := c.Resources[3].Commands["status"]
	command.Executable = "/home/SECRET/tool"
	command.WorkingDirectory = "/tmp/SECRET"
	command.Environment = map[string]string{"SECRET": "TOKEN_SECRET_VALUE"}
	c.Resources[3].Commands["status"] = command
	h := &readinessFake{}
	r, err := PlanResourceReadiness(context.Background(), h, AgentServiceProfile{"system", "hlcp-agent.service"}, c)
	var refusal *systemResourceRefusal
	if !errors.Is(err, ErrAgentActivationResources) || !errors.As(err, &refusal) || len(h.calls) != 0 || r.proof != [32]byte{} {
		t.Fatal(err, h.calls)
	}
	codes := refusal.Constraints()
	if !sortStrings(codes) || len(codes) != 8 {
		t.Fatal(codes)
	}
	codes[0] = "SECRET"
	data, _ := json.Marshal(refusal)
	for _, output := range []string{string(data), fmt.Sprintf("%v %#v", refusal, refusal), err.Error()} {
		if strings.Contains(output, "SECRET") || strings.Contains(output, "TOKEN_SECRET_VALUE") {
			t.Fatal("config leaked")
		}
	}
}
func sortStrings(values []string) bool {
	for i := 1; i < len(values); i++ {
		if values[i] <= values[i-1] {
			return false
		}
	}
	return true
}

func TestMixedSystemResourceRetainsScriptConstraintsAndProbeRefusal(t *testing.T) {
	c := readinessConfig()
	r := c.Resources[1]
	r.Actions = []string{"status", "backup"}
	r.Commands = map[string]agent.ScriptCommandConfig{"backup": {Executable: "/home/SECRET/backup", WorkingDirectory: "/tmp/SECRET"}}
	c.Resources = []agent.ResourceConfig{r}
	h := new(readinessFake)
	_, err := PlanResourceReadiness(context.Background(), h, AgentServiceProfile{"system", "hlcp-agent.service"}, c)
	var refusal *systemResourceRefusal
	if !errors.As(err, &refusal) || len(h.calls) != 0 {
		t.Fatal(err, h.calls)
	}
	codes := refusal.Constraints()
	for _, want := range []string{"system.podman_privileged_helpers_blocked", "system.script_dependencies_unverified", "system.script_protected_home", "system.script_private_tmp", "system.sandbox_probe_required"} {
		found := false
		for _, code := range codes {
			found = found || code == want
		}
		if !found {
			t.Fatal("missing mixed constraint", want, codes)
		}
	}
	if strings.Contains(fmt.Sprint(refusal), "SECRET") {
		t.Fatal("private paths exposed")
	}
	if _, err := sandboxProbeInputs(c); !errors.Is(err, ErrSandboxProbeUnsupported) {
		t.Fatal("mixed native sandbox gate bypassed", err)
	}
}

type sandboxRefusalHost struct {
	*agentActivationFake
	config agent.Config
}

func (h *sandboxRefusalHost) InspectAgent(context.Context, AgentServiceProfile) (AgentActivationSnapshot, error) {
	return AgentActivationSnapshot{}, systemResourceConstraints(h.config)
}
func TestSystemResourceConstraintsCLIReportsWithoutActivation(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		h := &sandboxRefusalHost{newAgentActivationFake(), readinessConfig()}
		h.snapshot.CallerUID = 0
		h.snapshot.CallerGID = 0
		args := []string{"--scope", "system", "--apply", "--yes"}
		if jsonOutput {
			args = append(args, "--json")
		}
		var out bytes.Buffer
		if code := runAgentActivationCLI(args, &out, h); code != 1 || len(h.actions) != 0 || !strings.Contains(out.String(), "system.sandbox_probe_required") || strings.Contains(out.String(), "SECRET") {
			t.Fatal(code, out.String())
		}
		if jsonOutput {
			var report agentActivationReport
			if json.Unmarshal(out.Bytes(), &report) != nil || report.Code != "RESOURCE_PREFLIGHT_REQUIRED" || !report.ReadOnly || !reflect.DeepEqual(report.Constraints, systemResourceConstraints(h.config).(*systemResourceRefusal).Constraints()) {
				t.Fatal(out.String())
			}
		}
	}
}
