package managedcli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupRejectsLinksAndInsecureFilesWithoutChangingSource(t *testing.T) {
	for _, kind := range []string{"valid", "symlink", "hardlink", "public"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			_ = os.Chmod(dir, 0700)
			path := filepath.Join(dir, "state.json")
			original := []byte("private state")
			if os.WriteFile(path, original, 0600) != nil {
				t.Fatal("fixture")
			}
			switch kind {
			case "symlink":
				_ = os.Symlink(path, filepath.Join(dir, "alias"))
			case "hardlink":
				_ = os.Link(path, filepath.Join(dir, "alias"))
			case "public":
				_ = os.Chmod(path, 0644)
			}
			var result []backupEntry
			size := 0
			err := backupTree(dir, os.Geteuid(), os.Getegid(), &result, &size, 0)
			if (err == nil) != (kind == "valid") {
				t.Fatalf("%s: %v", kind, err)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(after, original) {
				t.Fatal("source changed")
			}
		})
	}
}
func TestArchiveNoClobber(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.json")
	if writeNewArchive(path, []byte("first")) != nil {
		t.Fatal("create")
	}
	if writeNewArchive(path, []byte("second")) == nil {
		t.Fatal("overwritten")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "first" {
		t.Fatal("changed")
	}
}
