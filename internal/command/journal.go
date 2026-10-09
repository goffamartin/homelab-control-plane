package command

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"hlcp/internal/protocol"
)

const (
	defaultJournalLimit = 1024
	journalVersion      = 3
	defaultJournalBytes = 8 << 20
	defaultReplayWindow = 24 * time.Hour
)

var (
	ErrDuplicateConflict = errors.New("command ID is already associated with a different command")
	ErrJournalFull       = errors.New("command journal has no safely removable capacity")
	ErrJournalClosed     = errors.New("command journal is closed")
)

type Journal interface {
	Begin(protocol.CommandPayload) (Entry, bool, error)
	BeginQueued(protocol.CommandPayload) (Entry, bool, error)
	Lookup(protocol.CommandPayload) (Entry, bool, error)
	MarkRunning(string) error
	Complete(protocol.CommandResultPayload) error
	Close() error
}

type Entry struct {
	Phase       string                         `json:"phase,omitempty"`
	Command     protocol.CommandPayload        `json:"command"`
	Result      *protocol.CommandResultPayload `json:"result,omitempty"`
	AdmittedAt  time.Time                      `json:"admitted_at"`
	CompletedAt time.Time                      `json:"completed_at,omitempty"`
}

type FileJournal struct {
	mutex        sync.Mutex
	path         string
	nodeID       string
	limit        int
	entries      map[string]Entry
	order        []string
	lock         *os.File
	closed       bool
	failed       error
	now          func() time.Time
	maxBytes     int
	replayWindow time.Duration
	fault        func(string) error
}

type journalDocument struct {
	Version int     `json:"version"`
	NodeID  string  `json:"node_id"`
	Entries []Entry `json:"entries"`
}

func OpenFileJournal(path string, limit int) (*FileJournal, error) {
	return OpenNodeJournal(path, limit, "")
}

