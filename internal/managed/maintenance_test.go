package managed

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"testing"
	"time"
)

func TestIssuerMaintenanceRetainsRootKeyAndOldChains(t *testing.T) {
	now := time.Now().UTC()
	a, backup, err := NewAuthority("test backup password long enough", now)
	if err != nil {
		t.Fatal(err)
	}
	root, issuer, key := a.Material()
	material := AuthorityMaterial{root, issuer, key}
	if _, err = RenewIssuer(material, backup, "wrong password long enough", now.AddDate(0, 10, 0)); err == nil {
		t.Fatal("accepted wrong password")
	}
	renewed, err := RenewIssuer(material, backup, "test backup password long enough", now.AddDate(0, 10, 0))
	if err != nil {
		t.Fatal(err)
	}
	r, i, k := renewed.Material()
	if !bytes.Equal(root, r) || !bytes.Equal(key, k) || bytes.Equal(issuer, i) || !renewed.IssuerExpires().After(a.IssuerExpires()) {
		t.Fatal("maintenance changed identity or failed to extend issuer")
	}
	chain, leafkey, err := renewed.Server("controller.test", now.AddDate(0, 10, 0))
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(chain, leafkey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(pair.Certificate[0])
	if !leaf.NotAfter.After(now.AddDate(0, 10, 0)) {
		t.Fatal("invalid leaf")
	}
}
func TestStatusReporterDeduplicatesMetadata(t *testing.T) {
	var out bytes.Buffer
	report := StatusReporter(&out)
	s := CertificateStatus{Managed: true, Warning: "CERTIFICATE_EXPIRES_7D"}
	report(s)
	report(s)
	var events []map[string]any
	decoder := json.NewDecoder(&out)
	for decoder.More() {
		var e map[string]any
		if decoder.Decode(&e) != nil {
			break
		}
		events = append(events, e)
	}
	if len(events) != 1 {
		t.Fatal(len(events))
	}
}
