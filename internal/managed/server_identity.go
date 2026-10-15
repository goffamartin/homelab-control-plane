package managed

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"hlcp/internal/transport"
	"sync"
	"time"
)

type AuthorityMaterial struct {
	Root   []byte `json:"root"`
	Issuer []byte `json:"issuer"`
	Key    []byte `json:"key"`
}

func (a *Authority) Store(ctx context.Context, s *Store) error {
	root, issuer, key := a.Material()
	data, _ := json.Marshal(AuthorityMaterial{root, issuer, key})
	return s.Create(ctx, "authority.json", data)
}
func LoadStoredAuthority(s *Store, now time.Time) (*Authority, error) {
	data, err := s.Read("authority.json")
	if err != nil {
		return nil, ErrStore
	}
	var a AuthorityMaterial
	if DecodeJSON(data, &a, 64<<10) != nil {
		return nil, ErrPKI
	}
	return LoadAuthority(a.Root, a.Issuer, a.Key, now)
}

type serverIdentityDisk struct {
	Version     int       `json:"version"`
	Host        string    `json:"host"`
	Certificate []byte    `json:"certificate"`
	Key         []byte    `json:"key"`
	LastSuccess time.Time `json:"last_success"`
}
type ServerIdentity struct {
	mutex      sync.RWMutex
	renewMutex sync.Mutex
	store      *Store
	authority  *Authority
	disk       serverIdentityDisk
	pair       *tls.Certificate
}

func (*ServerIdentity) String() string     { return "managed server identity (redacted)" }
func (i *ServerIdentity) GoString() string { return i.String() }
func (*ServerIdentity) MarshalJSON() ([]byte, error) {
	return []byte(`{"server_identity":"redacted"}`), nil
}

const serverIdentityFile = "server-identity.json"

func CreateServerIdentity(ctx context.Context, s *Store, a *Authority, host string) error {
	chain, key, err := a.Server(host, time.Now())
	if err != nil {
		return err
	}
	data, _ := json.Marshal(serverIdentityDisk{Version: 1, Host: host, Certificate: chain, Key: key, LastSuccess: time.Now()})
	return s.Create(ctx, serverIdentityFile, data)
}
func verifyServerDisk(d serverIdentityDisk, a *Authority, now time.Time) (*tls.Certificate, error) {
	if d.Version != 1 || len(d.Certificate) > 16<<10 || len(d.Key) > 16<<10 {
		return nil, ErrPKI
	}
	pair, err := tls.X509KeyPair(d.Certificate, d.Key)
	if err != nil || len(pair.Certificate) < 2 {
		return nil, ErrPKI
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || leaf.IsCA {
		return nil, ErrPKI
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(a.RootPEM())
	intermediates := x509.NewCertPool()
	for _, der := range pair.Certificate[1:] {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, ErrPKI
		}
		intermediates.AddCert(cert)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: now, DNSName: d.Host, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return nil, ErrPKI
	}
	pair.Leaf = leaf
	return &pair, nil
}
func OpenServerIdentity(s *Store, a *Authority, host string) (*ServerIdentity, error) {
	data, err := s.Read(serverIdentityFile)
	if err != nil {
		return nil, ErrStore
	}
	var d serverIdentityDisk
	if DecodeJSON(data, &d, 64<<10) != nil || d.Host != host {
		return nil, ErrPKI
	}
	pair, err := verifyServerDisk(d, a, time.Now())
	if err != nil {
		// A valid but expired server leaf may be renewed locally using the
		// still-valid issuer. Never treat corrupt/untrusted material as absent.
		old, e := tls.X509KeyPair(d.Certificate, d.Key)
		if e != nil || len(old.Certificate) == 0 {
			return nil, ErrPKI
		}
		leaf, e := x509.ParseCertificate(old.Certificate[0])
		if e != nil || time.Now().Before(leaf.NotAfter) {
			return nil, ErrPKI
		}
		pair, err = verifyServerDisk(d, a, leaf.NotBefore.Add(time.Second))
		if err != nil {
			return nil, ErrPKI
		}
	}
	return &ServerIdentity{store: s, authority: a, disk: d, pair: pair}, nil
}
func (i *ServerIdentity) TLSConfig() (*tls.Config, error) {
	return transport.ServerTLSConfigDynamic(i.authority.RootPEM(), func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		i.mutex.RLock()
		defer i.mutex.RUnlock()
		return i.pair, nil
	})
}
func (i *ServerIdentity) EnrollmentTLSConfig() (*tls.Config, error) {
	config, err := i.TLSConfig()
	if err != nil {
		return nil, err
	}
	config.ClientAuth = tls.VerifyClientCertIfGiven
	config.NextProtos = []string{"http/1.1"}
	config.VerifyConnection = nil
	return config, nil
}
func (i *ServerIdentity) Renew(ctx context.Context, now time.Time) error {
	i.renewMutex.Lock()
	defer i.renewMutex.Unlock()
	i.mutex.RLock()
	d := i.disk
	pair := i.pair
	i.mutex.RUnlock()
	if now.Before(RenewalTime(pair.Leaf)) {
		return nil
	}
	previous := append([]byte(nil), d.Certificate...)
	chain, key, err := i.authority.Server(d.Host, now)
	if err != nil {
		return err
	}
	d.Certificate = chain
	d.Key = key
	d.LastSuccess = now
	updated, err := verifyServerDisk(d, i.authority, now)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(d)
	if err := i.store.Update(ctx, serverIdentityFile, func(before []byte) ([]byte, error) {
		var old serverIdentityDisk
		if DecodeJSON(before, &old, 64<<10) != nil || old.Host != d.Host || !bytes.Equal(old.Certificate, previous) {
			return nil, ErrStore
		}
		return data, nil
	}); err != nil {
		return err
	}
	i.mutex.Lock()
	i.disk = d
	i.pair = updated
	i.mutex.Unlock()
	return nil
}
func (i *ServerIdentity) Status(now time.Time) CertificateStatus {
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	return CertificateStatus{Managed: true, Expires: i.pair.Leaf.NotAfter, RenewAt: RenewalTime(i.pair.Leaf), LastSuccess: i.disk.LastSuccess, Warning: expiryWarning(i.pair.Leaf.NotAfter, now), IssuerExpires: i.authority.issuer.NotAfter, IssuerWarning: expiryWarning(i.authority.issuer.NotAfter, now), RootExpires: i.authority.root.NotAfter}
}
func (i *ServerIdentity) Maintain(ctx context.Context, report func(CertificateStatus)) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		_ = i.Renew(ctx, time.Now())
		if report != nil {
			report(i.Status(time.Now()))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
