package adapter

import (
	"context"
	"errors"
	"hlcp/internal/protocol"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"
)

var logProcessSlots = make(chan struct{}, 2)

type logOutput struct {
	ctx       context.Context
	cancel    context.CancelFunc
	records   chan protocol.LogRecord
	query     protocol.LogQuery
	stream    string
	buf       []byte
	truncated bool
	bytes     *atomic.Int64
	limited   *atomic.Bool
}

func (w *logOutput) Write(p []byte) (int, error) {
	if w.bytes.Add(int64(len(p))) > 8<<20 {
		w.limited.Store(true)
		w.cancel()
		return 0, errors.New("log byte limit")
	}
	for _, b := range p {
		if w.ctx.Err() != nil {
			return 0, w.ctx.Err()
		}
		if b == '\n' {
			w.flush()
		} else if len(w.buf) < protocol.MaxLogText {
			w.buf = append(w.buf, b)
		} else {
			w.truncated = true
		}
	}
	return len(p), nil
}
func (w *logOutput) flush() {
	if len(w.buf) == 0 && !w.truncated {
		return
	}
	text := strings.TrimSuffix(string(w.buf), "\r")
	stamp := ""
	if prefix, rest, ok := strings.Cut(text, " "); ok {
		if t, err := time.Parse(time.RFC3339Nano, prefix); err == nil {
			stamp = t.UTC().Format(time.RFC3339Nano)
			text = rest
		}
	}
	text = strings.ToValidUTF8(strings.ReplaceAll(text, "\x00", "�"), "�")
	if len(text) > protocol.MaxLogText {
		text = text[:protocol.MaxLogText]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
		w.truncated = true
	}
	if (w.query.Stream == "both" || w.query.Stream == w.stream) && strings.Contains(text, w.query.Contains) {
		record := protocol.LogRecord{Timestamp: stamp, Stream: w.stream, Text: text, Truncated: w.truncated}
		select {
		case w.records <- record:
		case <-w.ctx.Done():
		}
	}
	w.buf = w.buf[:0]
	w.truncated = false
}
func containerLogsProcess(parent context.Context, executable string, args []string, q protocol.LogQuery, emit func(protocol.LogRecord) error) string {
	select {
	case logProcessSlots <- struct{}{}:
	default:
		return "CAPACITY"
	}
	releaseSlot := true
	defer func() {
		if releaseSlot {
			<-logProcessSlots
		}
	}()
	limit := 15 * time.Second
	if q.Follow {
		limit = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, limit)
	defer cancel()
	records := make(chan protocol.LogRecord, 32)
	var bytes atomic.Int64
	var limited atomic.Bool
	stdout := &logOutput{ctx: ctx, cancel: cancel, records: records, query: q, stream: "stdout", bytes: &bytes, limited: &limited}
	stderr := &logOutput{ctx: ctx, cancel: cancel, records: records, query: q, stream: "stderr", bytes: &bytes, limited: &limited}
	cmd := exec.Command(executable, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "RUNTIME_FAILED"
	}
	defer outPipe.Close()
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		return "RUNTIME_FAILED"
	}
	defer errPipe.Close()
	if ctx.Err() != nil {
		return "CANCELED"
	}
	if cmd.Start() != nil {
		return "RUNTIME_FAILED"
	}
	kill := func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	stop := context.AfterFunc(ctx, func() { kill(); outPipe.Close(); errPipe.Close() })
	defer stop()
	defer kill()
	done := make(chan error, 1)
	var copies sync.WaitGroup
	var copyFailed atomic.Bool
	copyOutput := func(reader io.Reader, output *logOutput) {
		defer copies.Done()
		if _, err := io.Copy(output, reader); err != nil && ctx.Err() == nil {
			copyFailed.Store(true)
		}
		output.flush()
	}
	copies.Add(2)
	go copyOutput(outPipe, stdout)
	go copyOutput(errPipe, stderr)
	go func() { copies.Wait(); close(records); done <- cmd.Wait() }()
	emissionFailed := false
	for record := range records {
		if emissionFailed {
			continue
		}
		if emit(record) != nil {
			emissionFailed = true
			cancel()
		}
	}
	select {
	case err = <-done:
	case <-ctx.Done():
		kill()
		outPipe.Close()
		errPipe.Close()
		timer := time.NewTimer(time.Second)
		select {
		case err = <-done:
			timer.Stop()
		case <-timer.C:
			// A kernel-unreapable client keeps its slot until Wait completes.
			// At most two such reapers can exist; session shutdown stays bounded.
			releaseSlot = false
			go func() { <-done; <-logProcessSlots }()
			return "RUNTIME_FAILED"
		}
	}
	if limited.Load() {
		return "BYTE_LIMIT"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "TIME_LIMIT"
	}
	if parent.Err() != nil {
		return "CANCELED"
	}
	if emissionFailed {
		return "BACKPRESSURE"
	}
	if err != nil || copyFailed.Load() {
		return "RUNTIME_FAILED"
	}
	return "OK"
}
