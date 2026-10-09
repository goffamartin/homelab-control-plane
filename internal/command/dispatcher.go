// Package command executes registered resource actions with durable
// command-ID deduplication.
package command

import (
	"errors"
	"sync"

	"hlcp/internal/protocol"
)

type Executor func(action string) protocol.CommandResultPayload

type Dispatcher struct {
	mutex     sync.Mutex
	executors map[string]Executor
	journal   Journal
}

func NewDispatcher(journal Journal) *Dispatcher {
	return &Dispatcher{executors: make(map[string]Executor), journal: journal}
}

// Register exposes exactly one configured resource. Callers cannot pass an
// arbitrary implementation target through HLCP.
func (dispatcher *Dispatcher) Register(resourceID string, executor Executor) {
	dispatcher.executors[resourceID] = executor
}

func (dispatcher *Dispatcher) Execute(command protocol.CommandPayload) (protocol.CommandACKPayload, protocol.CommandResultPayload) {
	if err := command.Validate(); err != nil {
		return reject(command, "INVALID_REQUEST", err.Error())
	}
	dispatcher.mutex.Lock()
	defer dispatcher.mutex.Unlock()
	executor, found := dispatcher.executors[command.ResourceID]
	if !found {
		return reject(command, "TARGET_NOT_FOUND", "resource is not registered")
	}
	entry, fresh, err := dispatcher.journal.Begin(command)
	if err != nil {
		if errors.Is(err, ErrDuplicateConflict) {
			return reject(command, "DUPLICATE_CONFLICT", err.Error())
		}
		return reject(command, "JOURNAL_FAILURE", err.Error())
	}
	if !fresh {
		if entry.Result != nil {
			return protocol.CommandACKPayload{CommandID: command.CommandID, Status: "completed"}, *entry.Result
		}
		return protocol.CommandACKPayload{CommandID: command.CommandID, Status: "in_progress"}, protocol.CommandResultPayload{CommandID: command.CommandID, Code: "IN_PROGRESS", Message: "command is already running"}
	}
	result := executor(command.Action)
	result.CommandID = command.CommandID
	if err := dispatcher.journal.Complete(result); err != nil {
		return protocol.CommandACKPayload{CommandID: command.CommandID, Status: "accepted"}, protocol.CommandResultPayload{CommandID: command.CommandID, Code: "JOURNAL_FAILURE", Message: err.Error()}
	}
	return protocol.CommandACKPayload{CommandID: command.CommandID, Status: "accepted"}, result
}

func reject(command protocol.CommandPayload, code, message string) (protocol.CommandACKPayload, protocol.CommandResultPayload) {
	return protocol.CommandACKPayload{CommandID: command.CommandID, Status: "rejected"}, protocol.CommandResultPayload{CommandID: command.CommandID, Code: code, Message: message}
}
