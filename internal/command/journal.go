package command

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"hlcp/internal/protocol"
)

const defaultJournalLimit = 1024

var ErrDuplicateConflict = errors.New("command ID is already associated with a different command")

type Journal interface {
	Begin(protocol.CommandPayload) (Entry, bool, error)
	Complete(protocol.CommandResultPayload) error
}

type Entry struct {
	Command protocol.CommandPayload        `json:"command"`
	Result  *protocol.CommandResultPayload `json:"result,omitempty"`
}

type FileJournal struct {
	mutex   sync.Mutex
	path    string
	limit   int
	entries map[string]Entry
	order   []string
}

type journalDocument struct {
	Entries []Entry `json:"entries"`
}

func OpenFileJournal(path string, limit int) (*FileJournal, error) {
	if path == "" {
		return nil, errors.New("command journal path is required")
	}
	if limit <= 0 {
		limit = defaultJournalLimit
	}
	journal := &FileJournal{path: path, limit: limit, entries: make(map[string]Entry)}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read command journal: %w", err)
	}
	if len(data) > 0 {
		var document journalDocument
		if err := json.Unmarshal(data, &document); err != nil {
			return nil, fmt.Errorf("decode command journal: %w", err)
		}
		for _, entry := range document.Entries {
			if err := entry.Command.Validate(); err != nil {
				return nil, fmt.Errorf("invalid command journal entry: %w", err)
			}
			if _, exists := journal.entries[entry.Command.CommandID]; exists {
				return nil, fmt.Errorf("duplicate command journal entry %q", entry.Command.CommandID)
			}
			journal.entries[entry.Command.CommandID] = entry
			journal.order = append(journal.order, entry.Command.CommandID)
		}
	}
	changed := false
	for id, entry := range journal.entries {
		if entry.Result == nil {
			result := protocol.CommandResultPayload{CommandID: id, Code: "EXECUTION_UNKNOWN", Message: "agent restarted before the command result was recorded"}
			entry.Result = &result
			journal.entries[id] = entry
			changed = true
		}
	}
	if changed {
		if err := journal.persistLocked(); err != nil {
			return nil, err
		}
	}
	return journal, nil
}

func (journal *FileJournal) Begin(command protocol.CommandPayload) (Entry, bool, error) {
	journal.mutex.Lock()
	defer journal.mutex.Unlock()
	if entry, exists := journal.entries[command.CommandID]; exists {
		if entry.Command.ResourceID != command.ResourceID || entry.Command.Action != command.Action {
			return Entry{}, false, ErrDuplicateConflict
		}
		return cloneEntry(entry), false, nil
	}
	previousEntries := journal.entries
	previousOrder := journal.order
	journal.entries = cloneEntries(journal.entries)
	journal.order = append([]string(nil), journal.order...)
	entry := Entry{Command: command}
	journal.entries[command.CommandID] = entry
	journal.order = append(journal.order, command.CommandID)
	journal.trimLocked()
	if err := journal.persistLocked(); err != nil {
		journal.entries = previousEntries
		journal.order = previousOrder
		return Entry{}, false, err
	}
	return entry, true, nil
}

func (journal *FileJournal) Complete(result protocol.CommandResultPayload) error {
	journal.mutex.Lock()
	defer journal.mutex.Unlock()
	entry, exists := journal.entries[result.CommandID]
	if !exists {
		return errors.New("command is not present in the journal")
	}
	previous := entry
	entry.Result = &result
	journal.entries[result.CommandID] = entry
	if err := journal.persistLocked(); err != nil {
		journal.entries[result.CommandID] = previous
		return err
	}
	return nil
}

func (journal *FileJournal) trimLocked() {
	for len(journal.order) > journal.limit {
		oldest := journal.order[0]
		journal.order = journal.order[1:]
		delete(journal.entries, oldest)
	}
}

func (journal *FileJournal) persistLocked() error {
	document := journalDocument{Entries: make([]Entry, 0, len(journal.order))}
	for _, id := range journal.order {
		document.Entries = append(document.Entries, cloneEntry(journal.entries[id]))
	}
	data, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode command journal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(journal.path), 0o700); err != nil {
		return fmt.Errorf("create command journal directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(journal.path), ".hlcp-journal-*")
	if err != nil {
		return fmt.Errorf("create temporary command journal: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write command journal: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync command journal: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close command journal: %w", err)
	}
	if err := os.Rename(temporaryPath, journal.path); err != nil {
		return fmt.Errorf("replace command journal: %w", err)
	}
	return nil
}

func cloneEntry(entry Entry) Entry {
	if entry.Result != nil {
		result := *entry.Result
		entry.Result = &result
	}
	return entry
}

func cloneEntries(entries map[string]Entry) map[string]Entry {
	cloned := make(map[string]Entry, len(entries))
	for id, entry := range entries {
		cloned[id] = cloneEntry(entry)
	}
	return cloned
}