// OpenNodeJournal owns a process-level lock for its entire lifetime. The
// existing journal is never treated as empty if it is truncated or invalid.
func OpenNodeJournal(path string, limit int, nodeID string) (_ *FileJournal, returnedErr error) {
	if path == "" {
		return nil, errors.New("command journal path is required")
	}
	if limit < 0 || limit > 1_000_000 {
		return nil, errors.New("invalid journal capacity")
	}
	if limit == 0 {
		limit = defaultJournalLimit
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	directory := filepath.Dir(absolute)
	if err := prepareJournalDirectory(directory); err != nil {
		return nil, err
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, err
	}
	absolute = filepath.Join(directory, filepath.Base(absolute))
	if info, err := os.Lstat(absolute); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("journal file must not be a symbolic link")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	lock, err := acquireJournalLock(absolute + ".lock")
	if err != nil {
		return nil, err
	}
	defer func() {
		if returnedErr != nil {
			lock.Close()
		}
	}()
	journal := &FileJournal{path: absolute, nodeID: nodeID, limit: limit, entries: make(map[string]Entry), lock: lock, now: time.Now, maxBytes: defaultJournalBytes, replayWindow: defaultReplayWindow}
	data, exists, err := readJournal(absolute, journal.maxBytes)
	if err != nil {
		return nil, err
	}
	if !exists {
		if err := journal.persistLocked(); err != nil {
			return nil, err
		}
		return journal, nil
	}
	var document journalDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode command journal: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("command journal must contain exactly one document")
	}
	legacy := document.Version == 0
	previous := document.Version == 2
	if !legacy && !previous && document.Version != journalVersion {
		return nil, errors.New("unsupported command journal version")
	}
	if !legacy && document.NodeID != nodeID {
		return nil, errors.New("command journal belongs to a different node")
	}
	if document.Entries == nil || len(document.Entries) > limit {
		return nil, errors.New("invalid journal entries or capacity smaller than retained history")
	}
	now := journal.now().UTC()
	changed := legacy || previous
	for _, entry := range document.Entries {
		if entry.Phase != "" && entry.Phase != "queued" && entry.Phase != "running" {
			return nil, errors.New("invalid journal execution phase")
		}
		if err := entry.Command.Validate(); err != nil {
			return nil, fmt.Errorf("invalid journal command: %w", err)
		}
		id := entry.Command.CommandID
		if _, exists := journal.entries[id]; exists {
			return nil, errors.New("duplicate command journal entry")
		}
		if entry.Result != nil {
			if err := entry.Result.Validate(); err != nil {
				return nil, fmt.Errorf("invalid journal result: %w", err)
			}
			if entry.Result.CommandID != id {
				return nil, errors.New("journal result ID does not match command")
			}
		}
		if legacy {
			entry.AdmittedAt = now
			if entry.Result != nil {
				entry.CompletedAt = now
			}
		} else if entry.AdmittedAt.IsZero() || (entry.Result == nil && !entry.CompletedAt.IsZero()) || (entry.Result != nil && (entry.CompletedAt.IsZero() || entry.CompletedAt.Before(entry.AdmittedAt))) {
			return nil, errors.New("invalid command journal timestamps")
		}
		if entry.Result == nil {
			code, message := "EXECUTION_UNKNOWN", "agent restarted before the command result was recorded"
			if entry.Phase == "queued" {
				code, message = "EXECUTION_NOT_STARTED", "agent restarted before execution started"
			}
			result := protocol.CommandResultPayload{CommandID: id, Code: code, Message: message}
			entry.Result = &result
			entry.CompletedAt = now
			if now.Before(entry.AdmittedAt) {
				entry.CompletedAt = entry.AdmittedAt
			}
			changed = true
		}
		journal.entries[id] = entry
		journal.order = append(journal.order, id)
	}
	if legacy {
		if err := backupLegacy(absolute+".v1.bak", data); err != nil {
			return nil, err
		}
	}
	if previous {
		if err := backupLegacy(absolute+".v2.bak", data); err != nil {
			return nil, err
		}
	}
	if changed {
		if err := journal.persistLocked(); err != nil {
			return nil, err
		}
	}
	return journal, nil
}

func readJournal(path string, limit int) ([]byte, bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, true, errors.New("journal must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, true, err
	}
	if len(data) == 0 || len(data) > limit {
		return nil, true, errors.New("empty or oversized command journal")
	}
	return data, true, nil
}

func (journal *FileJournal) Close() error {
	journal.mutex.Lock()
	defer journal.mutex.Unlock()
	if journal.closed {
		return nil
	}
	journal.closed = true
	return journal.lock.Close()
}

func (journal *FileJournal) availableLocked() error {
	if journal.closed {
		return ErrJournalClosed
	}
	if journal.failed != nil {
		return fmt.Errorf("command journal is unavailable until recovery: %w", journal.failed)
	}
	return nil
}

func (journal *FileJournal) Begin(command protocol.CommandPayload) (Entry, bool, error) {
	return journal.begin(command, "running")
}

func (journal *FileJournal) BeginQueued(command protocol.CommandPayload) (Entry, bool, error) {
	return journal.begin(command, "queued")
}

func (journal *FileJournal) Lookup(command protocol.CommandPayload) (Entry, bool, error) {
	journal.mutex.Lock()
	defer journal.mutex.Unlock()
	if err := journal.availableLocked(); err != nil {
		return Entry{}, false, err
	}
	entry, exists := journal.entries[command.CommandID]
	if exists && (entry.Command.ResourceID != command.ResourceID || entry.Command.Action != command.Action) {
		return Entry{}, false, ErrDuplicateConflict
	}
	return cloneEntry(entry), exists, nil
}

