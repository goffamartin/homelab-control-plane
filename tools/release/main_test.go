package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseInputs(t *testing.T) {
	for _, input := range []string{"", "../linux", "linux/amd64,linux/amd64", "darwin/amd64"} {
		if _, err := parseTargets(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
	if targets, err := parseTargets("windows/amd64,linux/arm64,linux/amd64"); err != nil || targets[0] != "linux/amd64" {
		t.Fatalf("targets=%v err=%v", targets, err)
	}
	for _, label := range []string{"", "a b", "v1 -X bad", "../v1"} {
		if metadataPattern.MatchString(label) {
			t.Fatalf("accepted metadata %q", label)
		}
	}
	t.Setenv("GOFLAGS", "-tags=unreviewed")
	env := buildEnv("linux/arm64")
	for _, wanted := range []string{"GOFLAGS=", "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0", "GOPROXY=off", "GOTOOLCHAIN=local"} {
		count := 0
		key, _, _ := strings.Cut(wanted, "=")
		for _, item := range env {
			name, _, _ := strings.Cut(item, "=")
			if strings.EqualFold(key, name) {
				count++
				if item != wanted {
					t.Fatalf("environment=%s want=%s", item, wanted)
				}
			}
		}
		if count != 1 {
			t.Fatalf("key %s count=%d", key, count)
		}
	}
}

func TestChecksum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	digest, err := checksum(path)
	if err != nil || digest != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("digest=%s err=%v", digest, err)
	}
}
