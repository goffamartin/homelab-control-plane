package controller

import (
	"encoding/json"
	"hlcp/internal/localapi"
	"hlcp/internal/protocol"
	"hlcp/internal/telemetry"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScopedAPIInventoryResultsAndExecutionDenied(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(filepath.Join(dir, "observer.token"), []byte(strings.Repeat("o", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	policy := `{"version":1,"principals":[{"id":"mcp-observer","role":"observer","token_file":"observer.token","scope":{"nodes":{"n":{"resources":["catalog"]}}}}]}`
	if err := os.WriteFile(path, []byte(policy), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := localapi.LoadPolicy(path)
	if err != nil {
		t.Fatal(err)
	}
	tracker, err := OpenCommandTracker(filepath.Join(dir, "ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()
	sessions := NewActiveSessions(tracker)
	server := &Server{sessions: sessions, registry: sessions.registry, config: Config{Telemetry: telemetry.New(nil)}}
	for _, node := range []string{"n", "hidden-node"} {
		server.registry.nodes[node] = NodeRecord{NodeID: node, State: "online", Capabilities: []string{"resource.control", "docker"}, Inventory: protocol.InventorySnapshot{Revision: 1, Resources: []protocol.ResourceDescriptor{{ID: "catalog", Type: "container", Actions: []string{"status", "restart"}}, {ID: "hidden-resource", Type: "container", Actions: []string{"status"}}}}, Resources: protocol.StateSnapshot{InventoryRevision: 1, Resources: []protocol.ResourceState{{ResourceID: "catalog", State: "running"}, {ResourceID: "hidden-resource", State: "hidden-secret-state"}}}}
	}
	for _, resource := range []string{"catalog", "hidden-resource"} {
		_, err := tracker.Admit("n", protocol.CommandPayload{CommandID: resource, ResourceID: resource, Action: "status"}, time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		tracker.CompleteNode("n", protocol.CommandResultPayload{CommandID: resource, Success: true, Code: "OK", Message: resource})
	}
	before, err := os.ReadFile(filepath.Join(dir, "ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	h := server.HTTPHandlerWithAuthorizer(localapi.NewAuthorizer(p))
	for _, test := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/v1/nodes", "", 200}, {"GET", "/v1/nodes/n", "", 200}, {"GET", "/v1/nodes/n/actions", "", 200},
		{"GET", "/v1/nodes/n/resources", "", 200}, {"GET", "/v1/nodes/n/resources/catalog", "", 200}, {"GET", "/v1/nodes/n/resources/catalog/actions", "", 200},
		{"GET", "/v1/nodes/hidden-node", "", 403}, {"GET", "/v1/nodes/n/resources/hidden-resource", "", 403}, {"GET", "/v1/nodes/n/resources/hidden-resource/actions", "", 403},
		{"GET", "/v1/nodes/n/commands/catalog", "", 200}, {"GET", "/v1/nodes/n/commands/hidden-resource", "", 404},
		{"POST", "/v1/nodes/n/resolve", `{"command_id":"preview","resource_id":"catalog","action":"restart"}`, 200},
		{"POST", "/v1/nodes/n/resolve", `{"command_id":"hidden-resource","resource_id":"catalog","action":"status"}`, 404},
		{"POST", "/v1/nodes/n/resolve", `{"command_id":"preview","resource_id":"hidden-resource","action":"status"}`, 403},
		{"POST", "/v1/nodes/n/commands", `{"command_id":"new","resource_id":"catalog","action":"status"}`, 403},
		{"POST", "/v1/nodes/n/commands", `{"command_id":"new","resource_id":"catalog","action":"restart"}`, 403},
		{"POST", "/v1/nodes/n/commands", `broken`, 403},
	} {
		r := httptest.NewRequest(test.method, "http://127.0.0.1"+test.path, strings.NewReader(test.body))
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("o", 32))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Role", "operator")
		r.Header.Set("X-User", "admin")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatalf("%s %s => %d %s", test.method, test.path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "hidden-") {
			t.Fatalf("scope leak: %s", w.Body.String())
		}
	}
	after, _ := os.ReadFile(filepath.Join(dir, "ledger.json"))
	if string(before) != string(after) {
		t.Fatal("observer modified durable ledger")
	}
	data, _ := json.Marshal(server.Telemetry().Events())
	if strings.Contains(string(data), "admin") || !strings.Contains(string(data), "mcp-observer") {
		t.Fatal("audit trusted spoofed actor")
	}
}

type revokingReader struct {
	reader io.Reader
	revoke func()
	done   bool
}

func (r *revokingReader) Read(data []byte) (int, error) {
	if !r.done {
		r.done = true
		r.revoke()
	}
	return r.reader.Read(data)
}

func TestPolicyRevokedDuringDecodeCannotAdmitCommand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(filepath.Join(dir, "api.token"), []byte(strings.Repeat("o", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	operator := `{"version":1,"principals":[{"id":"operator","role":"operator","token_file":"api.token","scope":{"all_nodes":true}}]}`
	if err := os.WriteFile(path, []byte(operator), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := localapi.LoadPolicy(path)
	if err != nil {
		t.Fatal(err)
	}
	a := localapi.NewAuthorizer(p)
	sessions, _, _ := retrySession(t, RetryPolicy{ACKTimeout: 10 * time.Millisecond, ResultTimeout: 20 * time.Millisecond, MaxAttempts: 1})
	server := &Server{sessions: sessions, registry: sessions.registry}
	body := &revokingReader{reader: strings.NewReader(`{"command_id":"revoked","resource_id":"catalog","action":"restart"}`), revoke: func() {
		observer := strings.Replace(operator, `"role":"operator"`, `"role":"observer"`, 1)
		if err := os.WriteFile(path, []byte(observer), 0600); err != nil {
			t.Fatal(err)
		}
		if err := a.Reload(path); err != nil {
			t.Fatal(err)
		}
	}}
	r := httptest.NewRequest("POST", "http://127.0.0.1/v1/nodes/n/commands", body)
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("o", 32))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.HTTPHandlerWithAuthorizer(a).ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if _, exists := sessions.tracker.Progress("n", "revoked"); exists {
		t.Fatal("revoked request admitted to ledger")
	}
}
