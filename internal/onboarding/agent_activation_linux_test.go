package onboarding

import (
	"context"
	"go.yaml.in/yaml/v4"
	"hlcp/internal/agent"
	"hlcp/internal/setup"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const agentUserProperties = "LoadState=loaded\nActiveState=inactive\nUnitFileState=disabled\nFragmentPath=/usr/lib/systemd/user/hlcp-agent.service\nDropInPaths=\nNeedDaemonReload=no\nUser=\nGroup=\n"

func TestNativeAgentPropertiesAndRestrictedCommands(t *testing.T) {
	profile := AgentServiceProfile{"user", "hlcp-agent.service"}
	fields := strings.Split(activationProperties, ",")
	p, err := agentPropertyMap([]byte(agentUserProperties), fields)
	if err != nil || validateAgentUnitProperties(p, profile, false) != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"NeedDaemonReload=no", "NeedDaemonReload=yes"}, {"DropInPaths=", "DropInPaths=/custom"}, {"User=", "User=root"}, {"inactive", "failed"}, {"disabled", "enabled-runtime"}, {"/usr/lib/systemd/user/", "/home/user/.config/systemd/user/"}} {
		p, err := agentPropertyMap([]byte(strings.Replace(agentUserProperties, pair[0], pair[1], 1)), fields)
		if err == nil && validateAgentUnitProperties(p, profile, false) == nil {
			t.Fatal(pair)
		}
	}
	if _, err := agentPropertyMap([]byte(agentUserProperties+"User=\n"), fields); err == nil {
		t.Fatal("duplicate property accepted")
	}
	if _, err := agentPropertyMap([]byte(agentUserProperties+"SECRET=value\n"), fields); err == nil {
		t.Fatal("unknown property accepted")
	}
	if _, err := agentPropertyMap([]byte(strings.Repeat("x", 16*1024+1)), fields); err == nil {
		t.Fatal("overflow accepted")
	}
	p, _ = agentPropertyMap([]byte(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(agentUserProperties, "LoadState=loaded", "LoadState=not-found"), "UnitFileState=disabled", "UnitFileState="), "FragmentPath=/usr/lib/systemd/user/hlcp-agent.service", "FragmentPath=")), fields)
	if validateAgentUnitProperties(p, profile, true) != nil || validateAgentUnitProperties(p, profile, false) == nil {
		t.Fatal("absent peer/selected distinction")
	}
	for _, test := range []struct{ operation, unit string }{{"restart", "hlcp-agent.service"}, {"stop", "hlcp-agent.service"}, {"enable", "other.service"}, {"show", "other.service"}, {"linger", "hlcp-agent.service"}} {
		if _, err := agentServiceProcess(context.Background(), AgentServiceProfile{"system", "hlcp-agent.service"}, test.operation, test.unit, time.Second); err == nil {
			t.Fatal("unapproved command accepted")
		}
	}
	if _, err := (nativeAgentServiceHost{}).InspectAgent(context.Background(), profile); err == nil {
		t.Fatal("test binary/root user scope accepted")
	}
}
func TestNativeAgentImportedConfigReadOnly(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	home := t.TempDir()
	owner := setup.Owner{UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())}
	scope := "user"
	if owner.UID == 0 {
		scope = "system"
	}
	profile := AgentServiceProfile{Scope: scope, Unit: "hlcp-agent.service"}
	journal := filepath.Join(home, ".local", "state", "hlcp", "command-journal.json")
	if scope == "system" {
		journal = "/var/lib/hlcp/command-journal.json"
	}
	p, err := PlanAgent(context.Background(), dir, owner, AgentOptions{NodeID: "debian-01", ControllerAddress: "controller.test:9443", ServerName: "controller.test", JournalFile: journal, AgentVersion: "test", Scope: scope}, agentMaterial(t), testNow)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err = p.Apply(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "agent.key"))
	files, err := agentServiceConfig(context.Background(), dir, owner, profile, home, testNow)
	if err != nil || len(files) != 5 {
		t.Fatal(err)
	}
	if _, err = agentServiceConfig(context.Background(), dir, owner, profile, home, testNow.Add(400*24*time.Hour)); err == nil {
		t.Fatal("expired cert accepted")
	}
	config, err := agent.Parse(files[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	config.NodeID = "wrong-node"
	data, _ := yaml.Marshal(config)
	if err = os.WriteFile(filepath.Join(dir, "agent.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = agentServiceConfig(context.Background(), dir, owner, profile, home, testNow); err == nil {
		t.Fatal("node URI mismatch accepted")
	}
	config.NodeID = "debian-01"
	config.Capabilities = []string{"resource.control", "podman"}
	data, _ = yaml.Marshal(config)
	os.WriteFile(filepath.Join(dir, "agent.yaml"), data, 0600)
	if _, err = agentServiceConfig(context.Background(), dir, owner, profile, home, testNow); err != nil {
		t.Fatal("capabilities alone must not introduce resources", err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "agent.key"))
	if string(after) != string(before) {
		t.Fatal("credentials changed")
	}
}
func TestNativeAgentJournalMetadataDoesNotOpenHistory(t *testing.T) {
	// Strict parent trust (no sticky /tmp); fixtures run in a disposable root container.
	if os.Geteuid() != 0 {
		t.Skip("root fixture directory")
	}
	dir, err := os.MkdirTemp("/root", "hlcp-agent-state-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "history.json")
	owner := setup.Owner{}
	if err := agentJournalReadiness(path, owner); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("PRIVATE existing unknown outcomes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = agentJournalReadiness(path, owner); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "PRIVATE existing unknown outcomes" {
		t.Fatal("journal modified")
	}
	link := filepath.Join(dir, "link.json")
	os.Symlink(path, link)
	if agentJournalReadiness(link, owner) == nil {
		t.Fatal("journal symlink accepted")
	}
	os.Chmod(path, 0644)
	if agentJournalReadiness(path, owner) == nil {
		t.Fatal("public journal accepted")
	}
}

func TestNativePackagedAgentServiceAccount(t *testing.T) {
	if os.Getenv("HLCP_TEST_SERVICE_ACCOUNT") != "1" {
		t.Skip("isolated service account fixture")
	}
	account, err := serviceActivationAccount("hlcp")
	if err != nil || !strings.HasPrefix(account, "hlcp:") {
		t.Fatal("agent service account refused", err)
	}
	if _, err = serviceActivationAccount("root"); err == nil {
		t.Fatal("unapproved service account accepted")
	}
}
