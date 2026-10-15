package managed

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"time"
)

// RenewIssuer retains the issuer key and root, so already issued chains remain
// usable. This is scheduled maintenance, not recovery from key compromise.
// Root private material is decrypted only for this explicit offline operation.
func RenewIssuer(material AuthorityMaterial, backup []byte, password string, now time.Time) (*Authority, error) {
	if VerifyRootBackup(backup, password, material.Root) != nil {
		return nil, ErrPKI
	}
	var disk RootBackup
	if DecodeJSON(backup, &disk, 64<<10) != nil {
		return nil, ErrPKI
	}
	aead, err := backupAEAD(password, disk.Salt)
	if err != nil {
		return nil, ErrPKI
	}
	plain, err := aead.Open(nil, disk.Nonce, disk.Ciphertext, []byte("HLCP root backup v1"))
	if err != nil {
		return nil, ErrPKI
	}
	defer clear(plain)
	var archive rootArchive
	if DecodeJSON(plain, &archive, 64<<10) != nil {
		return nil, ErrPKI
	}
	defer clear(archive.Key)
	rootPair, err := tls.X509KeyPair(archive.Certificate, archive.Key)
	if err != nil || len(rootPair.Certificate) != 1 {
		return nil, ErrPKI
	}
	root, err := x509.ParseCertificate(rootPair.Certificate[0])
	if err != nil || now.Before(root.NotBefore) || !now.Before(root.NotAfter) || root.CheckSignatureFrom(root) != nil {
		return nil, ErrPKI
	}
	rootKey, ok := rootPair.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, ErrPKI
	}
	issuerPair, err := tls.X509KeyPair(material.Issuer, material.Key)
	if err != nil || len(issuerPair.Certificate) != 1 {
		return nil, ErrPKI
	}
	old, err := x509.ParseCertificate(issuerPair.Certificate[0])
	if err != nil || old.CheckSignatureFrom(root) != nil || !old.IsCA || !old.MaxPathLenZero || old.MaxPathLen != 0 {
		return nil, ErrPKI
	}
	key, ok := issuerPair.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, ErrPKI
	}
	end := now.AddDate(1, 0, 0)
	if end.After(root.NotAfter) {
		end = root.NotAfter
	}
	if !end.After(old.NotAfter) || end.Sub(now) < LeafLifetime {
		return nil, ErrPKI
	}
	template := &x509.Certificate{Subject: pkix.Name{CommonName: "HLCP Managed Issuing CA"}, NotBefore: now.Add(-5 * time.Minute), NotAfter: end, IsCA: true, BasicConstraintsValid: true, MaxPathLen: 0, MaxPathLenZero: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	issuer, err := createCertificate(template, root, &key.PublicKey, rootKey)
	if err != nil {
		return nil, err
	}
	return LoadAuthority(material.Root, certPEM(issuer), material.Key, now)
}

// IdentityStatus is safe for concurrent read-only snapshots and also explains
// expired material without ever enabling it for a connection.
func IdentityStatus(data []byte, id string, now time.Time) (CertificateStatus, error) {
	var d identityDisk
	if DecodeJSON(data, &d, 128<<10) != nil || d.Version != 1 || d.NodeID != id {
		return CertificateStatus{}, ErrPKI
	}
	pair, err := tls.X509KeyPair(d.Certificate, d.Key)
	if err != nil || len(pair.Certificate) < 2 {
		return CertificateStatus{}, ErrPKI
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return CertificateStatus{}, ErrPKI
	}
	check := now
	if !now.Before(leaf.NotAfter) {
		check = leaf.NotBefore.Add(time.Second)
	}
	if VerifyAgentMaterial(id, d.Certificate, d.Key, d.CA, check) != nil {
		return CertificateStatus{}, ErrPKI
	}
	issuer, err := x509.ParseCertificate(pair.Certificate[1])
	if err != nil {
		return CertificateStatus{}, ErrPKI
	}
	block, rest := pem.Decode(d.CA)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return CertificateStatus{}, ErrPKI
	}
	root, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return CertificateStatus{}, ErrPKI
	}
	return CertificateStatus{Managed: true, Expires: leaf.NotAfter, RenewAt: RenewalTime(leaf), NextAttempt: d.NextAttempt, LastSuccess: d.LastSuccess, Failures: d.Failures, Warning: expiryWarning(leaf.NotAfter, now), IssuerExpires: issuer.NotAfter, IssuerWarning: expiryWarning(issuer.NotAfter, now), RootExpires: root.NotAfter}, nil
}

// Reports only bounded lifecycle metadata, once per changed state.
func StatusReporter(out io.Writer) func(CertificateStatus) {
	var previous CertificateStatus
	first := true
	return func(s CertificateStatus) {
		if !first && s == previous {
			return
		}
		first = false
		previous = s
		_ = json.NewEncoder(out).Encode(struct {
			Event  string            `json:"event"`
			Status CertificateStatus `json:"status"`
		}{"certificate_lifecycle", s})
	}
}
