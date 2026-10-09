package command

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// BlobStore shares the agent journal's tested lock/replace/directory-sync
// primitives. Its owner serializes reads and writes; Close is idempotent.
type BlobStore struct {
	path     string
	limit    int
	lock     *os.File
	once     sync.Once
	closeErr error
}

func OpenBlobStore(path string, limit int) (*BlobStore, error) {
	if path == "" || limit < 1 {
		return nil, errors.New("invalid durable store path/limit")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := prepareJournalDirectory(filepath.Dir(absolute)); err != nil {
		return nil, err
	}
	directory, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return nil, err
	}
	absolute = filepath.Join(directory, filepath.Base(absolute))
	if info, err := os.Lstat(absolute); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("durable store must not be a symlink")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	lock, err := acquireJournalLock(absolute + ".lock")
	if err != nil {
		return nil, err
	}
	return &BlobStore{path: absolute, limit: limit, lock: lock}, nil
}
func (store *BlobStore) Read() ([]byte, bool, error) { return readJournal(store.path, store.limit) }
func (store *BlobStore) Write(data []byte) error {
	if len(data) > store.limit {
		return errors.New("durable store byte capacity exceeded")
	}
	return durableReplace(store.path, data, nil)
}
func (store *BlobStore) Close() error {
	store.once.Do(func() { store.closeErr = store.lock.Close() })
	return store.closeErr
}
