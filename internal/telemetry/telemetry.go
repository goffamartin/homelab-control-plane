// Package telemetry provides bounded, best-effort diagnostics, never a durable audit ledger.
package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const BufferCapacity = 512
const MaxFieldBytes = 128

// Kind is a closed set: identities and command IDs never become metric labels.
type Kind uint8

const (
	Authenticated Kind = iota
	APIRejected
	ConnectionRejected
	SessionStarted
	SessionEnded
	SessionReplaced
	Reconnect
	LivenessChanged
	CommandAdmitted
	CommandDuplicate
	CommandRejected
	CommandAttempt
	CommandRetry
	CommandACK
	CommandRunning
	CommandResult
	CommandUnresolved
	StorageFailure
	RecoveryQuery
	WriteQueueFull
	PolicyReload
	kindCount
)

var names = [kindCount]string{"authenticated", "api_rejected", "connection_rejected", "session_started", "session_ended", "session_replaced", "reconnect", "liveness_changed", "command_admitted", "command_duplicate", "command_rejected", "command_attempt", "command_retry", "command_ack", "command_running", "command_result", "command_unresolved", "storage_failure", "recovery_query", "write_queue_full", "policy_reload"}

type Gauge uint8

const (
	Connections Gauge = iota
	Sessions
	ActiveCommands
	LedgerEntries
	AgentPending
	JournalEntries
	ConnectionLimit
	ActiveCommandLimit
	LedgerLimit
	AgentPendingLimit
	JournalLimit
	gaugeCount
)

var gaugeNames = [gaugeCount]string{"connections", "sessions", "active_commands", "ledger_entries", "agent_pending", "journal_entries", "connection_limit", "active_command_limit", "ledger_limit", "agent_pending_limit", "journal_limit"}

// Event intentionally has no error, message, header or payload field. Callers
// supply controlled outcome codes, never adapter output or authentication secrets.
type Event struct {
	Kind           Kind      `json:"-"`
	Name           string    `json:"event"`
	At             time.Time `json:"at"`
	ActorID        string    `json:"actor_id,omitempty"`
	NodeID         string    `json:"node_id,omitempty"`
	ResourceID     string    `json:"resource_id,omitempty"`
	CommandID      string    `json:"command_id,omitempty"`
	SessionID      string    `json:"session_id,omitempty"`
	Action         string    `json:"action,omitempty"`
	Code           string    `json:"code,omitempty"`
	Attempt        int       `json:"attempt,omitempty"`
	DurationMillis int64     `json:"duration_ms,omitempty"`
	Sequence       uint64    `json:"-"`
}

type Sink func(context.Context, Event) error

// Outcome never logs an arbitrary agent-supplied result code.
func Outcome(code string) string {
	switch code {
	case "OK", "EXECUTION_UNKNOWN", "EXECUTION_NOT_STARTED", "TIMEOUT", "JOURNAL_FAILURE", "JOURNAL_FULL", "QUEUE_FULL", "AGENT_STOPPING", "ACTION_NOT_SUPPORTED", "TARGET_NOT_FOUND", "INVALID_REQUEST", "DUPLICATE_CONFLICT", "INVALID_CONFIGURATION", "EXECUTION_FAILED", "RUNTIME_UNAVAILABLE", "PERMISSION_DENIED", "OBSERVATION_FAILED":
		return code
	default:
		return "OTHER_RESULT"
	}
}

type Collector struct {
	started                                            time.Time
	mutex                                              sync.Mutex
	ring                                               [BufferCapacity]Event
	next, size                                         int
	closed                                             bool
	sequence                                           uint64
	counts                                             [kindCount]atomic.Uint64
	gauges                                             [gaugeCount]atomic.Int64
	readBytes, writtenBytes, readFrames, writtenFrames atomic.Uint64
	dropped, sinkErrors                                atomic.Uint64
	durationCount, durationMillis, durationMax         atomic.Uint64
	queue                                              chan Event
	cancel                                             context.CancelFunc
	done                                               chan struct{}
}

func New(sink Sink) *Collector {
	c := &Collector{started: time.Now().UTC(), done: make(chan struct{})}
	if sink == nil {
		close(c.done)
		return c
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.queue = make(chan Event, BufferCapacity)
	go func() {
		defer close(c.done)
		for {
			select {
			case <-ctx.Done():
				return
			case e := <-c.queue:
				if ctx.Err() != nil {
					c.dropped.Add(1)
					return
				}
				if err := sink(ctx, e); err != nil {
					c.sinkErrors.Add(1)
				}
			}
		}
	}()
	return c
}

func JSONSink(writer io.Writer) Sink {
	encoder := json.NewEncoder(writer)
	return func(_ context.Context, event Event) error { return encoder.Encode(event) }
}

// Field bounds retained allocations, repairs control characters and never keeps
// a substring backed by an arbitrarily large caller allocation.
func Field(value string) string {
	if len(value) > MaxFieldBytes {
		value = value[:MaxFieldBytes]
	}
	var b strings.Builder
	b.Grow(len(value))
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if ch < 0x20 || ch > 0x7e {
			ch = '?'
		}
		b.WriteByte(ch)
	}
	return b.String()
}

