package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hlcp/internal/protocol"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBoundedRingAndFixedCardinality(t *testing.T) {
	c := New(nil)
	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := 0; i < 1000; i++ {
				c.Record(Event{Kind: CommandResult, NodeID: fmt.Sprint(i), CommandID: fmt.Sprint(i), Code: "OK", DurationMillis: 7})
				_ = c.Snapshot()
				_ = c.Events()
			}
		}()
	}
	workers.Wait()
	s := c.Snapshot()
	if s.Counters["command_result"] != 8000 || len(s.Counters) != int(kindCount)+9 || s.Counters["result_duration_sum_ms"] != 56000 || s.Counters["result_duration_max_ms"] != 7 {
		t.Fatalf("unexpected fixed counters: %+v", s)
	}
	events := c.Events()
	if len(events) != BufferCapacity {
		t.Fatalf("buffer grew to %d", len(events))
	}
	for i := 1; i < len(events); i++ {
		if events[i].At.IsZero() {
			t.Fatal("missing event timestamp")
		}
	}
	events[0].Code = "tampered"
	if c.Events()[0].Code == "tampered" {
		t.Fatal("Events aliases internal buffer")
	}
	fresh := New(nil).Snapshot()
	if fresh.Counters["command_result"] != 0 || fresh.StartedAt.Before(s.StartedAt) {
		t.Fatal("metrics leaked across lifetimes")
	}
}

func TestBlockedSinkCannotBlockRecordOrClose(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	c := New(func(context.Context, Event) error { once.Do(func() { close(entered) }); <-release; return nil })
	defer close(release)
	c.Record(Event{Kind: Authenticated})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("sink never entered")
	}
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10000; i++ {
			c.Record(Event{Kind: CommandACK})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked sink blocked admission")
	}
	if c.Snapshot().Counters["audit_dropped"] == 0 || len(c.Events()) != BufferCapacity {
		t.Fatal("flood was not bounded")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := c.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close=%v", err)
	}
	c.Record(Event{Kind: CommandACK}) // Closing diagnostics is not closing command admission.
	if c.Snapshot().Counters["command_ack"] != 10001 {
		t.Fatal("counter lost after close")
	}
}

func TestSinkFailureAndCancellation(t *testing.T) {
	called := make(chan struct{})
	c := New(func(ctx context.Context, _ Event) error {
		close(called)
		<-ctx.Done()
		return errors.New("secret disk error")
	})
	c.Record(Event{Kind: SessionStarted})
	<-called
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if c.Snapshot().Counters["audit_sink_errors"] != 1 {
		t.Fatal("sink failure not counted")
	}
}

func TestFieldsAreBoundedAndOutcomeIsControlled(t *testing.T) {
	c := New(nil)
	c.Record(Event{Kind: CommandResult, Code: Outcome("secret-result-code"), CommandID: strings.Repeat("x", 1<<20), ActorID: "actor\n\x1b", Name: "forged", DurationMillis: -1})
	e := c.Events()[0]
	if len(e.CommandID) != MaxFieldBytes || e.ActorID != "actor??" || e.Name != "command_result" || e.Code != "OTHER_RESULT" || e.DurationMillis != 0 {
		t.Fatalf("event=%+v", e)
	}
	var buffer bytes.Buffer
	if err := JSONSink(&buffer)(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	var decoded Event
	if err := json.Unmarshal(buffer.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buffer.String(), "secret-result-code") || strings.Contains(buffer.String(), "forged") {
		t.Fatal("unsafe fields escaped")
	}
	c.Record(Event{Kind: 255})
	if len(c.Events()) != 1 {
		t.Fatal("unknown kind accepted")
	}
	var disabled *Collector
	disabled.Record(e)
	disabled.SetGauge(Sessions, 2)
	_ = disabled.Snapshot()
	_ = disabled.Events()
	if err := disabled.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type memoryConn struct {
	net.Conn
	buffer    bytes.Buffer
	failWrite bool
}

func (c *memoryConn) Read(p []byte) (int, error) { return c.buffer.Read(p) }
func (c *memoryConn) Write(p []byte) (int, error) {
	if c.failWrite {
		n, _ := c.buffer.Write(p[:3])
		return n, io.ErrClosedPipe
	}
	return c.buffer.Write(p)
}

func TestFramesAndPartialBytes(t *testing.T) {
	c := New(nil)
	raw := &memoryConn{}
	conn := Wrap(raw, c)
	frame, err := protocol.NewFrame(protocol.MessageHeartbeat, 1, []byte{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := protocol.WriteFrame(conn, frame); err != nil {
		t.Fatal(err)
	}
	if _, err := protocol.Decode(conn); err != nil {
		t.Fatal(err)
	}
	raw.failWrite = true
	if err := protocol.WriteFrame(conn, frame); err == nil {
		t.Fatal("failed write succeeded")
	}
	if _, err := protocol.Decode(conn); err == nil {
		t.Fatal("partial frame succeeded")
	}
	s := c.Snapshot().Counters
	if s["written_frames"] != 1 || s["read_frames"] != 1 || s["written_bytes"] != 23 || s["read_bytes"] != 23 {
		t.Fatalf("I/O counters=%v", s)
	}
}
