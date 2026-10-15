package managed

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"hlcp/internal/transport"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const identityFile = "identity.json"

type identityDisk struct {
	Version       int       `json:"version"`
	NodeID        string    `json:"node_id"`
	EnrollmentURL string    `json:"enrollment_url"`
	Certificate   []byte    `json:"certificate"`
	Key           []byte    `json:"key"`
	CA            []byte    `json:"ca"`
	CSR           []byte    `json:"csr"`
	PendingID     string    `json:"pending_id"`
	PendingAt     time.Time `json:"pending_at"`
	NextAttempt   time.Time `json:"next_attempt"`
	LastSuccess   time.Time `json:"last_success"`
	Failures      int       `json:"failures"`
}
type AgentIdentity struct {
	mutex      sync.RWMutex
	renewMutex sync.Mutex
	store      *Store
	disk       identityDisk
	pair       *tls.Certificate
}

func (*AgentIdentity) String() string               { return "managed agent identity (redacted)" }
func (i *AgentIdentity) GoString() string           { return i.String() }
func (*AgentIdentity) MarshalJSON() ([]byte, error) { return []byte(`{"identity":"redacted"}`), nil }
func CreateAgentIdentity(ctx context.Context, store *Store, id, enrollment string, key, csr []byte, result EnrollmentResult) error {
	if !validEnrollmentURL(enrollment) || VerifyAgentMaterial(id, result.Certificate, key, result.CA, time.Now()) != nil {
		return ErrPKI
	}
	d := identityDisk{Version: 1, NodeID: id, EnrollmentURL: enrollment, Key: key, CSR: csr, CA: result.CA, Certificate: result.Certificate, LastSuccess: time.Now()}
	data, _ := json.Marshal(d)
	return store.Create(ctx, identityFile, data)
}
func OpenAgentIdentity(store *Store, id string) (*AgentIdentity, error) {
	data, err := store.Read(identityFile)
	if err != nil {
		return nil, ErrStore
	}
	var d identityDisk
	if DecodeJSON(data, &d, 128<<10) != nil || d.Version != 1 || d.NodeID != id || !validEnrollmentURL(d.EnrollmentURL) || len(d.Key) > 16<<10 || len(d.CSR) > 16<<10 || d.Failures < 0 || d.Failures > 30 || VerifyAgentMaterial(id, d.Certificate, d.Key, d.CA, time.Now()) != nil {
		return nil, ErrPKI
	}
	pair, err := tls.X509KeyPair(d.Certificate, d.Key)
	if err != nil {
		return nil, ErrPKI
	}
	pair.Leaf, _ = x509.ParseCertificate(pair.Certificate[0])
	return &AgentIdentity{store: store, disk: d, pair: &pair}, nil
}
func (i *AgentIdentity) TLSConfig(serverName string) (*tls.Config, error) {
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	return transport.ClientTLSConfigDynamic(i.disk.CA, serverName, func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
		i.mutex.RLock()
		defer i.mutex.RUnlock()
		return i.pair, nil
	})
}

type CertificateStatus struct {
	Managed       bool      `json:"managed"`
	Expires       time.Time `json:"expires"`
	RenewAt       time.Time `json:"renew_at"`
	NextAttempt   time.Time `json:"next_attempt"`
	LastSuccess   time.Time `json:"last_success"`
	Failures      int       `json:"failures"`
	Warning       string    `json:"warning,omitempty"`
	IssuerExpires time.Time `json:"issuer_expires,omitempty"`
	IssuerWarning string    `json:"issuer_warning,omitempty"`
	RootExpires   time.Time `json:"root_expires,omitempty"`
}

