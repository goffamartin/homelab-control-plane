package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCLIAuthenticatesAndDoesNotFollowRedirects(t *testing.T) {
	token := "01234567890123456789012345678901"
	file := filepath.Join(t.TempDir(), "api.token")
	if err := os.WriteFile(file, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := apiTokenFile
	apiTokenFile = file
	defer func() { apiTokenFile = previous }()
	targetCalled := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalled = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("missing bearer credentials")
		}
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	_, err := authenticatedRequest(http.MethodGet, server.URL, nil)
	if err == nil || targetCalled {
		t.Fatalf("redirect followed=%v err=%v", targetCalled, err)
	}
}