func (c *Collector) Record(e Event) {
	if c == nil || e.Kind >= kindCount {
		return
	}
	e.Name, e.At = names[e.Kind], time.Now().UTC()
	e.ActorID, e.NodeID, e.ResourceID = Field(e.ActorID), Field(e.NodeID), Field(e.ResourceID)
	e.CommandID, e.SessionID, e.Action, e.Code = Field(e.CommandID), Field(e.SessionID), Field(e.Action), Field(e.Code)
	if e.DurationMillis < 0 {
		e.DurationMillis = 0
	}
	c.counts[e.Kind].Add(1)
	if e.Kind == CommandResult {
		d := uint64(e.DurationMillis)
		c.durationCount.Add(1)
		c.durationMillis.Add(d)
		for old := c.durationMax.Load(); d > old; old = c.durationMax.Load() {
			if c.durationMax.CompareAndSwap(old, d) {
				break
			}
		}
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.sequence++
	e.Sequence = c.sequence
	c.ring[c.next] = e
	c.next = (c.next + 1) % BufferCapacity
	if c.size < BufferCapacity {
		c.size++
	}
	if c.queue != nil {
		if c.closed {
			c.dropped.Add(1)
			return
		}
		select {
		case c.queue <- e:
		default:
			c.dropped.Add(1)
		}
	}
}

func (c *Collector) SetGauge(g Gauge, value int) {
	if c != nil && g < gaugeCount {
		c.gauges[g].Store(int64(value))
	}
}

// Snapshot is process-lifetime diagnostics, not an atomic cross-subsystem view.
type Snapshot struct {
	StartedAt time.Time         `json:"started_at"`
	Counters  map[string]uint64 `json:"counters"`
	Gauges    map[string]int64  `json:"gauges"`
}

func (c *Collector) Snapshot() Snapshot {
	s := Snapshot{Counters: make(map[string]uint64), Gauges: make(map[string]int64)}
	if c == nil {
		return s
	}
	s.StartedAt = c.started
	for i, name := range names {
		s.Counters[name] = c.counts[i].Load()
	}
	for i, name := range gaugeNames {
		s.Gauges[name] = c.gauges[i].Load()
	}
	s.Counters["read_bytes"], s.Counters["written_bytes"] = c.readBytes.Load(), c.writtenBytes.Load()
	s.Counters["read_frames"], s.Counters["written_frames"] = c.readFrames.Load(), c.writtenFrames.Load()
	s.Counters["audit_dropped"], s.Counters["audit_sink_errors"] = c.dropped.Load(), c.sinkErrors.Load()
	s.Counters["result_duration_count"], s.Counters["result_duration_sum_ms"], s.Counters["result_duration_max_ms"] = c.durationCount.Load(), c.durationMillis.Load(), c.durationMax.Load()
	return s
}

// Events returns a bounded chronological copy. API cursors/permissions belong to F10C.
func (c *Collector) Events() []Event {
	return c.Window().Events
}

type Window struct {
	Oldest, Latest uint64
	Events         []Event
}

func (c *Collector) Window() Window {
	if c == nil {
		return Window{}
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	events := make([]Event, c.size)
	for i := range events {
		events[i] = c.ring[(c.next-c.size+i+BufferCapacity)%BufferCapacity]
	}
	oldest := c.sequence + 1
	if len(events) > 0 {
		oldest = events[0].Sequence
	}
	return Window{Oldest: oldest, Latest: c.sequence, Events: events}
}

// Close discards queued diagnostics; it is bounded even if a sink ignores
// cancellation. One stuck sink worker may remain until process exit, never one
// goroutine per event. Delivery/flush is deliberately not a durability promise.
func (c *Collector) Close(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.mutex.Lock()
	if !c.closed {
		c.closed = true
		if c.cancel != nil {
			c.cancel()
		}
		if c.queue != nil {
			for {
				select {
				case <-c.queue:
					c.dropped.Add(1)
				default:
					goto drained
				}
			}
		}
	}
drained:
	c.mutex.Unlock()
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Conn observes plaintext HLCP bytes, including partial I/O, not TLS overhead.
type Conn struct {
	net.Conn
	collector *Collector
}

func Wrap(connection net.Conn, collector *Collector) *Conn { return &Conn{connection, collector} }
func (c *Conn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if c.collector != nil {
		c.collector.readBytes.Add(uint64(n))
	}
	return n, err
}
func (c *Conn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if c.collector != nil {
		c.collector.writtenBytes.Add(uint64(n))
	}
	return n, err
}
func (c *Conn) FrameRead() {
	if c.collector != nil {
		c.collector.readFrames.Add(1)
	}
}
func (c *Conn) FrameWritten() {
	if c.collector != nil {
		c.collector.writtenFrames.Add(1)
	}
}

func (c *Conn) ReportWriteQueueFull() {
	c.collector.Record(Event{Kind: WriteQueueFull, Code: "WRITE_QUEUE_FULL"})
}

type actorKey struct{}

func WithActor(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, actorKey{}, Field(id))
}
func Actor(ctx context.Context) string {
	if id, ok := ctx.Value(actorKey{}).(string); ok {
		return id
	}
	return "controller"
}
