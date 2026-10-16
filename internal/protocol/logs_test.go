package protocol

import (
	"strings"
	"testing"
)

func TestLogContractBoundsAndCBOR(t *testing.T) {
	valid := DefaultLogQuery()
	valid.Since = "2026-10-16T00:00:00Z"
	valid.Until = "2026-10-16T01:00:00Z"
	request := LogRequest{RequestID: "read-1", ResourceID: "app", Query: valid}
	data, err := EncodePayload(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded LogRequest
	if DecodePayload(data, &decoded) != nil || decoded != request || decoded.Validate() != nil {
		t.Fatal(decoded)
	}
	for _, mutate := range []func(*LogQuery){func(q *LogQuery) { q.Tail = -1 }, func(q *LogQuery) { q.Tail = 1001 }, func(q *LogQuery) { q.Since = "--details" }, func(q *LogQuery) { q.Since = q.Until }, func(q *LogQuery) { q.Stream = "shell" }, func(q *LogQuery) { q.Contains = strings.Repeat("x", 257) }, func(q *LogQuery) { q.Contains = "\x00" }} {
		q := valid
		mutate(&q)
		if q.Validate() == nil {
			t.Fatal("invalid query accepted", q)
		}
	}
	for _, event := range []LogEvent{{RequestID: "read-1", Sequence: 1, Kind: "start"}, {RequestID: "read-1", Sequence: 2, Kind: "record", Record: &LogRecord{Stream: "stdout", Timestamp: "2026-10-16T00:00:00Z", Text: "line"}}, {RequestID: "read-1", Sequence: 3, Kind: "end", Code: "OK"}} {
		if event.Validate() != nil {
			t.Fatal(event)
		}
		raw, _ := EncodePayload(event)
		var round LogEvent
		if DecodePayload(raw, &round) != nil || round.Validate() != nil {
			t.Fatal(round)
		}
	}
	for _, event := range []LogEvent{{RequestID: "read-1", Sequence: 0, Kind: "end", Code: "OK"}, {RequestID: "read-1", Sequence: 1, Kind: "exec"}, {RequestID: "read-1", Sequence: 1, Kind: "record", Record: &LogRecord{Stream: "stdout", Text: strings.Repeat("x", MaxLogText+1)}}, {RequestID: "read-1", Sequence: 1, Kind: "end", Code: "SECRET arbitrary cause"}} {
		if event.Validate() == nil {
			t.Fatal("invalid event accepted")
		}
	}
}
func FuzzLogPayload(f *testing.F) {
	raw, _ := EncodePayload(LogRequest{RequestID: "read", ResourceID: "app", Query: DefaultLogQuery()})
	f.Add(raw)
	f.Fuzz(func(t *testing.T, data []byte) {
		var r LogRequest
		if DecodePayload(data, &r) == nil {
			_ = r.Validate()
		}
		var e LogEvent
		if DecodePayload(data, &e) == nil {
			_ = e.Validate()
		}
	})
}
