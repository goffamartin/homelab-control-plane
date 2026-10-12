package command

import (
	"context"
	"encoding/json"
	"fmt"
	"hlcp/internal/protocol"
	"hlcp/internal/telemetry"
	"path/filepath"
	"strings"
	"testing"
)

func TestDispatcherAuditDoesNotCopyAdapterOutput(t *testing.T) {
	journal, err := OpenFileJournal(filepath.Join(t.TempDir(), "journal.json"), 16)
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := telemetry.New(nil)
	dispatcher := NewObservedDispatcher(journal, diagnostics, "debian-01")
	t.Cleanup(func() { _ = dispatcher.Close() })
	dispatcher.Register("catalog", func(context.Context, string) protocol.CommandResultPayload {
		return protocol.CommandResultPayload{Success: true, Code: "OK", Message: "private-key-secret-from-container"}
	})
	command := protocol.CommandPayload{CommandID: "op-1", ResourceID: "catalog", Action: "restart"}
	_, result := dispatcher.Execute(command)
	if !result.Success {
		t.Fatal(result)
	}
	_, result = dispatcher.Execute(command)
	if !result.Success {
		t.Fatal(result)
	}
	_, result = dispatcher.Execute(protocol.CommandPayload{CommandID: "op-2", ResourceID: "missing", Action: "restart"})
	if result.Code != "TARGET_NOT_FOUND" {
		t.Fatal(result)
	}
	s := diagnostics.Snapshot().Counters
	if s["command_admitted"] != 1 || s["command_duplicate"] != 1 || s["command_running"] != 1 || s["command_result"] != 1 || s["command_rejected"] != 1 {
		t.Fatalf("counters=%v", s)
	}
	data, _ := json.Marshal(diagnostics.Events())
	if strings.Contains(string(data), "secret") {
		t.Fatal("adapter output copied into audit")
	}
	entries, capacity := journal.Usage()
	if entries != 1 || capacity != 16 {
		t.Fatalf("usage=%d/%d", entries, capacity)
	}
}

func TestCapacityRejectionsAreObservableWithoutGrowingQueue(t *testing.T) {
	for _, limit := range []int{1, 64} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			journal, err := OpenFileJournal(filepath.Join(t.TempDir(), "journal.json"), limit)
			if err != nil {
				t.Fatal(err)
			}
			diagnostics := telemetry.New(nil)
			dispatcher := NewObservedDispatcher(journal, diagnostics, "debian-01")
			defer dispatcher.Close()
			dispatcher.Register("catalog", func(context.Context, string) protocol.CommandResultPayload {
				t.Error("executed without start permission")
				return protocol.CommandResultPayload{Code: "EXECUTION_UNKNOWN"}
			})
			count, want := QueueCapacity, "QUEUE_FULL"
			if limit == 1 {
				count, want = 1, "JOURNAL_FULL"
			}
			for i := 0; i < count; i++ {
				if a := dispatcher.Admit(protocol.CommandPayload{CommandID: fmt.Sprint(i), ResourceID: "catalog", Action: "restart"}); a.Ack.Status != "accepted" {
					t.Fatal(a)
				}
			}
			a := dispatcher.Admit(protocol.CommandPayload{CommandID: "extra", ResourceID: "catalog", Action: "restart"})
			if a.Result == nil || a.Result.Code != want {
				t.Fatal(a)
			}
			s := diagnostics.Snapshot()
			if s.Counters["command_rejected"] != 1 || s.Gauges["agent_pending"] != int64(count) || s.Gauges["journal_entries"] != int64(count) || s.Gauges["journal_limit"] != int64(limit) {
				t.Fatalf("snapshot=%+v", s)
			}
		})
	}
}
