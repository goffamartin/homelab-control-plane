package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
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

func TestCLIProcessHelper(t *testing.T) {
	if os.Getenv("HLCP_CLI_PROCESS_HELPER") != "1" {
		return
	}
	for index, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"hlcp"}, os.Args[index+1:]...)
			main()
			os.Exit(0)
		}
	}
	t.Fatal("missing CLI helper arguments")
}

func TestCLIUsesServerDryRunAndResourceDetail(t *testing.T) {
	for _, test := range []struct {
		name               string
		args               []string
		method, path, body string
		status             int
		failed             bool
	}{
		{name: "dry-run", args: []string{"-n", "n", "-r", "catalog", "-a", "restart", "--dry-run"}, method: "POST", path: "/v1/nodes/n/resolve", status: 200, body: `{"node":"n","resource":"catalog","action":"restart","known_operation":false}`},
		{name: "missing-resource", args: []string{"-n", "n", "-r", "missing", "-a", "restart", "--dry-run"}, method: "POST", path: "/v1/nodes/n/resolve", status: 404, body: `{"code":"RESOURCE_NOT_FOUND","message":"resource not found"}`, failed: true},
		{name: "resource-detail", args: []string{"-n", "n", "-r", "catalog"}, method: "GET", path: "/v1/nodes/n/resources/catalog", status: 200, body: `{"id":"catalog","type":"container","observation":{"state":"stopped","stale":true}}`},
		{name: "unsupported-node-action", args: []string{"-n", "n", "-a", "restart"}, method: "POST", path: "/v1/nodes/n/resolve", status: 422, body: `{"code":"ACTION_NOT_SUPPORTED","message":"only node status is supported"}`, failed: true},
		{name: "node-status-dry-run", args: []string{"-n", "n", "-a", "status", "--dry-run"}, method: "POST", path: "/v1/nodes/n/resolve", status: 200, body: `{"node":"n","action":"status","read_only":true}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			token := "01234567890123456789012345678901"
			file := filepath.Join(t.TempDir(), "api.token")
			if err := os.WriteFile(file, []byte(token), 0600); err != nil {
				t.Fatal(err)
			}
			var called atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called.Add(1)
				if r.Method != test.method || r.URL.Path != test.path || r.Header.Get("Authorization") != "Bearer "+token {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if r.Method == "POST" {
					var payload map[string]string
					if json.NewDecoder(r.Body).Decode(&payload) != nil || payload["action"] == "" {
						t.Error("invalid resolve body")
					}
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				w.Write([]byte(test.body))
			}))
			defer server.Close()
			args := append([]string{"-test.run=^TestCLIProcessHelper$", "--", "--api", server.URL, "--api-token-file", file}, test.args...)
			child := exec.Command(os.Args[0], args...)
			child.Env = append(os.Environ(), "HLCP_CLI_PROCESS_HELPER=1")
			output, err := child.CombinedOutput()
			if (err != nil) != test.failed || called.Load() != 1 {
				t.Fatalf("err=%v requests=%d output=%s", err, called.Load(), output)
			}
			if !test.failed && !strings.Contains(string(output), "catalog") && test.name != "node-status-dry-run" {
				t.Fatalf("output=%s", output)
			}
		})
	}
}
