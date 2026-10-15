package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"time"
)

// Seed is generated only after setup consent and durably retained by root so
// an interrupted setup resumes the SAME authority and initial server identity.
type Seed struct {
	files  map[string][]byte
	backup []byte
	config RuntimeConfig
}

func (*Seed) String() string               { return "managed setup seed (redacted)" }
func (s *Seed) GoString() string           { return s.String() }
func (*Seed) MarshalJSON() ([]byte, error) { return []byte(`{"seed":"redacted"}`), nil }

type seedDisk struct {
	Version int               `json:"version"`
	Files   map[string][]byte `json:"files"`
	Backup  []byte            `json:"backup"`
}

func NewSeed(config RuntimeConfig, password string, now time.Time) (*Seed, error) {
	if config.Validate() != nil {
		return nil, ErrInput
	}
	a, backup, err := NewAuthority(password, now)
	if err != nil {
		return nil, err
	}
	chain, key, err := a.Server(config.ServerName, now)
	if err != nil {
		return nil, err
	}
	root, issuer, issuerKey := a.Material()
	authorityData, _ := json.Marshal(AuthorityMaterial{root, issuer, issuerKey})
	serverData, _ := json.Marshal(serverIdentityDisk{Version: 1, Host: config.ServerName, Certificate: chain, Key: key, LastSuccess: now})
	configData, _ := json.Marshal(config)
	registryData, _ := json.Marshal(registryState{Version: 1, Nodes: []node{}})
	return &Seed{files: map[string][]byte{"authority.json": authorityData, serverIdentityFile: serverData, "runtime.json": configData, registryFile: registryData}, backup: backup, config: config}, nil
}
func (s *Seed) Encode() []byte {
	data, _ := json.Marshal(seedDisk{Version: 1, Files: s.files, Backup: s.backup})
	return data
}
func DecodeSeed(data []byte, password string, now time.Time) (*Seed, error) {
	var disk seedDisk
	if DecodeJSON(data, &disk, 256<<10) != nil || disk.Version != 1 || len(disk.Files) != 4 {
		return nil, ErrPKI
	}
	var authority AuthorityMaterial
	var server serverIdentityDisk
	var config RuntimeConfig
	if DecodeJSON(disk.Files["authority.json"], &authority, 64<<10) != nil || DecodeJSON(disk.Files[serverIdentityFile], &server, 64<<10) != nil || DecodeJSON(disk.Files["runtime.json"], &config, 16<<10) != nil || config.Validate() != nil || server.Host != config.ServerName {
		return nil, ErrPKI
	}
	a, err := LoadAuthority(authority.Root, authority.Issuer, authority.Key, now)
	if err != nil {
		return nil, err
	}
	if VerifyRootBackup(disk.Backup, password, authority.Root) != nil {
		return nil, ErrPKI
	}
	if _, err := verifyServerDisk(server, a, now); err != nil {
		return nil, err
	}
	if _, err := parseRegistry(disk.Files[registryFile]); err != nil {
		return nil, err
	}
	return &Seed{files: disk.Files, backup: disk.Backup, config: config}, nil
}
func (s *Seed) Config() RuntimeConfig { return s.config }
func (s *Seed) Backup() []byte        { return bytes.Clone(s.backup) }
func (s *Seed) InitialTLS() (chain, key, root []byte) {
	var server serverIdentityDisk
	_ = json.Unmarshal(s.files[serverIdentityFile], &server)
	var authority AuthorityMaterial
	_ = json.Unmarshal(s.files["authority.json"], &authority)
	return server.Certificate, server.Key, authority.Root
}
func (s *Seed) Provision(ctx context.Context, store *Store) error {
	for _, name := range []string{"authority.json", serverIdentityFile, "runtime.json", registryFile} {
		if before, err := store.Read(name); err == nil {
			if !bytes.Equal(before, s.files[name]) {
				return ErrConflict
			}
			continue
		}
		if err := store.Create(ctx, name, s.files[name]); err != nil {
			return err
		}
	}
	return nil
}
