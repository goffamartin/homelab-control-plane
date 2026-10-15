package onboarding

import (
	"context"
	"errors"
	"fmt"
	"hlcp/internal/agent"
	"reflect"
	"strings"
	"testing"
)

type readinessFake struct {
	calls  []string
	fail   string
	zero   bool
	cancel context.CancelFunc
}

func (h *readinessFake) inspect(code string) ([32]byte, error) {
	h.calls = append(h.calls, code)
	if h.cancel != nil {
		h.cancel()
	}
	if h.fail == code {
		return [32]byte{}, errors.New("SECRET")
	}
	if h.zero {
		return [32]byte{}, nil
	}
	return [32]byte{1}, nil
}
func (h *readinessFake) RuntimeExecutable(_ context.Context, name string) ([32]byte, error) {
	return h.inspect(name)
}
func (h *readinessFake) DockerSocket(context.Context) ([32]byte, error) { return h.inspect("socket") }
func (h *readinessFake) PodmanMappings(context.Context) ([32]byte, error) {
	return h.inspect("mappings")
}
func (h *readinessFake) ScriptFiles(_ context.Context, c agent.ScriptCommandConfig) ([32]byte, error) {
	return h.inspect(c.Executable)
}
func readinessConfig() agent.Config {
	return agent.Config{ControllerAddress: "controller:9443", ServerName: "controller", CertificateFile: "/cert", PrivateKeyFile: "/key", CAFile: "/ca", JournalFile: "/journal", NodeID: "node", AgentVersion: "test", Capabilities: []string{"resource.control", "docker", "podman", "script", "systemd"}, Resources: []agent.ResourceConfig{
		{ID: "docker", Type: "container", Adapter: "docker", Target: "app", Actions: []string{"restart"}},
		{ID: "podman", Type: "container", Adapter: "podman", Target: "app", Actions: []string{"status"}},
		{ID: "service", Type: "service", Adapter: "systemd", Target: "app.service", Actions: []string{"status"}},
		{ID: "task", Type: "task", Adapter: "script", Actions: []string{"status", "diagnose"}, Commands: map[string]agent.ScriptCommandConfig{
			"status":   {Executable: "/opt/status", WorkingDirectory: "/opt", Argv: []string{"SECRET"}},
			"diagnose": {Executable: "/opt/diagnose", WorkingDirectory: "/opt"},
		}},
	}}
}
func TestResourceReadinessOrderingRedactionAndProof(t *testing.T) {
	h := &readinessFake{}
	config := readinessConfig()
	profile := AgentServiceProfile{"user", "hlcp-agent.service"}
	result, err := PlanResourceReadiness(context.Background(), h, profile, config)
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"docker", "socket", "podman", "mappings", "systemctl", "/opt/diagnose", "/opt/status"}
	if !reflect.DeepEqual(h.calls, expected) || result.proof == [32]byte{} {
		t.Fatal(h.calls)
	}
	preview := result.Preview()
	if !preview.ReadOnly || len(preview.Checks) != 7 || len(preview.Unverified) != 3 {
		t.Fatal(preview)
	}
	preview.Checks[0] = "MUTATED"
	preview.Unverified[0] = "MUTATED"
	output := fmt.Sprintf("%v %#v", result, result)
	for _, secret := range []string{"SECRET", "/opt/", "Proof", "MUTATED"} {
		if strings.Contains(output, secret) {
			t.Fatal("private evidence exposed")
		}
	}
	result2, err := PlanResourceReadiness(context.Background(), &readinessFake{}, profile, config)
	if err != nil || result2.proof != result.proof {
		t.Fatal("unstable evidence", err)
	}
	command := config.Resources[3].Commands["status"]
	command.Argv = []string{"changed"}
	config.Resources[3].Commands["status"] = command
	result2, err = PlanResourceReadiness(context.Background(), &readinessFake{}, profile, config)
	if err != nil || result2.proof == result.proof {
		t.Fatal("config not bound to evidence", err)
	}
}
func TestResourceReadinessRefusalsBeforeInspection(t *testing.T) {
	for _, scenario := range []string{"system", "invalid", "systemd-mutation", "missing-host"} {
		t.Run(scenario, func(t *testing.T) {
			h := &readinessFake{}
			var host ResourceReadinessHost = h
			c := readinessConfig()
			p := AgentServiceProfile{"user", "hlcp-agent.service"}
			switch scenario {
			case "system":
				p.Scope = "system"
			case "invalid":
				c.NodeID = ""
			case "systemd-mutation":
				c.Resources[2].Actions = []string{"restart"}
			case "missing-host":
				host = nil
			}
			if _, err := PlanResourceReadiness(context.Background(), host, p, c); !errors.Is(err, ErrAgentActivationResources) || len(h.calls) != 0 {
				t.Fatal(err, h.calls)
			}
		})
	}
	c := readinessConfig()
	c.Resources = nil
	for _, scope := range []string{"user", "system"} {
		if _, err := PlanResourceReadiness(context.Background(), nil, AgentServiceProfile{scope, "hlcp-agent.service"}, c); err != nil {
			t.Fatal(err)
		}
	}
}
func TestResourceReadinessFailedEvidenceAndCancellation(t *testing.T) {
	profile := AgentServiceProfile{"user", "hlcp-agent.service"}
	for _, code := range []string{"docker", "socket", "podman", "mappings", "systemctl", "/opt/diagnose", "/opt/status"} {
		h := &readinessFake{fail: code}
		r, err := PlanResourceReadiness(context.Background(), h, profile, readinessConfig())
		if !errors.Is(err, ErrAgentActivationResources) || strings.Contains(err.Error(), "SECRET") || r.proof != [32]byte{} {
			t.Fatal(err)
		}
	}
	if _, err := PlanResourceReadiness(context.Background(), &readinessFake{zero: true}, profile, readinessConfig()); !errors.Is(err, ErrAgentActivationResources) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := &readinessFake{}
	if _, err := PlanResourceReadiness(ctx, h, profile, readinessConfig()); !errors.Is(err, context.Canceled) || len(h.calls) != 0 {
		t.Fatal(err, h.calls)
	}
	ctx, cancel = context.WithCancel(context.Background())
	h = &readinessFake{cancel: cancel}
	if r, err := PlanResourceReadiness(ctx, h, profile, readinessConfig()); !errors.Is(err, context.Canceled) || r.proof != [32]byte{} {
		t.Fatal(err)
	}
}

