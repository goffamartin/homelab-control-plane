package main

import (
	"bytes"
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

func TestCLIOutputAndExitContracts(t *testing.T) {
	for _, test := range []struct {
		name                    string
		args                    []string
		status                  int
		body, path              string
		code                    int
		json, submit, noRequest bool
	}{
		{name: "text-list", args: []string{"-n"}, status: 200, body: `[{"node_id":"n","state":"offline"}]`, path: "/v1/nodes"},
		{name: "json-list", args: []string{"--nodes", "--json"}, status: 200, body: `[{"node_id":"n","state":"offline"}]`, path: "/v1/nodes", json: true},
		{name: "json-precision", args: []string{"-n", "n", "--json"}, status: 200, body: `{"node_id":"n","state_revision":18446744073709551615}`, path: "/v1/nodes/n", json: true},
		{name: "success", args: []string{"-n", "n", "-r", "r", "-a", "restart", "--command-id", "op-1", "--json"}, status: 200, body: `{"command_id":"op-1","success":true,"code":"OK"}`, path: "/v1/nodes/n/commands", json: true, submit: true},
		{name: "failed-action", args: []string{"-n", "n", "-r", "r", "-a", "restart", "--command-id", "op-1", "--json"}, status: 200, body: `{"command_id":"op-1","success":false,"code":"EXECUTION_FAILED"}`, path: "/v1/nodes/n/commands", code: 3, json: true, submit: true},
		{name: "unknown-action", args: []string{"-n", "n", "-r", "r", "-a", "restart", "--command-id", "op-1"}, status: 200, body: `{"command_id":"op-1","success":false,"code":"EXECUTION_UNKNOWN"}`, path: "/v1/nodes/n/commands", code: 5, submit: true},
		{name: "timeout", args: []string{"-n", "n", "-r", "r", "-a", "restart", "--command-id", "op-1"}, status: 504, body: `{"code":"RESULT_TIMEOUT","message":"result timeout"}`, path: "/v1/nodes/n/commands", code: 5, submit: true},
		{name: "rejected", args: []string{"-n", "n", "-r", "missing"}, status: 404, body: `{"code":"RESOURCE_NOT_FOUND","message":"missing"}`, path: "/v1/nodes/n/resources/missing", code: 2},
		{name: "lookup-success", args: []string{"-n", "n", "--result", "op-1", "--json"}, status: 200, body: `{"state":"completed","result":{"success":true,"code":"OK"}}`, path: "/v1/nodes/n/commands/op-1", json: true},
		{name: "lookup-pending", args: []string{"-n", "n", "--result", "op-1", "--json"}, status: 200, body: `{"state":"running"}`, path: "/v1/nodes/n/commands/op-1", code: 5, json: true},
		{name: "bad-json", args: []string{"-n"}, status: 200, body: `not json`, path: "/v1/nodes", code: 4},
		{name: "conflict", args: []string{"--nodes", "--node", "n"}, code: 2, noRequest: true},
		{name: "help", args: []string{"--help"}, noRequest: true},
		{name: "version", args: []string{"--version", "--json"}, noRequest: true, json: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "api.token")
			if err := os.WriteFile(file, []byte("01234567890123456789012345678901"), 0600); err != nil {
				t.Fatal(err)
			}
			var called atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called.Add(1)
				if r.URL.Path != test.path {
					t.Errorf("path=%s want=%s", r.URL.Path, test.path)
				}
				if test.submit {
					var payload map[string]string
					if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&payload) != nil || payload["command_id"] != "op-1" {
						t.Error("submission lost stable ID")
					}
				} else if r.Method != "GET" {
					t.Error("lookup/discovery must be read-only")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				w.Write([]byte(test.body))
			}))
			defer server.Close()
			args := append([]string{"-test.run=^TestCLIProcessHelper$", "--", "--api", server.URL, "--api-token-file", file}, test.args...)
			child := exec.Command(os.Args[0], args...)
			child.Env = append(os.Environ(), "HLCP_CLI_PROCESS_HELPER=1")
			var stdout, stderr bytes.Buffer
			child.Stdout, child.Stderr = &stdout, &stderr
			err := child.Run()
			code := 0
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					code = exit.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
			if code != test.code {
				t.Fatalf("exit=%d want=%d stdout=%s stderr=%s", code, test.code, stdout.String(), stderr.String())
			}
			wantCalls := int32(1)
			if test.noRequest {
				wantCalls = 0
			}
			if called.Load() != wantCalls {
				t.Fatalf("requests=%d want=%d", called.Load(), wantCalls)
			}
			if test.json && !json.Valid(stdout.Bytes()) {
				t.Fatalf("invalid JSON stdout: %s", stdout.String())
			}
			if test.name == "json-precision" && !strings.Contains(stdout.String(), "18446744073709551615") {
				t.Fatal("JSON integer precision lost")
			}
			if test.submit && !strings.Contains(stderr.String(), "command_id: op-1") {
				t.Fatal("ID missing on stderr")
			}
			if test.status >= 400 && stdout.Len() != 0 {
				t.Fatal("API error leaked onto stdout")
			}
			if test.name == "text-list" && (json.Valid(stdout.Bytes()) || !strings.Contains(stdout.String(), "offline")) {
				t.Fatalf("invalid text output: %s", stdout.String())
			}
		})
	}
}

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

func TestCLIConnectionErrorExit(t *testing.T) {
	file := filepath.Join(t.TempDir(), "api.token")
	if err := os.WriteFile(file, []byte("01234567890123456789012345678901"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := server.URL
	server.Close()
	child := exec.Command(os.Args[0], "-test.run=^TestCLIProcessHelper$", "--", "--api", address, "--api-token-file", file, "--nodes")
	child.Env = append(os.Environ(), "HLCP_CLI_PROCESS_HELPER=1")
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	err := child.Run()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 4 || stdout.Len() != 0 || stderr.Len() == 0 {
		t.Fatalf("err=%v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
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
