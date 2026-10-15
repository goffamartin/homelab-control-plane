package managed

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"hlcp/internal/transport"
	"time"
)

type RenewalRequest struct {
	RequestID string `json:"request_id"`
	CSR       []byte `json:"csr"`
}

func RenewalTime(cert *x509.Certificate) time.Time {
	age := RenewalAge
	if third := cert.NotAfter.Sub(cert.NotBefore) / 3; third < age {
		age = third
	}
	return cert.NotBefore.Add(age)
}
func nodeAllows(n *node, cert *x509.Certificate, now time.Time) bool {
	if !n.Active || n.KeyHash != sha256.Sum256(cert.RawSubjectPublicKeyInfo) || now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
		return false
	}
	for _, c := range n.Credentials {
		if c.Serial == cert.SerialNumber.Text(16) && now.Before(c.Expires) {
			return true
		}
	}
	return false
}
func (r *Registry) Renew(ctx context.Context, cert *x509.Certificate, request RenewalRequest) ([]byte, error) {
	if cert == nil || len(request.RequestID) != 32 || len(request.CSR) > 16<<10 {
		return nil, ErrAdmission
	}
	if _, err := hex.DecodeString(request.RequestID); err != nil {
		return nil, ErrAdmission
	}
	id, err := transport.AgentNodeID(cert)
	if err != nil {
		return nil, ErrAdmission
	}
	var issued []byte
	hash := sha256.Sum256(request.CSR)
	err = r.update(ctx, func(state *registryState) error {
		for index := range state.Nodes {
			n := &state.Nodes[index]
			if n.ID != id {
				continue
			}
			if !nodeAllows(n, cert, r.now()) {
				return ErrAdmission
			}
			if n.RenewalID == request.RequestID {
				if n.RenewalCSR != hash || r.now().Sub(n.RenewedAt) > EnrollmentRetryWindow {
					return ErrAdmission
				}
				issued = append([]byte(nil), n.Certificate...)
				return nil
			}
			if r.now().Before(RenewalTime(cert)) || (!n.RenewedAt.IsZero() && r.now().Sub(n.RenewedAt) < time.Hour) {
				return ErrAdmission
			}
			chain, err := r.authority.IssueAgent(id, request.CSR, r.now())
			if err != nil {
				return ErrAdmission
			}
			b, _ := pem.Decode(chain)
			next, _ := x509.ParseCertificate(b.Bytes)
			if !next.NotAfter.After(cert.NotAfter) {
				return ErrAdmission
			}
			if sha256.Sum256(next.RawSubjectPublicKeyInfo) != n.KeyHash {
				return ErrAdmission
			}
			credentials := make([]credential, 0, 8)
			for _, c := range n.Credentials {
				if r.now().Before(c.Expires) {
					credentials = append(credentials, c)
				}
			}
			if len(credentials) >= 8 {
				return ErrAdmission
			}
			n.Credentials = append(credentials, credential{Serial: next.SerialNumber.Text(16), Expires: next.NotAfter})
			n.RenewalID = request.RequestID
			n.RenewalCSR = hash
			n.RenewedAt = r.now()
			n.Certificate = chain
			issued = append([]byte(nil), chain...)
			return nil
		}
		return ErrAdmission
	})
	return issued, err
}
