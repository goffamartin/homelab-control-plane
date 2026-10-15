package onboarding

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientTrustedGroupParents(t *testing.T) {
	owner := clientTestOwner(t)
	home := t.TempDir()
	parent := filepath.Join(home, ".config")
	if err := os.Mkdir(parent, 0771); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0771); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(parent, "hlcp")
	credential, _ := NewClientCredential([]byte(strings.Repeat("a", 64)))
	if r := inspectClientPath(directory, owner, false); r == nil || r.Code != "GROUP_WRITABLE_PARENT" || r.Path != parent || r.Group != owner.GID {
		t.Fatal("missing precise group warning", r)
	}
	if r := inspectClientPath(directory, owner, true); r != nil {
		t.Fatal(r)
	}
	if p, err := planGrantClient(context.Background(), directory, owner, credential); err == nil {
		p.Close()
		t.Fatal("implicit group trust")
	}
	p, err := planGrantClientWithTrust(context.Background(), directory, owner, credential, true)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err := p.Apply(context.Background(), false, credential); err == nil {
		t.Fatal("no consent")
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("preview/declined consent wrote user path")
	}
	if _, err := p.Apply(context.Background(), true, credential); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(parent)
	if info.Mode().Perm() != 0771 {
		t.Fatal("parent permissions changed")
	}
	info, _ = os.Stat(directory)
	if info.Mode().Perm() != 0700 {
		t.Fatal("private directory loosened")
	}
	for _, name := range []string{"cli.json", "cli.token"} {
		info, err := os.Stat(filepath.Join(directory, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("private file loosened")
		}
	}
	repeat, err := planGrantClientWithTrust(context.Background(), directory, owner, credential, true)
	if err != nil {
		t.Fatal(err)
	}
	defer repeat.Close()
	if result, err := repeat.Apply(context.Background(), true, credential); err != nil || result.Changed != 0 {
		t.Fatal("repeat not idempotent", err)
	}
}

func TestClientTrustedGroupStillRefusesUnsafePathsAndRebinding(t *testing.T) {
	owner := clientTestOwner(t)
	credential, _ := NewClientCredential([]byte(strings.Repeat("a", 64)))
	for _, kind := range []string{"world-writable", "symlink", "nonprivate-profile", "rebound-parent"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			parent := filepath.Join(home, ".config")
			directory := filepath.Join(parent, "hlcp")
			if err := os.Mkdir(parent, 0771); err != nil {
				t.Fatal(err)
			}
			os.Chmod(parent, 0771)
			switch kind {
			case "world-writable":
				os.Chmod(parent, 0777)
			case "symlink":
				os.Rename(parent, parent+"-real")
				os.Symlink(parent+"-real", parent)
			case "nonprivate-profile":
				os.Mkdir(directory, 0755)
				os.Chmod(directory, 0755)
			}
			if kind != "rebound-parent" {
				if r := inspectClientPath(directory, owner, true); r == nil {
					t.Fatal("unsafe path accepted", kind)
				}
				if p, err := planGrantClientWithTrust(context.Background(), directory, owner, credential, true); err == nil {
					p.Close()
					t.Fatal("unsafe plan accepted", kind)
				}
				return
			}
			p, err := planGrantClientWithTrust(context.Background(), directory, owner, credential, true)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			if err := os.Rename(parent, parent+"-old"); err != nil {
				t.Fatal(err)
			}
			os.Mkdir(parent, 0700)
			if _, err := p.Apply(context.Background(), true, credential); err == nil {
				t.Fatal("rebound parent adopted")
			}
			if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Fatal("replacement parent modified")
			}
		})
	}
}
