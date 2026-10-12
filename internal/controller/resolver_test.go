package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hlcp/internal/liveness"
	"hlcp/internal/protocol"
)

func TestResolveAndExecuteShareRejections(t *testing.T) {
	for _, test := range []struct {
		name, node, resource, action, code string
		status                             int
		change                             func(*Server)
	}{
		{name: "missing-node", node: "missing", resource: "catalog", action: "restart", status: 404, code: "NODE_NOT_FOUND"},
		{name: "missing-resource", node: "n", resource: "missing", action: "restart", status: 404, code: "RESOURCE_NOT_FOUND"},
		{name: "unsupported-action", node: "n", resource: "catalog", action: "remove", status: 422, code: "ACTION_NOT_SUPPORTED"},
		{name: "offline", node: "n", resource: "catalog", action: "restart", status: 503, code: "NODE_OFFLINE", change: func(s *Server) {
			record := s.registry.nodes["n"]
			record.State = liveness.NodeOffline
			s.registry.nodes["n"] = record
		}},
		{name: "no-control-capability", node: "n", resource: "catalog", action: "restart", status: 422, code: "CAPABILITY_NOT_SUPPORTED", change: func(s *Server) {
			record := s.registry.nodes["n"]
			record.Capabilities = []string{"docker"}
			s.registry.nodes["n"] = record
		}},
		{name: "no-runtime-capability", node: "n", resource: "catalog", action: "restart", status: 422, code: "CAPABILITY_NOT_SUPPORTED", change: func(s *Server) {
			record := s.registry.nodes["n"]
			record.Capabilities = []string{"resource.control"}
			s.registry.nodes["n"] = record
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			sessions, _, _ := retrySession(t, DefaultRetryPolicy())
			server := &Server{registry: sessions.registry, sessions: sessions}
			if test.change != nil {
				test.change(server)
			}
			body, _ := json.Marshal(protocol.CommandPayload{CommandID: "c", ResourceID: test.resource, Action: test.action})
			for _, route := range []string{"resolve", "commands"} {
				response := httptest.NewRecorder()
				server.HTTPHandler(testAPIToken).ServeHTTP(response, apiRequest(http.MethodPost, "/v1/nodes/"+test.node+"/"+route, string(body)))
				if response.Code != test.status || !strings.Contains(response.Body.String(), test.code) {
					t.Fatalf("route=%s response=%d %s", route, response.Code, response.Body.String())
				}
			}
			if _, exists := sessions.tracker.Progress("n", "c"); exists {
				t.Fatal("rejected operation entered ledger")
			}
		})
	}
}

func TestDryRunDoesNotWriteLedgerOrCommand(t *testing.T) {
	sessions, _, peer := retrySession(t, DefaultRetryPolicy())
	path := filepath.Join(t.TempDir(), "ledger.json")
	tracker, err := OpenCommandTracker(path)
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()
	sessions.tracker = tracker
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{registry: sessions.registry, sessions: sessions}
	response := httptest.NewRecorder()
	server.HTTPHandler(testAPIToken).ServeHTTP(response, apiRequest(http.MethodPost, "/v1/nodes/n/resolve", `{"command_id":"c","resource_id":"catalog","action":"restart"}`))
	var plan Resolution
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &plan) != nil || plan.Command == nil || plan.KnownOperation || plan.Action != "restart" {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("dry-run changed durable ledger")
	}
	peer.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	if _, err := protocol.Decode(peer); err == nil {
		t.Fatal("dry-run sent a frame")
	}
}

func TestResolverPreservesKnownOperationWithoutInventory(t *testing.T) {
	tracker := NewCommandTracker()
	tracker.Admit("n", retryCommand(), time.Now().Add(time.Minute))
	sessions := NewActiveSessions(tracker)
	server := &Server{sessions: sessions, registry: sessions.registry}
	plan, err := server.Resolve("n", retryCommand())
	if err != nil || !plan.KnownOperation || !plan.ReadOnly {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	payload := retryCommand()
	payload.Action = "stop"
	if _, err := server.Resolve("n", payload); err == nil || err.Code != "COMMAND_CONFLICT" {
		t.Fatalf("err=%v", err)
	}
}

func TestNodeActionAndRealResourceDetail(t *testing.T) {
	sessions, _, _ := retrySession(t, DefaultRetryPolicy())
	server := &Server{registry: sessions.registry, sessions: sessions}
	record := server.registry.nodes["n"]
	record.State = liveness.NodeOffline
	record.Resources = protocol.StateSnapshot{InventoryRevision: 1, Resources: []protocol.ResourceState{{ResourceID: "catalog", State: "stopped", ObservedAt: time.Now().UTC(), Code: "OK"}}}
	server.registry.nodes["n"] = record
	plan, err := server.Resolve("n", protocol.CommandPayload{Action: "status"})
	if err != nil || !plan.ReadOnly || plan.Command != nil {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	if _, err := server.Resolve("n", protocol.CommandPayload{Action: "restart"}); err == nil || err.Code != "ACTION_NOT_SUPPORTED" {
		t.Fatalf("err=%v", err)
	}
	response := httptest.NewRecorder()
	server.HTTPHandler(testAPIToken).ServeHTTP(response, apiRequest(http.MethodGet, "/v1/nodes/n/resources/catalog", ""))
	var detail ResourceDetail
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &detail) != nil || detail.ID != "catalog" || detail.Observation == nil || detail.Observation.State != "stopped" || !detail.Observation.Stale {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
}

func TestResolveRejectsMalformedJSON(t *testing.T) {
	sessions, _, _ := retrySession(t, DefaultRetryPolicy())
	server := &Server{registry: sessions.registry, sessions: sessions}
	for _, body := range []string{`{"action":"status","extra":true}`, `{"action":"status"} {}`, strings.Repeat(" ", 8193) + `{}`} {
		response := httptest.NewRecorder()
		server.HTTPHandler(testAPIToken).ServeHTTP(response, apiRequest(http.MethodPost, "/v1/nodes/n/resolve", body))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("response=%d %s", response.Code, response.Body.String())
		}
	}
}
