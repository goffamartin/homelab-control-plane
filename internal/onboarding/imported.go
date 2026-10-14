// Package onboarding prepares validated local setup plans, never remote actions.
package onboarding

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"hlcp/internal/setup"
	"hlcp/internal/transport"
	"strings"
	"time"
)

var ErrImportedTLS = errors.New("onboarding: imported TLS material is invalid, expired, mismatched or untrusted")

// ImportedTLS keeps key material out of ordinary reports/debug formatting.
// The issuer bundle validates our own leaf; peer trust validates the other side.
type ImportedTLS struct{ certificate, key, peerTrust, issuerTrust []byte }

func NewImportedTLS(certificate, key, peerTrust, issuerTrust []byte) (ImportedTLS, error) {
	for _, data := range [][]byte{certificate, key, peerTrust, issuerTrust} {
		if len(data) == 0 || len(data) > setup.MaximumFileBytes {
			return ImportedTLS{}, ErrImportedTLS
		}
	}
	return ImportedTLS{bytes.Clone(certificate), bytes.Clone(key), bytes.Clone(peerTrust), bytes.Clone(issuerTrust)}, nil
}
func (ImportedTLS) MarshalJSON() ([]byte, error) { return []byte(`{"imported_tls":"redacted"}`), nil }
func (material ImportedTLS) String() string      { data, _ := json.Marshal(material); return string(data) }
func (material ImportedTLS) GoString() string    { return material.String() }

type Identity struct{ Role, NodeID, ServerName string }
type TLSMetadata struct {
	Role    string    `json:"role"`
	Expires time.Time `json:"expires"`
}

func (material ImportedTLS) Validate(identity Identity, now time.Time) (TLSMetadata, error) {
	for _, data := range [][]byte{material.certificate, material.key, material.peerTrust, material.issuerTrust} {
		if len(data) == 0 || len(data) > setup.MaximumFileBytes {
			return TLSMetadata{}, ErrImportedTLS
		}
	}
	certificates, err := parseCertificates(material.certificate)
	if err != nil {
		return TLSMetadata{}, ErrImportedTLS
	}
	if _, err := tls.X509KeyPair(material.certificate, material.key); err != nil {
		return TLSMetadata{}, ErrImportedTLS
	}
	if err := validatePrivateKey(material.key); err != nil {
		return TLSMetadata{}, ErrImportedTLS
	}
	leaf := certificates[0]
	if err := validateLeaf(leaf, identity, now); err != nil {
		return TLSMetadata{}, ErrImportedTLS
	}
	if _, err := validCA(material.peerTrust, now); err != nil {
		return TLSMetadata{}, ErrImportedTLS
	}
	roots, err := validCA(material.issuerTrust, now)
	if err != nil {
		return TLSMetadata{}, ErrImportedTLS
	}
	// A deployment leaf must not reuse the private key of a supplied CA.
	// Matching the leaf key alone would otherwise accidentally import a CA key.
	for _, bundle := range [][]byte{material.peerTrust, material.issuerTrust} {
		cas, _ := parseCertificates(bundle)
		for _, ca := range cas {
			if bytes.Equal(leaf.RawSubjectPublicKeyInfo, ca.RawSubjectPublicKeyInfo) {
				return TLSMetadata{}, ErrImportedTLS
			}
		}
	}
	intermediates := x509.NewCertPool()
	for _, cert := range certificates[1:] {
		if !cert.IsCA {
			return TLSMetadata{}, ErrImportedTLS
		}
		if bytes.Equal(leaf.RawSubjectPublicKeyInfo, cert.RawSubjectPublicKeyInfo) {
			return TLSMetadata{}, ErrImportedTLS
		}
		intermediates.AddCert(cert)
	}
	usage := x509.ExtKeyUsageClientAuth
	dns := ""
	if identity.Role == "controller" {
		usage = x509.ExtKeyUsageServerAuth
		dns = identity.ServerName
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: now, DNSName: dns, KeyUsages: []x509.ExtKeyUsage{usage}}); err != nil {
		return TLSMetadata{}, ErrImportedTLS
	}
	return TLSMetadata{Role: identity.Role, Expires: leaf.NotAfter}, nil
}

func validateLeaf(leaf *x509.Certificate, identity Identity, now time.Time) error {
	if leaf.IsCA || now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return ErrImportedTLS
	}
	switch identity.Role {
	case "agent":
		node, err := transport.AgentNodeID(leaf)
		if err != nil || node != identity.NodeID || len(leaf.URIs) != 1 {
			return ErrImportedTLS
		}
	case "controller":
		if identity.ServerName == "" || leaf.VerifyHostname(identity.ServerName) != nil {
			return ErrImportedTLS
		}
		explicit := false
		for _, eku := range leaf.ExtKeyUsage {
			if eku == x509.ExtKeyUsageServerAuth {
				explicit = true
			}
		}
		if !explicit {
			return ErrImportedTLS
		}
	default:
		return ErrImportedTLS
	}
	return nil
}

func parseCertificates(data []byte) ([]*x509.Certificate, error) {
	if len(data) == 0 || len(data) > setup.MaximumFileBytes {
		return nil, ErrImportedTLS
	}
	var result []*x509.Certificate
	for len(bytes.TrimSpace(data)) > 0 {
		data = bytes.TrimSpace(data)
		if !bytes.HasPrefix(data, []byte("-----BEGIN CERTIFICATE-----")) || len(result) >= 16 {
			return nil, ErrImportedTLS
		}
		block, rest := pem.Decode(data)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, ErrImportedTLS
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, ErrImportedTLS
		}
		result = append(result, cert)
		data = rest
	}
	if len(result) == 0 {
		return nil, ErrImportedTLS
	}
	return result, nil
}

func validCA(data []byte, now time.Time) (*x509.CertPool, error) {
	certificates, err := parseCertificates(data)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	for _, cert := range certificates {
		if !cert.IsCA || !cert.BasicConstraintsValid || now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) ||
			(cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageCertSign == 0) {
			return nil, ErrImportedTLS
		}
		pool.AddCert(cert)
	}
	return pool, nil
}

func validatePrivateKey(data []byte) error {
	if len(data) == 0 || len(data) > setup.MaximumFileBytes {
		return ErrImportedTLS
	}
	block, rest := pem.Decode(data)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 || len(block.Headers) != 0 ||
		!strings.HasPrefix(string(bytes.TrimSpace(data)), "-----BEGIN "+block.Type+"-----") {
		return ErrImportedTLS
	}
	var err error
	switch block.Type {
	case "PRIVATE KEY":
		_, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		_, err = x509.ParseECPrivateKey(block.Bytes)
	case "RSA PRIVATE KEY":
		_, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	default:
		return ErrImportedTLS
	}
	if err != nil {
		return ErrImportedTLS
	}
	return nil
}
