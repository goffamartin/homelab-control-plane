package main

import (
	"context"
	"crypto/tls"
	"hlcp/internal/controller"
	"hlcp/internal/liveness"
	"hlcp/internal/telemetry"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestServeProcessesStopsAndPropagatesAPIFailure(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "signal", true: "api-failure"}[failed], func(t *testing.T) {
			server, err := controller.Listen("127.0.0.1:0", controller.Config{TLSConfig: &tls.Config{}, LivenessPolicy: liveness.Policy{HeartbeatInterval: time.Second, DegradedAfter: 2, OfflineAfter: 3}})
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if failed {
				listener.Close()
			} else {
				cancel()
			}
			done := make(chan error, 1)
			go func() { done <- serveProcesses(ctx, server, &http.Server{Handler: http.NewServeMux()}, listener) }()
			select {
			case err := <-done:
				if failed != (err != nil) {
					t.Fatalf("failed=%v err=%v", failed, err)
				}
			case <-time.After(time.Second):
				t.Fatal("process shutdown blocked")
			}
		})
	}
}

func TestPolicyModeIsExclusiveAndReloadWorkerJoins(t *testing.T) {
	if _, err := loadAPIAuthorizer("policy.json", "token", true); err == nil {
		t.Fatal("ambiguous credential modes accepted")
	}
	dir := t.TempDir()
	token := filepath.Join(dir, "api.token")
	if err := os.WriteFile(token, []byte(strings.Repeat("a", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAPIAuthorizer("", token, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"principals":[{"id":"mcp","role":"observer","token_file":"api.token","scope":{"all_nodes":true}}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := loadAPIAuthorizer(path, "nonexistent-default-token", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 1)
	done := make(chan struct{})
	diagnostics := telemetry.New(nil)
	go func() { defer close(done); runPolicyReloads(ctx, signals, a, path, diagnostics) }()
	signals <- syscall.SIGHUP
	deadline := time.Now().Add(time.Second)
	for diagnostics.Snapshot().Counters["policy_reload"] == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if diagnostics.Snapshot().Counters["policy_reload"] != 1 {
		cancel()
		t.Fatal("reload signal ignored")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reload worker did not join")
	}
}
