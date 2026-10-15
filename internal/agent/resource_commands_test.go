package agent

import (
	"go.yaml.in/yaml/v4"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func mixedConfig(t *testing.T, adapter string) Config {
	t.Helper()
	c, err := Parse([]byte(validConfigYAML))
	if err != nil {
		t.Fatal(err)
	}
	c.Capabilities = []string{"resource.control", adapter, "script"}
	r := &c.Resources[0]
	r.Adapter = adapter
	if adapter == "systemd" {
		r.Type = "service"
		r.Target = "example.service"
	}
	return c
}

func TestResourceCommandsEmptyRoundTripCompatibility(t *testing.T) {
	for _, name := range []string{"docker", "podman", "systemd"} {
		t.Run(name, func(t *testing.T) {
			for _, empty := range []map[string]ScriptCommandConfig{nil, {}} {
				c := mixedConfig(t, name)
				c.Capabilities = c.Capabilities[:2]
				c.Resources[0].Commands = empty
				raw, err := yaml.Marshal(c)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(raw), "commands:") {
					t.Fatal("empty commands serialized", string(raw))
				}
				parsed, err := Parse(raw)
				if err != nil || len(parsed.Resources[0].Commands) != 0 {
					t.Fatal(err)
				}
				for _, value := range []string{"{}", "null"} {
					// The resource is last except for observation_interval; insert inside it.
					legacy := strings.Replace(string(raw), "observation_interval:", "      commands: "+value+"\nobservation_interval:", 1)
					if _, err := Parse([]byte(legacy)); err != nil {
						t.Fatalf("legacy %s: %v\n%s", value, err, legacy)
					}
				}
			}
		})
	}
}

func TestMixedResourceCommandsValidationAndInventory(t *testing.T) {
	for _, name := range []string{"docker", "podman", "systemd"} {
		t.Run(name, func(t *testing.T) {
			valid := func() Config {
				c := mixedConfig(t, name)
				r := &c.Resources[0]
				r.Actions = append(r.Actions, "backup")
				r.Commands = map[string]ScriptCommandConfig{"backup": {Executable: "/bin/bash", Argv: []string{"/opt/backup.sh"}, WorkingDirectory: "/opt", Environment: map[string]string{"LANG": "C"}}}
				return c
			}
			c := valid()
			if err := c.Validate(); err != nil {
				t.Fatal(err)
			}
			raw, err := yaml.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := Parse(raw)
			if err != nil || !reflect.DeepEqual(parsed.Resources[0], c.Resources[0]) {
				t.Fatal(err)
			}
			sync, err := c.synchronization()
			if err != nil || sync.Inventory.Resources[0].Type != c.Resources[0].Type || !reflect.DeepEqual(sync.Inventory.Resources[0].Actions, []string{"status", "restart", "backup"}) {
				t.Fatal(sync, err)
			}
			for _, reserved := range []string{"status", "start", "stop", "restart"} {
				bad := valid()
				r := &bad.Resources[0]
				r.Actions = append([]string{reserved}, "backup")
				r.Commands[reserved] = r.Commands["backup"]
				if err := bad.Validate(); err == nil {
					t.Fatal("native action overridden", reserved)
				}
			}
			for _, kind := range []string{"missing-command", "unlisted-command", "capability", "relative-executable", "invalid-argv", "timeout"} {
				bad := valid()
				r := &bad.Resources[0]
				spec := r.Commands["backup"]
				switch kind {
				case "missing-command":
					delete(r.Commands, "backup")
				case "unlisted-command":
					r.Commands["hidden"] = spec
				case "capability":
					bad.Capabilities = bad.Capabilities[:2]
				case "relative-executable":
					spec.Executable = "bash"
					r.Commands["backup"] = spec
				case "invalid-argv":
					spec.Argv = []string{"\x00"}
					r.Commands["backup"] = spec
				case "timeout":
					spec.Timeout = MaximumScriptTimeout + 1
					r.Commands["backup"] = spec
				}
				if err := bad.Validate(); err == nil {
					t.Fatal("invalid mixed resource accepted", kind)
				}
			}
			if runtime.GOOS != "linux" {
				c.JournalFile = filepath.Join(t.TempDir(), "journal.json")
				if _, err := c.NewDispatcher(); err == nil {
					t.Fatal("non-Linux execution accepted")
				}
				if _, err := os.Stat(c.JournalFile); !os.IsNotExist(err) {
					t.Fatal("unsupported execution touched journal")
				}
			}
		})
	}
}
