package transport

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
)

// Dynamic configs retain the same explicit role/ALPN checks as file snapshots.
func ClientTLSConfigDynamic(ca []byte, serverName string, get func(*tls.CertificateRequestInfo) (*tls.Certificate, error)) (*tls.Config, error) {
	pool := x509.NewCertPool()
	if serverName == "" || get == nil || !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("invalid dynamic client TLS configuration")
	}
	config := clientTLSConfig(tls.Certificate{}, pool, serverName)
	config.Certificates = nil
	config.GetClientCertificate = get
	return config, nil
}
func ServerTLSConfigDynamic(ca []byte, get func(*tls.ClientHelloInfo) (*tls.Certificate, error)) (*tls.Config, error) {
	pool := x509.NewCertPool()
	if get == nil || !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("invalid dynamic server TLS configuration")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, GetCertificate: get, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert, NextProtos: []string{ALPN}, VerifyConnection: verifyAgent}, nil
}
