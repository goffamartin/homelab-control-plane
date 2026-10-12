package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

const policyJSON = `{"version":1,"principals":[{"id":"mcp","role":"observer","token_file":"observer.token","scope":{"nodes":{"node":{"resources":["catalog"]}}}}]}`

func policyFixture(t *testing.T) (string, *Authorizer) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	for name, data := range map[string]string{"policy.json": policyJSON, "observer.token": strings.Repeat("a", 32)} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p, err := LoadPolicy(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, NewAuthorizer(p)
}

func TestDuplicatePrincipalOrCredentialAndPrivateToken(t *testing.T) {
	for _, sameID := range []bool{true, false} {
		path, _ := policyFixture(t)
		var document struct {
			Version    int             `json:"version"`
			Principals []principalSpec `json:"principals"`
		}
		if err := json.Unmarshal([]byte(policyJSON), &document); err != nil {
			t.Fatal(err)
		}
		second := document.Principals[0]
		if !sameID {
			second.ID = "other"
		}
		document.Principals = append(document.Principals, second)
		data, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadPolicy(path); err == nil {
			t.Fatal("duplicate principal or credential accepted")
		}
	}
	if runtime.GOOS != "windows" {
		path, _ := policyFixture(t)
		if err := os.Chmod(filepath.Join(filepath.Dir(path), "observer.token"), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadPolicy(path); err == nil {
			t.Fatal("public token accepted")
		}
	}
}
func authorizedContext(t *testing.T, a *Authorizer) context.Context {
	t.Helper()
	var ctx context.Context
	h := ProtectPolicy(a, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ctx = r.Context() }), nil)
	r := httptest.NewRequest("GET", "http://127.0.0.1/v1/nodes", nil)
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 32))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if ctx == nil {
		t.Fatalf("auth=%d", w.Code)
	}
	return ctx
}

func TestStrictPolicyLoading(t *testing.T) {
	for _, body := range []string{
		strings.Replace(policyJSON, `"observer"`, `"admin"`, 1),
		strings.Replace(policyJSON, `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(policyJSON, `"role":"observer"`, `"role":"observer","ROLE":"operator"`, 1),
		strings.Replace(policyJSON, `"resources":["catalog"]`, `"resources":["catalog","catalog"]`, 1),
		strings.Replace(policyJSON, `"resources":["catalog"]`, `"all_resources":true,"resources":["catalog"]`, 1),
		strings.Replace(policyJSON, `"nodes":`, `"all_nodes":true,"nodes":`, 1),
		strings.Replace(policyJSON, `"token_file"`, `"token"`, 1),
		policyJSON + `{}`, `null`, strings.Repeat(" ", maxPolicyBytes) + policyJSON,
	} {
		path, _ := policyFixture(t)
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadPolicy(path); err == nil || strings.Contains(err.Error(), "observer.token") {
			t.Fatalf("invalid policy accepted or leaked filename: %v", err)
		}
	}
	path, _ := policyFixture(t)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadPolicy(path); err == nil {
			t.Fatal("public policy accepted")
		}
	}
}

func TestAuthenticationScopeAndSpoofedHeaders(t *testing.T) {
	_, a := policyFixture(t)
	ctx := authorizedContext(t, a)
	if Identity(ctx) != "mcp" || !Allowed(ctx, "node", "catalog", false) || Allowed(ctx, "node", "other", false) || Allowed(ctx, "other", "", false) || Allowed(ctx, "node", "catalog", true) {
		t.Fatal("scope escaped")
	}
	called := false
	err := GuardAdmission(ctx, "node", "catalog", func() error { called = true; return nil })
	if !errors.Is(err, ErrForbidden) || called {
		t.Fatal("observer admitted")
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1/v1/nodes", nil)
	r.Header.Add("Authorization", "Bearer "+strings.Repeat("a", 32))
	r.Header.Add("Authorization", "Bearer "+strings.Repeat("a", 32))
	r.Header.Set("X-Role", "operator")
	w := httptest.NewRecorder()
	ProtectPolicy(a, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("duplicate Authorization accepted") }), nil).ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
}

func TestReloadInvalidatesUnadmittedSnapshotsAndPreservesLastGoodPolicy(t *testing.T) {
	path, a := policyFixture(t)
	operator := strings.Replace(policyJSON, `"observer"`, `"operator"`, 1)
	if err := os.WriteFile(path, []byte(operator), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.Reload(path); err != nil {
		t.Fatal(err)
	}
	ctx := authorizedContext(t, a)
	if err := GuardAdmission(ctx, "node", "catalog", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("broken secret policy"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.Reload(path); err == nil {
		t.Fatal("invalid reload accepted")
	}
	if err := GuardAdmission(ctx, "node", "catalog", func() error { return nil }); err != nil {
		t.Fatal("failed reload replaced active policy")
	}
	if err := os.WriteFile(path, []byte(operator), 0600); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 100; j++ {
				_ = GuardAdmission(ctx, "node", "catalog", func() error { return nil })
			}
		}()
	}
	if err := a.Reload(path); err != nil {
		t.Fatal(err)
	}
	workers.Wait()
	called := false
	if err := GuardAdmission(ctx, "node", "catalog", func() error { called = true; return nil }); !errors.Is(err, ErrForbidden) || called {
		t.Fatal("old snapshot admitted after reload")
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "observer.token"), []byte(strings.Repeat("b", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.Reload(path); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1/v1/nodes", nil)
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 32))
	if a.authenticate(r) != nil {
		t.Fatal("rotated token remained active")
	}
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("b", 32))
	if a.authenticate(r) == nil {
		t.Fatal("new token inactive")
	}
}
