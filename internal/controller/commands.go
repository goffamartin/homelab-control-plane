package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"hlcp/internal/command"
	"hlcp/internal/protocol"
)

var (
	ErrCommandAlreadyTracked = errors.New("hlcp controller: command already tracked")
	ErrCommandCapacity       = errors.New("hlcp controller: active command capacity is full")
	ErrCommandConflict       = errors.New("hlcp controller: command ID has a different payload")
	ErrLedgerFull            = errors.New("hlcp controller: protected operation ledger is full")
	ErrLedgerFailure         = errors.New("hlcp controller: operation ledger unavailable")
	errCommandStateChanged   = errors.New("command no longer needs execution delivery")
)

const maxActiveCommands = 128
const maxLedgerOperations = 1024
const ledgerReplayWindow = 24 * time.Hour

type commandKey struct{ nodeID, commandID string }
type CommandProgress struct {
	NodeID        string                         `json:"node_id"`
	Command       protocol.CommandPayload        `json:"command"`
	AdmittedAt    time.Time                      `json:"admitted_at"`
	Deadline      time.Time                      `json:"deadline"`
	AckStatus     string                         `json:"ack_status,omitempty"`
	Attempts      int                            `json:"attempts"`
	LastAttemptAt time.Time                      `json:"last_attempt_at,omitempty"`
	State         string                         `json:"state"`
	Result        *protocol.CommandResultPayload `json:"result,omitempty"`
	CompletedAt   time.Time                      `json:"completed_at,omitempty"`
	WaitError     string                         `json:"wait_error,omitempty"`
}
type CommandTracker struct {
	mutex        sync.Mutex
	pending      map[commandKey]*commandWaiter
	store        *command.BlobStore
	failed       error
	closed       bool
	now          func() time.Time
	persistFault func() error
}
type commandWaiter struct {
	progress CommandProgress
	changed  chan struct{}
}
type ledgerDocument struct {
	Version    int               `json:"version"`
	Operations []CommandProgress `json:"operations"`
}

