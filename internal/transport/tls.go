// Package transport builds the authenticated TLS layer below HLCP framing.
package transport

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
)

const ALPN = "hlcp/1"

var ErrEmptyTrustBundle = errors.New("hlcp transport: trust bundle contains no certificates")

// TLSFiles identify a leaf certificate/key pair and the CA bundle used to
// validate the peer. The files are deployment secrets and must not be stored
// in the source repository.
type TLSFiles struct {
	CertificateFile string
	PrivateKeyFile  string
	CAFile          string
	ServerName      string // Required by clients; ignored by servers.
}

// ServerTLSConfig creates a controller configuration that requires and
// validates a client certificate from every agent.
func ServerTLSConfig(files TLSFiles) (*tls.Config, error) {
	certificate, err := loadCertificate(files)
	if err != nil {
		return nil, err
	}
	clientCAs, err := loadCertPool(files.CAFile)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		MinVersion:       tls.VersionTLS13,
		Certificates:     []tls.Certificate{certificate},
		ClientAuth:       tls.RequireAndVerifyClientCert,
		ClientCAs:        clientCAs,
		NextProtos:       []string{ALPN},
		VerifyConnection: verifyAgent,
	}, nil
}

// ClientTLSConfig creates an agent configuration that presents its own leaf
// certificate and validates the controller certificate and its DNS name.
func ClientTLSConfig(files TLSFiles) (*tls.Config, error) {
	if files.ServerName == "" {
		return nil, errors.New("hlcp transport: client server name is required")
	}
	certificate, err := loadCertificate(files)
	if err != nil {
		return nil, err
	}
	rootCAs, err := loadCertPool(files.CAFile)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		MinVersion:       tls.VersionTLS13,
		Certificates:     []tls.Certificate{certificate},
		RootCAs:          rootCAs,
		ServerName:       files.ServerName,
		NextProtos:       []string{ALPN},
		VerifyConnection: verifyController,
	}, nil
}

func loadCertificate(files TLSFiles) (tls.Certificate, error) {
	if files.CertificateFile == "" || files.PrivateKeyFile == "" {
		return tls.Certificate{}, errors.New("hlcp transport: certificate and private key files are required")
	}
	certificate, err := tls.LoadX509KeyPair(files.CertificateFile, files.PrivateKeyFile)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("load TLS certificate: %w", err)
	}
	return certificate, nil
}

func loadCertPool(path string) (*x509.CertPool, error) {
	if path == "" {
		return nil, errors.New("hlcp transport: CA file is required")
	}
	pemData, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read CA bundle: %w", err)
	}
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(pemData) {
		return nil, fmt.Errorf("%w: %s", ErrEmptyTrustBundle, path)
	}
	return certPool, nil
}
