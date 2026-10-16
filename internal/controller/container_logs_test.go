package controller

import (
	"encoding/json"
	"hlcp/internal/protocol"
	"hlcp/internal/session"
	"strings"
	"testing"
	"time"
)

func TestContainerLogQueryValidation(t *testing.T) {
	for _, raw := range []string{"tail=-1", "tail=1001", "tail=1&tail=2", "unknown=x", "since=10m", "stream=exec", "limit=0", "limit=201", "cursor=id&contains=SECRET", "cursor=id&tail=2", "contains=", "since=2026-10-16T02%3A00%3A00Z&until=2026-10-16T01%3A00%3A00Z"} {
		if _, _, _, err := parseContainerLogQuery(raw, false); err == nil {
			t.Fatal("invalid query accepted", raw)
		}
	}
	for _, raw := range []string{"limit=100", "cursor=id"} {
		if _, _, _, err := parseContainerLogQuery(raw, true); err == nil {
			t.Fatal("invalid follow accepted", raw)
		}
	}
	q, _, limit, err := parseContainerLogQuery("tail=10&stream=stderr&contains=error&since=2026-10-16T00%3A00%3A00Z", false)
	if err != nil || q.Tail != 10 || q.Stream != "stderr" || q.Contains != "error" || limit != 100 {
		t.Fatal(q, err)
	}
}
func TestLogSnapshotsAreBoundedAndCursorBound(t *testing.T) {
	h := &containerLogHistory{ready: true, key: [32]byte{1}, snapshots: map[string]*containerLogSnapshot{}}
	defer func() {
		h.mutex.Lock()
		if h.timer != nil {
			h.timer.Stop()
		}
		h.mutex.Unlock()
	}()
	snapshot := &containerLogSnapshot{id: logID(), node: "n", resource: "app", binding: [32]byte{2}, created: time.Now(), code: "OK"}
	for i := 0; i < 20; i++ {
		snapshot.records = append(snapshot.records, protocol.LogRecord{Stream: "stdout", Text: strings.Repeat("<", 4096)})
	}
	if !h.store(snapshot) {
		t.Fatal("store")
	}
	page := h.page(snapshot, 0, 200)
	data, _ := json.Marshal(page)
	if len(data) > 66<<10 || len(page.Records) != 2 || page.NextCursor == "" {
		t.Fatal("page byte budget", len(data), len(page.Records))
	}
	found, offset, code := h.lookup(page.NextCursor, "n", "app", snapshot.binding)
	if code != "" || offset != 2 || found != snapshot {
		t.Fatal(offset, code)
	}
	for _, row := range []struct {
		node, resource, cursor string
		binding                [32]byte
	}{{"n", "app", page.NextCursor, [32]byte{3}}, {"other", "app", page.NextCursor, snapshot.binding}, {"n", "other", page.NextCursor, snapshot.binding}, {"n", "app", page.NextCursor + "x", snapshot.binding}} {
		if _, _, code := h.lookup(row.cursor, row.node, row.resource, row.binding); code == "" {
			t.Fatal("foreign or tampered cursor accepted")
		}
	}
	snapshot.created = time.Now().Add(-3 * time.Minute)
	if _, _, code := h.lookup(page.NextCursor, "n", "app", snapshot.binding); code != "CURSOR_EXPIRED" {
		t.Fatal(code)
	}
	for i := 0; i < 10; i++ {
		h.store(&containerLogSnapshot{id: logID(), created: time.Now()})
	}
	if len(h.snapshots) > 8 {
		t.Fatal("unbounded history cache")
	}
}
func TestLogBrokerBackpressureAndStaleWriter(t *testing.T) {
	sessions, writer, _ := retrySession(t, DefaultRetryPolicy())
	read := &logRead{id: "read", node: "n", writer: writer, events: make(chan protocol.LogEvent, 16), failed: make(chan struct{})}
	sessions.logs.pending[read.id] = read
	if sessions.HandleLogEvent("n", new(session.Writer), protocol.LogEvent{RequestID: "read", Sequence: 1, Kind: "start"}) {
		t.Fatal("stale writer accepted")
	}
	started := time.Now()
	sessions.HandleLogEvent("n", writer, protocol.LogEvent{RequestID: "read", Sequence: 1, Kind: "start"})
	for sequence := uint64(2); sequence < 20; sequence++ {
		sessions.HandleLogEvent("n", writer, protocol.LogEvent{RequestID: "read", Sequence: sequence, Kind: "record", Record: &protocol.LogRecord{Stream: "stdout", Text: "PRIVATE"}})
	}
	if time.Since(started) > time.Second || sessions.logs.failure(read) != "BACKPRESSURE" {
		t.Fatal("reader blocked on logs")
	}
	bad := &logRead{id: "bad", node: "n", writer: writer, events: make(chan protocol.LogEvent, 16), failed: make(chan struct{})}
	sessions.logs.pending[bad.id] = bad
	sessions.HandleLogEvent("n", writer, protocol.LogEvent{RequestID: "bad", Sequence: 2, Kind: "start"})
	if sessions.logs.failure(bad) != "INVALID_SEQUENCE" {
		t.Fatal("sequence gap accepted")
	}
}
