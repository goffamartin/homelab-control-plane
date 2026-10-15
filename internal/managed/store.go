package managed

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var ErrStore = errors.New("managed private store unavailable or unsafe; preserve state for inspection")

const MaximumStateBytes = 8 << 20

// Store pins a private directory and holds an OS lock until Close. It does not
// create/repair the directory; setup/join must explicitly provision it first.
type Store struct {
	mutex  sync.Mutex
	root   *os.Root
	lock   *os.File
	failed bool
}

func (*Store) String() string               { return "managed private store (redacted)" }
func (s *Store) GoString() string           { return s.String() }
func (*Store) MarshalJSON() ([]byte, error) { return []byte(`{"store":"redacted"}`), nil }

func OpenStore(path string) (*Store, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(filepath.Separator) {
		return nil, ErrStore
	}
	root, lock, err := openPrivateStore(path)
	if err != nil {
		return nil, ErrStore
	}
	return &Store{root: root, lock: lock}, nil
}
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.root == nil {
		return nil
	}
	e1 := s.lock.Close()
	e2 := s.root.Close()
	s.root = nil
	return errors.Join(e1, e2)
}
func storeName(name string) bool {
	return len(name) > 0 && len(name) < 100 && name[0] != '.' && filepath.Base(name) == name && !strings.ContainsAny(name, "/\\\x00")
}
func (s *Store) read(name string) ([]byte, error) {
	if s.root == nil || s.failed || !storeName(name) {
		return nil, ErrStore
	}
	f, err := openStoreFile(s.root, name, os.O_RDONLY)
	if os.IsNotExist(err) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, ErrStore
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaximumStateBytes+1))
	if err != nil || len(data) > MaximumStateBytes {
		return nil, ErrStore
	}
	return data, nil
}
func (s *Store) Read(name string) ([]byte, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.read(name)
}

// Update atomically publishes one complete bounded snapshot. After uncertain
// durability the store is poisoned rather than silently retrying a mutation.
func (s *Store) Update(ctx context.Context, name string, fn func([]byte) ([]byte, error)) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.root == nil || s.failed || !storeName(name) || ctx.Err() != nil {
		return ErrStore
	}
	before, err := s.read(name)
	if err != nil && !os.IsNotExist(err) {
		return ErrStore
	}
	data, err := fn(before)
	if err != nil {
		return err
	}
	if len(data) == 0 || len(data) > MaximumStateBytes || ctx.Err() != nil {
		return ErrStore
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return ErrStore
	}
	temp := ".managed-stage-" + hex.EncodeToString(id[:])
	f, err := openStoreFile(s.root, temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return ErrStore
	}
	defer s.root.Remove(temp)
	n, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil || n != len(data) || ctx.Err() != nil {
		return ErrStore
	}
	if err := s.root.Rename(temp, name); err != nil {
		return ErrStore
	}
	dir, err := s.root.Open(".")
	if err != nil {
		s.failed = true
		return ErrStore
	}
	err = dir.Sync()
	_ = dir.Close()
	if err != nil {
		s.failed = true
		return ErrStore
	}
	return nil
}
func (s *Store) Create(ctx context.Context, name string, data []byte) error {
	return s.Update(ctx, name, func(before []byte) ([]byte, error) {
		if before != nil {
			return nil, ErrStore
		}
		return data, nil
	})
}
