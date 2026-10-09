package agent

import (
	"context"
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
	if got := reconnectDelay(20, 1); got != 36*time.Second {
		t.Fatalf("capped delay = %v", got)
	}
}

func TestWaitForReconnectStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitForReconnect(ctx, time.Hour) {
		t.Fatal("canceled context must not wait for reconnect")
	}
}
