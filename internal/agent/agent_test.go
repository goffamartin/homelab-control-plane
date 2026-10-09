package agent

import (
	"context"
	"crypto/tls"
	"hlcp/internal/session"
	"net"
	"testing"
	"time"
)

func TestReconnectDelayIsBoundedAndJittered(t *testing.T) {
	if got := reconnectDelay(0, 0); got != 800*time.Millisecond {
		t.Fatalf("initial delay = %v", got)
	}
	if got := reconnectDelay(0, 1); got != 1200*time.Millisecond {
		t.Fatalf("initial delay = %v", got)
	}
	if got := reconnectDelay(20, 1); got != 30*time.Second {
		t.Fatalf("capped delay = %v", got)
	}
}

func TestTLSBlackholeStopsWithContext(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		connection, err := listener.Accept()
		if err == nil {
			accepted <- connection
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := runSession(ctx, Config{ControllerAddress: listener.Addr().String()}, &tls.Config{ServerName: "controller.test"}, session.Synchronization{}, nil)
		done <- err
	}()
	select {
	case connection := <-accepted:
		defer connection.Close()
	case <-time.After(time.Second):
		t.Fatal("no TCP connection")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("TLS blackhole unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("TLS handshake ignored cancellation")
	}
}

func TestWaitForReconnectStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitForReconnect(ctx, time.Hour) {
		t.Fatal("canceled context must not wait for reconnect")
	}
}
