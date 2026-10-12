package agent

import (
	"context"
	"hlcp/internal/localapi"
	"hlcp/internal/telemetry"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScopedCredentialsWithRealMTLSCommandPath(t *testing.T) {
	serverFiles, clientFiles := e2ePKI(t)
	dir := t.TempDir()
	diagnostics := telemetry.New(nil)
	fixture := startE2EController(t, serverFiles, filepath.Join(dir, "ledger.json"), diagnostics)
	runner := &e2eRunner{}
	startE2EAgent(t, Config{ControllerAddress: fixture.server.Address().String(), ServerName: clientFiles.ServerName, CertificateFile: clientFiles.CertificateFile, PrivateKeyFile: clientFiles.PrivateKeyFile, CAFile: clientFiles.CAFile, NodeID: "debian-01", AgentVersion: "scope-e2e", Capabilities: []string{"resource.control", "docker"}, JournalFile: filepath.Join(dir, "journal.json"), runner: runner, Resources: []ResourceConfig{{ID: "catalog", Type: "container", Adapter: "docker", Target: "catalog", Actions: []string{"status", "restart"}}}})
	e2eEventually(t, "session ready", func() bool {
		record, ok := fixture.server.Registry().Get("debian-01")
		return ok && record.State == "online"
	})
	policy := `{"version":1,"principals":[{"id":"mcp","role":"observer","token_file":"mcp.token","scope":{"nodes":{"debian-01":{"resources":["catalog"]}}}},{"id":"operator","role":"operator","token_file":"operator.token","scope":{"nodes":{"debian-01":{"resources":["catalog"]}}}}]}`
	for name, data := range map[string]string{"policy.json": policy, "mcp.token": strings.Repeat("m", 32), "operator.token": strings.Repeat("o", 32)} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p, err := localapi.LoadPolicy(filepath.Join(dir, "policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(fixture.server.HTTPHandlerWithAuthorizer(localapi.NewAuthorizer(p)))
	defer api.Close()
	before := diagnostics.Snapshot().Counters["written_frames"]
	// Diagnostic polling shares neither the agent command writer nor the ledger.
	for i := 0; i < 30; i++ {
		r, err := http.NewRequest("GET", api.URL+"/v1/nodes/debian-01/resources/catalog/diagnostics/logs", nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("m", 32))
		response, err := api.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != 200 && response.StatusCode != 429 {
			t.Fatal(response.StatusCode)
		}
	}
	if runner.calls.Load() != 0 || diagnostics.Snapshot().Counters["command_admitted"] != 0 || diagnostics.Snapshot().Counters["written_frames"] != before {
		t.Fatal("diagnostics modified the command path")
	}
	for _, test := range []struct {
		token, action string
		status        int
	}{{"m", "status", 403}, {"m", "restart", 403}, {"o", "restart", 200}} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		r, err := http.NewRequestWithContext(ctx, "POST", api.URL+"/v1/nodes/debian-01/commands", strings.NewReader(`{"command_id":"scope-op","resource_id":"catalog","action":"`+test.action+`"}`))
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+strings.Repeat(test.token, 32))
		r.Header.Set("X-Role", "operator")
		response, err := api.Client().Do(r)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		_ = response.Body.Close()
		cancel()
		if response.StatusCode != test.status {
			t.Fatal(response.StatusCode)
		}
		if test.token == "m" && (runner.calls.Load() != 0 || diagnostics.Snapshot().Counters["command_admitted"] != 0 || diagnostics.Snapshot().Counters["written_frames"] != before) {
			t.Fatal("observer emitted command or side effect")
		}
	}
	if runner.calls.Load() != 1 {
		t.Fatal("operator did not execute exactly once")
	}
	found := false
	for _, event := range diagnostics.Events() {
		if event.Name == "command_admitted" && event.CommandID == "scope-op" {
			found = event.ActorID == "operator"
		}
	}
	if !found {
		t.Fatal("authenticated operator missing from audit")
	}
}
