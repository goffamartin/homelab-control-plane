package command

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Persist each newly created directory in its parent before admitting work.
func prepareJournalDirectory(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return errors.New("journal directory is not a directory")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(path)
	if err := prepareJournalDirectory(parent); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if err := syncJournalDirectory(parent); err != nil {
		return err
	}
	return syncJournalDirectory(path)
}

// fault is a test seam at each durability boundary, before performing it.
func durableReplace(path string, data []byte, fault func(string) error) error {
	check := func(stage string) error {
		if fault != nil {
			return fault(stage)
		}
		return nil
	}
	if err := check("create"); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".hlcp-journal-*")
	if err != nil {
		return err
	}
	defer func() { _ = temporary.Close(); _ = os.Remove(temporary.Name()) }()
	if err := check("write"); err != nil {
		return err
	}
	written, err := temporary.Write(data)
	if err != nil {
		return err
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	if err := check("sync"); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := check("close"); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := check("rename"); err != nil {
		return err
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return err
	}
	if err := check("directory-sync"); err != nil {
		return err
	}
	if err := syncJournalDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync journal directory: %w", err)
	}
	return nil
}

func backupLegacy(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create exclusive legacy journal backup: %w", err)
	}
	defer file.Close()
	written, err := file.Write(data)
	if err != nil {
		return err
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return syncJournalDirectory(filepath.Dir(path))
}
