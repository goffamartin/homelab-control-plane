package managed

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"time"
)

// Peer trust is a small explicit bundle of valid self-signed CA anchors, never
// an arbitrary chain, URL or inferred host-store trust.
func ValidatePeerTrust(data []byte, now time.Time) error {
	return validateTrust(data, now, true)
}
func validateTrust(data []byte, now time.Time, current bool) error {
	if len(data) == 0 || len(data) > 64<<10 {
		return ErrPKI
	}
	count := 0
	for len(bytes.TrimSpace(data)) > 0 {
		block, rest := pem.Decode(data)
		if block == nil || block.Type != "CERTIFICATE" {
			return ErrPKI
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !c.IsCA || !c.BasicConstraintsValid || c.CheckSignatureFrom(c) != nil || (current && (now.Before(c.NotBefore) || !now.Before(c.NotAfter))) {
			return ErrPKI
		}
		count++
		if count > 4 {
			return ErrPKI
		}
		data = rest
	}
	if count == 0 {
		return ErrPKI
	}
	return nil
}
func (r *Registry) ConfigureLegacy(trust []byte) error {
	if ValidatePeerTrust(trust, r.now()) != nil {
		return ErrPKI
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(trust) {
		return ErrPKI
	}
	r.legacyRoots = pool
	return nil
}
func NewMigrationSeed(config RuntimeConfig, password string, ids []string, now time.Time) (*Seed, error) {
	if !config.LegacyServer || len(ids) > MaximumNodes {
		return nil, ErrInput
	}
	seed, err := NewSeed(config, password, now)
	if err != nil {
		return nil, err
	}
	state := registryState{Version: 1, Nodes: []node{}}
	seen := map[string]bool{}
	for _, id := range ids {
		if !nodePattern.MatchString(id) || seen[id] {
			return nil, ErrInput
		}
		seen[id] = true
		state.Nodes = append(state.Nodes, node{ID: id, Generation: 1, Active: true, Legacy: true, Credentials: []credential{}})
	}
	seed.files[registryFile], _ = json.Marshal(state)
	return seed, nil
}
func VerifyLegacyServer(chain, key, trust []byte, host string, now time.Time) error {
	if ValidatePeerTrust(trust, now) != nil {
		return ErrPKI
	}
	pair, err := tls.X509KeyPair(chain, key)
	if err != nil || len(pair.Certificate) == 0 {
		return ErrPKI
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || leaf.IsCA {
		return ErrPKI
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(trust)
	intermediates := x509.NewCertPool()
	for _, der := range pair.Certificate[1:] {
		c, e := x509.ParseCertificate(der)
		if e != nil {
			return ErrPKI
		}
		intermediates.AddCert(c)
	}
	_, err = leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, DNSName: host, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	return err
}
func FinishMigration(ctx context.Context, s *Store) error {
	raw, err := s.Read("runtime.json")
	if err != nil {
		return err
	}
	var config RuntimeConfig
	if DecodeJSON(raw, &config, 128<<10) != nil || config.Version != 1 {
		return ErrInput
	}
	if !config.LegacyServer {
		return config.Validate()
	}
	data, err := s.Read(registryFile)
	if err != nil {
		return err
	}
	state, err := parseRegistry(data)
	if err != nil {
		return err
	}
	for _, n := range state.Nodes {
		if (n.Active && n.Legacy) || (!n.Active && n.RequestID == "" && n.InvitationHash != [32]byte{}) {
			return ErrConflict
		}
	}
	if s.Update(ctx, "runtime-before-migration-finish.json", func([]byte) ([]byte, error) { return raw, nil }) != nil {
		return ErrStore
	}
	config.LegacyServer = false
	config.LegacyTrust = ""
	if config.Validate() != nil {
		return ErrInput
	}
	next, _ := json.Marshal(config)
	return s.Update(ctx, "runtime.json", func(before []byte) ([]byte, error) {
		if !bytes.Equal(before, raw) {
			return nil, ErrConflict
		}
		return next, nil
	})
}
