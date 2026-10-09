// Package command executes registered actions with durable admission and one bounded worker.
package command

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"hlcp/internal/protocol"
)

const QueueCapacity = 16

type Executor func(context.Context, string) protocol.CommandResultPayload

type Job struct {
	command   protocol.CommandPayload
	executor  Executor
	start     chan struct{}
	startOnce sync.Once
	skip      bool
	Done      chan struct{}
	Result    protocol.CommandResultPayload
}

type Admission struct {
	Ack    protocol.CommandACKPayload
	Result *protocol.CommandResultPayload
	Job    *Job
}

type registration struct {
	execute Executor
	actions map[string]bool
}

type Dispatcher struct {
	mutex     sync.Mutex
	executors map[string]registration
	pending   map[string]*Job
	journal   Journal
	queue     chan *Job
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	closed    bool
}

func NewDispatcher(journal Journal) *Dispatcher {
	ctx, cancel := context.WithCancel(context.Background())
	dispatcher := &Dispatcher{executors: make(map[string]registration), pending: make(map[string]*Job), journal: journal, queue: make(chan *Job, QueueCapacity), ctx: ctx, cancel: cancel, done: make(chan struct{})}
	go dispatcher.work()
	return dispatcher
}

func (dispatcher *Dispatcher) Close() error {
	dispatcher.mutex.Lock()
	if !dispatcher.closed {
		dispatcher.closed = true
		close(dispatcher.queue)
		dispatcher.cancel()
	}
	dispatcher.mutex.Unlock()
	<-dispatcher.done
	return dispatcher.journal.Close()
}

func (dispatcher *Dispatcher) Register(resourceID string, executor Executor, actions ...string) {
	dispatcher.mutex.Lock()
	defer dispatcher.mutex.Unlock()
	if len(actions) == 0 {
		actions = []string{"status", "start", "stop", "restart"}
	}
	allowed := make(map[string]bool)
	for _, action := range actions {
		allowed[action] = true
	}
	dispatcher.executors[resourceID] = registration{executor, allowed}
}

func rejected(command protocol.CommandPayload, code, message string) Admission {
	result := protocol.CommandResultPayload{CommandID: command.CommandID, Code: code, Message: message}
	return Admission{Ack: protocol.CommandACKPayload{CommandID: command.CommandID, Status: "rejected"}, Result: &result}
}

func journalRejection(command protocol.CommandPayload, err error) Admission {
	code := "JOURNAL_FAILURE"
	if errors.Is(err, ErrDuplicateConflict) {
		code = "DUPLICATE_CONFLICT"
	}
	if errors.Is(err, ErrJournalFull) {
		code = "JOURNAL_FULL"
	}
	return rejected(command, code, err.Error())
}

// Admit persists queued intent before ACK. Start must be called only after
// successful ACK delivery. Duplicates never acquire another execution slot.
func (dispatcher *Dispatcher) Admit(command protocol.CommandPayload) Admission {
	if err := command.Validate(); err != nil {
		return rejected(command, "INVALID_REQUEST", err.Error())
	}
	dispatcher.mutex.Lock()
	defer dispatcher.mutex.Unlock()
	if dispatcher.closed {
		return rejected(command, "AGENT_STOPPING", "agent is stopping")
	}
	entry, exists, err := dispatcher.journal.Lookup(command)
	if err != nil {
		return journalRejection(command, err)
	}
	if exists {
		if entry.Result != nil {
			result := *entry.Result
			return Admission{Ack: protocol.CommandACKPayload{CommandID: command.CommandID, Status: "completed"}, Result: &result}
		}
		job := dispatcher.pending[command.CommandID]
		if job == nil {
			return rejected(command, "EXECUTION_UNKNOWN", "command has no active worker")
		}
		return Admission{Ack: protocol.CommandACKPayload{CommandID: command.CommandID, Status: "in_progress"}, Job: job}
	}
	resource, found := dispatcher.executors[command.ResourceID]
	if !found {
		return rejected(command, "TARGET_NOT_FOUND", "resource is not registered")
	}
	if !resource.actions[command.Action] {
		return rejected(command, "ACTION_NOT_SUPPORTED", "action is not enabled")
	}
	if len(dispatcher.pending) >= QueueCapacity {
		return rejected(command, "QUEUE_FULL", "agent execution capacity is full")
	}
	if _, _, err := dispatcher.journal.BeginQueued(command); err != nil {
		return journalRejection(command, err)
	}
	job := &Job{command: command, executor: resource.execute, start: make(chan struct{}), Done: make(chan struct{})}
	dispatcher.pending[command.CommandID] = job
	dispatcher.queue <- job
	return Admission{Ack: protocol.CommandACKPayload{CommandID: command.CommandID, Status: "accepted"}, Job: job}
}

