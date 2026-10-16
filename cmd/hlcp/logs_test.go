package main

import (
	"bytes"
	"context"
	"encoding/json"
	"hlcp/internal/protocol"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLogStreamOutputAndIncompleteEnd(t *testing.T) {
	var wire bytes.Buffer
	for _, event := range []protocol.LogEvent{{RequestID: "r", Sequence: 1, Kind: "start"}, {RequestID: "r", Sequence: 2, Kind: "record", Record: &protocol.LogRecord{Stream: "stdout", Text: "\x1b[2JPRIVATE"}}, {RequestID: "r", Sequence: 3, Kind: "end", Code: "OK"}} {
		json.NewEncoder(&wire).Encode(logMessage{SchemaVersion: 1, LogEvent: event})
	}
	var out, diagnostic bytes.Buffer
	if code := consumeLogStream(context.Background(), bytes.NewReader(wire.Bytes()), false, &out, &diagnostic); code != 0 || strings.ContainsRune(out.String(), '\x1b') || !strings.Contains(out.String(), "PRIVATE") {
		t.Fatal(code, out.String(), diagnostic.String())
	}
	out.Reset()
	diagnostic.Reset()
	if code := consumeLogStream(context.Background(), strings.NewReader(`{"schema_version":1,"request_id":"r","sequence":1,"kind":"start","gap":false}`+"\n"), true, &out, &diagnostic); code != 5 || !strings.Contains(out.String(), "SESSION_LOST") || !strings.Contains(out.String(), `"gap":true`) {
		t.Fatal(code, out.String())
	}
	out.Reset()
	if code := consumeLogStream(context.Background(), strings.NewReader(`{"schema_version":2}`+"\n"), true, &out, &diagnostic); code != 5 || !strings.Contains(out.String(), "INVALID_SEQUENCE") {
		t.Fatal(code, out.String())
	}
}

func TestContainerLogsCLIHistoryFollowPreviewAndScriptFallback(t *testing.T) {
	token := strings.Repeat("k", 32)
	tokenFile := filepath.Join(t.TempDir(), "token")
	if os.WriteFile(tokenFile, []byte(token), 0600) != nil {
		t.Fatal("token")
	}
	for _, mode := range []string{"history", "follow", "preview", "cursor", "forbidden", "interrupted", "script", "invalid-options"} {
		t.Run(mode, func(t *testing.T) {
			var logCalls, posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+token {
					t.Error("missing credential")
				}
				if strings.HasSuffix(r.URL.Path, "/commands") {
					posts.Add(1)
					var command map[string]string
					json.NewDecoder(r.Body).Decode(&command)
					json.NewEncoder(w).Encode(map[string]any{"command_id": command["command_id"], "success": true, "code": "OK", "message": "script logs"})
					return
				}
				if !strings.Contains(r.URL.Path, "/logs") {
					kind := "container"
					if mode == "script" {
						kind = "task"
					}
					json.NewEncoder(w).Encode(map[string]any{"type": kind, "actions": []string{"logs"}})
					return
				}
				logCalls.Add(1)
				if mode == "forbidden" {
					w.WriteHeader(403)
					w.Write([]byte(`{"code":"FORBIDDEN"}`))
					return
				}
				if mode == "cursor" {
					if r.URL.Query().Get("cursor") != "opaque" || len(r.URL.Query()) != 2 {
						t.Error("cursor filters changed")
					}
				}
				if mode == "follow" || mode == "interrupted" {
					json.NewEncoder(w).Encode(logMessage{SchemaVersion: 1, LogEvent: protocol.LogEvent{RequestID: "read", Sequence: 1, Kind: "start"}})
					if mode == "interrupted" {
						return
					}
					json.NewEncoder(w).Encode(logMessage{SchemaVersion: 1, LogEvent: protocol.LogEvent{RequestID: "read", Sequence: 2, Kind: "record", Record: &protocol.LogRecord{Stream: "stderr", Text: "private sample"}}})
					json.NewEncoder(w).Encode(logMessage{SchemaVersion: 1, LogEvent: protocol.LogEvent{RequestID: "read", Sequence: 3, Kind: "end", Code: "OK"}})
					return
				}
				json.NewEncoder(w).Encode(logPage{SchemaVersion: 1, Code: "OK", Records: []protocol.LogRecord{{Stream: "stderr", Text: "private sample"}}})
			}))
			defer server.Close()
			args := []string{"-test.run=^TestCLIProcessHelper$", "--", "--api", server.URL, "--api-token-file", tokenFile, "-n", "n", "-r", "app", "-a", "logs", "--json"}
			switch mode {
			case "follow", "interrupted":
				args = append(args, "--follow", "--tail", "0")
			case "preview":
				args = append(args, "--dry-run")
			case "cursor":
				args = append(args, "--cursor", "opaque")
			case "invalid-options":
				args = append(args, "--tail", "1001")
			}
			child := exec.Command(os.Args[0], args...)
			child.Env = append(os.Environ(), "HLCP_CLI_PROCESS_HELPER=1")
			var out, diagnostic bytes.Buffer
			child.Stdout = &out
			child.Stderr = &diagnostic
			err := child.Run()
			code := 0
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			want := 0
			if mode == "invalid-options" {
				want = 2
			}
			if mode == "forbidden" {
				want = 4
			}
			if mode == "interrupted" {
				want = 5
			}
			if code != want {
				t.Fatal(code, out.String(), diagnostic.String())
			}
			if mode == "invalid-options" {
				if logCalls.Load() != 0 || posts.Load() != 0 || !json.Valid(out.Bytes()) || !strings.Contains(out.String(), "INVALID_OPTIONS") {
					t.Fatal("invalid options did not return machine error", out.String())
				}
			} else if mode == "preview" {
				if logCalls.Load() != 0 || !strings.Contains(out.String(), `"runtime_verified":false`) {
					t.Fatal("preview executed query", out.String())
				}
			} else if mode == "script" {
				if posts.Load() != 1 || logCalls.Load() != 0 {
					t.Fatal("script action compatibility", posts.Load(), logCalls.Load())
				}
			} else if logCalls.Load() != 1 || posts.Load() != 0 {
				t.Fatal("logs used execution API", posts.Load(), logCalls.Load())
			}
			if mode == "history" || mode == "cursor" {
				if !json.Valid(out.Bytes()) || !strings.Contains(out.String(), "private sample") {
					t.Fatal(out.String())
				}
			}
			if mode == "interrupted" && !strings.Contains(out.String(), "SESSION_LOST") {
				t.Fatal("lost stream falsely completed")
			}
		})
	}
}
