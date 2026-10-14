package onboarding

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/url"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

type authority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func testCA(t *testing.T, name string) authority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name},
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
		NotBefore: testNow.Add(-time.Hour), NotAfter: testNow.AddDate(2, 0, 0)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return authority{cert, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}
func leafMaterial(t *testing.T, issuer, peer authority, template x509.Certificate) ImportedTLS {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template.SerialNumber = big.NewInt(2)
	if template.NotBefore.IsZero() {
		template.NotBefore = testNow.Add(-time.Hour)
	}
	if template.NotAfter.IsZero() {
		template.NotAfter = testNow.AddDate(1, 0, 0)
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, issuer.cert, &key.PublicKey, issuer.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewImportedTLS(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), peer.pem, issuer.pem)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func agentMaterial(t *testing.T) ImportedTLS {
	t.Helper()
	uri, _ := url.Parse("urn:hlcp:node:debian-01")
	return leafMaterial(t, testCA(t, "client issuer"), testCA(t, "server trust"), x509.Certificate{
		URIs: []*url.URL{uri}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
}

func TestSeparateIssuerAndPeerTrust(t *testing.T) {
	material := agentMaterial(t)
	if _, err := material.Validate(Identity{Role: "agent", NodeID: "debian-01"}, testNow); err != nil {
		t.Fatal(err)
	}
	material.issuerTrust = bytes.Clone(material.peerTrust)
	if _, err := material.Validate(Identity{Role: "agent", NodeID: "debian-01"}, testNow); err == nil {
		t.Fatal("wrong issuer accepted")
	}
}

func TestIdentityValidityKeyAndBundles(t *testing.T) {
	identity := Identity{Role: "agent", NodeID: "debian-01"}
	for _, change := range []func(*ImportedTLS){
		func(m *ImportedTLS) { m.key = agentMaterial(t).key },
		func(m *ImportedTLS) { m.key = append(m.key, []byte("SECRET trailing key data")...) },
		func(m *ImportedTLS) { m.peerTrust = append(m.peerTrust, []byte("SECRET prose")...) },
		func(m *ImportedTLS) { m.issuerTrust = append(m.issuerTrust, m.key...) },
		func(m *ImportedTLS) { m.certificate = bytes.Repeat([]byte("x"), (1<<20)+1) },
		func(m *ImportedTLS) { m.peerTrust = m.certificate },
	} {
		material := agentMaterial(t)
		change(&material)
		if _, err := material.Validate(identity, testNow); err == nil {
			t.Fatal("invalid material accepted")
		}
	}
	material := agentMaterial(t)
	for _, row := range []struct {
		identity Identity
		now      time.Time
	}{
		{Identity{Role: "agent", NodeID: "wrong"}, testNow}, {Identity{Role: "controller", ServerName: "controller.test"}, testNow},
		{identity, testNow.Add(-2 * time.Hour)}, {identity, testNow.AddDate(1, 0, 0)},
	} {
		if _, err := material.Validate(row.identity, row.now); err == nil {
			t.Fatal("identity/time accepted")
		}
	}
	uri, _ := url.Parse("urn:hlcp:node:debian-01")
	for _, spec := range []x509.Certificate{
		{URIs: []*url.URL{uri}}, {URIs: []*url.URL{uri}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}},
		{URIs: []*url.URL{uri}, IsCA: true, BasicConstraintsValid: true, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}},
	} {
		material := leafMaterial(t, testCA(t, "issuer"), testCA(t, "peer"), spec)
		if _, err := material.Validate(identity, testNow); err == nil {
			t.Fatal("implicit EKU/CA leaf accepted")
		}
	}
}

func TestControllerIdentityAndRedactedClone(t *testing.T) {
	material := leafMaterial(t, testCA(t, "server issuer"), testCA(t, "client trust"), x509.Certificate{
		DNSNames: []string{"controller.test"}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	if _, err := material.Validate(Identity{Role: "controller", ServerName: "controller.test"}, testNow); err != nil {
		t.Fatal(err)
	}
	if _, err := material.Validate(Identity{Role: "controller", ServerName: "wrong.test"}, testNow); err == nil {
		t.Fatal("hostname accepted")
	}
	key := bytes.Clone(material.key)
	copy, err := NewImportedTLS(material.certificate, key, material.peerTrust, material.issuerTrust)
	if err != nil {
		t.Fatal(err)
	}
	key[0] = 0
	if _, err := copy.Validate(Identity{Role: "controller", ServerName: "controller.test"}, testNow); err != nil {
		t.Fatal("input not cloned")
	}
	data, _ := json.Marshal(copy)
	for _, report := range []string{string(data), fmt.Sprintf("%v %+v %#v", copy, &copy, copy)} {
		if bytes.Contains([]byte(report), []byte("BEGIN")) || bytes.Contains([]byte(report), []byte("controller.test")) {
			t.Fatal("material leaked")
		}
	}
}

func TestCAKeyCannotBeImportedAsDeploymentKey(t *testing.T) {
	ca := testCA(t, "issuer")
	uri, _ := url.Parse("urn:hlcp:node:debian-01")
	leaf := &x509.Certificate{SerialNumber: big.NewInt(3), NotBefore: testNow.Add(-time.Hour), NotAfter: testNow.AddDate(1, 0, 0),
		URIs: []*url.URL{uri}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca.cert, &ca.key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(ca.key)
	if err != nil {
		t.Fatal(err)
	}
	material, err := NewImportedTLS(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), testCA(t, "peer").pem, ca.pem)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := material.Validate(Identity{Role: "agent", NodeID: "debian-01"}, testNow); err == nil {
		t.Fatal("CA key reuse accepted")
	}
}

func TestHostSyntax(t *testing.T) {
	for _, good := range []string{"192.168.0.15", "::1", "controller.test", "controller.test."} {
		if !validHost(good) {
			t.Fatal(good)
		}
	}
	for _, bad := range []string{"", "https://controller.test", "*.test", "bad name", "bad\nname", "-bad", "bad..test", "bad%zone"} {
		if validHost(bad) {
			t.Fatal(bad)
		}
	}
}

func TestIntermediateChainAndPathLength(t *testing.T) {
	root := testCA(t, "root")
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "issuer"},
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, MaxPathLenZero: true,
		NotBefore: testNow.Add(-time.Hour), NotAfter: testNow.AddDate(1, 6, 0)}
	der, err := x509.CreateCertificate(rand.Reader, template, root.cert, &key.PublicKey, root.key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	issuer := authority{cert, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
	uri, _ := url.Parse("urn:hlcp:node:debian-01")
	material := leafMaterial(t, issuer, testCA(t, "peer"), x509.Certificate{URIs: []*url.URL{uri}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	material.certificate = append(material.certificate, issuer.pem...)
	material.issuerTrust = root.pem
	identity := Identity{Role: "agent", NodeID: "debian-01"}
	if _, err := material.Validate(identity, testNow); err != nil {
		t.Fatal(err)
	}
	limited := *root.cert
	limited.MaxPathLen = 0
	limited.MaxPathLenZero = true
	limitedDER, err := x509.CreateCertificate(rand.Reader, &limited, &limited, &root.key.PublicKey, root.key)
	if err != nil {
		t.Fatal(err)
	}
	material.issuerTrust = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: limitedDER})
	if _, err := material.Validate(identity, testNow); err == nil {
		t.Fatal("pathlen restriction ignored")
	}
}
