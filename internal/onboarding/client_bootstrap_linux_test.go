package onboarding

import (
	"context"
	"errors"
	"hlcp/internal/setup"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeClientBootstrapPreviewConsentAndRepeat(t *testing.T) {
	owner := clientTestOwner(t)
	ctx := context.Background()
	home := t.TempDir()
	directory := filepath.Join(home, ".config", "hlcp")
	standin, _ := NewClientCredential([]byte(strings.Repeat("0", 64)))
	actual, _ := NewClientCredential([]byte(strings.Repeat("a", 64)))
	p, err := planGrantClient(ctx, directory, owner, standin)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	preview := p.Preview()
	if len(preview.Changes) != 4 || preview.Changes[0].Name != "user-config-directory" || preview.Changes[1].Name != "configuration-directory" {
		t.Fatal(preview)
	}
	if _, err = os.Stat(filepath.Join(home, ".config")); !os.IsNotExist(err) {
		t.Fatal("preview created parent")
	}
	if _, err = p.Apply(ctx, false, actual); !errors.Is(err, setup.ErrConsentRequired) {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(home, ".config")); !os.IsNotExist(err) {
		t.Fatal("no-consent wrote parent")
	}
	result, err := p.Apply(ctx, true, actual)
	if err != nil || result.Changed != 2 {
		t.Fatal(result, err)
	}
	for _, path := range []string{filepath.Join(home, ".config"), directory} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatal("unsafe bootstrap directory")
		}
	}
	data, _ := os.ReadFile(filepath.Join(directory, "cli.token"))
	if string(data) != string(actual.token) {
		t.Fatal("stand-in applied")
	}
	repeat, err := planGrantClient(ctx, directory, owner, actual)
	if err != nil {
		t.Fatal(err)
	}
	defer repeat.Close()
	if len(repeat.Preview().Changes) != 2 {
		t.Fatal("repeat proposed directory writes")
	}
	if r, err := repeat.Apply(ctx, true, actual); err != nil || r.Changed != 0 {
		t.Fatal(r, err)
	}
}
func TestNativeClientBootstrapRacesAndPreservation(t *testing.T) {
	owner := clientTestOwner(t)
	ctx := context.Background()
	token, _ := NewClientCredential([]byte(strings.Repeat("a", 64)))
	for _, kind := range []string{"directory", "symlink", "rebound-home"} {
		home := t.TempDir()
		directory := filepath.Join(home, ".config", "hlcp")
		p, err := planGrantClient(ctx, directory, owner, token)
		if err != nil {
			t.Fatal(err)
		}
		switch kind {
		case "directory":
			if err = os.Mkdir(filepath.Dir(directory), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(filepath.Dir(directory), "manual"), []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
		case "symlink":
			if err = os.Symlink(t.TempDir(), filepath.Dir(directory)); err != nil {
				t.Fatal(err)
			}
		case "rebound-home":
			if err = os.Rename(home, home+"-original"); err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(home + "-original")
			if err = os.Mkdir(home, 0700); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = p.Apply(ctx, true, token); !errors.Is(err, setup.ErrStalePlan) {
			t.Fatal(kind, err)
		}
		p.Close()
		if _, err = os.Stat(filepath.Join(directory, "cli.token")); !os.IsNotExist(err) {
			t.Fatal("raced target adopted")
		}
		if kind == "directory" {
			data, _ := os.ReadFile(filepath.Join(filepath.Dir(directory), "manual"))
			if string(data) != "preserve" {
				t.Fatal("manual files removed")
			}
		}
	}
	home := t.TempDir()
	parent := filepath.Join(home, ".config")
	os.Mkdir(parent, 0755)
	p, err := planGrantClient(ctx, filepath.Join(parent, "hlcp"), owner, token)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if len(p.Preview().Changes) != 3 {
		t.Fatal("existing parent proposed for repair")
	}
	if _, err = p.Apply(ctx, true, token); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(parent)
	if info.Mode().Perm() != 0755 {
		t.Fatal("existing parent chmodded")
	}
}
