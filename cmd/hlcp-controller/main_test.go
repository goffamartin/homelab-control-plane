package main

import (
	"context"
	"crypto/tls"
	"hlcp/internal/controller"
	"hlcp/internal/liveness"
	"net"
	"net/http"
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
