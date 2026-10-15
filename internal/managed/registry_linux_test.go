package managed

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func registryFixture(t *testing.T) (*Registry, *Store, *time.Time) {
	t.Helper()
	a, _, now := fixtureAuthority(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := InitializeRegistry(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	r, err := OpenRegistry(s, a)
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return now }
	return r, s, &now
}
func TestManagedPairingDurabilityRetryAndRevocation(t *testing.T) {
	r, s, now := registryFixture(t)
	i, err := r.CreateInvitation(context.Background(), "node-01", "https://controller.test:9445", false)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseInvitation(i.Encode())
	if err != nil || parsed.Secret != i.Secret {
		t.Fatal(err)
	}
	_, csr, _ := NewAgentRequest("node-01")
	request := EnrollmentRequest{NodeID: i.NodeID, Secret: i.Secret, RequestID: "0123456789abcdef0123456789abcdef", CSR: csr}
	chain, err := r.Enroll(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := pem.Decode(chain)
	leaf, _ := x509.ParseCertificate(b.Bytes)
	if !r.Allows(leaf) {
		t.Fatal("issued identity denied")
	}
	retry, err := r.Enroll(context.Background(), request)
	if err != nil || !bytes.Equal(chain, retry) {
		t.Fatal("lost response retry", err)
	}
	reopened, err := OpenRegistry(s, r.authority)
	if err != nil {
		t.Fatal(err)
	}
	reopened.now = r.now
	retry, err = reopened.Enroll(context.Background(), request)
	if err != nil || !bytes.Equal(chain, retry) {
		t.Fatal("durable retry", err)
	}
	request.RequestID = "1123456789abcdef0123456789abcdef"
	if _, err := r.Enroll(context.Background(), request); err == nil {
		t.Fatal("replay with different request")
	}
	data, err := s.Read(registryFile)
	if err != nil || bytes.Contains(data, []byte(i.Secret)) {
		t.Fatal("plaintext pairing secret stored")
	}
	if _, err := r.CreateInvitation(context.Background(), i.NodeID, i.URL, false); err == nil {
		t.Fatal("implicit identity replacement")
	}
	if err := r.Revoke(context.Background(), i.NodeID); err != nil {
		t.Fatal(err)
	}
	if r.Allows(leaf) {
		t.Fatal("revoked identity accepted")
	}
	if _, err := r.Enroll(context.Background(), EnrollmentRequest{NodeID: i.NodeID, Secret: i.Secret, RequestID: "0123456789abcdef0123456789abcdef", CSR: csr}); err == nil {
		t.Fatal("revoked retry")
	}
	i, err = r.CreateInvitation(context.Background(), i.NodeID, i.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(PairingLifetime)
	if _, err := r.Enroll(context.Background(), EnrollmentRequest{NodeID: i.NodeID, Secret: i.Secret, RequestID: "0123456789abcdef0123456789abcdef", CSR: csr}); err == nil {
		t.Fatal("expired invitation")
	}
}
func TestConcurrentEnrollmentConsumesOneRequest(t *testing.T) {
	r, _, _ := registryFixture(t)
	i, err := r.CreateInvitation(context.Background(), "node-01", "https://controller.test:9445", false)
	if err != nil {
		t.Fatal(err)
	}
	_, csr, _ := NewAgentRequest(i.NodeID)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{"0123456789abcdef0123456789abcdef", "1123456789abcdef0123456789abcdef"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := r.Enroll(context.Background(), EnrollmentRequest{NodeID: i.NodeID, Secret: i.Secret, RequestID: id, CSR: csr})
			results <- err
		}(id)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal("double enrollment", success)
	}
}
func TestPrivateStoreLockSymlinkAndPreservation(t *testing.T) {
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if other, err := OpenStore(dir); err == nil {
		other.Close()
		t.Fatal("concurrent process store lock")
	}
	ctx := context.Background()
	if err := s.Create(ctx, "value.json", []byte("original")); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, "value.json", []byte("replacement")); err == nil {
		t.Fatal("overwrote existing file")
	}
	got, _ := s.Read("value.json")
	if string(got) != "original" {
		t.Fatal("preservation")
	}
	outside := filepath.Join(t.TempDir(), "outside")
	_ = os.WriteFile(outside, []byte("SECRET"), 0600)
	if err := os.Symlink(outside, filepath.Join(dir, "link.key")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read("link.key"); err == nil {
		t.Fatal("followed symlink")
	}
	if err := s.Create(ctx, "link.key", []byte("replacement")); err == nil {
		t.Fatal("replaced symlink")
	}
	if data, _ := os.ReadFile(outside); string(data) != "SECRET" {
		t.Fatal("outside changed")
	}
	if _, err := s.Read("../outside"); err == nil {
		t.Fatal("path traversal")
	}
}
