package agent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"hlcp/internal/transport"
)

// All keys are disposable and exist only in the test's private temp directory.
func e2ePKI(t *testing.T) (transport.TLSFiles, transport.TLSFiles) {
	t.Helper()
	directory := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "HLCP E2E CA"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(directory, "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0600); err != nil {
		t.Fatal(err)
	}
	leaf := func(name string, serial int64, usage x509.ExtKeyUsage) transport.TLSFiles {
		leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{SerialNumber: big.NewInt(serial), DNSNames: []string{name},
			NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
		if usage == x509.ExtKeyUsageClientAuth {
			identity, _ := url.Parse("urn:hlcp:node:debian-01")
			template.URIs = []*url.URL{identity}
		}
		der, err := x509.CreateCertificate(rand.Reader, template, ca, &leafKey.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
		if err != nil {
			t.Fatal(err)
		}
		files := transport.TLSFiles{CertificateFile: filepath.Join(directory, name+".pem"), PrivateKeyFile: filepath.Join(directory, name+".key"), CAFile: caFile, ServerName: "controller.test"}
		if err := os.WriteFile(files.CertificateFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(files.PrivateKeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
			t.Fatal(err)
		}
		return files
	}
	return leaf("controller.test", 2, x509.ExtKeyUsageServerAuth), leaf("agent.test", 3, x509.ExtKeyUsageClientAuth)
}
