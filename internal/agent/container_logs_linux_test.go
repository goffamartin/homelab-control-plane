package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"hlcp/internal/controller"
	"hlcp/internal/localapi"
	"hlcp/internal/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func logHTTP(t *testing.T, ctx context.Context, api, path, token string) *http.Response {
	t.Helper()
	r, err := http.NewRequestWithContext(ctx, "GET", api+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+token)
	response, err := (&http.Client{Timeout: 8 * time.Second}).Do(r)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
func logPageHTTP(t *testing.T, api, path, token string) (controller.ContainerLogPage, int) {
	t.Helper()
	response := logHTTP(t, context.Background(), api, path, token)
	defer response.Body.Close()
	var page controller.ContainerLogPage
	if response.StatusCode == 200 {
		if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
			t.Fatal(err)
		}
	}
	return page, response.StatusCode
}
func logStreamRecord(t *testing.T, decoder *json.Decoder) controller.ContainerLogMessage {
	t.Helper()
	for i := 0; i < 10; i++ {
		var message controller.ContainerLogMessage
		if err := decoder.Decode(&message); err != nil {
			t.Fatal(err)
		}
		if message.Record != nil || message.Kind == "end" {
			return message
		}
	}
	t.Fatal("no log record")
	return controller.ContainerLogMessage{}
}

