package managed

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func enrollmentFixture(t *testing.T) (*Registry, *EnrollmentService, *httptest.Server) {
	t.Helper()
	a, _, err := NewAuthority("test backup password sufficiently long", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := InitializeRegistry(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	r, err := OpenRegistry(store, a)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewEnrollmentService(r, "127.0.0.1:9443", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	cert, key, err := a.Server("127.0.0.1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(a.RootPEM())
	server := httptest.NewUnstartedServer(s)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}, ClientCAs: pool, ClientAuth: tls.VerifyClientCertIfGiven}
	server.StartTLS()
	t.Cleanup(server.Close)
	return r, s, server
}
func TestPinnedEnrollmentEndToEnd(t *testing.T) {
	r, _, server := enrollmentFixture(t)
	i, err := r.CreateInvitation(context.Background(), "node-01", server.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	key, csr, _ := NewAgentRequest(i.NodeID)
	result, err := EnrollAgent(context.Background(), i, "0123456789abcdef0123456789abcdef", key, csr)
	if err != nil || VerifyAgentMaterial(i.NodeID, result.Certificate, key, result.CA, time.Now()) != nil {
		t.Fatal(err)
	}
	if result.ServerName != "127.0.0.1" || result.Controller != "127.0.0.1:9443" {
		t.Fatal("wrong runtime destination")
	}
	wrong := i
	wrong.Fingerprint = strings.Repeat("0", 64)
	if _, err := EnrollAgent(context.Background(), wrong, "0123456789abcdef0123456789abcdef", key, csr); err == nil {
		t.Fatal("wrong fingerprint accepted")
	}
}
func TestBootstrapDoesNotSendSecretBeforeTLSAuthentication(t *testing.T) {
	r, _, real := enrollmentFixture(t)
	i, err := r.CreateInvitation(context.Background(), "node-01", real.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	var posts atomic.Int32
	// This server can copy the public CA, but its TLS identity is not signed by it.
	mitm := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			posts.Add(1)
		}
		writeManagedJSON(w, struct {
			Root []byte `json:"root"`
		}{r.authority.RootPEM()})
	}))
	defer mitm.Close()
	i.URL = mitm.URL
	key, csr, _ := NewAgentRequest(i.NodeID)
	if _, err := EnrollAgent(context.Background(), i, "0123456789abcdef0123456789abcdef", key, csr); err == nil {
		t.Fatal("MITM accepted")
	}
	if posts.Load() != 0 {
		t.Fatal("secret request reached unauthenticated TLS server")
	}
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		http.Redirect(w, request, mitm.URL+"/v1/bootstrap", http.StatusFound)
	}))
	defer redirect.Close()
	i.URL = redirect.URL
	if _, _, err := PinnedEnrollmentClient(context.Background(), i); err == nil {
		t.Fatal("redirect accepted")
	}
}
func TestManagedJSONRejectsDuplicateCaseUnknownAndTrailing(t *testing.T) {
	for _, data := range []string{`{"node_id":"a","node_id":"b"}`, `{"Node_ID":"a"}`, `{"node_id":"a","unknown":1}`, `{} {}`, `{"node_id":`} {
		var request EnrollmentRequest
		if DecodeJSON([]byte(data), &request, 1024) == nil {
			t.Fatal(data)
		}
	}
}
