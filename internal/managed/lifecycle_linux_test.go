package managed

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"testing"
	"time"
)

func TestManagedAgentRenewalAndAtomicHotLoad(t *testing.T) {
	now := time.Now()
	epoch := now.Add(-35 * 24 * time.Hour)
	r, _, server := enrollmentFixtureAt(t, epoch)
	r.now = func() time.Time { return epoch }
	i, err := r.CreateInvitation(context.Background(), "node-01", server.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	key, csr, _ := NewAgentRequest(i.NodeID)
	result, err := EnrollAgent(context.Background(), i, "0123456789abcdef0123456789abcdef", key, csr)
	if err != nil {
		t.Fatal(err)
	}
	r.now = time.Now
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := CreateAgentIdentity(context.Background(), store, i.NodeID, i.URL, key, csr, result); err != nil {
		t.Fatal(err)
	}
	identity, err := OpenAgentIdentity(store, i.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	config, err := identity.TLSConfig("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	before, err := config.GetClientCertificate(&tls.CertificateRequestInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.Renew(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	after, err := config.GetClientCertificate(&tls.CertificateRequestInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before.Certificate[0], after.Certificate[0]) || !after.Leaf.NotAfter.After(before.Leaf.NotAfter) {
		t.Fatal("TLS snapshot did not advance")
	}
	if !r.Allows(before.Leaf) || !r.Allows(after.Leaf) {
		t.Fatal("valid overlap lost")
	}
	if err := r.Revoke(context.Background(), i.NodeID); err != nil {
		t.Fatal(err)
	}
	if r.Allows(after.Leaf) {
		t.Fatal("revocation bypass")
	}
	if _, err := r.Renew(context.Background(), after.Leaf, RenewalRequest{RequestID: "1123456789abcdef0123456789abcdef", CSR: csr}); err == nil {
		t.Fatal("revoked renewal")
	}
	status := identity.Status(now)
	if status.Failures != 0 || status.Warning != "" || !status.LastSuccess.Equal(now) {
		t.Fatal("renewal diagnostics")
	}
}
func TestRenewalFailureRetainsIdentityAndPendingRequest(t *testing.T) {
	now := time.Now()
	epoch := now.Add(-35 * 24 * time.Hour)
	r, _, server := enrollmentFixtureAt(t, epoch)
	r.now = func() time.Time { return epoch }
	i, err := r.CreateInvitation(context.Background(), "node-01", server.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	key, csr, _ := NewAgentRequest(i.NodeID)
	result, err := EnrollAgent(context.Background(), i, "0123456789abcdef0123456789abcdef", key, csr)
	if err != nil {
		t.Fatal(err)
	}
	server.Close()
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if CreateAgentIdentity(context.Background(), store, i.NodeID, i.URL, key, csr, result) != nil {
		t.Fatal("create")
	}
	identity, err := OpenAgentIdentity(store, i.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.Renew(context.Background(), now); err == nil {
		t.Fatal("outage hidden")
	}
	if identity.disk.PendingID == "" || identity.disk.Failures != 1 || !bytes.Equal(identity.disk.Certificate, result.Certificate) || !identity.disk.NextAttempt.After(now) {
		t.Fatal("failed renewal lost state")
	}
	reopened, err := OpenAgentIdentity(store, i.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.disk.PendingID != identity.disk.PendingID {
		t.Fatal("lost durable renewal identity")
	}
}
func TestControllerCertificateRenewalHotLoad(t *testing.T) {
	now := time.Now()
	epoch := now.Add(-35 * 24 * time.Hour)
	a, _, err := NewAuthority("test backup password sufficiently long", epoch)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	chain, key, err := a.Server("controller.test", epoch)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(serverIdentityDisk{Version: 1, Host: "controller.test", Certificate: chain, Key: key, LastSuccess: epoch})
	if store.Create(context.Background(), serverIdentityFile, data) != nil {
		t.Fatal("create")
	}
	i, err := OpenServerIdentity(store, a, "controller.test")
	if err != nil {
		t.Fatal(err)
	}
	config, err := i.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	before, _ := config.GetCertificate(&tls.ClientHelloInfo{})
	if i.Renew(context.Background(), now) != nil {
		t.Fatal("renew")
	}
	after, _ := config.GetCertificate(&tls.ClientHelloInfo{})
	if bytes.Equal(before.Certificate[0], after.Certificate[0]) || !after.Leaf.NotAfter.After(before.Leaf.NotAfter) {
		t.Fatal("server hot load")
	}
	if a.Store(context.Background(), store) != nil {
		t.Fatal("authority save")
	}
	loaded, err := LoadStoredAuthority(store, now)
	if err != nil || loaded.Fingerprint() != a.Fingerprint() {
		t.Fatal(err)
	}
}
func TestCredentialSerialPreventsOldGenerationReuseOfSameKey(t *testing.T) {
	r, _, _ := registryFixture(t)
	i, err := r.CreateInvitation(context.Background(), "node-01", "https://controller.test:9445", false)
	if err != nil {
		t.Fatal(err)
	}
	_, csr, _ := NewAgentRequest(i.NodeID)
	first, err := r.Enroll(context.Background(), EnrollmentRequest{NodeID: i.NodeID, Secret: i.Secret, RequestID: "0123456789abcdef0123456789abcdef", CSR: csr})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := pem.Decode(first)
	old, _ := x509.ParseCertificate(b.Bytes)
	i, err = r.CreateInvitation(context.Background(), i.NodeID, i.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Enroll(context.Background(), EnrollmentRequest{NodeID: i.NodeID, Secret: i.Secret, RequestID: "1123456789abcdef0123456789abcdef", CSR: csr}); err != nil {
		t.Fatal(err)
	}
	if r.Allows(old) {
		t.Fatal("old generation admitted via reused key")
	}
}