func expiryWarning(expires, now time.Time) string {
	remaining := expires.Sub(now)
	switch {
	case remaining <= 0:
		return "CERTIFICATE_EXPIRED"
	case remaining <= 24*time.Hour:
		return "CERTIFICATE_EXPIRES_1D"
	case remaining <= 7*24*time.Hour:
		return "CERTIFICATE_EXPIRES_7D"
	case remaining <= 14*24*time.Hour:
		return "CERTIFICATE_EXPIRES_14D"
	case remaining <= 30*24*time.Hour:
		return "CERTIFICATE_EXPIRES_30D"
	}
	return ""
}
func (i *AgentIdentity) Status(now time.Time) CertificateStatus {
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	return CertificateStatus{Managed: true, Expires: i.pair.Leaf.NotAfter, RenewAt: RenewalTime(i.pair.Leaf), NextAttempt: i.disk.NextAttempt, LastSuccess: i.disk.LastSuccess, Failures: i.disk.Failures, Warning: expiryWarning(i.pair.Leaf.NotAfter, now)}
}
func renewalDelay(failures int) time.Duration {
	if failures > 8 {
		failures = 8
	}
	delay := time.Second * time.Duration(1<<uint(failures))
	if delay > 15*time.Minute {
		delay = 15 * time.Minute
	}
	var jitter [1]byte
	_, _ = rand.Read(jitter[:])
	return delay + time.Duration(jitter[0])*delay/512
}
func (i *AgentIdentity) save(ctx context.Context, d identityDisk) error {
	data, err := json.Marshal(d)
	if err != nil {
		return ErrStore
	}
	return i.store.Update(ctx, identityFile, func(before []byte) ([]byte, error) {
		var old identityDisk
		if DecodeJSON(before, &old, 128<<10) != nil || old.NodeID != d.NodeID || !bytes.Equal(old.Key, d.Key) || !bytes.Equal(old.CA, d.CA) {
			return nil, ErrStore
		}
		return data, nil
	})
}

// Renew persists request identity before network I/O and publishes the complete
// validated replacement atomically. Failure retains the last valid TLS snapshot.
func (i *AgentIdentity) Renew(ctx context.Context, now time.Time) error {
	i.renewMutex.Lock()
	defer i.renewMutex.Unlock()
	i.mutex.RLock()
	d := i.disk
	current := i.pair
	i.mutex.RUnlock()
	if !now.Before(current.Leaf.NotAfter) {
		return ErrAdmission
	}
	if now.Before(RenewalTime(current.Leaf)) || now.Before(d.NextAttempt) {
		return nil
	}
	if d.PendingID == "" || now.Sub(d.PendingAt) > EnrollmentRetryWindow {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return ErrStore
		}
		d.PendingID = hex.EncodeToString(id[:])
		d.PendingAt = now
		if i.save(ctx, d) != nil {
			return ErrStore
		}
		i.mutex.Lock()
		i.disk = d
		i.mutex.Unlock()
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(d.CA)
	u, _ := url.Parse(d.EnrollmentURL)
	client := enrollmentHTTPClient(&tls.Config{MinVersion: tls.VersionTLS13, ServerName: u.Hostname(), RootCAs: pool, Certificates: []tls.Certificate{*current}})
	body, _ := json.Marshal(RenewalRequest{RequestID: d.PendingID, CSR: d.CSR})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, d.EnrollmentURL+"/v1/renew", bytes.NewReader(body))
	if err != nil {
		return ErrInput
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	var result EnrollmentResult
	if err == nil {
		err = readResponse(response, &result, 64<<10)
	}
	if err != nil || !bytes.Equal(result.CA, d.CA) || VerifyAgentMaterial(d.NodeID, result.Certificate, d.Key, d.CA, now) != nil {
		d.Failures++
		if d.Failures > 30 {
			d.Failures = 30
		}
		d.NextAttempt = now.Add(renewalDelay(d.Failures))
		persist, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer stop()
		if i.save(persist, d) != nil {
			return ErrStore
		}
		i.mutex.Lock()
		i.disk = d
		i.mutex.Unlock()
		return ErrAdmission
	}
	pair, err := tls.X509KeyPair(result.Certificate, d.Key)
	if err != nil {
		return ErrPKI
	}
	pair.Leaf, _ = x509.ParseCertificate(pair.Certificate[0])
	if !pair.Leaf.NotAfter.After(current.Leaf.NotAfter) {
		return ErrPKI
	}
	d.Certificate = result.Certificate
	d.PendingID = ""
	d.PendingAt = time.Time{}
	d.NextAttempt = time.Time{}
	d.LastSuccess = now
	d.Failures = 0
	if i.save(ctx, d) != nil {
		return ErrStore
	}
	i.mutex.Lock()
	i.disk = d
	i.pair = &pair
	i.mutex.Unlock()
	return nil
}
func (i *AgentIdentity) Maintain(ctx context.Context, report func(CertificateStatus)) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		_ = i.Renew(ctx, time.Now())
		if report != nil {
			report(i.Status(time.Now()))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
