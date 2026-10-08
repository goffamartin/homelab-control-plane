// Package command executes only registered resource actions and caches results
// by command ID to make retries safe within the agent process lifetime.
package command

import (
	"sync"

	"hlcp/internal/protocol"
)

type Executor func(action string) protocol.CommandResultPayload

type Dispatcher struct {
	mutex     sync.Mutex
	executors map[string]Executor
	results   map[string]protocol.CommandResultPayload
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{executors: make(map[string]Executor), results: make(map[string]protocol.CommandResultPayload)}
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
	if cached, found := dispatcher.results[command.CommandID]; found {
		return protocol.CommandACKPayload{CommandID: command.CommandID, Status: "completed"}, cached
	}
	executor, found := dispatcher.executors[command.ResourceID]
	if !found {
		return reject(command, "TARGET_NOT_FOUND", "resource is not registered")
	}
	result := executor(command.Action)
	result.CommandID = command.CommandID
	dispatcher.results[command.CommandID] = result
	return protocol.CommandACKPayload{CommandID: command.CommandID, Status: "accepted"}, result
}

func reject(command protocol.CommandPayload, code, message string) (protocol.CommandACKPayload, protocol.CommandResultPayload) {
	return protocol.CommandACKPayload{CommandID: command.CommandID, Status: "rejected"}, protocol.CommandResultPayload{CommandID: command.CommandID, Code: code, Message: message}
}
