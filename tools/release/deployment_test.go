package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDeploymentReproducibleAndAllowlisted(t *testing.T) {
	root := t.TempDir()
	for _, name := range deploymentFiles {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("template\r\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "private.key"), []byte("SECRET"), 0600); err != nil {
		t.Fatal(err)
	}
	var first, second bytes.Buffer
	if err := writeDeployment(root, &first); err != nil {
		t.Fatal(err)
	}
	for _, name := range deploymentFiles {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.WriteFile(path, []byte("template\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, time.Now(), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeDeployment(root, &second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("non-deterministic archive")
	}
	gz, err := gzip.NewReader(bytes.NewReader(first.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for _, name := range deploymentFiles {
		header, err := tr.Next()
		if err != nil || header.Name != name || header.Mode != 0644 {
			t.Fatalf("entry %s: %v", name, err)
		}
		data, err := io.ReadAll(tr)
		if err != nil || string(data) != "template\n" {
			t.Fatal("invalid data")
		}
	}
	if _, err := tr.Next(); err != io.EOF {
		t.Fatal("unexpected archive member")
	}
	path := filepath.Join(root, filepath.FromSlash(deploymentFiles[0]))
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeDeployment(root, io.Discard); err == nil {
		t.Fatal("accepted directory")
	}
}
