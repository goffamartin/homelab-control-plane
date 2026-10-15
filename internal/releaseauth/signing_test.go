package releaseauth

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func TestAuthenticatedMetadata(t *testing.T) {
	private, public, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(private)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	key := parsed.(ed25519.PrivateKey)
	data := []byte("signed release metadata")
	sig := ed25519.Sign(key, data)
	if Verify(public, data, sig) != nil {
		t.Fatal("valid signature refused")
	}
	if Verify(public, append(data, '!'), sig) == nil || Verify(public, data, nil) == nil {
		t.Fatal("tampered or missing signature accepted")
	}
	_, other, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if Verify(other, data, sig) == nil {
		t.Fatal("wrong trust key accepted")
	}
}
