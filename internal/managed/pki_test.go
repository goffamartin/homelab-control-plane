package managed

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"fmt"
	"testing"
	"time"
)

func fixtureAuthority(t *testing.T) (*Authority, []byte, time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	a, b, err := NewAuthority("correct horse battery staple", now)
	if err != nil {
		t.Fatal(err)
	}
	return a, b, now
}
func TestManagedAuthorityBackupAndLeafs(t *testing.T) {
	a, backup, now := fixtureAuthority(t)
	root, issuer, key := a.Material()
	if VerifyRootBackup(backup, "correct horse battery staple", root) != nil {
		t.Fatal("backup verification")
	}
	if VerifyRootBackup(backup, "incorrect horse battery staple", root) == nil {
		t.Fatal("bad password")
	}
	if bytes.Contains(backup, []byte("PRIVATE KEY")) || bytes.Contains(backup, root) || bytes.Contains([]byte(fmt.Sprintf("%#v", a)), []byte("PRIVATE KEY")) {
		t.Fatal("private material exposed")
	}
	loaded, err := LoadAuthority(root, issuer, key, now)
	if err != nil || loaded.Fingerprint() != a.Fingerprint() {
		t.Fatal(err)
	}
	server, serverKey, err := a.Server("192.0.2.10", now)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(server, serverKey)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(root)
	intermediates := x509.NewCertPool()
	intermediates.AppendCertsFromPEM(issuer)
	leaf, _ := x509.ParseCertificate(pair.Certificate[0])
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: now, DNSName: "192.0.2.10", KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Fatal(err)
	}
	clientKey, csr, err := NewAgentRequest("node-01")
	if err != nil {
		t.Fatal(err)
	}
	client, err := loaded.IssueAgent("node-01", csr, now)
	if err != nil {
		t.Fatal(err)
	}
	cp, err := tls.X509KeyPair(client, clientKey)
	if err != nil {
		t.Fatal(err)
	}
	cl, _ := x509.ParseCertificate(cp.Certificate[0])
	if cl.IsCA || len(cl.URIs) != 1 || cl.URIs[0].String() != "urn:hlcp:node:node-01" || cl.NotAfter.Sub(now) != LeafLifetime {
		t.Fatal("leaf identity/lifetime")
	}
	if _, err := cl.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err == nil {
		t.Fatal("client gained server usage")
	}
	if a.root.MaxPathLen != 1 || !a.issuer.MaxPathLenZero || a.issuer.MaxPathLen != 0 {
		t.Fatal("CA hierarchy")
	}
}
func TestManagedPKIRejectsIdentityCAAndExpiredIssuer(t *testing.T) {
	a, _, now := fixtureAuthority(t)
	_, csr, _ := NewAgentRequest("node-01")
	if _, err := a.IssueAgent("other-node", csr, now); err == nil {
		t.Fatal("wrong identity")
	}
	if _, err := a.IssueAgent("node-01", csr, now.AddDate(2, 0, 0)); err == nil {
		t.Fatal("expired issuer")
	}
	if _, err := a.IssueAgent("node-01", append(csr, []byte("TRAILING")...), now); err == nil {
		t.Fatal("trailing CSR")
	}
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caRequest, _ := asn1.Marshal(struct{ IsCA bool }{true})
	der, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "node-01"}, ExtraExtensions: []pkix.Extension{{Id: []int{2, 5, 29, 19}, Critical: true, Value: caRequest}}}, k)
	if _, err := a.IssueAgent("node-01", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), now); err == nil {
		t.Fatal("CA request")
	}
	nearExpiry := a.issuer.NotAfter.Add(-48 * time.Hour)
	chain, err := a.IssueAgent("node-01", csr, nearExpiry)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := pem.Decode(chain)
	leaf, _ := x509.ParseCertificate(b.Bytes)
	if leaf.NotAfter.After(a.issuer.NotAfter) {
		t.Fatal("leaf outlives issuer")
	}
	if _, err := a.IssueAgent("node-01", csr, a.issuer.NotAfter.Add(-time.Hour)); err == nil {
		t.Fatal("unusable remaining validity")
	}
	root, issuer, key := a.Material()
	if _, err := LoadAuthority(root, issuer, key, now.AddDate(2, 0, 0)); err == nil {
		t.Fatal("loaded expired issuer")
	}
	if _, err := LoadAuthority(root, issuer, keyPEM(k), now); err == nil {
		t.Fatal("wrong key")
	}
	if _, _, err := NewAuthority("too short", now); err == nil {
		t.Fatal("weak backup password")
	}
}
