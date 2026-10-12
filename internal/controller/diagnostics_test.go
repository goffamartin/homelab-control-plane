package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hlcp/internal/localapi"
	"hlcp/internal/telemetry"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func diagnosticFixture(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	policy := `{"version":1,"principals":[{"id":"global","role":"observer","token_file":"g.token","scope":{"all_nodes":true}},{"id":"other-global","role":"observer","token_file":"h.token","scope":{"all_nodes":true}},{"id":"scoped","role":"observer","token_file":"s.token","scope":{"nodes":{"n":{"resources":["catalog"]}}}}]}`
	for name, data := range map[string]string{"policy.json": policy, "g.token": strings.Repeat("g", 32), "h.token": strings.Repeat("h", 32), "s.token": strings.Repeat("s", 32)} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p, err := localapi.LoadPolicy(filepath.Join(dir, "policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{registry: NewRegistry(), config: Config{Telemetry: telemetry.New(nil)}}
	return s, s.HTTPHandlerWithAuthorizer(localapi.NewAuthorizer(p))
}
func diagnosticRequest(t *testing.T, h http.Handler, token, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", "http://127.0.0.1"+path, nil)
	r.Header.Set("Authorization", "Bearer "+strings.Repeat(token, 32))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func diagnosticPage(t *testing.T, w *httptest.ResponseRecorder) diagnosticLogPage {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var p diagnosticLogPage
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDiagnosticScopeAndNoHiddenIdentifiers(t *testing.T) {
	s, h := diagnosticFixture(t)
	for _, e := range []telemetry.Event{
		{Kind: telemetry.CommandResult, NodeID: "n", ResourceID: "catalog", CommandID: "visible"},
		{Kind: telemetry.CommandResult, NodeID: "n", ResourceID: "hidden-resource", CommandID: "hidden-command"},
		{Kind: telemetry.RecoveryQuery, NodeID: "n", CommandID: "hidden-command-without-resource"},
		{Kind: telemetry.SessionStarted, NodeID: "n", SessionID: "session"},
		{Kind: telemetry.SessionStarted, NodeID: "hidden-node"},
	} {
		s.Telemetry().Record(e)
	}
	for _, path := range []string{"/v1/diagnostics/metrics", "/v1/diagnostics/logs", "/v1/nodes/hidden-node/diagnostics/logs", "/v1/nodes/n/resources/hidden-resource/diagnostics/logs"} {
		if w := diagnosticRequest(t, h, "s", path); w.Code != 403 {
			t.Fatalf("scope escaped at %s: %d", path, w.Code)
		}
	}
	for _, path := range []string{"/v1/nodes/n/diagnostics/logs", "/v1/nodes/n/resources/catalog/diagnostics/logs"} {
		w := diagnosticRequest(t, h, "s", path)
		p := diagnosticPage(t, w)
		if strings.Contains(w.Body.String(), "hidden-") || strings.Contains(w.Body.String(), "sequence") || len(p.Events) == 0 {
			t.Fatalf("leak or empty page: %s", w.Body.String())
		}
	}
	if w := diagnosticRequest(t, h, "g", "/v1/diagnostics/metrics"); w.Code != 200 || !strings.Contains(w.Body.String(), "read_frames") {
		t.Fatal("global metrics unavailable")
	}
}

func TestCursorPagingGapRestartAndBinding(t *testing.T) {
	s, h := diagnosticFixture(t)
	for i := 0; i < 5; i++ {
		s.Telemetry().Record(telemetry.Event{Kind: telemetry.CommandResult, NodeID: "n", ResourceID: "catalog", CommandID: fmt.Sprint(i)})
	}
	path := "/v1/nodes/n/resources/catalog/diagnostics/logs"
	first := diagnosticPage(t, diagnosticRequest(t, h, "s", path+"?limit=2"))
	if len(first.Events) != 2 || !first.Truncated || first.Gap || first.Reset {
		t.Fatalf("first=%+v", first)
	}
	second := diagnosticPage(t, diagnosticRequest(t, h, "s", path+"?limit=2&cursor="+url.QueryEscape(first.NextCursor)))
	if len(second.Events) != 2 || string(second.Events[0]) == string(first.Events[0]) {
		t.Fatal("cursor duplicated records")
	}
	third := diagnosticPage(t, diagnosticRequest(t, h, "s", path+"?cursor="+second.NextCursor))
	if len(third.Events) != 1 || third.Truncated {
		t.Fatalf("third=%+v", third)
	}
	for i := 0; i < telemetry.BufferCapacity+1; i++ {
		s.Telemetry().Record(telemetry.Event{Kind: telemetry.CommandResult, NodeID: "n", ResourceID: "catalog"})
	}
	expired := diagnosticPage(t, diagnosticRequest(t, h, "s", path+"?limit=1&cursor="+first.NextCursor))
	if !expired.Gap {
		t.Fatal("overwritten history not signaled")
	}
	if w := diagnosticRequest(t, h, "g", path+"?cursor="+first.NextCursor); w.Code != 400 {
		t.Fatal("cursor transferred between identities")
	}
	if w := diagnosticRequest(t, h, "s", "/v1/nodes/n/diagnostics/logs?cursor="+first.NextCursor); w.Code != 400 {
		t.Fatal("cursor transferred between queries")
	}
	token, err := base64.RawURLEncoding.DecodeString(first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	token[len(token)-1] ^= 1
	if w := diagnosticRequest(t, h, "s", path+"?cursor="+base64.RawURLEncoding.EncodeToString(token)); w.Code != 400 {
		t.Fatal("forged cursor accepted")
	}
	_, restarted := diagnosticFixture(t)
	p := diagnosticPage(t, diagnosticRequest(t, restarted, "s", path+"?cursor="+first.NextCursor))
	if !p.Reset {
		t.Fatal("restart not signaled")
	}
}

func TestResponseByteLimitAndMalformedQuery(t *testing.T) {
	s, h := diagnosticFixture(t)
	large := strings.Repeat("<", telemetry.MaxFieldBytes)
	for i := 0; i < 300; i++ {
		s.Telemetry().Record(telemetry.Event{Kind: telemetry.CommandResult, NodeID: large, ResourceID: large, CommandID: large, ActorID: large, SessionID: large, Action: large, Code: large})
	}
	w := diagnosticRequest(t, h, "g", "/v1/diagnostics/logs?limit=200")
	p := diagnosticPage(t, w)
	if w.Body.Len() > diagnosticMaxBytes || !p.Truncated || len(p.Events) >= 200 || len(p.Events) == 0 {
		t.Fatalf("bytes=%d records=%d truncated=%v", w.Body.Len(), len(p.Events), p.Truncated)
	}
	for _, query := range []string{"limit=0", "limit=201", "limit=1&limit=2", "cursor=", "cursor=bad", "unknown=1", strings.Repeat("x", 1025)} {
		if w := diagnosticRequest(t, h, "h", "/v1/diagnostics/logs?"+query); w.Code != 400 {
			t.Fatalf("query %q => %d", query, w.Code)
		}
	}
	if w := diagnosticRequest(t, h, "g", "/v1/diagnostics/metrics?limit=1"); w.Code != 400 {
		t.Fatal("metrics accepted query")
	}
}

func TestDiagnosticBudgetsHaveFixedCapacity(t *testing.T) {
	d := newDiagnostics()
	now := time.Now()
	d.now = func() time.Time { return now }
	for i := 0; i < diagnosticRequestBudget; i++ {
		release, ok := d.reserve("one")
		if !ok {
			t.Fatal("early request limit")
		}
		release(100)
	}
	if _, ok := d.reserve("one"); ok {
		t.Fatal("request rate not bounded")
	}
	now = now.Add(diagnosticWindow)
	for i := 0; i < 4; i++ {
		release, ok := d.reserve("one")
		if !ok {
			t.Fatal("early byte limit")
		}
		release(diagnosticMaxBytes)
	}
	if _, ok := d.reserve("one"); ok {
		t.Fatal("byte rate not bounded")
	}
	d = newDiagnostics()
	releases := []func(int){}
	for i := 0; i < 4; i++ {
		for j := 0; j < 2; j++ {
			release, ok := d.reserve(fmt.Sprint(i))
			if !ok {
				t.Fatal("early concurrent limit")
			}
			releases = append(releases, release)
		}
		if _, ok := d.reserve(fmt.Sprint(i)); ok {
			t.Fatal("principal concurrent limit")
		}
	}
	if _, ok := d.reserve("other"); ok {
		t.Fatal("global concurrent limit")
	}
	for _, release := range releases {
		release(0)
	}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			for j := 0; j < 100; j++ {
				if release, ok := d.reserve(fmt.Sprint(i)); ok {
					release(100)
				}
			}
		}(i)
	}
	workers.Wait()
	if d.active != 0 {
		t.Fatal("slots leaked")
	}
	d = newDiagnostics()
	d.now = func() time.Time { return now }
	for i := 0; i < 64; i++ {
		release, ok := d.reserve(fmt.Sprint(i))
		if !ok {
			t.Fatal("early table limit")
		}
		release(0)
	}
	if _, ok := d.reserve("extra"); ok || len(d.budgets) != 64 {
		t.Fatal("principal table grew")
	}
	now = now.Add(time.Minute)
	release, ok := d.reserve("extra")
	if !ok {
		t.Fatal("expired table slots not reclaimed")
	}
	release(0)
}

func TestCanceledDiagnosticAndQuotaResponse(t *testing.T) {
	s, h := diagnosticFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("GET", "http://127.0.0.1/v1/diagnostics/logs", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("g", 32))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 499 || s.diagnostics.active != 0 {
		t.Fatal("canceled request acquired slot")
	}
	for i := 0; i < diagnosticRequestBudget; i++ {
		if w := diagnosticRequest(t, h, "g", "/v1/diagnostics/metrics"); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	w = diagnosticRequest(t, h, "g", "/v1/diagnostics/metrics")
	if w.Code != 429 || w.Header().Get("Retry-After") == "" || s.diagnostics.active != 0 {
		t.Fatal("quota response or release missing")
	}
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
	bounded  bool
}

func (w *deadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}
func (w *deadlineRecorder) Write(data []byte) (int, error) {
	w.bounded = !w.deadline.IsZero() && time.Until(w.deadline) > 0 && time.Until(w.deadline) <= diagnosticTimeout
	return w.ResponseRecorder.Write(data)
}
func TestDiagnosticWriteDeadlineSurvivesAuditWrapper(t *testing.T) {
	_, h := diagnosticFixture(t)
	r := httptest.NewRequest("GET", "http://127.0.0.1/v1/diagnostics/metrics", nil)
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("g", 32))
	w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	h.ServeHTTP(w, r)
	if w.Code != 200 || !w.bounded || !w.deadline.IsZero() {
		t.Fatal("diagnostic write deadline lost through wrapper or not restored")
	}
}

func TestLateReservationCannotRefundAnotherWindow(t *testing.T) {
	d := newDiagnostics()
	now := time.Now()
	d.now = func() time.Time { return now }
	old, ok := d.reserve("actor")
	if !ok {
		t.Fatal("first reservation")
	}
	now = now.Add(diagnosticWindow)
	current, ok := d.reserve("actor")
	if !ok {
		t.Fatal("new window reservation")
	}
	old(0)
	if d.budgets["actor"].bytes != diagnosticMaxBytes {
		t.Fatal("late release refunded a different window")
	}
	current(diagnosticMaxBytes)
	if d.active != 0 || d.budgets["actor"].bytes != diagnosticMaxBytes {
		t.Fatal("quota or slot accounting corrupted")
	}
}
