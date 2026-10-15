// Package releaseauth authenticates exact release metadata, not just checksums.
package releaseauth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

var ErrSignature = errors.New("release signature or signing key invalid")

func Generate() (private, public []byte, err error) {
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		return nil, nil, e
	}
	privDER, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		return nil, nil, e
	}
	pubDER, e := x509.MarshalPKIXPublicKey(pub)
	if e != nil {
		return nil, nil, e
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}), pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}), nil
}
func publicKey(data []byte) (ed25519.PublicKey, error) {
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PUBLIC KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, ErrSignature
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, ErrSignature
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, ErrSignature
	}
	return key, nil
}
func Verify(public, data, signature []byte) error {
	key, err := publicKey(public)
	if err != nil || len(data) == 0 || len(data) > 1<<20 || len(signature) != ed25519.SignatureSize || !ed25519.Verify(key, data, signature) {
		return ErrSignature
	}
	return nil
}
func SignDirectory(directory, privatePath string, expectedPublic []byte) error {
	info, err := os.Lstat(privatePath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<10 || (runtime.GOOS != "windows" && info.Mode().Perm() != 0600) {
		return ErrSignature
	}
	data, err := os.ReadFile(privatePath)
	if err != nil {
		return ErrSignature
	}
	defer clear(data)
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return ErrSignature
	}
	defer clear(block.Bytes)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return ErrSignature
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return ErrSignature
	}
	defer clear(key)
	pub, err := publicKey(expectedPublic)
	if err != nil || !bytes.Equal(pub, key.Public().(ed25519.PublicKey)) {
		return ErrSignature
	}
	for _, name := range []string{"build-manifest.json", "SHA256SUMS"} {
		path := filepath.Join(directory, name)
		info, e := os.Lstat(path)
		if e != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 1<<20 {
			return ErrSignature
		}
		contents, e := os.ReadFile(path)
		if e != nil {
			return ErrSignature
		}
		sig := ed25519.Sign(key, contents)
		f, e := os.OpenFile(path+".sig", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return ErrSignature
		}
		_, e = f.Write(sig)
		closeErr := f.Close()
		if e != nil || closeErr != nil {
			return ErrSignature
		}
	}
	return nil
}
