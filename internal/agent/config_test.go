package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validConfigYAML = `controller_address: controller.test:9443
server_name: controller.test
certificate_file: /etc/hlcp/agent.crt
private_key_file: /etc/hlcp/agent.key
ca_file: /etc/hlcp/ca.crt
journal_file: /var/lib/hlcp/command-journal.json
journal_retention: 1024
node_id: debian-01
agent_version: 0.1.0
capabilities: [resource.control, docker]
resources:
  - id: example-catalog
    type: container
    adapter: docker
    target: example-catalog
    actions: [status, restart]
`

func TestLoadRejectsUnknownConfigurationField(t *testing.T) {
	path := writeConfig(t, validConfigYAML+"unexpected: true\n")
	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want unknown field error")
	}
}

func TestObservationConfigurationDoesNotDeclareState(t *testing.T) {
	if _, err := Load(writeConfig(t, validConfigYAML+"    state: running\n")); err == nil {
		t.Fatal("legacy YAML state accepted")
	}
	for _, interval := range []string{"-1s", "500ms", "6m"} {
		if _, err := Load(writeConfig(t, validConfigYAML+"observation_interval: "+interval+"\n")); err == nil {
			t.Fatalf("interval=%s accepted", interval)
		}
	}
	if _, err := Load(writeConfig(t, validConfigYAML+"observation_interval: 10s\n")); err != nil {
		t.Fatal(err)
	}
}

func TestConfigRequiresDeclaredResourceCapabilities(t *testing.T) {
	for _, capabilities := range []string{"[]", "[docker]", "[resource.control]"} {
		config := strings.Replace(validConfigYAML, "[resource.control, docker]", capabilities, 1)
		if _, err := Load(writeConfig(t, config)); err == nil {
			t.Fatalf("accepted capabilities %s", capabilities)
		}
	}
}

func TestLoadRejectsMultipleYAMLDocuments(t *testing.T) {
	path := writeConfig(t, validConfigYAML+"---\nnode_id: other\n")
	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want multiple document error")
	}
}

func TestConfigRejectsInvalidAdapterActionAndTarget(t *testing.T) {
	for name, replacement := range map[string]string{
		"adapter": "adapter: shell",
		"action":  "actions: [status, exec]",
		"target":  "target: --all",
	} {
		t.Run(name, func(t *testing.T) {
			config := validConfigYAML
			switch name {
			case "adapter":
				config = strings.Replace(config, "adapter: docker", replacement, 1)
			case "action":
				config = strings.Replace(config, "actions: [status, restart]", replacement, 1)
			case "target":
				config = strings.Replace(config, "target: example-catalog", replacement, 1)
			}
			if _, err := Load(writeConfig(t, config)); err == nil {
				t.Fatal("Load() error = nil, want validation error")
			}
		})
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