func TestResourceReadinessBoundedChecks(t *testing.T) {
	for _, count := range []int{128, 129, 257} {
		c := readinessConfig()
		template := c.Resources[3]
		c.Resources = nil
		for i := 0; i < count; i++ {
			resource := template
			resource.ID = fmt.Sprintf("task-%d", i)
			resource.Actions = []string{"status"}
			resource.Commands = map[string]agent.ScriptCommandConfig{"status": template.Commands["status"]}
			c.Resources = append(c.Resources, resource)
		}
		h := &readinessFake{}
		_, err := PlanResourceReadiness(context.Background(), h, AgentServiceProfile{"user", "hlcp-agent.service"}, c)
		if count == 128 {
			if err != nil || len(h.calls) != 128 {
				t.Fatal(count, err, len(h.calls))
			}
		} else if !errors.Is(err, ErrAgentActivationResources) || len(h.calls) != 0 {
			t.Fatal(count, err, len(h.calls))
		}
	}
}

func TestResourceReadinessScriptOnlyHasNoRuntimeDependency(t *testing.T) {
	c := readinessConfig()
	c.Resources = c.Resources[3:]
	c.Capabilities = []string{"resource.control", "script"}
	h := &readinessFake{}
	if _, err := PlanResourceReadiness(context.Background(), h, AgentServiceProfile{"user", "hlcp-agent-podman.service"}, c); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h.calls, []string{"/opt/diagnose", "/opt/status"}) {
		t.Fatal("compatibility unit must not introduce a container runtime dependency", h.calls)
	}
}

func TestMixedReadinessChecksNativeRuntimeAndCustomFiles(t *testing.T) {
	for _, name := range []string{"docker", "podman", "systemd"} {
		t.Run(name, func(t *testing.T) {
			c := readinessConfig()
			r := c.Resources[0]
			r.Adapter = name
			r.Actions = []string{"status", "backup"}
			r.Commands = map[string]agent.ScriptCommandConfig{"backup": {Executable: "/opt/backup", WorkingDirectory: "/opt", Argv: []string{"SECRET"}}}
			if name == "systemd" {
				r.Type = "service"
				r.Target = "app.service"
			}
			c.Resources = []agent.ResourceConfig{r}
			host := new(readinessFake)
			result, err := PlanResourceReadiness(context.Background(), host, AgentServiceProfile{"user", "hlcp-agent.service"}, c)
			if err != nil {
				t.Fatal(err)
			}
			expected := []string{name}
			switch name {
			case "docker":
				expected = append(expected, "socket")
			case "podman":
				expected = append(expected, "mappings")
			case "systemd":
				expected = []string{"systemctl"}
			}
			expected = append(expected, "/opt/backup")
			if !reflect.DeepEqual(host.calls, expected) {
				t.Fatal(host.calls, expected)
			}
			if strings.Contains(fmt.Sprint(result), "SECRET") || strings.Contains(fmt.Sprint(result), "/opt/") {
				t.Fatal("private data exposed")
			}
			for _, failure := range expected {
				if _, err := PlanResourceReadiness(context.Background(), &readinessFake{fail: failure}, AgentServiceProfile{"user", "hlcp-agent.service"}, c); !errors.Is(err, ErrAgentActivationResources) {
					t.Fatal("missing mixed dependency accepted", failure, err)
				}
			}
			spec := r.Commands["backup"]
			spec.Argv = []string{"changed"}
			r.Commands["backup"] = spec
			changed, err := PlanResourceReadiness(context.Background(), new(readinessFake), AgentServiceProfile{"user", "hlcp-agent.service"}, c)
			if err != nil || changed.proof == result.proof {
				t.Fatal("custom command not bound to readiness proof", err)
			}
		})
	}
}

func TestMixedReadinessCommandLimitBeforeInspection(t *testing.T) {
	c := readinessConfig()
	c.Resources = nil
	for i := 0; i < 129; i++ {
		c.Resources = append(c.Resources, agent.ResourceConfig{ID: fmt.Sprintf("container-%d", i), Type: "container", Adapter: "podman", Target: "app", Actions: []string{"status", "backup"}, Commands: map[string]agent.ScriptCommandConfig{"backup": {Executable: "/opt/backup", WorkingDirectory: "/opt"}}})
	}
	h := new(readinessFake)
	if _, err := PlanResourceReadiness(context.Background(), h, AgentServiceProfile{"user", "hlcp-agent.service"}, c); !errors.Is(err, ErrAgentActivationResources) || len(h.calls) != 0 {
		t.Fatal("mixed script bound bypassed", err, h.calls)
	}
}
