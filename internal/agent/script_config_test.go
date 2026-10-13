package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const scriptConfigYAML = `controller_address: controller.test:9443
server_name: controller.test
certificate_file: /etc/hlcp/agent.crt
private_key_file: /etc/hlcp/agent.key
ca_file: /etc/hlcp/ca.crt
journal_file: /var/lib/hlcp/command-journal.json
node_id: debian-01
agent_version: test
capabilities: [resource.control, script]
resources:
  - id: diagnostics
    type: task
    adapter: script
    actions: [diagnose, reload]
    commands:
      diagnose:
        executable: /bin/bash
        argv: [/opt/hlcp/diagnose.sh]
        working_directory: /opt/hlcp
        environment: {LANG: C.UTF-8}
      reload:
        executable: /opt/hlcp/reload
        argv: []
        working_directory: /opt/hlcp
        timeout: 20s
`

func TestScriptConfigurationContract(t *testing.T) {
	config, err := Load(writeConfig(t, scriptConfigYAML))
	if err != nil {
		t.Fatal(err)
	}
	resource := config.Resources[0]
	if resource.Commands["diagnose"].ExecutionTimeout() != 10*time.Second || resource.Commands["reload"].ExecutionTimeout() != 20*time.Second {
		t.Fatal("incorrect timeout defaults")
	}
	sync, err := config.synchronization()
	if err != nil || sync.Inventory.Resources[0].Type != "task" || sync.State.Resources[0].Code != "NOT_OBSERVED" {
		t.Fatalf("sync=%+v err=%v", sync, err)
	}
	// P02 validates without probing nonexistent executable/cwd/TLS files.
	// P03 will remove this staged startup guard only when execution is ready.
	config.JournalFile = filepath.Join(t.TempDir(), "journal.json")
	if _, err := config.NewDispatcher(); err == nil || !strings.Contains(err.Error(), "P03") {
		t.Fatalf("script execution was enabled prematurely: %v", err)
	}
	if _, err := os.Stat(config.JournalFile); !os.IsNotExist(err) {
		t.Fatalf("staged script startup touched journal: %v", err)
	}
}

func TestScriptYAMLRejectsAmbiguity(t *testing.T) {
	for name, change := range map[string][2]string{
		"duplicate-command":     {"      diagnose:\n", "      diagnose: {}\n      diagnose:\n"},
		"duplicate-field":       {"        executable: /bin/bash", "        executable: /bin/bash\n        executable: /bin/true"},
		"unknown-field":         {"        executable: /bin/bash", "        command: echo unsafe"},
		"case-variant-field":    {"        argv:", "        Argv:"},
		"missing-command":       {"      reload:", "      extra:"},
		"duplicate-action":      {"[diagnose, reload]", "[diagnose, diagnose]"},
		"missing-action":        {"[diagnose, reload]", "[diagnose]"},
		"invalid-action":        {"[diagnose, reload]", "['bash -c', reload]"},
		"wrong-type":            {"type: task", "type: container"},
		"target":                {"adapter: script", "adapter: script\n    target: anything"},
		"no-script-capability":  {"[resource.control, script]", "[resource.control, docker]"},
		"no-control-capability": {"[resource.control, script]", "[script]"},
		"relative-executable":   {"executable: /bin/bash", "executable: bash"},
		"relative-cwd":          {"working_directory: /opt/hlcp", "working_directory: ."},
		"negative-timeout":      {"timeout: 20s", "timeout: -1s"},
		"excess-timeout":        {"timeout: 20s", "timeout: 21s"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, strings.Replace(scriptConfigYAML, change[0], change[1], 1))); err == nil {
				t.Fatal("invalid script configuration accepted")
			}
		})
	}
	if _, err := Load(writeConfig(t, validConfigYAML+"    commands: {}\n")); err == nil {
		t.Fatal("non-script commands silently ignored")
	}
}

