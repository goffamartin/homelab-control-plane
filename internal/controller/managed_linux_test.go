package controller

import (
	"context"
	"hlcp/internal/agent"
	"hlcp/internal/liveness"
	"hlcp/internal/localapi"
	"hlcp/internal/managed"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func managedControllerFixture(t *testing.T) (*managed.Registry, *managed.Authority, *managed.Store) {
	t.Helper()
	a, _, err := managed.NewAuthority("test backup password sufficiently long", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	store, err := managed.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := managed.InitializeRegistry(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	r, err := managed.OpenRegistry(store, a)
	if err != nil {
		t.Fatal(err)
	}
	return r, a, store
}
func TestManagedPairingAPIOperatorOnly(t *testing.T) {
	r, _, _ := managedControllerFixture(t)
	sessions := NewActiveSessions(NewCommandTracker())
	s := &Server{sessions: sessions, registry: sessions.registry, config: Config{ManagedRegistry: r, EnrollmentURL: "https://controller.test:9445"}}
	dir := t.TempDir()
	token := strings.Repeat("o", 32)
	_ = os.WriteFile(filepath.Join(dir, "observer.token"), []byte(token), 0600)
	policyPath := filepath.Join(dir, "policy.json")
	_ = os.WriteFile(policyPath, []byte(`{"version":1,"principals":[{"id":"observer","role":"observer","token_file":"observer.token","scope":{"all_nodes":true}}]}`), 0600)
	policy, err := localapi.LoadPolicy(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	observer := s.HTTPHandlerWithAuthorizer(localapi.NewAuthorizer(policy))
	for _, method := range []string{http.MethodPost, http.MethodDelete, http.MethodGet} {
		path := "/v1/pairing"
		if method == http.MethodDelete {
			path += "/node-01"
		}
		request := httptest.NewRequest(method, path, strings.NewReader(`{"node":"node-01"}`))
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		observer.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatal(method, response.Code, response.Body.String())
		}
	}
	list, err := r.List()
	if err != nil || len(list) != 0 {
		t.Fatal("observer changed registry", err)
	}
	request := apiRequest(http.MethodPost, "/v1/pairing", `{"node":"node-01"}`)
	response := httptest.NewRecorder()
	s.HTTPHandler(testAPIToken).ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "hlcp1.") {
		t.Fatal(response.Code, response.Body.String())
	}
}
func TestManagedRevocationDisconnectsLiveAgent(t *testing.T) {
	r, a, store := managedControllerFixture(t)
	if managed.CreateServerIdentity(context.Background(), store, a, "127.0.0.1") != nil {
		t.Fatal("identity create")
	}
	identity, err := managed.OpenServerIdentity(store, a, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	tlsConfig, err := identity.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	s, err := Listen("127.0.0.1:0", Config{TLSConfig: tlsConfig, ManagedRegistry: r, EnrollmentURL: "https://127.0.0.1:9445", LivenessPolicy: liveness.Policy{HeartbeatInterval: time.Second, DegradedAfter: 2, OfflineAfter: 3}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	defer func() { cancel(); s.Close(); <-done }()
	i, err := r.CreateInvitation(ctx, "node-01", "https://127.0.0.1:9445", false)
	if err != nil {
		t.Fatal(err)
	}
	key, csr, err := managed.NewAgentRequest(i.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	chain, err := r.Enroll(ctx, managed.EnrollmentRequest{NodeID: i.NodeID, Secret: i.Secret, RequestID: "0123456789abcdef0123456789abcdef", CSR: csr})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath := filepath.Join(dir, "agent.crt")
	keyPath := filepath.Join(dir, "agent.key")
	caPath := filepath.Join(dir, "ca.crt")
	_ = os.WriteFile(certPath, chain, 0600)
	_ = os.WriteFile(keyPath, key, 0600)
	_ = os.WriteFile(caPath, a.RootPEM(), 0600)
	agentCtx, stopAgent := context.WithCancel(ctx)
	agentDone := make(chan error, 1)
	go func() {
		agentDone <- agent.Run(agentCtx, agent.Config{NodeID: i.NodeID, AgentVersion: "managed-test", ControllerAddress: s.Address().String(), ServerName: "127.0.0.1", CertificateFile: certPath, PrivateKeyFile: keyPath, CAFile: caPath, JournalFile: filepath.Join(dir, "journal.json")})
	}()
	defer func() { stopAgent(); <-agentDone }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		record, ok := s.Registry().Get(i.NodeID)
		if ok && record.State == "online" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("managed agent not online")
		}
		time.Sleep(10 * time.Millisecond)
	}
	response := httptest.NewRecorder()
	s.HTTPHandler(testAPIToken).ServeHTTP(response, apiRequest(http.MethodDelete, "/v1/pairing/"+i.NodeID, ""))
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	deadline = time.Now().Add(2 * time.Second)
	for {
		record, _ := s.Registry().Get(i.NodeID)
		if record.State == "offline" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("revoked session remained online")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if list, err := r.List(); err != nil || len(list) != 1 || list[0].Active {
		t.Fatal("revocation not durable", err)
	}
}
