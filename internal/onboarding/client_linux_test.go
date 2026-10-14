package onboarding

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hlcp/internal/cli"
	"hlcp/internal/localapi"
	"hlcp/internal/setup"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func clientTestOwner(t *testing.T) setup.Owner {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("run this native integration as a registered non-root user")
	}
	owner, err := (nativeSetupHost{}).Owner()
	if err != nil {
		t.Fatal(err)
	}
	return owner
}
func TestNativeClientPreviewCreateRepeatConflict(t *testing.T) {
	owner := clientTestOwner(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "hlcp")
	token := strings.Repeat("a", 64)
	c, _ := NewClientCredential([]byte(token))
	p, err := PlanClient(ctx, dir, owner, "http://127.0.0.1:9444", c)
	if err != nil {
		t.Fatal(err)
	}
	preview := p.Preview()
	if len(preview.Changes) != 3 || preview.Changes[0].Action != "create-directory" {
		t.Fatal(preview)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("preview created directory")
	}
	if _, err = p.Apply(ctx, false); !errors.Is(err, setup.ErrConsentRequired) {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprintf("%#v", *p), token) {
		t.Fatal("plan leaked bearer")
	}
	if r, err := p.Apply(ctx, true); err != nil || r.Changed != 2 {
		t.Fatal(r, err)
	}
	p.Close()
	for _, name := range []string{"cli.json", "cli.token"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("unsafe created file")
		}
	}
	data, _ := os.ReadFile(filepath.Join(dir, "cli.json"))
	config, err := cli.ParseClientConfig(data, filepath.Join(dir, "cli.json"))
	if err != nil {
		t.Fatal(err)
	}
	if value, err := localapi.LoadToken(config.TokenFile); err != nil || value != token {
		t.Fatal("runtime credential incompatibility")
	}
	p, err = PlanClient(ctx, dir, owner, "http://127.0.0.1:9444", c)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if r, err := p.Apply(ctx, true); err != nil || r.Changed != 0 {
		t.Fatal(r, err)
	}
	changed, _ := NewClientCredential([]byte(strings.Repeat("b", 64)))
	if _, err := PlanClient(ctx, dir, owner, "http://127.0.0.1:9444", changed); !errors.Is(err, setup.ErrExistingConflict) {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "cli.json"))
	if !bytes.Equal(after, data) {
		t.Fatal("existing profile replaced")
	}
	if value, _ := localapi.LoadToken(config.TokenFile); value != token {
		t.Fatal("token rotated")
	}
}
func TestNativeClientPartialUnsafeAndRacedTarget(t *testing.T) {
	owner := clientTestOwner(t)
	ctx := context.Background()
	c, _ := NewClientCredential([]byte(strings.Repeat("a", 64)))
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	if err := os.WriteFile(filepath.Join(dir, "cli.token"), c.token, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanClient(ctx, dir, owner, "http://127.0.0.1:9444", c); !errors.Is(err, setup.ErrRecoveryRequired) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cli.json")); !os.IsNotExist(err) {
		t.Fatal("partial profile repaired")
	}
	dir = filepath.Join(t.TempDir(), "hlcp")
	p, err := PlanClient(ctx, dir, owner, "http://127.0.0.1:9444", c)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Apply(ctx, true); !errors.Is(err, setup.ErrStalePlan) {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "cli.token")); !os.IsNotExist(err) {
		t.Fatal("raced directory adopted")
	}
	unsafe := t.TempDir()
	os.Chmod(unsafe, 0755)
	if _, err := PlanClient(ctx, unsafe, owner, "http://127.0.0.1:9444", c); err == nil {
		t.Fatal("unsafe target accepted")
	}
	wrong := owner
	wrong.GID++
	if _, err := PlanClient(ctx, filepath.Join(t.TempDir(), "hlcp"), wrong, "http://127.0.0.1:9444", c); !errors.Is(err, ErrClientOptions) {
		t.Fatal(err)
	}
}
