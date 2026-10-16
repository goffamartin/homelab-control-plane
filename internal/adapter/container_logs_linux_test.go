package adapter

import (
	"context"
	"errors"
	"hlcp/internal/protocol"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLogProcessFiltersBoundsAndCancellation(t *testing.T) {
	q := protocol.DefaultLogQuery()
	q.Contains = "keep"
	q.Stream = "stderr"
	var records []protocol.LogRecord
	code := containerLogsProcess(context.Background(), "/bin/sh", []string{"-c", "printf '2026-10-16T00:00:00Z discard\\n';printf '2026-10-16T00:00:01Z keep-secret\\n' >&2"}, q, func(r protocol.LogRecord) error { records = append(records, r); return nil })
	if code != "OK" || len(records) != 1 || records[0].Stream != "stderr" || records[0].Text != "keep-secret" || records[0].Timestamp != "2026-10-16T00:00:01Z" {
		t.Fatal(code, records)
	}
	q = protocol.DefaultLogQuery()
	records = nil
	code = containerLogsProcess(context.Background(), "/bin/sh", []string{"-c", "head -c 20000 /dev/zero | tr '\\000' x;printf '\\n'"}, q, func(r protocol.LogRecord) error { records = append(records, r); return nil })
	if code != "OK" || len(records) != 1 || !records[0].Truncated || len(records[0].Text) > protocol.MaxLogText {
		t.Fatal(code, len(records))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	code = containerLogsProcess(ctx, "/bin/sh", []string{"-c", "while :;do printf 'line\\n';sleep 0.05;done"}, q, func(protocol.LogRecord) error { return nil })
	if code != "TIME_LIMIT" || time.Since(started) > 2*time.Second {
		t.Fatal("cancellation did not reap log process", code)
	}
	code = containerLogsProcess(context.Background(), "/bin/sh", []string{"-c", "printf 'line\\n';while :;do sleep 0.05;done"}, q, func(protocol.LogRecord) error { return errors.New("slow consumer") })
	if code != "BACKPRESSURE" {
		t.Fatal(code)
	}
	code = containerLogsProcess(context.Background(), "/bin/sh", []string{"-c", "exit 1"}, q, func(protocol.LogRecord) error { return nil })
	if code != "RUNTIME_FAILED" {
		t.Fatal(code)
	}
}
func TestLogOutputSourceBudgetAndUTF8(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var bytes atomic.Int64
	var limited atomic.Bool
	output := &logOutput{ctx: ctx, cancel: cancel, records: make(chan protocol.LogRecord, 2), query: protocol.DefaultLogQuery(), stream: "stdout", bytes: &bytes, limited: &limited}
	output.Write([]byte("bad\xff\x00\n"))
	record := <-output.records
	if strings.ContainsRune(record.Text, 0) || (protocol.LogEvent{RequestID: "read", Sequence: 2, Kind: "record", Record: &record}).Validate() != nil {
		t.Fatal(record)
	}
	bytes.Store(8 << 20)
	if _, err := output.Write([]byte("x")); err == nil || !limited.Load() || ctx.Err() == nil {
		t.Fatal("source byte budget ignored")
	}
}
