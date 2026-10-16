package protocol

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const LogCapability = "resource.logs.v1"
const MaxLogText = 4096

var ErrInvalidLogs = errors.New("hlcp: invalid log request or event")

// Structured read-only parameters, never executable names, paths or CLI argv.
type LogQuery struct {
	Tail     int    `cbor:"tail" json:"tail"`
	Since    string `cbor:"since,omitempty" json:"since,omitempty"`
	Until    string `cbor:"until,omitempty" json:"until,omitempty"`
	Contains string `cbor:"contains,omitempty" json:"contains,omitempty"`
	Stream   string `cbor:"stream" json:"stream"`
	Follow   bool   `cbor:"follow" json:"follow"`
}

func DefaultLogQuery() LogQuery { return LogQuery{Tail: 100, Stream: "both"} }
func (q LogQuery) Validate() error {
	if q.Tail < 0 || q.Tail > 1000 || len(q.Contains) > 256 || !utf8.ValidString(q.Contains) || strings.ContainsRune(q.Contains, 0) {
		return ErrInvalidLogs
	}
	if q.Stream != "both" && q.Stream != "stdout" && q.Stream != "stderr" {
		return ErrInvalidLogs
	}
	var since, until time.Time
	for i, value := range []string{q.Since, q.Until} {
		if value != "" {
			if len(value) > 40 {
				return ErrInvalidLogs
			}
			t, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return ErrInvalidLogs
			}
			if i == 0 {
				since = t
			} else {
				until = t
			}
		}
	}
	if !since.IsZero() && !until.IsZero() && !since.Before(until) {
		return ErrInvalidLogs
	}
	return nil
}

type LogRequest struct {
	RequestID  string   `cbor:"request_id" json:"request_id"`
	ResourceID string   `cbor:"resource_id" json:"resource_id"`
	Query      LogQuery `cbor:"query" json:"query"`
}

func (r LogRequest) Validate() error {
	if !isIdentifier(r.RequestID, 64) || !isIdentifier(r.ResourceID, 128) {
		return ErrInvalidLogs
	}
	return r.Query.Validate()
}

type LogCancel struct {
	RequestID string `cbor:"request_id" json:"request_id"`
}

func (r LogCancel) Validate() error {
	if !isIdentifier(r.RequestID, 64) {
		return ErrInvalidLogs
	}
	return nil
}

type LogRecord struct {
	Timestamp string `cbor:"timestamp,omitempty" json:"timestamp,omitempty"`
	Stream    string `cbor:"stream" json:"stream"`
	Text      string `cbor:"text" json:"text"`
	Truncated bool   `cbor:"truncated" json:"truncated"`
}
type LogEvent struct {
	RequestID string     `cbor:"request_id" json:"request_id"`
	Sequence  uint64     `cbor:"sequence" json:"sequence"`
	Kind      string     `cbor:"kind" json:"kind"`
	Record    *LogRecord `cbor:"record,omitempty" json:"record,omitempty"`
	Code      string     `cbor:"code,omitempty" json:"code,omitempty"`
	Gap       bool       `cbor:"gap" json:"gap"`
}

func LogCode(code string) bool {
	switch code {
	case "OK", "NOT_ALLOWED", "UNSUPPORTED", "CAPACITY", "RUNTIME_FAILED", "TIME_LIMIT", "BYTE_LIMIT", "CANCELED", "BACKPRESSURE", "SESSION_LOST", "FORBIDDEN", "INVALID_SEQUENCE":
		return true
	}
	return false
}
func (e LogEvent) Validate() error {
	if !isIdentifier(e.RequestID, 64) || e.Sequence == 0 {
		return ErrInvalidLogs
	}
	switch e.Kind {
	case "start":
		if e.Sequence != 1 || e.Record != nil || e.Code != "" || e.Gap {
			return ErrInvalidLogs
		}
	case "end":
		if e.Record != nil || !LogCode(e.Code) || (e.Code != "OK" && !e.Gap) || (e.Code == "OK" && e.Sequence == 1) {
			return ErrInvalidLogs
		}
	case "record":
		if e.Record == nil || e.Code != "" || e.Gap {
			return ErrInvalidLogs
		}
		r := e.Record
		if r.Stream != "stdout" && r.Stream != "stderr" {
			return ErrInvalidLogs
		}
		if len(r.Text) > MaxLogText || !utf8.ValidString(r.Text) || strings.ContainsRune(r.Text, 0) {
			return ErrInvalidLogs
		}
		if r.Timestamp != "" {
			if len(r.Timestamp) > 40 {
				return ErrInvalidLogs
			}
			if _, err := time.Parse(time.RFC3339Nano, r.Timestamp); err != nil {
				return ErrInvalidLogs
			}
		}
	default:
		return ErrInvalidLogs
	}
	return nil
}
