package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"hlcp/internal/localapi"
	"hlcp/internal/protocol"
	"hlcp/internal/session"
	"sync"
	"time"
)

type logRead struct {
	id, node, resource string
	session            string
	writer             *session.Writer
	events             chan protocol.LogEvent
	failed             chan struct{}
	code               string
	sequence           uint64
	ended              bool
	binding            [32]byte
}
type containerLogBroker struct {
	mutex   sync.Mutex
	pending map[string]*logRead
}

func newContainerLogBroker() *containerLogBroker {
	return &containerLogBroker{pending: map[string]*logRead{}}
}
func logID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(id[:])
}
func (b *containerLogBroker) failLocked(r *logRead, code string) {
	if r.code == "" {
		r.code = code
		close(r.failed)
	}
}
func (b *containerLogBroker) failWriter(w *session.Writer) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	for _, r := range b.pending {
		if r.writer == w && !r.ended {
			b.failLocked(r, "SESSION_LOST")
		}
	}
}
func (b *containerLogBroker) failure(r *logRead) string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return r.code
}

func (s *Server) logResource(node, resource string) (NodeRecord, string) {
	record, exists := s.registry.Get(node)
	if !exists {
		return record, "NODE_NOT_FOUND"
	}
	found := false
	for _, r := range record.Inventory.Resources {
		if r.ID == resource && r.Type == "container" && hasCapability(r.Actions, "logs") {
			found = true
			break
		}
	}
	if !found {
		return record, "LOGS_NOT_ENABLED"
	}
	if !hasCapability(record.Capabilities, protocol.LogCapability) {
		return record, "LOGS_UNSUPPORTED"
	}
	if record.State != "online" {
		return record, "NODE_OFFLINE"
	}
	return record, ""
}
func (s *ActiveSessions) openLog(ctx context.Context, node, resource string, q protocol.LogQuery) (*logRead, string) {
	if !localapi.CurrentRead(ctx, node, resource) {
		return nil, "FORBIDDEN"
	}
	r := &logRead{id: logID(), node: node, resource: resource, events: make(chan protocol.LogEvent, 16), failed: make(chan struct{}), binding: localapi.DiagnosticBinding(ctx)}
	if r.id == "" {
		return nil, "LOGS_UNAVAILABLE"
	}
	s.registry.mutex.RLock()
	current, exists := s.active[node]
	if !exists || current.writer == nil || (current.admitted != nil && !current.admitted()) {
		s.registry.mutex.RUnlock()
		return nil, "NODE_OFFLINE"
	}
	r.writer = current.writer
	r.session = current.sessionID
	b := s.logs
	b.mutex.Lock()
	perNode, perIdentity := 0, 0
	for _, p := range b.pending {
		if p.node == node {
			perNode++
		}
		if p.binding == r.binding {
			perIdentity++
		}
	}
	if len(b.pending) >= 16 || perNode >= 2 || perIdentity >= 2 {
		b.mutex.Unlock()
		s.registry.mutex.RUnlock()
		return nil, "LOGS_CAPACITY"
	}
	b.pending[r.id] = r
	b.mutex.Unlock()
	s.registry.mutex.RUnlock()
	if !localapi.CurrentRead(ctx, node, resource) {
		s.closeLog(r)
		return nil, "FORBIDDEN"
	}
	if r.writer.SendContext(ctx, protocol.MessageLogRequest, protocol.LogRequest{RequestID: r.id, ResourceID: resource, Query: q}) != nil {
		s.closeLog(r)
		return nil, "NODE_OFFLINE"
	}
	return r, ""
}
func (s *ActiveSessions) closeLog(r *logRead) {
	s.logs.mutex.Lock()
	delete(s.logs.pending, r.id)
	s.logs.mutex.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = r.writer.SendContext(ctx, protocol.MessageLogCancel, protocol.LogCancel{RequestID: r.id})
}
func (s *ActiveSessions) HandleLogEvent(node string, w *session.Writer, event protocol.LogEvent) bool {
	s.registry.mutex.RLock()
	defer s.registry.mutex.RUnlock()
	current, exists := s.active[node]
	if !exists || current.writer != w || w == nil || (current.admitted != nil && !current.admitted()) {
		return false
	}
	b := s.logs
	b.mutex.Lock()
	defer b.mutex.Unlock()
	r := b.pending[event.RequestID]
	if r == nil || r.node != node || r.writer != w || r.ended || r.code != "" {
		return false
	}
	if event.Validate() != nil || event.Sequence != r.sequence+1 || (r.sequence == 0 && event.Kind == "record") || (r.sequence > 0 && event.Kind == "start") {
		b.failLocked(r, "INVALID_SEQUENCE")
		return false
	}
	r.sequence = event.Sequence
	r.ended = event.Kind == "end"
	select {
	case r.events <- event:
	default:
		b.failLocked(r, "BACKPRESSURE")
	}
	return true
}