func (dispatcher *Dispatcher) Start(job *Job) {
	job.startOnce.Do(func() { close(job.start) })
}

// Query reads only. In particular, it does not call BeginQueued, Start or an
// executor, even when the resource was removed or the journal has no entry.
func (dispatcher *Dispatcher) Query(command protocol.CommandPayload) protocol.CommandStatePayload {
	state := protocol.CommandStatePayload{CommandID: command.CommandID, State: "unknown"}
	if command.Validate() != nil {
		return state
	}
	entry, exists, err := dispatcher.journal.Lookup(command)
	if err != nil {
		return state
	}
	if !exists {
		state.State = "not_found"
		return state
	}
	if entry.Result != nil {
		state.State = "completed"
		state.Result = entry.Result
		return state
	}
	state.State = entry.Phase
	if state.State == "" {
		state.State = "unknown"
	}
	return state
}

// AbortBeforeACK leaves a definitive non-executed result if ACK delivery failed.
func (dispatcher *Dispatcher) AbortBeforeACK(job *Job) {
	job.startOnce.Do(func() { job.skip = true; close(job.start) })
}

func (dispatcher *Dispatcher) work() {
	defer close(dispatcher.done)
	for job := range dispatcher.queue {
		select {
		case <-job.start:
		case <-dispatcher.ctx.Done():
		}
		result := protocol.CommandResultPayload{CommandID: job.command.CommandID, Code: "EXECUTION_NOT_STARTED", Message: "command was not started"}
		if dispatcher.ctx.Err() == nil && !job.skip {
			if err := dispatcher.journal.MarkRunning(job.command.CommandID); err != nil {
				result.Code = "JOURNAL_FAILURE"
				result.Message = err.Error()
			} else {
				ctx, cancel := context.WithTimeout(dispatcher.ctx, 20*time.Second)
				result = job.executor(ctx, job.command.Action)
				if ctx.Err() != nil {
					result = protocol.CommandResultPayload{Code: "EXECUTION_UNKNOWN", Message: "execution interrupted; external outcome is uncertain"}
					if job.command.Action == "status" {
						result.Code = "TIMEOUT"
						result.Message = "status observation interrupted"
					}
				}
				cancel()
				result.CommandID = job.command.CommandID
				result.Message = strings.ToValidUTF8(result.Message, "?")
				if len(result.Message) > 4096 {
					result.Message = result.Message[:4096]
					for !utf8.ValidString(result.Message) {
						result.Message = result.Message[:len(result.Message)-1]
					}
				}
				if err := result.Validate(); err != nil {
					result = protocol.CommandResultPayload{CommandID: job.command.CommandID, Code: "EXECUTION_UNKNOWN", Message: "executor returned an invalid outcome"}
				}
			}
		}
		if err := dispatcher.journal.Complete(result); err != nil {
			result = protocol.CommandResultPayload{CommandID: job.command.CommandID, Code: "EXECUTION_UNKNOWN", Message: "command outcome could not be durably recorded"}
		}
		job.Result = result
		close(job.Done)
		dispatcher.mutex.Lock()
		delete(dispatcher.pending, job.command.CommandID)
		dispatcher.mutex.Unlock()
	}
}

// Execute is a synchronous convenience for local callers/tests, not the wire path.
func (dispatcher *Dispatcher) Execute(command protocol.CommandPayload) (protocol.CommandACKPayload, protocol.CommandResultPayload) {
	admission := dispatcher.Admit(command)
	if admission.Result != nil {
		return admission.Ack, *admission.Result
	}
	dispatcher.Start(admission.Job)
	<-admission.Job.Done
	return admission.Ack, admission.Job.Result
}
