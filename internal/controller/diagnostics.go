package controller

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hlcp/internal/localapi"
	"hlcp/internal/telemetry"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

const diagnosticMaxBytes = 64 << 10
const diagnosticMaxRecords = 200
const diagnosticTimeout = 5 * time.Second
const diagnosticWindow = 10 * time.Second
const diagnosticByteBudget = 256 << 10
const diagnosticRequestBudget = 10

type diagnosticBudget struct {
	at                      time.Time
	requests, bytes, active int
}
type diagnostics struct {
	mutex   sync.Mutex
	boot    [16]byte
	cipher  cipher.AEAD
	budgets map[string]*diagnosticBudget
	active  int
	now     func() time.Time
}

func newDiagnostics() *diagnostics {
	d := &diagnostics{budgets: make(map[string]*diagnosticBudget), now: time.Now}
	rand.Read(d.boot[:])
	key := make([]byte, 32)
	rand.Read(key)
	block, _ := aes.NewCipher(key)
	d.cipher, _ = cipher.NewGCM(block)
	return d
}

// Fixed, bounded identity table. Reloads cannot retain unlimited old principals.
func (d *diagnostics) reserve(actor string) (func(int), bool) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	now := d.now()
	b := d.budgets[actor]
	if b == nil {
		for id, old := range d.budgets {
			if old.active == 0 && now.Sub(old.at) >= time.Minute {
				delete(d.budgets, id)
			}
		}
		if len(d.budgets) >= 64 {
			return nil, false
		}
		b = &diagnosticBudget{at: now}
		d.budgets[actor] = b
	}
	if now.Sub(b.at) >= diagnosticWindow {
		b.at = now
		b.requests, b.bytes = 0, 0
	}
	if b.active >= 2 || d.active >= 8 || b.requests >= diagnosticRequestBudget || b.bytes+diagnosticMaxBytes > diagnosticByteBudget {
		return nil, false
	}
	b.requests++
	b.bytes += diagnosticMaxBytes
	b.active++
	d.active++
	// Reservations are charged in their original window. Late releases never
	// refund bytes from another window and cannot create a negative budget.
	window := b.at
	return func(actual int) {
		d.mutex.Lock()
		defer d.mutex.Unlock()
		b.active--
		d.active--
		if b.at.Equal(window) {
			b.bytes -= diagnosticMaxBytes - actual
		}
	}, true
}

func (d *diagnostics) cursor(sequence uint64, binding []byte) string {
	nonce := make([]byte, d.cipher.NonceSize())
	rand.Read(nonce)
	plain := make([]byte, 8)
	binary.BigEndian.PutUint64(plain, sequence)
	token := append(append([]byte(nil), d.boot[:]...), nonce...)
	token = d.cipher.Seal(token, nonce, plain, binding)
	return base64.RawURLEncoding.EncodeToString(token)
}
func (d *diagnostics) parseCursor(value string, binding []byte) (sequence uint64, reset bool, err error) {
	token, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || len(token) != 16+d.cipher.NonceSize()+8+d.cipher.Overhead() {
		return 0, false, errors.New("invalid cursor")
	}
	if string(token[:16]) != string(d.boot[:]) {
		return 0, true, nil
	}
	plain, err := d.cipher.Open(nil, token[16:16+d.cipher.NonceSize()], token[16+d.cipher.NonceSize():], binding)
	if err != nil {
		return 0, false, errors.New("invalid cursor")
	}
	return binary.BigEndian.Uint64(plain), false, nil
}

type diagnosticLogPage struct {
	BootID     string            `json:"boot_id"`
	ObservedAt time.Time         `json:"observed_at"`
	Events     []json.RawMessage `json:"events"`
	NextCursor string            `json:"next_cursor"`
	Truncated  bool              `json:"truncated"`
	Gap        bool              `json:"gap"`
	Reset      bool              `json:"reset"`
}

func diagnosticQuery(raw string, logs bool) (url.Values, error) {
	if len(raw) > 1024 {
		return nil, errors.New("query too large")
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, err
	}
	for key, list := range values {
		if !logs || (key != "cursor" && key != "limit") || len(list) != 1 || list[0] == "" {
			return nil, errors.New("invalid query")
		}
	}
	return values, nil
}

func (s *Server) registerDiagnostics(handle func(string, http.HandlerFunc)) {
	s.diagnosticsOnce.Do(func() { s.diagnostics = newDiagnostics() })
	for _, path := range []string{"GET /v1/diagnostics/logs", "GET /v1/nodes/{node}/diagnostics/logs", "GET /v1/nodes/{node}/resources/{resource}/diagnostics/logs"} {
		handle(path, s.diagnosticHandler(true))
	}
	handle("GET /v1/diagnostics/metrics", s.diagnosticHandler(false))
}

