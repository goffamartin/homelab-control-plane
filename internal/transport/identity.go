package transport

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"slices"
	"strings"
)

const nodeIdentityPrefix = "urn:hlcp:node:"

// AgentNodeID accepts exactly one HLCP identity from a verified leaf certificate.
// Common Name and DNS names are deliberately not identity fallbacks.
func AgentNodeID(certificate *x509.Certificate) (string, error) {
	if certificate == nil || !slices.Contains(certificate.ExtKeyUsage, x509.ExtKeyUsageClientAuth) {
		return "", errors.New("hlcp transport: explicit client authentication EKU is required")
	}
	var nodeID string
	for _, uri := range certificate.URIs {
		if !strings.HasPrefix(uri.String(), nodeIdentityPrefix) {
			continue
		}
		if nodeID != "" {
			return "", errors.New("hlcp transport: ambiguous agent identity")
		}
		candidate := strings.TrimPrefix(uri.String(), nodeIdentityPrefix)
		if len(candidate) == 0 || len(candidate) > 128 {
			return "", errors.New("hlcp transport: invalid agent identity")
		}
		for _, character := range candidate {
			if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune("._-:", character) {
				continue
			}
			return "", errors.New("hlcp transport: invalid agent identity")
		}
		nodeID = candidate
	}
	if nodeID == "" {
		return "", errors.New("hlcp transport: agent identity URI SAN is required")
	}
	return nodeID, nil
}

func verifiedPeer(state tls.ConnectionState) (*x509.Certificate, error) {
	if state.NegotiatedProtocol != ALPN {
		return nil, errors.New("hlcp transport: hlcp/1 ALPN is required")
	}
	if len(state.VerifiedChains) == 0 || len(state.VerifiedChains[0]) == 0 {
		return nil, errors.New("hlcp transport: verified peer certificate is required")
	}
	return state.VerifiedChains[0][0], nil
}

func verifyAgent(state tls.ConnectionState) error {
	certificate, err := verifiedPeer(state)
	if err != nil {
		return err
	}
	_, err = AgentNodeID(certificate)
	return err
}

func verifyController(state tls.ConnectionState) error {
	certificate, err := verifiedPeer(state)
	if err != nil {
		return err
	}
	if !slices.Contains(certificate.ExtKeyUsage, x509.ExtKeyUsageServerAuth) {
		return errors.New("hlcp transport: explicit server authentication EKU is required")
	}
	return nil
}
