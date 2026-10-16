package session

import (
	"context"
	"errors"
	"hlcp/internal/protocol"
	"sync"
	"time"
)

// LogHandler must terminate on cancellation and never execute a resource action.
type LogHandler func(context.Context, protocol.LogRequest, func(protocol.LogRecord) error) string
type agentLogs struct {
	ctx     context.Context
	writer  *Writer
	handler LogHandler
	mutex   sync.Mutex
	jobs    map[string]context.CancelFunc
	workers sync.WaitGroup
}

func newAgentLogs(ctx context.Context, w *Writer, handlers []LogHandler) *agentLogs {
	var handler LogHandler
	if len(handlers) > 0 {
		handler = handlers[0]
	}
	return &agentLogs{ctx: ctx, writer: w, handler: handler, jobs: map[string]context.CancelFunc{}}
}
func (l *agentLogs) request(r protocol.LogRequest) error {
	l.mutex.Lock()
	if _, duplicate := l.jobs[r.RequestID]; duplicate {
		l.mutex.Unlock()
		return protocol.ErrInvalidLogs
	}
	if l.handler == nil || len(l.jobs) >= 2 {
		code := "CAPACITY"
		if l.handler == nil {
			code = "UNSUPPORTED"
		}
		l.mutex.Unlock()
		return l.writer.SendContext(l.ctx, protocol.MessageLogEvent, protocol.LogEvent{RequestID: r.RequestID, Sequence: 1, Kind: "end", Code: code, Gap: true})
	}
	limit := 15 * time.Second
	if r.Query.Follow {
		limit = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(l.ctx, limit)
	l.jobs[r.RequestID] = cancel
	l.workers.Add(1)
	l.mutex.Unlock()
	go func() {
		defer l.workers.Done()
		defer cancel()
		defer func() { l.mutex.Lock(); delete(l.jobs, r.RequestID); l.mutex.Unlock() }()
		seq := uint64(1)
		if l.writer.SendContext(ctx, protocol.MessageLogEvent, protocol.LogEvent{RequestID: r.RequestID, Sequence: seq, Kind: "start"}) != nil {
			_ = l.writer.Close()
			return
		}
		pace := time.NewTicker(10 * time.Millisecond)
		defer pace.Stop()
		code := l.handler(ctx, r, func(record protocol.LogRecord) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-pace.C:
			}
			seq++
			return l.writer.SendContext(ctx, protocol.MessageLogEvent, protocol.LogEvent{RequestID: r.RequestID, Sequence: seq, Kind: "record", Record: &record})
		})
		if !protocol.LogCode(code) {
			code = "RUNTIME_FAILED"
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = "TIME_LIMIT"
		} else if ctx.Err() != nil {
			code = "CANCELED"
		}
		seq++
		if l.writer.SendContext(l.ctx, protocol.MessageLogEvent, protocol.LogEvent{RequestID: r.RequestID, Sequence: seq, Kind: "end", Code: code, Gap: code != "OK"}) != nil {
			_ = l.writer.Close()
		}
	}()
	return nil
}
func (l *agentLogs) cancel(id string) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	if cancel := l.jobs[id]; cancel != nil {
		cancel()
	}
}
