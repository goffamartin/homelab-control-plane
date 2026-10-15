package releaseauth

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignedManifestRefusesTamperArchitectureCompatibilityAndMissingAssets(t *testing.T) {
	private, public, e := Generate()
	if e != nil {
		t.Fatal(e)
	}
	block, _ := pem.Decode(private)
	parsed, _ := x509.ParsePKCS8PrivateKey(block.Bytes)
	key := parsed.(ed25519.PrivateKey)
	for _, kind := range []string{"valid", "tamper", "wrong-version", "wrong-arch", "missing", "incompatible", "dirty", "duplicate", "traversal", "unsigned"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			m := Manifest{Version: "v1.2.3", Revision: strings.Repeat("a", 40), GoVersion: "go1.27.2", ProtocolVersion: 1, StateVersion: 1, JournalVersion: 3, LedgerVersion: 1}
			for _, name := range []string{"hlcp-agent_1.2.3_amd64.deb", "hlcp-linux-amd64"} {
				content := []byte(name)
				hash := sha256.Sum256(content)
				m.Artifacts = append(m.Artifacts, Artifact{name, hex.EncodeToString(hash[:])})
				if os.WriteFile(filepath.Join(dir, name), content, 0600) != nil {
					t.Fatal("fixture")
				}
			}
			if kind == "incompatible" {
				m.StateVersion = 2
			}
			if kind == "dirty" {
				m.Revision += "-dirty"
			}
			if kind == "duplicate" {
				m.Artifacts = append(m.Artifacts, m.Artifacts[0])
			}
			if kind == "traversal" {
				m.Artifacts[0].Name = "../outside"
			}
			data, _ := json.Marshal(m)
			sig := ed25519.Sign(key, data)
			if kind == "tamper" {
				data = append(data, ' ')
			}
			if kind == "unsigned" {
				sig = nil
			}
			_ = os.WriteFile(filepath.Join(dir, "build-manifest.json"), data, 0600)
			_ = os.WriteFile(filepath.Join(dir, "build-manifest.json.sig"), sig, 0600)
			if kind == "missing" {
				_ = os.Remove(filepath.Join(dir, "hlcp-linux-amd64"))
			}
			version, arch := "v1.2.3", "amd64"
			if kind == "wrong-version" {
				version = "v1.2.4"
			}
			if kind == "wrong-arch" {
				arch = "arm64"
			}
			_, err := VerifyDirectory(dir, version, "agent", arch, public)
			if (err == nil) != (kind == "valid") {
				t.Fatalf("%s: %v", kind, err)
			}
		})
	}
}