func NewCommandTracker() *CommandTracker {
	return &CommandTracker{pending: make(map[commandKey]*commandWaiter), now: time.Now}
}
func OpenCommandTracker(path string) (_ *CommandTracker, returnedErr error) {
	store, err := command.OpenBlobStore(path, 8<<20)
	if err != nil {
		return nil, err
	}
	defer func() {
		if returnedErr != nil {
			store.Close()
		}
	}()
	tracker := NewCommandTracker()
	tracker.store = store
	data, exists, err := store.Read()
	if err != nil {
		return nil, err
	}
	if !exists {
		if err := tracker.persistLocked(); err != nil {
			return nil, err
		}
		return tracker, nil
	}
	var document ledgerDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("ledger contains trailing data")
	}
	if document.Version != 1 || document.Operations == nil || len(document.Operations) > maxLedgerOperations {
		return nil, errors.New("invalid operation ledger version/capacity")
	}
	for _, progress := range document.Operations {
		if err := validateProgress(progress); err != nil {
			return nil, err
		}
		key := commandKey{progress.NodeID, progress.Command.CommandID}
		if tracker.pending[key] != nil {
			return nil, errors.New("duplicate ledger operation")
		}
		if progress.Result == nil {
			progress.State = "unknown"
			if !tracker.now().Before(progress.Deadline) {
				progress.State = "expired"
			}
			progress.WaitError = "RESULT_TIMEOUT"
		}
		tracker.pending[key] = &commandWaiter{progress: progress, changed: make(chan struct{})}
	}
	// Recovery is query-only even if the previous process never recorded a send.
	if err := tracker.persistLocked(); err != nil {
		return nil, err
	}
	return tracker, nil
}
func validateProgress(progress CommandProgress) error {
	hello := protocol.HelloPayload{NodeID: progress.NodeID, AgentVersion: "ledger", ProtocolVersions: []uint8{protocol.ProtocolVersion}}
	if hello.Validate() != nil || progress.Command.Validate() != nil || progress.AdmittedAt.IsZero() || progress.Deadline.Before(progress.AdmittedAt) || progress.Attempts < 0 || progress.Attempts > 100 {
		return errors.New("invalid operation ledger metadata")
	}
	if (progress.Attempts == 0) != progress.LastAttemptAt.IsZero() {
		return errors.New("invalid operation ledger attempts")
	}
	if progress.AckStatus != "" && (protocol.CommandACKPayload{CommandID: progress.Command.CommandID, Status: progress.AckStatus}).Validate() != nil {
		return errors.New("invalid ledger ACK")
	}
	switch progress.State {
	case "accepted", "running", "completed", "unknown", "expired":
	default:
		return errors.New("invalid ledger state")
	}
	switch progress.WaitError {
	case "", "ACK_TIMEOUT", "RESULT_TIMEOUT", "CANCELED", "STOPPING":
	default:
		return errors.New("invalid ledger wait error")
	}
	if progress.Result != nil {
		if progress.Result.Validate() != nil || progress.Result.CommandID != progress.Command.CommandID || progress.CompletedAt.IsZero() || progress.CompletedAt.Before(progress.AdmittedAt) {
			return errors.New("invalid ledger result")
		}
		if progress.State != "completed" && progress.State != "unknown" {
			return errors.New("invalid terminal ledger state")
		}
	} else if !progress.CompletedAt.IsZero() || progress.State == "completed" {
		return errors.New("invalid ledger completion")
	}
	return nil
}
func (tracker *CommandTracker) availableLocked() error {
	if tracker.closed || tracker.failed != nil {
		return fmt.Errorf("%w: %v", ErrLedgerFailure, tracker.failed)
	}
	return nil
}
func (tracker *CommandTracker) persistLocked() error {
	if tracker.persistFault != nil {
		if err := tracker.persistFault(); err != nil {
			tracker.failed = err
			return fmt.Errorf("%w: %v", ErrLedgerFailure, err)
		}
	}
	if tracker.store == nil {
		return nil
	}
	document := ledgerDocument{Version: 1, Operations: make([]CommandProgress, 0, len(tracker.pending))}
	for _, waiter := range tracker.pending {
		document.Operations = append(document.Operations, waiter.progress)
	}
	sort.Slice(document.Operations, func(i, j int) bool {
		a, b := document.Operations[i], document.Operations[j]
		if a.NodeID != b.NodeID {
			return a.NodeID < b.NodeID
		}
		return a.Command.CommandID < b.Command.CommandID
	})
	data, err := json.Marshal(document)
	if err == nil {
		err = tracker.store.Write(data)
	}
	if err != nil {
		tracker.failed = err
		return fmt.Errorf("%w: %v", ErrLedgerFailure, err)
	}
	return nil
}
func (tracker *CommandTracker) Close() error {
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()
	if tracker.closed {
		return nil
	}
	tracker.closed = true
	for _, waiter := range tracker.pending {
		notify(waiter)
	}
	if tracker.store != nil {
		return tracker.store.Close()
	}
	return nil
}
func cloneProgress(progress CommandProgress) CommandProgress {
	if progress.Result != nil {
		result := *progress.Result
		progress.Result = &result
	}
	return progress
}
func (tracker *CommandTracker) Operation(nodeID, commandID string) (CommandProgress, bool, error) {
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()
	if err := tracker.availableLocked(); err != nil {
		return CommandProgress{}, false, err
	}
	waiter := tracker.pending[commandKey{nodeID, commandID}]
	if waiter == nil {
		return CommandProgress{}, false, nil
	}
	progress := cloneProgress(waiter.progress)
	if progress.Result == nil && progress.WaitError != "" && !tracker.now().Before(progress.Deadline) {
		progress.State = "expired"
	}
	return progress, true, nil
}
func (tracker *CommandTracker) Progress(nodeID, commandID string) (CommandProgress, bool) {
	progress, exists, _ := tracker.Operation(nodeID, commandID)
	return progress, exists
}
func (tracker *CommandTracker) Admit(nodeID string, payload protocol.CommandPayload, deadline time.Time) (bool, error) {
	if payload.Validate() != nil {
		return false, protocol.ErrInvalidCommand
	}
	hello := protocol.HelloPayload{NodeID: nodeID, AgentVersion: "ledger", ProtocolVersions: []uint8{protocol.ProtocolVersion}}
	if hello.Validate() != nil {
		return false, errors.New("invalid command node identity")
	}
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()
	if err := tracker.availableLocked(); err != nil {
		return false, err
	}
	key := commandKey{nodeID, payload.CommandID}
	if existing := tracker.pending[key]; existing != nil {
		if existing.progress.Command != payload {
			return false, ErrCommandConflict
		}
		return false, nil
	}
	active := 0
	for _, waiter := range tracker.pending {
		if waiter.progress.Result == nil && waiter.progress.WaitError == "" {
			active++
		}
	}
	if active >= maxActiveCommands {
		return false, ErrCommandCapacity
	}
	now := tracker.now()
	if !deadline.After(now) {
		return false, context.DeadlineExceeded
	}
	for key, waiter := range tracker.pending {
		p := waiter.progress
		if p.Result != nil && p.Result.Code != "EXECUTION_UNKNOWN" && !now.Before(p.CompletedAt.Add(ledgerReplayWindow)) {
			delete(tracker.pending, key)
		}
	}
	if len(tracker.pending) >= maxLedgerOperations {
		return false, ErrLedgerFull
	}
	tracker.pending[key] = &commandWaiter{progress: CommandProgress{NodeID: nodeID, Command: payload, AdmittedAt: now, Deadline: deadline, State: "accepted"}, changed: make(chan struct{})}
	if err := tracker.persistLocked(); err != nil {
		return false, err
	}
	return true, nil
}
func (tracker *CommandTracker) Track(commandID string) error { return tracker.TrackNode("", commandID) }
func (tracker *CommandTracker) TrackNode(nodeID, commandID string) error {
	return tracker.TrackCommand(nodeID, protocol.CommandPayload{CommandID: commandID}, tracker.now().Add(time.Minute))
}
func (tracker *CommandTracker) TrackCommand(nodeID string, payload protocol.CommandPayload, deadline time.Time) error {
	// Legacy in-memory fixtures can track only an ID; production uses Admit.
	if payload.ResourceID == "" {
		tracker.mutex.Lock()
		defer tracker.mutex.Unlock()
		if tracker.store != nil {
			return protocol.ErrInvalidCommand
		}
		if err := tracker.availableLocked(); err != nil {
			return err
		}
		key := commandKey{nodeID, payload.CommandID}
		if tracker.pending[key] != nil {
			return ErrCommandAlreadyTracked
		}
		if len(tracker.pending) >= maxActiveCommands {
			return ErrCommandCapacity
		}
		tracker.pending[key] = &commandWaiter{progress: CommandProgress{NodeID: nodeID, Command: payload, AdmittedAt: tracker.now(), Deadline: deadline, State: "accepted"}, changed: make(chan struct{})}
		return nil
	}
	fresh, err := tracker.Admit(nodeID, payload, deadline)
	if err == nil && !fresh {
		return ErrCommandAlreadyTracked
	}
	return err
}
func notify(waiter *commandWaiter) { close(waiter.changed); waiter.changed = make(chan struct{}) }
func (tracker *CommandTracker) AttemptNode(nodeID, commandID string) error {
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()
	if err := tracker.availableLocked(); err != nil {
		return err
	}
	waiter := tracker.pending[commandKey{nodeID, commandID}]
	if waiter != nil && (waiter.progress.Result != nil || waiter.progress.WaitError != "" || waiter.progress.AckStatus != "") {
		return errCommandStateChanged
	}
	if waiter == nil || !tracker.now().Before(waiter.progress.Deadline) {
		return ErrResultTimeout
	}
	if waiter.progress.Attempts >= 100 {
		return ErrACKTimeout
	}
	waiter.progress.Attempts++
	waiter.progress.LastAttemptAt = tracker.now()
	return tracker.persistLocked()
}
func (tracker *CommandTracker) AckNode(nodeID string, ack protocol.CommandACKPayload) bool {
	if ack.Validate() != nil {
		return false
	}
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()
	if tracker.availableLocked() != nil {
		return false
	}
	waiter := tracker.pending[commandKey{nodeID, ack.CommandID}]
	if waiter == nil || waiter.progress.Result != nil {
		return false
	}
	if waiter.progress.AckStatus == ack.Status {
		return true
	}
	switch waiter.progress.AckStatus {
	case "completed", "rejected":
		return waiter.progress.AckStatus == ack.Status
	case "in_progress":
		if ack.Status == "accepted" {
			return true
		}
	}
	waiter.progress.AckStatus = ack.Status
	if ack.Status == "in_progress" && waiter.progress.WaitError == "" {
		waiter.progress.State = "running"
	}
	err := tracker.persistLocked()
	notify(waiter)
	return err == nil
}
func (tracker *CommandTracker) Complete(result protocol.CommandResultPayload) bool {
	return tracker.CompleteNode("", result)
}
func (tracker *CommandTracker) PendingNode(nodeID, commandID string) bool {
	progress, ok := tracker.Progress(nodeID, commandID)
	return ok && progress.Result == nil
}
func (tracker *CommandTracker) CompleteNode(nodeID string, result protocol.CommandResultPayload) bool {
	if result.Validate() != nil {
		return false
	}
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()
	if tracker.availableLocked() != nil {
		return false
	}
	waiter := tracker.pending[commandKey{nodeID, result.CommandID}]
	if waiter == nil {
		return false
	}
	if waiter.progress.Result != nil {
		return *waiter.progress.Result == result
	}
	copy := result
	waiter.progress.Result = &copy
	waiter.progress.CompletedAt = tracker.now()
	if waiter.progress.CompletedAt.Before(waiter.progress.AdmittedAt) {
		waiter.progress.CompletedAt = waiter.progress.AdmittedAt
	}
	waiter.progress.State = "completed"
	if result.Code == "EXECUTION_UNKNOWN" {
		waiter.progress.State = "unknown"
	}
	waiter.progress.WaitError = ""
	err := tracker.persistLocked()
	notify(waiter)
	return err == nil
}
func (tracker *CommandTracker) FinishWait(nodeID, commandID, reason string) error {
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()
	if err := tracker.availableLocked(); err != nil {
		return err
	}
	waiter := tracker.pending[commandKey{nodeID, commandID}]
	if waiter == nil || waiter.progress.Result != nil || waiter.progress.WaitError != "" {
		return nil
	}
	waiter.progress.WaitError = reason
	waiter.progress.State = "unknown"
	if !tracker.now().Before(waiter.progress.Deadline) {
		waiter.progress.State = "expired"
	}
	err := tracker.persistLocked()
	notify(waiter)
	return err
}
func waitError(code string) error {
	switch code {
	case "ACK_TIMEOUT":
		return ErrACKTimeout
	case "CANCELED":
		return ErrResultTimeout
	case "STOPPING":
		return ErrLedgerFailure
	default:
		return ErrResultTimeout
	}
}
func (tracker *CommandTracker) Cancel(commandID string) { tracker.CancelNode("", commandID) }
func (tracker *CommandTracker) CancelNode(nodeID, commandID string) {
	// Only legacy test fixtures use deletion. Never delete durable operations.
	if tracker.store != nil {
		_ = tracker.FinishWait(nodeID, commandID, "CANCELED")
		return
	}
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()
	key := commandKey{nodeID, commandID}
	if waiter := tracker.pending[key]; waiter != nil {
		delete(tracker.pending, key)
		notify(waiter)
	}
}
func (tracker *CommandTracker) Wait(ctx context.Context, commandID string) (protocol.CommandResultPayload, error) {
	return tracker.WaitNode(ctx, "", commandID)
}
func (tracker *CommandTracker) WaitNode(ctx context.Context, nodeID, commandID string) (protocol.CommandResultPayload, error) {
	for {
		tracker.mutex.Lock()
		if err := tracker.availableLocked(); err != nil {
			tracker.mutex.Unlock()
			return protocol.CommandResultPayload{}, err
		}
		waiter := tracker.pending[commandKey{nodeID, commandID}]
		if waiter == nil {
			tracker.mutex.Unlock()
			return protocol.CommandResultPayload{}, errors.New("command is not tracked")
		}
		if waiter.progress.Result != nil {
			result := *waiter.progress.Result
			tracker.mutex.Unlock()
			return result, nil
		}
		if waiter.progress.WaitError != "" {
			err := waitError(waiter.progress.WaitError)
			tracker.mutex.Unlock()
			return protocol.CommandResultPayload{}, err
		}
		changed := waiter.changed
		tracker.mutex.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return protocol.CommandResultPayload{}, ctx.Err()
		}
	}
}
func (tracker *CommandTracker) Unresolved(nodeID string) []CommandProgress {
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()
	result := make([]CommandProgress, 0)
	if tracker.availableLocked() != nil {
		return result
	}
	for _, waiter := range tracker.pending {
		if waiter.progress.NodeID == nodeID && waiter.progress.Result == nil {
			result = append(result, cloneProgress(waiter.progress))
		}
	}
	return result
}
