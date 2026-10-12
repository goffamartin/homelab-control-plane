package command

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func FuzzJournalRecovery(f *testing.F) {
	f.Add([]byte(`{"version":3,"node_id":"n","entries":[]}`))
	f.Add([]byte(`{"version":3,"node_id":"n","entries":[{"phase":"queued","command":{"command_id":"op-1","resource_id":"catalog","action":"restart"},"admitted_at":"2026-01-01T00:00:00Z"}]}`))
	f.Add([]byte(`{"version":3,"node_id":"n","entries":[{"phase":"running","command":{"command_id":"op-1","resource_id":"catalog","action":"restart"},"admitted_at":"2026-01-01T00:00:00Z","completed_at":"2026-01-01T00:00:01Z","result":{"command_id":"op-1","success":true,"code":"OK","message":"done"}}]}`))
	f.Add([]byte(`{"version":99,"node_id":"n","entries":[]}`))
	f.Add([]byte(`{"version":3,"node_id":"n","entries":[]} {}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			t.Skip()
		}
		path := filepath.Join(t.TempDir(), "journal.json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		journal, err := OpenNodeJournal(path, 32, "n")
		if err != nil {
			after, readErr := os.ReadFile(path)
			if readErr != nil || !bytes.Equal(data, after) {
				t.Fatal("rejected journal was mutated")
			}
			return
		}
		if err := journal.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := OpenNodeJournal(path, 32, "n")
		if err != nil {
			t.Fatalf("accepted journal cannot reopen: %v", err)
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
	})
}
