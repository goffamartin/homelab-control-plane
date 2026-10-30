package main

import (
	"bytes"
	"encoding/json"
	"hlcp/internal/localapi"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCLITagFilterTransportAndOldControllerRefusal(t *testing.T) {
	for _, row := range []struct {
		name   string
		status int
		header bool
		args   []string
		code   int
	}{
		{"json", 200, true, []string{"-t", "kostka", "--tag", "production", "--json"}, 0},
		{"text", 200, true, []string{"-t", "kostka", "--tag", "production"}, 0},
		{"old-controller", 200, false, []string{"-t", "kostka", "--tag", "production"}, 4},
		{"old-controller-json", 200, false, []string{"-t", "kostka", "--tag", "production", "--json"}, 4},
		{"denied", 403, false, []string{"-t", "kostka", "--tag", "production"}, 2},
		{"mutation-refused", 200, true, []string{"-t", "kostka", "-a", "restart"}, 2},
	} {
		t.Run(row.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "api.token")
			token := strings.Repeat("t", 32)
			if err := os.WriteFile(file, []byte(token), 0600); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "GET" || r.URL.Path != "/v1/nodes/n/resources" || r.Header.Get("Authorization") != "Bearer "+token || !reflect.DeepEqual(r.URL.Query()["tag"], []string{"kostka", "production"}) {
					t.Error("invalid or mutating tag request", r.Method, r.URL)
				}
				if row.header {
					w.Header().Set(localapi.ResourceTagsHeader, "1")
				}
				w.WriteHeader(row.status)
				if row.status == 200 {
					w.Write([]byte(`[{"id":"app","type":"container","tags":["kostka","production"],"actions":["status"]}]`))
				} else {
					w.Write([]byte(`{"code":"FORBIDDEN","message":"forbidden"}`))
				}
			}))
			defer server.Close()
			args := []string{"-test.run=^TestCLIProcessHelper$", "--", "--api", server.URL, "--api-token-file", file, "-n", "n", "-r"}
			child := exec.Command(os.Args[0], append(args, row.args...)...)
			child.Env = append(os.Environ(), "HLCP_CLI_PROCESS_HELPER=1")
			var out, stderr bytes.Buffer
			child.Stdout, child.Stderr = &out, &stderr
			err := child.Run()
			code := 0
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					code = exit.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
			if code != row.code {
				t.Fatal(code, out.String(), stderr.String())
			}
			wantCalls := int32(1)
			if row.name == "mutation-refused" {
				wantCalls = 0
			}
			if calls.Load() != wantCalls {
				t.Fatal("unexpected request count", calls.Load())
			}
			if row.code == 0 && !strings.Contains(out.String(), "production") {
				t.Fatal("tags missing from output", out.String())
			}
			if strings.HasPrefix(row.name, "old-controller") && (strings.HasPrefix(strings.TrimSpace(out.String()), "[") || !strings.Contains(stderr.String()+out.String(), "does not support")) {
				t.Fatal("silently returned an unfiltered list", out.String(), stderr.String())
			}
			if strings.Contains(row.name, "json") && !json.Valid(out.Bytes()) {
				t.Fatal("invalid JSON output", out.String())
			}
			if row.name == "text" && !strings.Contains(out.String(), "TAGS") {
				t.Fatal("missing tags column", out.String())
			}
		})
	}
}

func TestTagCompletionUsesScopedCatalogAndQuietLegacyFailure(t *testing.T) {
	file := filepath.Join(t.TempDir(), "api.token")
	if err := os.WriteFile(file, []byte(strings.Repeat("t", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, status := range []int{200, 403, 404} {
		var calls int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.Method != "GET" || r.URL.Path != "/v1/nodes/n/tags" || r.URL.Query().Get("tag") != "kostka" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("t", 32) {
				t.Error("wrong catalog request", r.Method, r.URL)
			}
			w.WriteHeader(status)
			w.Write([]byte(`["kostka","production","production","$(whoami)","bad\nword"]`))
		}))
		var out bytes.Buffer
		completeHLCP([]string{"--api", server.URL, "--api-token-file", file, "-n", "n", "-r", "-t", "kostka", "--tag", ""}, &out)
		server.Close()
		want := ""
		if status == 200 {
			want = "production\n"
		}
		if calls != 1 || out.String() != want {
			t.Fatal(status, calls, out.String())
		}
	}
}
