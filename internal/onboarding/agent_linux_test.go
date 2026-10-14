package onboarding

import (
	"context"
	"errors"
	"fmt"
	"hlcp/internal/agent"
	"hlcp/internal/setup"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentPlanConsentRepeatAndPreservation(t *testing.T) {
	directory := t.TempDir()
	os.Chmod(directory, 0700)
	journal := filepath.Join(t.TempDir(), "existing-journal")
	if err := os.WriteFile(journal, []byte("PRIVATE existing history"), 0600); err != nil {
		t.Fatal(err)
	}
	options := AgentOptions{NodeID: "debian-01", ControllerAddress: "controller.test:9443", ServerName: "controller.test", JournalFile: journal, AgentVersion: "test"}
	owner := setup.Owner{UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())}
	material := agentMaterial(t)
	plan, err := PlanAgent(context.Background(), directory, owner, options, material, testNow)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	entries, _ := os.ReadDir(directory)
	if len(entries) != 0 || len(plan.Preview().Changes) != 5 {
		t.Fatal("preview mutated disk")
	}
	if _, err := plan.Apply(context.Background(), false); !errors.Is(err, setup.ErrConsentRequired) {
		t.Fatal("consent bypass")
	}
	if result, err := plan.Apply(context.Background(), true); err != nil || result.Changed != 5 {
		t.Fatalf("%+v %v", result, err)
	}
	config, err := agent.Load(filepath.Join(directory, "agent.yaml"))
	if err != nil || len(config.Resources) != 0 || len(config.Capabilities) != 0 || config.NodeID != options.NodeID {
		t.Fatalf("config %v", err)
	}
	again, err := PlanAgent(context.Background(), directory, owner, options, material, testNow)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	for _, change := range again.Preview().Changes {
		if change.Action != "unchanged" {
			t.Fatal("not idempotent")
		}
	}
	if result, err := again.Apply(context.Background(), true); err != nil || result.Changed != 0 {
		t.Fatalf("repeat %+v %v", result, err)
	}
	data, err := os.ReadFile(journal)
	if err != nil || string(data) != "PRIVATE existing history" {
		t.Fatal("journal changed")
	}
	path := filepath.Join(directory, "agent.yaml")
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	manual := append(data, []byte("\n# manual configuration\n")...)
	os.WriteFile(path, manual, 0600)
	if _, err := PlanAgent(context.Background(), directory, owner, options, material, testNow); !errors.Is(err, setup.ErrExistingConflict) {
		t.Fatalf("manual config %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(manual) {
		t.Fatal("manual config overwritten")
	}
	for _, name := range []string{"agent.yaml", "agent.crt", "agent.key", "ca.crt", "issuer-ca.crt"} {
		info, _ := os.Stat(filepath.Join(directory, name))
		if info.Mode().Perm() != 0600 {
			t.Fatal("private modes")
		}
	}
	report := fmt.Sprintf("%+v %#v", plan, plan)
	for _, secret := range []string{"BEGIN", "PRIVATE", "debian-01"} {
		if strings.Contains(report, secret) {
			t.Fatal("private plan formatting leaked")
		}
	}
	options.JournalFile = filepath.Join(directory, ".hlcp-setup-backup", "manifest.json")
	if _, err := PlanAgent(context.Background(), directory, owner, options, material, testNow); !errors.Is(err, ErrAgentOptions) {
		t.Fatal("state/config collision accepted")
	}
}
