// Package managed implements local managed PKI and authenticated onboarding.
package managed

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var ErrPKI = errors.New("managed PKI validation failed")
var nodePattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

const LeafLifetime = 90 * 24 * time.Hour
const RenewalAge = 30 * 24 * time.Hour
const backupIterations = 600000

// Private material never participates in ordinary log/JSON formatting.
type Authority struct {
	root, issuer *x509.Certificate
	key          *ecdsa.PrivateKey
}

func (*Authority) MarshalJSON() ([]byte, error) { return []byte(`{"authority":"redacted"}`), nil }
func (a *Authority) String() string             { return "managed authority (redacted)" }
func (a *Authority) GoString() string           { return a.String() }

type RootBackup struct {
	Version    int    `json:"version"`
	Salt       []byte `json:"salt"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}
type rootArchive struct {
	Certificate []byte `json:"certificate"`
	Key         []byte `json:"key"`
}

func serial() (*big.Int, error) {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 159))
	if err != nil || n.Sign() == 0 {
		return nil, ErrPKI
	}
	return n, nil
}
func keyPEM(key *ecdsa.PrivateKey) []byte {
	data, _ := x509.MarshalPKCS8PrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: data})
}
func certPEM(cert *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
}
func createCertificate(template, parent *x509.Certificate, key *ecdsa.PublicKey, signer *ecdsa.PrivateKey) (*x509.Certificate, error) {
	n, err := serial()
	if err != nil {
		return nil, err
	}
	template.SerialNumber = n
	der, err := x509.CreateCertificate(rand.Reader, template, parent, key, signer)
	if err != nil {
		return nil, ErrPKI
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, ErrPKI
	}
	return c, nil
}

// Root private key is returned ONLY encrypted, not retained by Authority.
func NewAuthority(password string, now time.Time) (*Authority, []byte, error) {
	if len(password) < 16 || len(password) > 1024 || now.IsZero() {
		return nil, nil, ErrPKI
	}
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, ErrPKI
	}
	rootTemplate := &x509.Certificate{Subject: pkix.Name{CommonName: "HLCP Managed Root CA"}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, MaxPathLen: 1, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	root, err := createCertificate(rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		return nil, nil, err
	}
	issuerKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, ErrPKI
	}
	issuerTemplate := &x509.Certificate{Subject: pkix.Name{CommonName: "HLCP Managed Issuing CA"}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(1, 0, 0), IsCA: true, BasicConstraintsValid: true, MaxPathLen: 0, MaxPathLenZero: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	issuer, err := createCertificate(issuerTemplate, root, &issuerKey.PublicKey, rootKey)
	if err != nil {
		return nil, nil, err
	}
	archive, _ := json.Marshal(rootArchive{certPEM(root), keyPEM(rootKey)})
	defer clear(archive)
	backup, err := encryptBackup(archive, password)
	if err != nil {
		return nil, nil, err
	}
	return &Authority{root: root, issuer: issuer, key: issuerKey}, backup, nil
}
func backupAEAD(password string, salt []byte) (cipher.AEAD, error) {
	key, err := pbkdf2.Key(sha256.New, password, salt, backupIterations, 32)
	if err != nil {
		return nil, ErrPKI
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrPKI
	}
	return cipher.NewGCM(block)
}
func encryptBackup(data []byte, password string) ([]byte, error) {
	b := RootBackup{Version: 1, Salt: make([]byte, 32), Nonce: make([]byte, 12)}
	if _, err := rand.Read(b.Salt); err != nil {
		return nil, ErrPKI
	}
	if _, err := rand.Read(b.Nonce); err != nil {
		return nil, ErrPKI
	}
	aead, err := backupAEAD(password, b.Salt)
	if err != nil {
		return nil, err
	}
	b.Ciphertext = aead.Seal(nil, b.Nonce, data, []byte("HLCP root backup v1"))
	return json.Marshal(b)
}

// VerifyRootBackup authenticates backup and key/cert match without exposing key.
func VerifyRootBackup(data []byte, password string, expectedRoot []byte) error {
	var b RootBackup
	if len(data) > 64<<10 || len(password) < 16 || len(password) > 1024 || json.Unmarshal(data, &b) != nil || b.Version != 1 || len(b.Salt) != 32 || len(b.Nonce) != 12 || len(b.Ciphertext) < 16 {
		return ErrPKI
	}
	aead, err := backupAEAD(password, b.Salt)
	if err != nil {
		return ErrPKI
	}
	plain, err := aead.Open(nil, b.Nonce, b.Ciphertext, []byte("HLCP root backup v1"))
	if err != nil {
		return ErrPKI
	}
	defer clear(plain)
	var archive rootArchive
	if json.Unmarshal(plain, &archive) != nil || !bytes.Equal(archive.Certificate, expectedRoot) {
		return ErrPKI
	}
	defer clear(archive.Key)
	if _, err := tls.X509KeyPair(archive.Certificate, archive.Key); err != nil {
		return ErrPKI
	}
	return nil
}
func (a *Authority) Material() (root, issuer, key []byte) {
	return certPEM(a.root), certPEM(a.issuer), keyPEM(a.key)
}
func (a *Authority) Fingerprint() string {
	sum := sha256.Sum256(a.root.Raw)
	return hex.EncodeToString(sum[:])
}
func (a *Authority) IssuerExpires() time.Time { return a.issuer.NotAfter }

func LoadAuthority(rootPEM, issuerPEM, privatePEM []byte, now time.Time) (*Authority, error) {
	parse := func(data []byte) (*x509.Certificate, error) {
		b, rest := pem.Decode(data)
		if b == nil || b.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
			return nil, ErrPKI
		}
		return x509.ParseCertificate(b.Bytes)
	}
	root, e1 := parse(rootPEM)
	issuer, e2 := parse(issuerPEM)
	block, rest := pem.Decode(privatePEM)
	if e1 != nil || e2 != nil || block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, ErrPKI
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, ErrPKI
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() || !root.IsCA || !issuer.IsCA || !root.BasicConstraintsValid || !issuer.BasicConstraintsValid || root.MaxPathLen != 1 || !issuer.MaxPathLenZero || issuer.MaxPathLen != 0 || issuer.KeyUsage&x509.KeyUsageCertSign == 0 || root.CheckSignatureFrom(root) != nil || issuer.CheckSignatureFrom(root) != nil || now.Before(root.NotBefore) || !now.Before(root.NotAfter) || now.Before(issuer.NotBefore) || !now.Before(issuer.NotAfter) || issuer.NotAfter.After(root.NotAfter) {
		return nil, ErrPKI
	}
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil || !bytes.Equal(pub, issuer.RawSubjectPublicKeyInfo) {
		return nil, ErrPKI
	}
	return &Authority{root: root, issuer: issuer, key: key}, nil
}

// NewAgentRequest generates the private key on the requesting node.
func NewAgentRequest(node string) (key, csr []byte, err error) {
	if !nodePattern.MatchString(node) {
		return nil, nil, ErrPKI
	}
	k, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, nil, ErrPKI
	}
	uri, _ := url.Parse("urn:hlcp:node:" + node)
	der, e := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: node}, URIs: []*url.URL{uri}}, k)
	if e != nil {
		return nil, nil, ErrPKI
	}
	return keyPEM(k), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), nil
}
func (a *Authority) IssueAgent(node string, request []byte, now time.Time) ([]byte, error) {
	if !nodePattern.MatchString(node) || len(request) > 16<<10 {
		return nil, ErrPKI
	}
	b, rest := pem.Decode(request)
	if b == nil || b.Type != "CERTIFICATE REQUEST" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, ErrPKI
	}
	csr, err := x509.ParseCertificateRequest(b.Bytes)
	if err != nil || csr.CheckSignature() != nil || csr.Subject.CommonName != node || len(csr.DNSNames) != 0 || len(csr.IPAddresses) != 0 || len(csr.EmailAddresses) != 0 || len(csr.URIs) != 1 || csr.URIs[0].String() != "urn:hlcp:node:"+node {
		return nil, ErrPKI
	}
	for _, ext := range csr.Extensions {
		if !ext.Id.Equal([]int{2, 5, 29, 17}) {
			return nil, ErrPKI
		}
	}
	pub, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return nil, ErrPKI
	}
	template := &x509.Certificate{Subject: pkix.Name{CommonName: node}, URIs: csr.URIs, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	return a.issueLeaf(template, pub, now)
}
func (a *Authority) Server(host string, now time.Time) (certificate, key []byte, err error) {
	if host == "" || len(host) > 253 || (!validDNS(host) && net.ParseIP(host) == nil) {
		return nil, nil, ErrPKI
	}
	k, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, nil, ErrPKI
	}
	template := &x509.Certificate{Subject: pkix.Name{CommonName: host}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}
	chain, e := a.issueLeaf(template, &k.PublicKey, now)
	return chain, keyPEM(k), e
}

var dnsPattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9.-]{0,251}[a-zA-Z0-9])?$`)

func validDNS(host string) bool {
	if !dnsPattern.MatchString(host) {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
	}
	return true
}
func (a *Authority) issueLeaf(template *x509.Certificate, pub *ecdsa.PublicKey, now time.Time) ([]byte, error) {
	if a == nil || a.issuer == nil || now.Before(a.issuer.NotBefore) || !now.Before(a.issuer.NotAfter) {
		return nil, ErrPKI
	}
	template.NotBefore = now.Add(-time.Minute)
	template.NotAfter = now.Add(LeafLifetime)
	if template.NotAfter.After(a.issuer.NotAfter) {
		template.NotAfter = a.issuer.NotAfter
	}
	if template.NotAfter.Sub(now) < 24*time.Hour {
		return nil, ErrPKI
	}
	template.KeyUsage = x509.KeyUsageDigitalSignature
	template.BasicConstraintsValid = true
	cert, err := createCertificate(template, a.issuer, pub, a.key)
	if err != nil {
		return nil, err
	}
	return append(certPEM(cert), certPEM(a.issuer)...), nil
}
