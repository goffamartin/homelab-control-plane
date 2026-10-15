package managedcli

import (
	"context"
	"encoding/json"
	"hlcp/internal/managed"
	"os"
	"path/filepath"
	"testing"
)

func TestSavedControllerRequestAndPreview6Fallback(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("private root-owned setup fixture")
	}
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	store, err := managed.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	config := managed.RuntimeConfig{Version: 1, Listen: "127.0.0.1:19443", ServerName: "127.0.0.1", EnrollmentListen: "127.0.0.1:19445", EnrollmentURL: "https://127.0.0.1:19445"}
	r := controllerRequest{Version: 1, Config: config, State: controllerState, Backup: "/root/hlcp-root-backup.json", CLIUser: "martin", TrustUserGroup: true, Start: true, Enable: true}
	if err := saveControllerRequest(context.Background(), store, r); err != nil {
		t.Fatal(err)
	}
	got, saved, err := loadControllerRequest(dir)
	if err != nil || !saved || got != r {
		t.Fatal("saved choices lost", err)
	}
	if err := os.Remove(filepath.Join(dir, "managed-setup-request.json")); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(controllerBootstrap{Version: 1, Config: config, BackupPath: r.Backup, Provisioned: true})
	if err := store.Create(context.Background(), "managed-bootstrap.json", data); err != nil {
		t.Fatal(err)
	}
	got, saved, err = loadControllerRequest(dir)
	if err != nil || saved || got.Config != config || got.Backup != r.Backup || got.CLIUser != "" {
		t.Fatal("preview.6 defaults not recovered", err)
	}
	data, _ = json.Marshal(controllerRequest{Version: 2})
	if err := store.Create(context.Background(), "managed-setup-request.json", data); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadControllerRequest(dir); err == nil {
		t.Fatal("invalid saved state silently ignored")
	}
}
