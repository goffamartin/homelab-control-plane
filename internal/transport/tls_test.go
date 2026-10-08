package transport

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMutualTLSHandshake(t *testing.T) {
	serverFiles, clientFiles := writeTestCertificates(t)
	serverConfig, err := ServerTLSConfig(serverFiles)
	if err != nil {
		t.Fatalf("ServerTLSConfig() error = %v", err)
	}
	clientConfig, err := ClientTLSConfig(clientFiles)
	if err != nil {
		t.Fatalf("ClientTLSConfig() error = %v", err)
	}

	serverConnection, clientConnection := net.Pipe()
	defer serverConnection.Close()
	defer clientConnection.Close()

	server := tls.Server(serverConnection, serverConfig)
	client := tls.Client(clientConnection, clientConfig)
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Handshake() }()

	if err := client.Handshake(); err != nil {
		t.Fatalf("client handshake error = %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server handshake error = %v", err)
	}
	if negotiated := client.ConnectionState().NegotiatedProtocol; negotiated != ALPN {
		t.Fatalf("negotiated ALPN = %q, want %q", negotiated, ALPN)
	}
}

func TestClientTLSConfigRequiresServerName(t *testing.T) {
	_, err := ClientTLSConfig(TLSFiles{})
	if err == nil {
		t.Fatal("ClientTLSConfig() error = nil, want a missing server name error")
	}
}

func writeTestCertificates(t *testing.T) (TLSFiles, TLSFiles) {
	t.Helper()
	directory := t.TempDir()

	caCertificate, caKey := createCA(t)
	caPEM := encodeCertificate(t, caCertificate.Raw)
	caFile := filepath.Join(directory, "ca.pem")
	if err := os.WriteFile(caFile, caPEM, 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}

	serverCertificate, serverKey := createLeaf(t, caCertificate, caKey, "controller.test", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	clientCertificate, clientKey := createLeaf(t, caCertificate, caKey, "agent.test", []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})

	serverCertFile, serverKeyFile := writeLeaf(t, directory, "server", serverCertificate, serverKey)
	clientCertFile, clientKeyFile := writeLeaf(t, directory, "client", clientCertificate, clientKey)

	return TLSFiles{CertificateFile: serverCertFile, PrivateKeyFile: serverKeyFile, CAFile: caFile}, TLSFiles{
		CertificateFile: clientCertFile,
		PrivateKeyFile:  clientKeyFile,
		CAFile:          caFile,
		ServerName:      "controller.test",
	}
}

func createCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "HLCP test CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	encoded, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}
	certificate, err := x509.ParseCertificate(encoded)
	if err != nil {
		t.Fatalf("parse CA certificate: %v", err)
	}
	return certificate, key
}

func createLeaf(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, commonName string, usages []x509.ExtKeyUsage) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: commonName},
		DNSNames:     []string{commonName},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  usages,
	}
	encoded, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf certificate: %v", err)
	}
	certificate, err := x509.ParseCertificate(encoded)
	if err != nil {
		t.Fatalf("parse leaf certificate: %v", err)
	}
	return certificate, key
}

func writeLeaf(t *testing.T, directory, name string, certificate *x509.Certificate, key *ecdsa.PrivateKey) (string, string) {
	t.Helper()
	certificateFile := filepath.Join(directory, name+".crt")
	keyFile := filepath.Join(directory, name+".key")
	if err := os.WriteFile(certificateFile, encodeCertificate(t, certificate.Raw), 0o600); err != nil {
		t.Fatalf("write %s certificate: %v", name, err)
	}
	encodedKey, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal %s key: %v", name, err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey}), 0o600); err != nil {
		t.Fatalf("write %s key: %v", name, err)
	}
	return certificateFile, keyFile
}

func encodeCertificate(t *testing.T, raw []byte) []byte {
	t.Helper()
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})
}
