package controller

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hlcp/internal/localapi"
	"hlcp/internal/protocol"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type containerLogSnapshot struct {
	id, node, resource, session string
	binding                     [32]byte
	created                     time.Time
	records                     []protocol.LogRecord
	code                        string
	gap, truncated              bool
}
type containerLogHistory struct {
	mutex     sync.Mutex
	key       [32]byte
	ready     bool
	snapshots map[string]*containerLogSnapshot
	timer     *time.Timer
}
type ContainerLogPage struct {
	SchemaVersion   int                  `json:"schema_version"`
	StreamSemantics string               `json:"stream_semantics"`
	CapturedAt      time.Time            `json:"captured_at"`
	SourceSession   string               `json:"source_session"`
	Records         []protocol.LogRecord `json:"records"`
	NextCursor      string               `json:"next_cursor,omitempty"`
	Code            string               `json:"code"`
	Gap             bool                 `json:"gap"`
	Truncated       bool                 `json:"truncated"`
}
type ContainerLogMessage struct {
	SchemaVersion   int    `json:"schema_version"`
	StreamSemantics string `json:"stream_semantics"`
	protocol.LogEvent
}

func (s *Server) registerContainerLogs(handle func(string, http.HandlerFunc)) {
	s.logHistoryOnce.Do(func() {
		h := &containerLogHistory{snapshots: map[string]*containerLogSnapshot{}}
		_, err := rand.Read(h.key[:])
		h.ready = err == nil
		s.logHistory = h
	})
	handle("GET /v1/nodes/{node}/resources/{resource}/logs", s.containerLogs(false))
	handle("GET /v1/nodes/{node}/resources/{resource}/logs/stream", s.containerLogs(true))
}
func parseContainerLogQuery(raw string, follow bool) (protocol.LogQuery, string, int, error) {
	q := protocol.DefaultLogQuery()
	q.Follow = follow
	if len(raw) > 2048 {
		return q, "", 0, protocol.ErrInvalidLogs
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return q, "", 0, err
	}
	for k, v := range values {
		if len(v) != 1 || v[0] == "" {
			return q, "", 0, protocol.ErrInvalidLogs
		}
		switch k {
		case "tail", "since", "until", "contains", "stream", "cursor", "limit":
		default:
			return q, "", 0, protocol.ErrInvalidLogs
		}
	}
	limit := 100
	if values.Has("limit") {
		limit, err = strconv.Atoi(values.Get("limit"))
		if err != nil || limit < 1 || limit > 200 {
			return q, "", 0, protocol.ErrInvalidLogs
		}
	}
	cursor := values.Get("cursor")
	if len(cursor) > 160 {
		return q, "", 0, protocol.ErrInvalidLogs
	}
	if follow && (cursor != "" || values.Has("limit")) {
		return q, "", 0, protocol.ErrInvalidLogs
	}
	if cursor != "" && len(values) > 2 {
		return q, "", 0, protocol.ErrInvalidLogs
	}
	if cursor != "" {
		for k := range values {
			if k != "cursor" && k != "limit" {
				return q, "", 0, protocol.ErrInvalidLogs
			}
		}
	}
	if values.Has("tail") {
		q.Tail, err = strconv.Atoi(values.Get("tail"))
		if err != nil {
			return q, "", 0, err
		}
	}
	q.Since = values.Get("since")
	q.Until = values.Get("until")
	q.Contains = values.Get("contains")
	if values.Has("stream") {
		q.Stream = values.Get("stream")
	}
	return q, cursor, limit, q.Validate()
}
func logHTTPError(w http.ResponseWriter, code string) {
	status := 503
	switch code {
	case "FORBIDDEN":
		status = 403
	case "INVALID_QUERY", "INVALID_CURSOR":
		status = 400
	case "NODE_NOT_FOUND", "LOGS_NOT_ENABLED":
		status = 404
	case "LOGS_UNSUPPORTED", "UNSUPPORTED":
		status = 422
	case "LOGS_CAPACITY", "CAPACITY":
		status = 429
	case "NOT_ALLOWED":
		status = 403
	case "CURSOR_EXPIRED":
		status = 410
	case "RUNTIME_FAILED":
		status = 502
	case "TIME_LIMIT":
		status = 504
	case "CANCELED":
		status = 499
	}
	localapi.Error(w, status, code, "Container log request did not complete. No container action was executed.")
}
func (s *Server) containerLogs(follow bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		node, resource := r.PathValue("node"), r.PathValue("resource")
		if !localapi.CurrentRead(r.Context(), node, resource) {
			forbidden(w)
			return
		}
		q, cursor, limit, err := parseContainerLogQuery(r.URL.RawQuery, follow)
		if err != nil {
			logHTTPError(w, "INVALID_QUERY")
			return
		}
		_, code := s.logResource(node, resource)
		if code != "" {
			logHTTPError(w, code)
			return
		}
		if follow {
			s.containerLogStream(w, r, node, resource, q)
			return
		}
		var snapshot *containerLogSnapshot
		offset := 0
		if cursor != "" {
			snapshot, offset, code = s.logHistory.lookup(cursor, node, resource, localapi.DiagnosticBinding(r.Context()))
			if code != "" {
				logHTTPError(w, code)
				return
			}
		} else {
			snapshot, code = s.captureContainerLogs(r.Context(), node, resource, q)
			if code != "" {
				logHTTPError(w, code)
				return
			}
			if !s.logHistory.store(snapshot) {
				logHTTPError(w, "LOGS_UNAVAILABLE")
				return
			}
		}
		page := s.logHistory.page(snapshot, offset, limit)
		if !localapi.CurrentRead(r.Context(), node, resource) {
			forbidden(w)
			return
		}
		control := http.NewResponseController(w)
		_ = control.SetWriteDeadline(time.Now().Add(5 * time.Second))
		defer control.SetWriteDeadline(time.Time{})
		writeJSON(w, page)
	}
}
func (s *Server) captureContainerLogs(parent context.Context, node, resource string, q protocol.LogQuery) (*containerLogSnapshot, string) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	read, code := s.sessions.openLog(ctx, node, resource, q)
	if code != "" {
		return nil, code
	}
	defer s.sessions.closeLog(read)
	snapshot := &containerLogSnapshot{id: logID(), node: node, resource: resource, session: read.session, binding: localapi.DiagnosticBinding(ctx), created: time.Now().UTC(), records: []protocol.LogRecord{}, code: "OK"}
	used := 0
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		if !localapi.CurrentRead(ctx, node, resource) {
			return nil, "FORBIDDEN"
		}
		if failure := s.sessions.logs.failure(read); failure != "" {
			snapshot.code = failure
			snapshot.gap = true
			return snapshot, ""
		}
		select {
		case <-ctx.Done():
			if parent.Err() != nil {
				return nil, "CANCELED"
			}
			snapshot.code = "TIME_LIMIT"
			snapshot.gap = true
			return snapshot, ""
		case <-tick.C:
		case <-read.failed:
		case event := <-read.events:
			if event.Kind == "end" {
				snapshot.code = event.Code
				snapshot.gap = event.Gap
				snapshot.truncated = snapshot.truncated || event.Code == "BYTE_LIMIT"
				if event.Code != "OK" && len(snapshot.records) == 0 {
					return nil, event.Code
				}
				return snapshot, ""
			}
			if event.Record != nil {
				data, _ := json.Marshal(event.Record)
				if used+len(data) > 512<<10 || len(snapshot.records) >= 1000 {
					snapshot.code = "BYTE_LIMIT"
					snapshot.truncated = true
					snapshot.gap = true
					return snapshot, ""
				}
				used += len(data)
				snapshot.records = append(snapshot.records, *event.Record)
				snapshot.truncated = snapshot.truncated || event.Record.Truncated
			}
		}
	}
}
func (h *containerLogHistory) store(s *containerLogSnapshot) bool {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	if !h.ready || s.id == "" {
		return false
	}
	now := time.Now()
	for id, old := range h.snapshots {
		if now.Sub(old.created) > 2*time.Minute {
			delete(h.snapshots, id)
		}
	}
	if len(h.snapshots) >= 8 {
		oldest := ""
		for id, old := range h.snapshots {
			if oldest == "" || old.created.Before(h.snapshots[oldest].created) {
				oldest = id
			}
		}
		delete(h.snapshots, oldest)
	}
	h.snapshots[s.id] = s
	h.expireLocked()
	return true
}
func (h *containerLogHistory) expireLocked() {
	if h.timer != nil || len(h.snapshots) == 0 {
		return
	}
	oldest := time.Now()
	for _, s := range h.snapshots {
		if s.created.Before(oldest) {
			oldest = s.created
		}
	}
	delay := time.Until(oldest.Add(2 * time.Minute))
	if delay < time.Millisecond {
		delay = time.Millisecond
	}
	h.timer = time.AfterFunc(delay, func() {
		h.mutex.Lock()
		defer h.mutex.Unlock()
		h.timer = nil
		for id, s := range h.snapshots {
			if time.Since(s.created) >= 2*time.Minute {
				delete(h.snapshots, id)
			}
		}
		h.expireLocked()
	})
}
func (h *containerLogHistory) cursor(s *containerLogSnapshot, offset int) string {
	body := s.id + "." + strconv.Itoa(offset)
	mac := hmac.New(sha256.New, h.key[:])
	mac.Write([]byte(body))
	mac.Write(s.binding[:])
	mac.Write([]byte(s.node + "\x00" + s.resource))
	return body + "." + hex.EncodeToString(mac.Sum(nil))
}
func (h *containerLogHistory) lookup(cursor, node, resource string, binding [32]byte) (*containerLogSnapshot, int, string) {
	parts := strings.Split(cursor, ".")
	if len(parts) != 3 {
		return nil, 0, "INVALID_CURSOR"
	}
	offset, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, 0, "INVALID_CURSOR"
	}
	h.mutex.Lock()
	defer h.mutex.Unlock()
	s := h.snapshots[parts[0]]
	if s == nil {
		return nil, 0, "CURSOR_EXPIRED"
	}
	if s.node != node || s.resource != resource || s.binding != binding || offset < 0 || offset > len(s.records) || !hmac.Equal([]byte(cursor), []byte(h.cursor(s, offset))) {
		return nil, 0, "INVALID_CURSOR"
	}
	if time.Since(s.created) > 2*time.Minute {
		delete(h.snapshots, s.id)
		return nil, 0, "CURSOR_EXPIRED"
	}
	return s, offset, ""
}
func (h *containerLogHistory) page(s *containerLogSnapshot, offset, limit int) ContainerLogPage {
	p := ContainerLogPage{SchemaVersion: 1, StreamSemantics: "runtime-pipes", CapturedAt: s.created, SourceSession: s.session, Records: []protocol.LogRecord{}, Code: s.code, Gap: s.gap, Truncated: s.truncated}
	used := 0
	for offset < len(s.records) && len(p.Records) < limit {
		data, _ := json.Marshal(s.records[offset])
		if used+len(data) > 64<<10 {
			break
		}
		used += len(data)
		p.Records = append(p.Records, s.records[offset])
		offset++
	}
	if offset < len(s.records) {
		p.NextCursor = h.cursor(s, offset)
	}
	return p
}
func (s *Server) containerLogStream(w http.ResponseWriter, r *http.Request, node, resource string, q protocol.LogQuery) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute+5*time.Second)
	defer cancel()
	read, code := s.sessions.openLog(ctx, node, resource, q)
	if code != "" {
		logHTTPError(w, code)
		return
	}
	defer s.sessions.closeLog(read)
	control := http.NewResponseController(w)
	defer control.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Cache-Control", "no-store")
	write := func(event protocol.LogEvent) bool {
		if err := control.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return false
		}
		if json.NewEncoder(w).Encode(ContainerLogMessage{SchemaVersion: 1, StreamSemantics: "runtime-pipes", LogEvent: event}) != nil {
			return false
		}
		return control.Flush() == nil
	}
	finish := func(code string) {
		_ = write(protocol.LogEvent{RequestID: read.id, Kind: "end", Code: code, Gap: true})
	}
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	used := 0
	for {
		if !localapi.CurrentRead(ctx, node, resource) {
			finish("FORBIDDEN")
			return
		}
		if code := s.sessions.logs.failure(read); code != "" {
			finish(code)
			return
		}
		select {
		case <-ctx.Done():
			if r.Context().Err() == nil {
				finish("TIME_LIMIT")
			}
			return
		case <-read.failed:
		case <-tick.C:
			if !write(protocol.LogEvent{RequestID: read.id, Kind: "keepalive"}) {
				return
			}
		case event := <-read.events:
			if !localapi.CurrentRead(ctx, node, resource) {
				finish("FORBIDDEN")
				return
			}
			data, _ := json.Marshal(event)
			used += len(data)
			if used > 4<<20 {
				finish("BYTE_LIMIT")
				return
			}
			if !write(event) {
				return
			}
			if event.Kind == "end" {
				return
			}
		}
	}
}
