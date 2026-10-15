package managed

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"time"
)

type TrustResult struct {
	CA     []byte `json:"ca"`
	PeerCA []byte `json:"peer_ca,omitempty"`
}

func ValidateEnrollmentTrust(root, peer []byte, now time.Time) error {
	if ValidatePeerTrust(root, now) != nil {
		return ErrPKI
	}
	if len(peer) == 0 {
		return nil
	}
	// An expired additional anchor does not invalidate the managed root. Each
	// actual connection still enforces its selected chain's validity dates.
	if validateTrust(peer, now, false) != nil || !bytes.Contains(peer, bytes.TrimSpace(root)) {
		return ErrPKI
	}
	return nil
}

// RefreshTrust is an explicit stopped-agent operation. A fresh runtime then
// builds its verified TLS trust pool from the atomically updated identity.
func (i *AgentIdentity) RefreshTrust(ctx context.Context) error {
	i.renewMutex.Lock()
	defer i.renewMutex.Unlock()
	i.mutex.RLock()
	d := i.disk
	pair := i.pair
	i.mutex.RUnlock()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(d.CA) {
		return ErrPKI
	}
	client := enrollmentHTTPClient(&tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{*pair}})
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, d.EnrollmentURL+"/v1/trust", nil)
	if err != nil {
		return ErrInput
	}
	response, err := client.Do(request)
	if err != nil {
		return ErrAdmission
	}
	var result TrustResult
	if readResponse(response, &result, 128<<10) != nil || !bytes.Equal(result.CA, d.CA) || ValidateEnrollmentTrust(d.CA, result.PeerCA, time.Now()) != nil {
		return ErrAdmission
	}
	d.PeerCA = result.PeerCA
	if i.save(ctx, d) != nil {
		return ErrStore
	}
	i.mutex.Lock()
	i.disk = d
	i.mutex.Unlock()
	return nil
}
