package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCompletionUsesOnlyScopedReadAPIAndPrivateProfile(t *testing.T) {
	token := strings.Repeat("t", 32)
	tokenFile := filepath.Join(t.TempDir(), "api.token")
	if os.WriteFile(tokenFile, []byte(token), 0600) != nil {
		t.Fatal("token")
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("completion sent mutating or unauthenticated request")
		}
		switch r.URL.Path {
		case "/v1/nodes":
			w.Write([]byte(`[{"node_id":"debian-01"},{"node_id":"bad\nword"}]`))
		case "/v1/nodes/debian-01/resources":
			w.Write([]byte(`[{"id":"app"}]`))
		case "/v1/nodes/debian-01/resources/app/actions":
			w.Write([]byte(`["status","restart","$(touch injected)"]`))
		default:
			t.Error(r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	prefix := []string{"--api", server.URL, "--api-token-file", tokenFile}
	for _, row := range []struct {
		args []string
		want string
	}{
		{[]string{"-n", "de"}, "debian-01\n"},
		{[]string{"-n", "debian-01", "-r", ""}, "app\n"},
		{[]string{"-n", "debian-01", "-r", "app", "-a", ""}, "status\nrestart\n"},
	} {
		var out bytes.Buffer
		completeHLCP(append(append([]string{}, prefix...), row.args...), &out)
		if out.String() != row.want {
			t.Fatal(out.String(), row.want)
		}
	}
	if requests.Load() != 3 {
		t.Fatal(requests.Load())
	}
	var out bytes.Buffer
	completeHLCP(append(prefix, "--j"), &out)
	if out.String() != "--json\n" || requests.Load() != 3 {
		t.Fatal("static completion contacted API", out.String())
	}
}
func TestCompletionQuietTimeoutRedirectAndForbidden(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "api.token")
	os.WriteFile(tokenFile, []byte(strings.Repeat("x", 32)), 0600)
	for _, mode := range []string{"timeout", "forbidden", "redirect", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			var seen atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen.Add(1)
				switch mode {
				case "timeout":
					<-r.Context().Done()
				case "forbidden":
					w.WriteHeader(403)
				case "redirect":
					http.Redirect(w, r, "/redirect", http.StatusFound)
				case "oversize":
					w.Write([]byte(strings.Repeat("x", (256<<10)+1)))
				}
			}))
			defer server.Close()
			var out bytes.Buffer
			started := time.Now()
			completeHLCP([]string{"--api", server.URL, "--api-token-file", tokenFile, "-n", ""}, &out)
			if out.Len() != 0 || seen.Load() != 1 || time.Since(started) > 2*time.Second {
				t.Fatal(mode, out.String(), seen.Load(), time.Since(started))
			}
		})
	}
}