func TestScriptCommandLimits(t *testing.T) {
	valid := func() ScriptCommandConfig {
		return ScriptCommandConfig{Executable: "/bin/bash", WorkingDirectory: "/opt/hlcp"}
	}
	for name, mutate := range map[string]func(*ScriptCommandConfig){
		"argv-count": func(c *ScriptCommandConfig) { c.Argv = make([]string, 33) },
		"argv-bytes": func(c *ScriptCommandConfig) { c.Argv = []string{strings.Repeat("x", 8192)} },
		"argv-nul":   func(c *ScriptCommandConfig) { c.Argv = []string{"\x00"} },
		"argv-utf8":  func(c *ScriptCommandConfig) { c.Argv = []string{string([]byte{255})} },
		"env-count": func(c *ScriptCommandConfig) {
			c.Environment = map[string]string{}
			for i := 0; i < 33; i++ {
				c.Environment[fmt.Sprintf("VAR%d", i)] = ""
			}
		},
		"env-bytes":         func(c *ScriptCommandConfig) { c.Environment = map[string]string{"X": strings.Repeat("x", 8191)} },
		"env-name":          func(c *ScriptCommandConfig) { c.Environment = map[string]string{"A=B": "value"} },
		"env-leading-digit": func(c *ScriptCommandConfig) { c.Environment = map[string]string{"1VAR": "value"} },
		"env-nul":           func(c *ScriptCommandConfig) { c.Environment = map[string]string{"VAR": "\x00"} },
		"path-nul":          func(c *ScriptCommandConfig) { c.Executable = "/bin/bash\x00" },
		"path-relative":     func(c *ScriptCommandConfig) { c.Executable = "./bash" },
		"path-unclean":      func(c *ScriptCommandConfig) { c.WorkingDirectory = "/opt/../etc" },
		"path-length":       func(c *ScriptCommandConfig) { c.Executable = "/" + strings.Repeat("x", 4096) },
	} {
		t.Run(name, func(t *testing.T) {
			c := valid()
			mutate(&c)
			if c.validate() == nil {
				t.Fatal("limit accepted")
			}
		})
	}
	c := valid()
	c.Argv = []string{strings.Repeat("x", 8191)}
	c.Environment = map[string]string{"X": strings.Repeat("x", 8189)}
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	c.Argv = []string{"$(touch /tmp/unsafe)", "; rm -rf /", "", "a b"}
	if err := c.validate(); err != nil {
		t.Fatal("literal fixed arguments should not be interpreted as shell syntax", err)
	}
}

func TestScriptActionCountLimit(t *testing.T) {
	resource := ResourceConfig{ID: "task", Type: "task", Adapter: "script", Commands: map[string]ScriptCommandConfig{}}
	for i := 0; i < 32; i++ {
		name := fmt.Sprintf("action%d", i)
		resource.Actions = append(resource.Actions, name)
		resource.Commands[name] = ScriptCommandConfig{Executable: "/bin/true", WorkingDirectory: "/opt"}
	}
	if err := resource.validate(); err != nil {
		t.Fatal(err)
	}
	resource.Actions = append(resource.Actions, "extra")
	resource.Commands["extra"] = resource.Commands["action0"]
	if resource.validate() == nil {
		t.Fatal("33 actions accepted")
	}
}

func TestScriptAndLegacyResourcesCanShareConfiguration(t *testing.T) {
	config, err := Load(writeConfig(t, scriptConfigYAML))
	if err != nil {
		t.Fatal(err)
	}
	config.Capabilities = append(config.Capabilities, "docker", "podman", "systemd")
	config.Resources = append(config.Resources,
		ResourceConfig{ID: "docker-test", Type: "container", Adapter: "docker", Target: "catalog", Actions: []string{"status", "restart"}},
		ResourceConfig{ID: "podman-test", Type: "container", Adapter: "podman", Target: "catalog", Actions: []string{"status", "restart"}},
		ResourceConfig{ID: "systemd-test", Type: "service", Adapter: "systemd", Target: "example.service", Actions: []string{"status", "restart"}},
	)
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
}
