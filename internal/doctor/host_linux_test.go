package doctor

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestNativeReadSafetyAndNoWrites(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "input")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	data, err := (NativeHost{}).ReadFile(path, true)
	if err != nil || string(data) != "fixture" {
		t.Fatalf("read: %v", err)
	}
	after, _ := os.Stat(path)
	if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || before.Mode() != after.Mode() {
		t.Fatal("mutated input")
	}
	entries, _ := os.ReadDir(directory)
	if len(entries) != 1 {
		t.Fatal("created files")
	}
	for _, mode := range []os.FileMode{0644, 0666} {
		os.Chmod(path, mode)
		if _, err := (NativeHost{}).ReadFile(path, true); err == nil {
			t.Fatal("unsafe private mode accepted")
		}
	}
	os.Chmod(path, 0600)
	link := filepath.Join(directory, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := (NativeHost{}).ReadFile(link, true); err == nil {
		t.Fatal("symlink accepted")
	}
	hard := filepath.Join(directory, "hard")
	if err := os.Link(path, hard); err != nil {
		t.Fatal(err)
	}
	if _, err := (NativeHost{}).ReadFile(path, true); err == nil {
		t.Fatal("hardlink accepted")
	}
	fifo := filepath.Join(directory, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := (NativeHost{}).ReadFile(fifo, true); err == nil {
		t.Fatal("FIFO accepted")
	}
	if _, err := (NativeHost{}).ReadFile(filepath.Join(directory, "missing"), true); err == nil {
		t.Fatal("missing accepted")
	}
}
