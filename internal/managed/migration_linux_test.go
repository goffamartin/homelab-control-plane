package managed

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"net/url"
	"os"
	"testing"
	"time"
)

func legacyFixture(t *testing.T) ([]byte, *x509.Certificate) {
	t.Helper()
	now := time.Now()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{Subject: pkix.Name{CommonName: "legacy root"}, NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(1, 0, 0), IsCA: true, BasicConstraintsValid: true, MaxPathLenZero: true, KeyUsage: x509.KeyUsageCertSign}
	root, err := createCertificate(template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	clientKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	uri, _ := url.Parse("urn:hlcp:node:legacy-01")
	leaf, err := createCertificate(&x509.Certificate{Subject: pkix.Name{CommonName: "legacy-01"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour), BasicConstraintsValid: true, URIs: []*url.URL{uri}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}, root, &clientKey.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return certPEM(root), leaf
}

func TestDynamicClientTrustRemovalAppliesToExistingTLSConfig(t *testing.T) {
	now := time.Now()
	current, _, err := NewAuthority("current backup password long enough", now)
	if err != nil {
		t.Fatal(err)
	}
	old, _, err := NewAuthority("legacy backup password long enough", now)
	if err != nil {
		t.Fatal(err)
	}
	key, csr, _ := NewAgentRequest("node-01")
	chain, err := current.IssueAgent("node-01", csr, now)
	if err != nil {
		t.Fatal(err)
	}
	result := EnrollmentResult{Certificate: chain, CA: current.RootPEM(), PeerCA: append(current.RootPEM(), old.RootPEM()...)}
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if CreateAgentIdentity(context.Background(), store, "node-01", "https://127.0.0.1:9445", key, csr, result) != nil {
		t.Fatal("identity")
	}
	identity, err := OpenAgentIdentity(store, "node-01")
	if err != nil {
		t.Fatal(err)
	}
	config, err := identity.TLSConfig("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	legacyChain, legacyKey, err := old.Server("127.0.0.1", now)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(legacyChain, legacyKey)
	if err != nil {
		t.Fatal(err)
	}
	var certs []*x509.Certificate
	for _, der := range pair.Certificate {
		c, e := x509.ParseCertificate(der)
		if e != nil {
			t.Fatal(e)
		}
		certs = append(certs, c)
	}
	state := tls.ConnectionState{NegotiatedProtocol: "hlcp/1", PeerCertificates: certs, VerifiedChains: [][]*x509.Certificate{certs}}
	if config.VerifyConnection(state) != nil {
		t.Fatal("legacy overlap refused")
	}
	identity.mutex.Lock()
	identity.disk.PeerCA = nil
	identity.mutex.Unlock()
	if config.VerifyConnection(state) == nil {
		t.Fatal("stale TLS config retained removed trust")
	}
}
func TestMigrationAdmissionGenerationAndFinish(t *testing.T) {
	oldRoot, oldLeaf := legacyFixture(t)
	_, impostor := legacyFixture(t)
	ctx := context.Background()
	config := RuntimeConfig{Version: 1, Listen: "127.0.0.1:19443", ServerName: "127.0.0.1", EnrollmentListen: "127.0.0.1:19445", EnrollmentURL: "https://127.0.0.1:19445", LegacyServer: true, LegacyTrust: string(oldRoot)}
	seed, err := NewMigrationSeed(config, "migration backup password long enough", []string{"legacy-01"}, time.Now())
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
	if seed.Provision(ctx, store) != nil {
		t.Fatal("provision")
	}
	a, err := LoadStoredAuthority(store, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := OpenRegistry(store, a)
	if err != nil {
		t.Fatal(err)
	}
	if registry.ConfigureLegacy(oldRoot) != nil {
		t.Fatal("legacy trust")
	}
	if !registry.Allows(oldLeaf) || registry.Allows(impostor) {
		t.Fatal("legacy trust admission failed")
	}
	if FinishMigration(ctx, store) == nil {
		t.Fatal("finished while legacy node active")
	}
	invitation, err := registry.CreateInvitation(ctx, "legacy-01", config.EnrollmentURL, true)
	if err != nil {
		t.Fatal(err)
	}
	if registry.Allows(oldLeaf) {
		t.Fatal("old generation remained admitted")
	}
	if FinishMigration(ctx, store) == nil {
		t.Fatal("finished with pending join")
	}
	key, csr, _ := NewAgentRequest("legacy-01")
	chain, err := registry.Enroll(ctx, EnrollmentRequest{NodeID: "legacy-01", Secret: invitation.Secret, RequestID: "0123456789abcdef0123456789abcdef", CSR: csr})
	if err != nil || VerifyAgentMaterial("legacy-01", chain, key, a.RootPEM(), time.Now()) != nil {
		t.Fatal(err)
	}
	before, _ := store.Read(registryFile)
	if FinishMigration(ctx, store) != nil {
		t.Fatal("finish")
	}
	after, _ := store.Read(registryFile)
	if !bytes.Equal(before, after) {
		t.Fatal("registry changed on trust removal")
	}
	data, _ := store.Read("runtime.json")
	var actual RuntimeConfig
	if json.Unmarshal(data, &actual) != nil || actual.LegacyServer || actual.LegacyTrust != "" {
		t.Fatal("legacy trust retained")
	}
}
func TestMigrationPeerTrustRefresh(t *testing.T) {
	registry, service, server := enrollmentFixture(t)
	oldRoot, _ := legacyFixture(t)
	service.PeerCA = append(registry.authority.RootPEM(), oldRoot...)
	invitation, err := registry.CreateInvitation(context.Background(), "node-01", server.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	key, csr, _ := NewAgentRequest("node-01")
	result, err := EnrollAgent(context.Background(), invitation, "0123456789abcdef0123456789abcdef", key, csr)
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
	if CreateAgentIdentity(context.Background(), store, "node-01", server.URL, key, csr, result) != nil {
		t.Fatal("identity")
	}
	identity, err := OpenAgentIdentity(store, "node-01")
	if err != nil {
		t.Fatal(err)
	}
	// Stop the test HTTP server while changing immutable handler configuration.
	server.CloseClientConnections()
	service.PeerCA = nil
	if identity.RefreshTrust(context.Background()) != nil {
		t.Fatal("refresh")
	}
	data, _ := store.Read(identityFile)
	var disk identityDisk
	_ = json.Unmarshal(data, &disk)
	if len(disk.PeerCA) != 0 || !bytes.Equal(disk.Key, key) {
		t.Fatal("trust refresh changed key or retained legacy")
	}
	if registry.Revoke(context.Background(), "node-01") != nil {
		t.Fatal("revoke")
	}
	if identity.RefreshTrust(context.Background()) == nil {
		t.Fatal("revoked trust refresh accepted")
	}
}
