package managed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type EnrollmentService struct {
	Registry                      *Registry
	ControllerAddress, ServerName string
	limitMutex                    sync.Mutex
	window                        time.Time
	requests                      int
	inflight                      chan struct{}
}

func NewEnrollmentService(r *Registry, controller, serverName string) (*EnrollmentService, error) {
	if r == nil || controller == "" || serverName == "" {
		return nil, ErrInput
	}
	return &EnrollmentService{Registry: r, ControllerAddress: controller, ServerName: serverName, inflight: make(chan struct{}, 32)}, nil
}
func (s *EnrollmentService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A global bounded gate deliberately avoids an attacker-controlled IP map.
	s.limitMutex.Lock()
	now := time.Now()
	if now.Sub(s.window) >= time.Minute {
		s.window = now
		s.requests = 0
	}
	s.requests++
	allowed := s.requests <= 120
	s.limitMutex.Unlock()
	if !allowed {
		http.Error(w, "enrollment rate limit", http.StatusTooManyRequests)
		return
	}
	select {
	case s.inflight <- struct{}{}:
		defer func() { <-s.inflight }()
	default:
		http.Error(w, "enrollment busy", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" {
		http.Error(w, "invalid request", 400)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/bootstrap":
		root := s.Registry.authority.RootPEM()
		writeManagedJSON(w, struct {
			Root []byte `json:"root"`
		}{root})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/enroll":
		data, err := io.ReadAll(io.LimitReader(r.Body, 64<<10+1))
		if err != nil || len(data) > 64<<10 {
			http.Error(w, "invalid request", 400)
			return
		}
		var request EnrollmentRequest
		if DecodeJSON(data, &request, 64<<10) != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		chain, err := s.Registry.Enroll(r.Context(), request)
		if err != nil {
			http.Error(w, "enrollment denied", http.StatusForbidden)
			return
		}
		root := s.Registry.authority.RootPEM()
		writeManagedJSON(w, EnrollmentResult{Certificate: chain, CA: root, Controller: s.ControllerAddress, ServerName: s.ServerName})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/renew":
		s.renew(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *EnrollmentService) renew(w http.ResponseWriter, r *http.Request) {
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
		http.Error(w, "renewal denied", http.StatusForbidden)
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, (32<<10)+1))
	if err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	var request RenewalRequest
	if DecodeJSON(data, &request, 32<<10) != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	chain, err := s.Registry.Renew(r.Context(), r.TLS.VerifiedChains[0][0], request)
	if err != nil {
		http.Error(w, "renewal denied", http.StatusForbidden)
		return
	}
	writeManagedJSON(w, EnrollmentResult{Certificate: chain, CA: s.Registry.authority.RootPEM(), Controller: s.ControllerAddress, ServerName: s.ServerName})
}
func writeManagedJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func enrollmentHTTPClient(config *tls.Config) *http.Client {
	return &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, TLSClientConfig: config, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 5 * time.Second, MaxResponseHeaderBytes: 16 << 10, MaxConnsPerHost: 2, DisableKeepAlives: true}}
}
func readResponse(response *http.Response, out any, limit int) error {
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ErrAdmission
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(limit+1)))
	if err != nil {
		return ErrAdmission
	}
	return DecodeJSON(data, out, limit)
}

// Bootstrap sends NO invitation secret, CSR or private material. Its untrusted
// response is accepted only after root DER fingerprint verification. All secret
// requests use a separate fully verified TLS1.3/hostname/CA client, no redirects.
func PinnedEnrollmentClient(ctx context.Context, i Invitation) (*http.Client, []byte, error) {
	if _, err := ParseInvitation(i.Encode()); err != nil {
		return nil, nil, ErrInput
	}
	bootstrap := enrollmentHTTPClient(&tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true}) // public bootstrap only, pinned below
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, i.URL+"/v1/bootstrap", nil)
	if err != nil {
		return nil, nil, ErrInput
	}
	response, err := bootstrap.Do(request)
	if err != nil {
		return nil, nil, ErrAdmission
	}
	var payload struct {
		Root []byte `json:"root"`
	}
	if readResponse(response, &payload, 32<<10) != nil {
		return nil, nil, ErrAdmission
	}
	block, rest := pem.Decode(payload.Root)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, nil, ErrAdmission
	}
	root, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, ErrAdmission
	}
	digest := sha256.Sum256(root.Raw)
	if hex.EncodeToString(digest[:]) != i.Fingerprint || !root.IsCA || root.CheckSignatureFrom(root) != nil || time.Now().Before(root.NotBefore) || !time.Now().Before(root.NotAfter) {
		return nil, nil, ErrAdmission
	}
	pool := x509.NewCertPool()
	pool.AddCert(root)
	u, _ := url.Parse(i.URL)
	client := enrollmentHTTPClient(&tls.Config{MinVersion: tls.VersionTLS13, RootCAs: pool, ServerName: u.Hostname()})
	return client, payload.Root, nil
}
func EnrollAgent(ctx context.Context, i Invitation, requestID string, key, csr []byte) (EnrollmentResult, error) {
	client, root, err := PinnedEnrollmentClient(ctx, i)
	if err != nil {
		return EnrollmentResult{}, err
	}
	data, _ := json.Marshal(EnrollmentRequest{NodeID: i.NodeID, Secret: i.Secret, RequestID: requestID, CSR: csr})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, i.URL+"/v1/enroll", bytes.NewReader(data))
	if err != nil {
		return EnrollmentResult{}, ErrInput
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return EnrollmentResult{}, ErrAdmission
	}
	var result EnrollmentResult
	if readResponse(response, &result, 64<<10) != nil || !bytes.Equal(root, result.CA) || VerifyAgentMaterial(i.NodeID, result.Certificate, key, root, time.Now()) != nil {
		return EnrollmentResult{}, ErrAdmission
	}
	return result, nil
}
func VerifyAgentMaterial(id string, chain, key, root []byte, now time.Time) error {
	pair, err := tls.X509KeyPair(chain, key)
	if err != nil || len(pair.Certificate) < 2 {
		return ErrPKI
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || leaf.IsCA || len(leaf.URIs) != 1 || leaf.URIs[0].String() != "urn:hlcp:node:"+id {
		return ErrPKI
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(root) {
		return ErrPKI
	}
	intermediate := x509.NewCertPool()
	for _, der := range pair.Certificate[1:] {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return ErrPKI
		}
		intermediate.AddCert(cert)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediate, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return ErrPKI
	}
	return nil
}