func (journal *FileJournal) MarkRunning(commandID string) error {
	journal.mutex.Lock()
	defer journal.mutex.Unlock()
	if err := journal.availableLocked(); err != nil {
		return err
	}
	entry, exists := journal.entries[commandID]
	if !exists || entry.Result != nil || entry.Phase != "queued" {
		return errors.New("command is not queued")
	}
	entry.Phase = "running"
	journal.entries[commandID] = entry
	if err := journal.persistLocked(); err != nil {
		journal.failed = err
		return err
	}
	return nil
}

func (journal *FileJournal) begin(command protocol.CommandPayload, phase string) (Entry, bool, error) {
	journal.mutex.Lock()
	defer journal.mutex.Unlock()
	if err := journal.availableLocked(); err != nil {
		return Entry{}, false, err
	}
	if err := command.Validate(); err != nil {
		return Entry{}, false, err
	}
	if entry, exists := journal.entries[command.CommandID]; exists {
		if entry.Command.ResourceID != command.ResourceID || entry.Command.Action != command.Action {
			return Entry{}, false, ErrDuplicateConflict
		}
		return cloneEntry(entry), false, nil
	}
	previousEntries, previousOrder := journal.entries, journal.order
	journal.entries = cloneEntries(journal.entries)
	journal.order = append([]string(nil), journal.order...)
	// Purge only completed results whose protected replay window has elapsed.
	journal.trimLocked()
	if len(journal.order) >= journal.limit {
		journal.entries, journal.order = previousEntries, previousOrder
		return Entry{}, false, ErrJournalFull
	}
	entry := Entry{Command: command, Phase: phase, AdmittedAt: journal.now().UTC()}
	journal.entries[command.CommandID] = entry
	journal.order = append(journal.order, command.CommandID)
	if err := journal.persistLocked(); err != nil {
		journal.entries, journal.order = previousEntries, previousOrder
		if !errors.Is(err, ErrJournalFull) {
			journal.failed = err
		}
		return Entry{}, false, err
	}
	return cloneEntry(entry), true, nil
}

func (journal *FileJournal) Complete(result protocol.CommandResultPayload) error {
	journal.mutex.Lock()
	defer journal.mutex.Unlock()
	if err := journal.availableLocked(); err != nil {
		return err
	}
	entry, exists := journal.entries[result.CommandID]
	if !exists {
		return errors.New("command is not present in the journal")
	}
	if entry.Result != nil {
		if *entry.Result == result {
			return nil
		}
		return errors.New("terminal command result is immutable")
	}
	if err := result.Validate(); err != nil {
		journal.failed = err
		return err
	}
	previous := entry
	entry.Result = &result
	entry.CompletedAt = journal.now().UTC()
	if entry.CompletedAt.Before(entry.AdmittedAt) {
		entry.CompletedAt = entry.AdmittedAt
	}
	journal.entries[result.CommandID] = entry
	if err := journal.persistLocked(); err != nil {
		journal.entries[result.CommandID] = previous
		journal.failed = err
		return err
	}
	return nil
}

func (journal *FileJournal) trimLocked() {
	now := journal.now()
	retained := journal.order[:0]
	for _, id := range journal.order {
		entry := journal.entries[id]
		if entry.Result != nil && entry.Result.Code != "EXECUTION_UNKNOWN" && entry.Result.Code != "IN_PROGRESS" && now.Sub(entry.CompletedAt) >= journal.replayWindow {
			delete(journal.entries, id)
		} else {
			retained = append(retained, id)
		}
	}
	journal.order = retained
}

func (journal *FileJournal) persistLocked() error {
	document := journalDocument{Version: journalVersion, NodeID: journal.nodeID, Entries: make([]Entry, 0, len(journal.order))}
	for _, id := range journal.order {
		document.Entries = append(document.Entries, cloneEntry(journal.entries[id]))
	}
	data, err := json.Marshal(document)
	if err != nil {
		return err
	}
	if len(data) > journal.maxBytes {
		return ErrJournalFull
	}
	return durableReplace(journal.path, data, journal.fault)
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