func (s *Server) diagnosticHandler(logs bool) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		node, resource := request.PathValue("node"), request.PathValue("resource")
		if node == "" && !localapi.GlobalRead(request.Context()) {
			forbidden(writer)
			return
		}
		query, err := diagnosticQuery(request.URL.RawQuery, logs)
		if err != nil {
			localapi.Error(writer, 400, "INVALID_QUERY", "invalid diagnostic query")
			return
		}
		if s.config.Telemetry == nil {
			localapi.Error(writer, 503, "DIAGNOSTICS_UNAVAILABLE", "diagnostics unavailable")
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), diagnosticTimeout)
		defer cancel()
		if ctx.Err() != nil {
			localapi.Error(writer, 499, "CANCELED", "diagnostic request canceled")
			return
		}
		// Reserve the full response before doing work; refund unused bytes afterwards.
		release, ok := s.diagnostics.reserve(localapi.Identity(ctx))
		if !ok {
			writer.Header().Set("Retry-After", "10")
			localapi.Error(writer, 429, "DIAGNOSTIC_BUDGET", "diagnostic budget exhausted")
			return
		}
		actual := diagnosticMaxBytes
		defer func() { release(actual) }()
		control := http.NewResponseController(writer)
		if err := control.SetWriteDeadline(time.Now().Add(diagnosticTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			localapi.Error(writer, 503, "DIAGNOSTICS_UNAVAILABLE", "diagnostic writer unavailable")
			return
		}
		defer control.SetWriteDeadline(time.Time{})
		var data []byte
		if logs {
			limit := 100
			if query.Has("limit") {
				limit, err = strconv.Atoi(query.Get("limit"))
				if err != nil || limit < 1 || limit > diagnosticMaxRecords {
					localapi.Error(writer, 400, "INVALID_QUERY", "limit must be 1..200")
					return
				}
			}
			data, err = s.logPage(ctx, node, resource, query.Get("cursor"), limit)
			if err != nil {
				if ctx.Err() != nil {
					localapi.Error(writer, 499, "CANCELED", "diagnostic request canceled")
					return
				}
				localapi.Error(writer, 400, "INVALID_CURSOR", "invalid diagnostic cursor")
				return
			}
		} else {
			data, err = json.Marshal(struct {
				BootID     string             `json:"boot_id"`
				ObservedAt time.Time          `json:"observed_at"`
				Metrics    telemetry.Snapshot `json:"metrics"`
			}{hex.EncodeToString(s.diagnostics.boot[:]), time.Now().UTC(), s.config.Telemetry.Snapshot()})
		}
		if ctx.Err() != nil {
			localapi.Error(writer, 499, "CANCELED", "diagnostic request canceled")
			return
		}
		if err != nil || len(data)+1 > diagnosticMaxBytes {
			localapi.Error(writer, 503, "DIAGNOSTICS_UNAVAILABLE", "diagnostic response unavailable")
			return
		}
		actual = len(data) + 1
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(append(data, '\n'))
	}
}

func (s *Server) logPage(ctx context.Context, node, resource, cursor string, limit int) ([]byte, error) {
	key := localapi.DiagnosticBinding(ctx)
	binding, _ := json.Marshal(struct {
		Key            [32]byte
		Node, Resource string
	}{key, node, resource})
	window := s.config.Telemetry.Window()
	after, reset := uint64(0), false
	if cursor != "" {
		var err error
		after, reset, err = s.diagnostics.parseCursor(cursor, binding)
		if err != nil {
			return nil, err
		}
	}
	page := diagnosticLogPage{BootID: hex.EncodeToString(s.diagnostics.boot[:]), ObservedAt: time.Now().UTC(), Events: make([]json.RawMessage, 0), Reset: reset}
	if after > window.Latest && !reset {
		return nil, errors.New("future cursor")
	}
	if after < window.Oldest-1 {
		page.Gap = true
		after = window.Oldest - 1
	}
	page.NextCursor = s.diagnostics.cursor(after, binding)
	baseline, _ := json.Marshal(page)
	size := len(baseline) + 17 // newline and boolean-size slack
	for _, event := range window.Events {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if event.Sequence <= after {
			continue
		}
		visible := node == "" || (event.NodeID == node && ((resource != "" && event.ResourceID == resource) || (resource == "" && ((event.ResourceID != "" && localapi.Allowed(ctx, node, event.ResourceID, false)) || (event.ResourceID == "" && event.CommandID == "")))))
		if !visible {
			after = event.Sequence
			continue
		}
		raw, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		if len(page.Events) >= limit || size+len(raw)+1 > diagnosticMaxBytes {
			page.Truncated = true
			break
		}
		page.Events = append(page.Events, raw)
		size += len(raw) + 1
		after = event.Sequence
	}
	page.NextCursor = s.diagnostics.cursor(after, binding)
	return json.Marshal(page)
}
