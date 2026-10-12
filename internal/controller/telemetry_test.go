package controller

import (
	"encoding/json"
	"errors"
	"hlcp/internal/protocol"
	"hlcp/internal/telemetry"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCommandAuditDurableTransitionsAndNoDuplicateCounts(t *testing.T) {
	tracker := NewCommandTracker()
	tracker.telemetry = telemetry.New(nil)
	now := time.Now()
	tracker.now = func() time.Time { return now }
	command := protocol.CommandPayload{CommandID: "op-1", ResourceID: "catalog", Action: "restart"}
	if fresh, err := tracker.admit("debian-01", command, now.Add(time.Minute), "local-api", "session-1"); err != nil || !fresh {
		t.Fatalf("admit=%v %v", fresh, err)
	}
	if fresh, err := tracker.Admit("debian-01", command, now.Add(time.Minute)); err != nil || fresh {
		t.Fatal("duplicate admission")
	}
	for i := 0; i < 2; i++ {
		if err := tracker.AttemptNode("debian-01", "op-1"); err != nil {
			t.Fatal(err)
		}
	}
	ack := protocol.CommandACKPayload{CommandID: "op-1", Status: "accepted"}
	if !tracker.AckNode("debian-01", ack) || !tracker.AckNode("debian-01", ack) {
		t.Fatal("ack")
	}
	if err := tracker.FinishWait("debian-01", "op-1", "RESULT_TIMEOUT"); err != nil {
		t.Fatal(err)
	}
	if err := tracker.FinishWait("debian-01", "op-1", "RESULT_TIMEOUT"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Second)
	result := protocol.CommandResultPayload{CommandID: "op-1", Code: "secret-code", Message: "secret-result-message"}
	if !tracker.CompleteNode("debian-01", result) || !tracker.CompleteNode("debian-01", result) {
		t.Fatal("late result")
	}
	s := tracker.telemetry.Snapshot()
	for name, want := range map[string]uint64{"command_admitted": 1, "command_duplicate": 1, "command_attempt": 2, "command_retry": 1, "command_ack": 1, "command_unresolved": 1, "command_result": 1, "result_duration_sum_ms": 3000} {
		if s.Counters[name] != want {
			t.Fatalf("%s=%d want %d", name, s.Counters[name], want)
		}
	}
	if s.Gauges["active_commands"] != 0 || s.Gauges["ledger_entries"] != 1 {
		t.Fatalf("gauges=%v", s.Gauges)
	}
	data, _ := json.Marshal(tracker.telemetry.Events())
	if strings.Contains(string(data), "secret-") {
		t.Fatalf("secret escaped: %s", data)
	}
	events := tracker.telemetry.Events()
	if events[0].ActorID != "local-api" || events[len(events)-1].Code != "OTHER_RESULT" {
		t.Fatal("actor/outcome mismatch")
	}
}

func TestFailedPersistenceDoesNotAuditSuccessfulAdmission(t *testing.T) {
	tracker := NewCommandTracker()
	tracker.telemetry = telemetry.New(nil)
	tracker.persistFault = func() error { return errors.New("secret storage location") }
	_, err := tracker.Admit("debian-01", protocol.CommandPayload{CommandID: "op-1", ResourceID: "catalog", Action: "restart"}, time.Now().Add(time.Minute))
	if !errors.Is(err, ErrLedgerFailure) {
		t.Fatal(err)
	}
	s := tracker.telemetry.Snapshot().Counters
	if s["storage_failure"] != 1 || s["command_rejected"] != 1 || s["command_admitted"] != 0 {
		t.Fatalf("counters=%v", s)
	}
	data, _ := json.Marshal(tracker.telemetry.Events())
	if strings.Contains(string(data), "secret") {
		t.Fatal("raw error was logged")
	}
}

func TestAPIAuditNeverCopiesUntrustedHeadersOrURLs(t *testing.T) {
	diagnostics := telemetry.New(nil)
	server := &Server{registry: NewRegistry(), config: Config{Telemetry: diagnostics}}
	handler := server.HTTPHandler(strings.Repeat("x", 32))
	for _, test := range []struct {
		host, origin, token string
		status              int
	}{
		{"evil-secret.example", "", "", 403},
		{"127.0.0.1", "https://secret-origin.example", "", 403},
		{"127.0.0.1", "", "secret-token", 401},
		{"127.0.0.1", "", strings.Repeat("x", 32), 200},
	} {
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/v1/nodes?secret-query=1", nil)
		request.Host = test.host
		request.Header.Set("Origin", test.origin)
		request.Header.Set("Authorization", "Bearer "+test.token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("status=%d", response.Code)
		}
	}
	s := diagnostics.Snapshot().Counters
	if s["authenticated"] != 1 || s["api_rejected"] != 3 {
		t.Fatalf("counters=%v", s)
	}
	data, _ := json.Marshal(diagnostics.Events())
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), strings.Repeat("x", 32)) {
		t.Fatal("header or URI copied into audit")
	}
}