func TestContainerLogHistoryAndStreamingThroughMTLS(t *testing.T) {
	for _, runtime := range []string{"docker", "podman"} {
		t.Run(runtime, func(t *testing.T) {
			serverFiles, clientFiles := e2ePKI(t)
			dir := t.TempDir()
			ledger := filepath.Join(dir, "ledger.json")
			fixture := startE2EController(t, serverFiles, ledger)
			c := mixedConfig(t, runtime)
			c.ControllerAddress = fixture.server.Address().String()
			c.ServerName = clientFiles.ServerName
			c.CertificateFile = clientFiles.CertificateFile
			c.PrivateKeyFile = clientFiles.PrivateKeyFile
			c.CAFile = clientFiles.CAFile
			c.JournalFile = filepath.Join(dir, "journal.json")
			c.Capabilities = []string{"resource.control", runtime}
			c.ObservationInterval = 5 * time.Minute
			resource := &c.Resources[0]
			resource.Actions = []string{"status", "restart", "logs"}
			c.runner = new(mixedE2ERunner)
			var calls atomic.Int32
			canceled := make(chan struct{}, 8)
			c.logReader = func(ctx context.Context, name, target string, q protocol.LogQuery, emit func(protocol.LogRecord) error) string {
				calls.Add(1)
				if name != runtime || target != resource.Target {
					return "NOT_ALLOWED"
				}
				if q.Follow {
					if emit(protocol.LogRecord{Stream: "stdout", Text: "PRIVATE-live"}) != nil {
						return "CANCELED"
					}
					<-ctx.Done()
					canceled <- struct{}{}
					return "CANCELED"
				}
				for _, r := range []protocol.LogRecord{{Stream: "stdout", Timestamp: "2026-10-16T00:00:01Z", Text: "PRIVATE-alpha"}, {Stream: "stderr", Timestamp: "2026-10-16T00:00:02Z", Text: "PRIVATE-beta"}, {Stream: "stdout", Timestamp: "2026-10-16T00:00:03Z", Text: "PRIVATE-gamma"}} {
					if q.Stream != "both" && q.Stream != r.Stream || !strings.Contains(r.Text, q.Contains) {
						continue
					}
					if emit(r) != nil {
						return "CANCELED"
					}
				}
				return "OK"
			}
			stop := startE2EAgent(t, c)
			e2eEventually(t, "log capable node online", func() bool {
				record, ok := fixture.server.Registry().Get(c.NodeID)
				return ok && record.State == "online" && hasString(record.Capabilities, protocol.LogCapability)
			})
			base := "/v1/nodes/debian-01/resources/" + url.PathEscape(resource.ID)
			native := scriptRequest(t, fixture.api.URL, "commands", protocol.CommandPayload{CommandID: "baseline", ResourceID: resource.ID, Action: "status"})
			if native.status != 200 || !native.result.Success {
				t.Fatal(native)
			}
			beforeJournal, _ := os.ReadFile(c.JournalFile)
			beforeLedger, _ := os.ReadFile(ledger)
			page, status := logPageHTTP(t, fixture.api.URL, base+"/logs?limit=1", e2eToken)
			if status != 200 || len(page.Records) != 1 || page.Records[0].Text != "PRIVATE-alpha" || page.NextCursor == "" || page.SchemaVersion != 1 || page.Code != "OK" {
				t.Fatal(status, page)
			}
			next, status := logPageHTTP(t, fixture.api.URL, base+"/logs?cursor="+url.QueryEscape(page.NextCursor)+"&limit=1", e2eToken)
			if status != 200 || len(next.Records) != 1 || next.Records[0].Text != "PRIVATE-beta" || calls.Load() != 1 {
				t.Fatal(status, next, calls.Load())
			}
			_, status = logPageHTTP(t, fixture.api.URL, base+"/logs?cursor="+url.QueryEscape(page.NextCursor)+"x", e2eToken)
			if status != 400 {
				t.Fatal("tampered cursor", status)
			}
			filtered, status := logPageHTTP(t, fixture.api.URL, base+"/logs?stream=stderr&contains=beta&since=2026-10-16T00%3A00%3A00Z", e2eToken)
			if status != 200 || len(filtered.Records) != 1 || filtered.Records[0].Text != "PRIVATE-beta" {
				t.Fatal(status, filtered)
			}
			another := httptest.NewServer(fixture.server.HTTPHandler(strings.Repeat("z", 32)))
			defer another.Close()
			_, status = logPageHTTP(t, another.URL, base+"/logs?cursor="+url.QueryEscape(page.NextCursor), strings.Repeat("z", 32))
			if status != 400 {
				t.Fatal("cursor shared across credentials", status)
			}
			beforeCalls := calls.Load()
			for _, query := range []string{"tail=-1", "stream=shell", "contains=x&contains=y", "exec=sh", "cursor=x&tail=1"} {
				_, status = logPageHTTP(t, fixture.api.URL, base+"/logs?"+query, e2eToken)
				if status != 400 || calls.Load() != beforeCalls {
					t.Fatal("invalid request reached agent", query, status)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			stream := logHTTP(t, ctx, fixture.api.URL, base+"/logs/stream?tail=0", e2eToken)
			decoder := json.NewDecoder(stream.Body)
			live := logStreamRecord(t, decoder)
			if stream.StatusCode != 200 || live.Record == nil || live.Record.Text != "PRIVATE-live" {
				t.Fatal("stream", live)
			}
			cancel()
			stream.Body.Close()
			select {
			case <-canceled:
			case <-time.After(2 * time.Second):
				t.Fatal("HTTP disconnect did not cancel agent log process")
			}
			afterJournal, _ := os.ReadFile(c.JournalFile)
			afterLedger, _ := os.ReadFile(ledger)
			if !bytes.Equal(beforeJournal, afterJournal) || !bytes.Equal(beforeLedger, afterLedger) {
				t.Fatal("log reads changed execution persistence")
			}
			telemetry, _ := json.Marshal(fixture.server.Telemetry().Window())
			if strings.Contains(string(telemetry), "PRIVATE-") {
				t.Fatal("log content entered telemetry")
			}
			disconnected := logHTTP(t, context.Background(), fixture.api.URL, base+"/logs/stream?tail=0", e2eToken)
			defer disconnected.Body.Close()
			decoder = json.NewDecoder(disconnected.Body)
			logStreamRecord(t, decoder)
			stop()
			var end controller.ContainerLogMessage
			if err := decoder.Decode(&end); err != nil || end.Kind != "end" || !end.Gap || end.Code != "SESSION_LOST" {
				t.Fatal("session loss not explicit", end, err)
			}
		})
	}
}
func hasString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func TestContainerLogsScopedObserverAndLiveRevocation(t *testing.T) {
	serverFiles, clientFiles := e2ePKI(t)
	dir := t.TempDir()
	fixture := startE2EController(t, serverFiles, filepath.Join(dir, "ledger.json"))
	c := mixedConfig(t, "docker")
	c.ControllerAddress = fixture.server.Address().String()
	c.ServerName = clientFiles.ServerName
	c.CertificateFile = clientFiles.CertificateFile
	c.PrivateKeyFile = clientFiles.PrivateKeyFile
	c.CAFile = clientFiles.CAFile
	c.JournalFile = filepath.Join(dir, "journal.json")
	c.Resources[0].Actions = []string{"status", "logs"}
	c.runner = new(mixedE2ERunner)
	done := make(chan struct{}, 4)
	var calls atomic.Int32
	c.logReader = func(ctx context.Context, _ string, _ string, q protocol.LogQuery, emit func(protocol.LogRecord) error) string {
		calls.Add(1)
		emit(protocol.LogRecord{Stream: "stdout", Text: "PRIVATE"})
		if q.Follow {
			<-ctx.Done()
			done <- struct{}{}
		}
		return "OK"
	}
	startE2EAgent(t, c)
	e2eEventually(t, "observer log node", func() bool { r, ok := fixture.server.Registry().Get(c.NodeID); return ok && r.State == "online" })
	policy := `{"version":1,"principals":[{"id":"observer","role":"observer","token_file":"observer.token","scope":{"nodes":{"debian-01":{"resources":["example-catalog"]}}}}]}`
	token := strings.Repeat("o", 32)
	os.WriteFile(filepath.Join(dir, "observer.token"), []byte(token), 0600)
	policyPath := filepath.Join(dir, "policy.json")
	os.WriteFile(policyPath, []byte(policy), 0600)
	p, err := localapi.LoadPolicy(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	authorizer := localapi.NewAuthorizer(p)
	api := httptest.NewServer(fixture.server.HTTPHandlerWithAuthorizer(authorizer))
	defer api.Close()
	base := "/v1/nodes/debian-01/resources/example-catalog"
	_, status := logPageHTTP(t, api.URL, base+"/logs", token)
	if status != 200 {
		t.Fatal("observer cannot read permitted logs", status)
	}
	before := calls.Load()
	for _, path := range []string{"/v1/nodes/hidden/resources/app/logs", "/v1/nodes/debian-01/resources/hidden/logs"} {
		response := logHTTP(t, context.Background(), api.URL, path, token)
		response.Body.Close()
		if response.StatusCode != 403 || calls.Load() != before {
			t.Fatal("scope escaped", response.StatusCode)
		}
	}
	response := logHTTP(t, context.Background(), api.URL, base+"/logs/stream?tail=0", token)
	defer response.Body.Close()
	decoder := json.NewDecoder(response.Body)
	logStreamRecord(t, decoder)
	policy = strings.ReplaceAll(policy, "observer.token", "replacement.token")
	os.WriteFile(filepath.Join(dir, "replacement.token"), []byte(strings.Repeat("p", 32)), 0600)
	os.WriteFile(policyPath, []byte(policy), 0600)
	if authorizer.Reload(policyPath) != nil {
		t.Fatal("reload")
	}
	for i := 0; i < 3; i++ {
		var message controller.ContainerLogMessage
		if err := decoder.Decode(&message); err != nil {
			t.Fatal(err)
		}
		if message.Kind == "end" {
			if message.Code != "FORBIDDEN" || !message.Gap {
				t.Fatal(message)
			}
			break
		}
		if i == 2 {
			t.Fatal("revocation not signaled")
		}
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("revoked log reader not canceled")
	}
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
	if strings.Contains(string(body), "PRIVATE") {
		t.Fatal("data after revocation")
	}
}
