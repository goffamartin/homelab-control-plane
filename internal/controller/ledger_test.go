package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hlcp/internal/command"
	"hlcp/internal/liveness"
	"hlcp/internal/protocol"
	"hlcp/internal/session"
)

func testLedger(t *testing.T, path string) *CommandTracker {
	t.Helper()
	tracker, err := OpenCommandTracker(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tracker.Close() })
	return tracker
}

type queryOnlyConnection struct {
	net.Conn
	types chan protocol.MessageType
}

func (connection queryOnlyConnection) Write(data []byte) (int, error) {
	if len(data) >= protocol.HeaderSize && data[0] == 'H' && data[1] == 'L' {
		connection.types <- protocol.MessageType(data[3])
	}
	return connection.Conn.Write(data)
}

func TestReconciliationWireResponseIsPersistedWithoutExecution(t *testing.T) {
	directory := t.TempDir()
	journal, err := command.OpenFileJournal(filepath.Join(directory, "agent.json"), 16)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := command.NewDispatcher(journal)
	defer dispatcher.Close()
	var calls atomic.Int32
	dispatcher.Register("catalog", func(context.Context, string) protocol.CommandResultPayload {
		calls.Add(1)
		return protocol.CommandResultPayload{Success: true, Code: "OK"}
	})
	dispatcher.Execute(retryCommand())
	path := filepath.Join(directory, "controller.json")
	before := testLedger(t, path)
	before.Admit("n", retryCommand(), time.Now().Add(time.Minute))
	before.AttemptNode("n", "c")
	before.Close()
	tracker := testLedger(t, path)
	sessions := NewActiveSessions(tracker)
	controller, agent := net.Pipe()
	types := make(chan protocol.MessageType, 16)
	writer := session.NewWriter(queryOnlyConnection{controller, types}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sessions.Publish(session.Registration{NodeID: "n", SessionID: "s"}, session.Synchronization{}, time.Now(), writer, cancel)
	agentDone := make(chan error, 1)
	go func() { agentDone <- session.RunAgentCommandLoop(ctx, agent, session.NewWriter(agent, 1), dispatcher) }()
	inbound := session.StartInbound(ctx, controller)
	livenessTracker, _ := liveness.NewTracker(liveness.Policy{HeartbeatInterval: time.Second, DegradedAfter: 2, OfflineAfter: 3}, time.Now())
	inboundDone := make(chan error, 1)
	go func() {
		inboundDone <- session.RunInboundLoop(ctx, inbound, livenessTracker, nil, func(ack protocol.CommandACKPayload) bool { return sessions.HandleAck("n", writer, ack) }, func(result protocol.CommandResultPayload) bool { return sessions.HandleResult("n", writer, result) }, func(state protocol.CommandStatePayload) bool { return sessions.HandleCommandState("n", writer, state) })
	}()
	reconcileDone := make(chan struct{})
	go func() { defer close(reconcileDone); sessions.Reconcile(ctx, "n", writer) }()
	defer func() {
		cancel()
		controller.Close()
		agent.Close()
		<-agentDone
		<-inboundDone
		<-inbound.Done
		<-reconcileDone
	}()
	for {
		progress, _ := tracker.Progress("n", "c")
		if progress.Result != nil {
			if !progress.Result.Success || calls.Load() != 1 {
				t.Fatalf("progress=%+v calls=%d", progress, calls.Load())
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("query result was not persisted")
		case <-time.After(time.Millisecond):
		}
	}
	select {
	case messageType := <-types:
		if messageType != protocol.MessageCommandQuery {
			t.Fatalf("sent=%v", messageType)
		}
	default:
		t.Fatal("no query sent")
	}
}

func TestLedgerIdempotenceConflictAndCachedHTTPWithoutInventory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	tracker := testLedger(t, path)
	payload := retryCommand()
	if fresh, err := tracker.Admit("n", payload, time.Now().Add(time.Minute)); err != nil || !fresh {
		t.Fatalf("fresh=%v err=%v", fresh, err)
	}
	if fresh, err := tracker.Admit("n", payload, time.Now().Add(time.Minute)); err != nil || fresh {
		t.Fatalf("duplicate=%v %v", fresh, err)
	}
	changed := payload
	changed.Action = "stop"
	if _, err := tracker.Admit("n", changed, time.Now().Add(time.Minute)); !errors.Is(err, ErrCommandConflict) {
		t.Fatalf("err=%v", err)
	}
	if !tracker.CompleteNode("n", protocol.CommandResultPayload{CommandID: "c", Success: true, Code: "OK", Message: "once"}) {
		t.Fatal("result rejected")
	}
	tracker.Close()
	restarted := testLedger(t, path)
	sessions := NewActiveSessions(restarted)
	server := &Server{sessions: sessions, registry: sessions.registry}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		url := "/v1/nodes/n/commands"
		if method == http.MethodGet {
			url += "/c"
		}
		response := httptest.NewRecorder()
		server.HTTPHandler(testAPIToken).ServeHTTP(response, apiRequest(method, url, `{"command_id":"c","resource_id":"catalog","action":"restart"}`))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"success":true`) {
			t.Fatalf("response=%d %s", response.Code, response.Body.String())
		}
	}
	response := httptest.NewRecorder()
	server.HTTPHandler(testAPIToken).ServeHTTP(response, apiRequest(http.MethodPost, "/v1/nodes/n/commands", `{"command_id":"c","resource_id":"catalog","action":"stop"}`))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "COMMAND_CONFLICT") {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
}

func TestLateResultSurvivesCanceledCallerAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	tracker := testLedger(t, path)
	sessions := NewActiveSessions(tracker)
	connection, peer := net.Pipe()
	defer connection.Close()
	defer peer.Close()
	writer := session.NewWriter(connection, 1)
	sessions.Publish(session.Registration{NodeID: "n", SessionID: "s"}, session.Synchronization{}, time.Now(), writer, func() {})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := sessions.Execute(ctx, "n", retryCommand()); done <- err }()
	peer.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := protocol.Decode(peer); err != nil {
		t.Fatal(err)
	}
	sessions.HandleAck("n", writer, protocol.CommandACKPayload{CommandID: "c", Status: "accepted"})
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("client cancellation blocked")
	}
	progress, exists := tracker.Progress("n", "c")
	if !exists || progress.Attempts != 1 || progress.Result != nil {
		t.Fatalf("progress=%+v", progress)
	}
	if !sessions.HandleResult("n", writer, protocol.CommandResultPayload{CommandID: "c", Success: true, Code: "OK"}) {
		t.Fatal("late result rejected")
	}
	tracker.Close()
	restarted := testLedger(t, path)
	progress, exists = restarted.Progress("n", "c")
	if !exists || progress.Result == nil || !progress.Result.Success || progress.Attempts != 1 {
		t.Fatalf("progress=%+v", progress)
	}
}

func TestConcurrentIdempotentCallersShareOneExecution(t *testing.T) {
	tracker := testLedger(t, filepath.Join(t.TempDir(), "ledger.json"))
	sessions := NewActiveSessions(tracker)
	connection, peer := net.Pipe()
	defer connection.Close()
	defer peer.Close()
	writer := session.NewWriter(connection, 1)
	sessions.Publish(session.Registration{NodeID: "n", SessionID: "s"}, session.Synchronization{}, time.Now(), writer, func() {})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 2)
	execute := func() {
		result, err := sessions.Execute(ctx, "n", retryCommand())
		if err == nil && !result.Success {
			err = errors.New("unexpected failure")
		}
		done <- err
	}
	go execute()
	peer.SetDeadline(time.Now().Add(time.Second))
	if _, err := protocol.Decode(peer); err != nil {
		t.Fatal(err)
	}
	sessions.HandleAck("n", writer, protocol.CommandACKPayload{CommandID: "c", Status: "accepted"})
	go execute()
	peer.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if _, err := protocol.Decode(peer); err == nil {
		t.Fatal("duplicate caller sent another command")
	}
	if !sessions.HandleResult("n", writer, protocol.CommandResultPayload{CommandID: "c", Success: true, Code: "OK"}) {
		t.Fatal("result rejected")
	}
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("caller did not receive shared result")
		}
	}
	progress, _ := tracker.Progress("n", "c")
	if progress.Attempts != 1 {
		t.Fatalf("progress=%+v", progress)
	}
}

func TestCancelBeforeAdmissionDoesNotCreateLedgerOperation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	tracker := testLedger(t, path)
	sessions := NewActiveSessions(tracker)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sessions.Execute(ctx, "n", retryCommand()); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	tracker.Close()
	recovered := testLedger(t, path)
	if _, exists := recovered.Progress("n", "c"); exists {
		t.Fatal("canceled operation was admitted")
	}
}

func TestCancellationAfterAdmissionButBeforeSendIsNotExecuted(t *testing.T) {
	tracker := testLedger(t, filepath.Join(t.TempDir(), "ledger.json"))
	sessions := NewActiveSessions(tracker)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tracker.persistFault = func() error { cancel(); return nil }
	if _, err := sessions.Execute(ctx, "n", retryCommand()); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	progress, exists := tracker.Progress("n", "c")
	if !exists || progress.Attempts != 0 || progress.Result == nil || progress.Result.Code != "EXECUTION_NOT_STARTED" {
		t.Fatalf("progress=%+v", progress)
	}
}

func TestAttemptAdmissionRechecksACKAndCompletion(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		tracker := NewCommandTracker()
		tracker.Admit("n", retryCommand(), time.Now().Add(time.Minute))
		if terminal {
			tracker.CompleteNode("n", protocol.CommandResultPayload{CommandID: "c", Success: true, Code: "OK"})
		} else {
			tracker.AckNode("n", protocol.CommandACKPayload{CommandID: "c", Status: "accepted"})
		}
		if err := tracker.AttemptNode("n", "c"); !errors.Is(err, errCommandStateChanged) {
			t.Fatalf("err=%v", err)
		}
		progress, _ := tracker.Progress("n", "c")
		if progress.Attempts != 0 {
			t.Fatal("attempt admitted after ACK/result")
		}
	}
}

func TestControllerRestartReconcilesSideEffectWithQueryOnly(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprint(expired), func(t *testing.T) {
			directory := t.TempDir()
			journal, err := command.OpenFileJournal(filepath.Join(directory, "agent.json"), 16)
			if err != nil {
				t.Fatal(err)
			}
			dispatcher := command.NewDispatcher(journal)
			defer dispatcher.Close()
			calls := 0
			dispatcher.Register("catalog", func(context.Context, string) protocol.CommandResultPayload {
				calls++
				return protocol.CommandResultPayload{Success: true, Code: "OK"}
			})
			path := filepath.Join(directory, "ledger.json")
			tracker := testLedger(t, path)
			deadline := time.Now().Add(time.Minute)
			if expired {
				tracker.now = func() time.Time { return time.Now().Add(-2 * time.Hour) }
				deadline = time.Now().Add(-time.Hour)
			}
			if _, err := tracker.Admit("n", retryCommand(), deadline); err != nil {
				t.Fatal(err)
			}
			if err := tracker.AttemptNode("n", "c"); err != nil {
				t.Fatal(err)
			}
			_, result := dispatcher.Execute(retryCommand())
			if !result.Success {
				t.Fatalf("result=%+v", result)
			}
			// The result is intentionally lost before controller persistence.
			tracker.Close()
			restarted := testLedger(t, path)
			sessions := NewActiveSessions(restarted)
			controller, agent := net.Pipe()
			defer controller.Close()
			defer agent.Close()
			writer := session.NewWriter(controller, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sessions.Publish(session.Registration{NodeID: "n", SessionID: "new"}, session.Synchronization{}, time.Now(), writer, cancel)
			done := make(chan struct{})
			go func() { defer close(done); sessions.Reconcile(ctx, "n", writer) }()
			defer func() { cancel(); controller.Close(); agent.Close(); <-done }()
			agent.SetDeadline(time.Now().Add(2 * time.Second))
			frame, err := protocol.Decode(agent)
			if err != nil || frame.Header.Type != protocol.MessageCommandQuery {
				t.Fatalf("restart sent type=%v err=%v", frame.Header.Type, err)
			}
			var payload protocol.CommandPayload
			if err := protocol.DecodePayload(frame.Payload, &payload); err != nil || payload != retryCommand() {
				t.Fatalf("payload=%+v err=%v", payload, err)
			}
			state := dispatcher.Query(payload)
			if state.State != "completed" || !sessions.HandleCommandState("n", writer, state) {
				t.Fatalf("state=%+v", state)
			}
			if sessions.HandleCommandState("foreign", writer, state) {
				t.Fatal("foreign state accepted")
			}
			cached, err := sessions.Execute(context.Background(), "n", retryCommand())
			if err != nil || !cached.Success || calls != 1 {
				t.Fatalf("result=%+v err=%v calls=%d", cached, err, calls)
			}
		})
	}
}

func TestLedgerPersistenceFailureFailsClosed(t *testing.T) {
	for _, stage := range []string{"admit", "attempt", "result"} {
		t.Run(stage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ledger.json")
			tracker := testLedger(t, path)
			fail := func() error { return errors.New("disk failure") }
			if stage == "admit" {
				tracker.persistFault = fail
			}
			_, err := tracker.Admit("n", retryCommand(), time.Now().Add(time.Minute))
			if stage == "admit" {
				if !errors.Is(err, ErrLedgerFailure) {
					t.Fatalf("err=%v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				tracker.persistFault = fail
				if stage == "attempt" {
					if !errors.Is(tracker.AttemptNode("n", "c"), ErrLedgerFailure) {
						t.Fatal("attempt persist failure ignored")
					}
				} else if tracker.CompleteNode("n", protocol.CommandResultPayload{CommandID: "c", Success: true, Code: "OK"}) {
					t.Fatal("unpersisted success accepted")
				}
			}
			if _, _, err := tracker.Operation("n", "c"); !errors.Is(err, ErrLedgerFailure) {
				t.Fatal("unpersisted state exposed")
			}
			if _, err := tracker.Admit("n", protocol.CommandPayload{CommandID: "other", ResourceID: "catalog", Action: "restart"}, time.Now().Add(time.Minute)); !errors.Is(err, ErrLedgerFailure) {
				t.Fatalf("err=%v", err)
			}
			tracker.Close()
			recovered := testLedger(t, path)
			if _, fresh := recovered.Progress("n", "c"); fresh && stage == "admit" {
				t.Fatal("failed admission persisted")
			}
		})
	}
}

func TestLedgerRetentionCapacityAndUnknownProtection(t *testing.T) {
	tracker := NewCommandTracker()
	now := time.Now()
	tracker.now = func() time.Time { return now }
	for i := 0; i < maxLedgerOperations; i++ {
		payload := retryCommand()
		payload.CommandID = fmt.Sprintf("c-%d", i)
		if _, err := tracker.Admit("n", payload, now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		code := "OK"
		if i == 0 {
			code = "EXECUTION_UNKNOWN"
		}
		tracker.CompleteNode("n", protocol.CommandResultPayload{CommandID: payload.CommandID, Success: code == "OK", Code: code})
	}
	if _, err := tracker.Admit("n", retryCommand(), now.Add(time.Minute)); !errors.Is(err, ErrLedgerFull) {
		t.Fatalf("err=%v", err)
	}
	now = now.Add(25 * time.Hour)
	if _, err := tracker.Admit("n", retryCommand(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, exists := tracker.Progress("n", "c-0"); !exists {
		t.Fatal("unknown outcome evicted")
	}
	if _, exists := tracker.Progress("n", "c-1"); exists {
		t.Fatal("expired terminal history not reclaimed")
	}
}

func TestLedgerRejectsInvalidFilesAndSecondOwner(t *testing.T) {
	for _, body := range []string{"", "{", `{}`, `{"version":99,"operations":[]}`, `{"version":1,"operations":[],"extra":true}`, `{"version":1,"operations":[]} {}`} {
		path := filepath.Join(t.TempDir(), "ledger.json")
		os.WriteFile(path, []byte(body), 0600)
		if tracker, err := OpenCommandTracker(path); err == nil {
			tracker.Close()
			t.Fatalf("accepted=%q", body)
		}
	}
	path := filepath.Join(t.TempDir(), "ledger.json")
	tracker := testLedger(t, path)
	if second, err := OpenCommandTracker(path); err == nil {
		second.Close()
		t.Fatal("second owner accepted")
	}
	tracker.Close()
	testLedger(t, path)
}

func TestLedgerRecoveryAfterAbruptExit(t *testing.T) {
	if path := os.Getenv("HLCP_LEDGER_CRASH_PATH"); path != "" {
		tracker, err := OpenCommandTracker(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tracker.Admit("n", retryCommand(), time.Now().Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := tracker.AttemptNode("n", "c"); err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "ledger.json")
	child := exec.Command(os.Args[0], "-test.run=^TestLedgerRecoveryAfterAbruptExit$")
	child.Env = append(os.Environ(), "HLCP_LEDGER_CRASH_PATH="+path)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("child: %v %s", err, output)
	}
	tracker := testLedger(t, path)
	operation, ok := tracker.Progress("n", "c")
	if !ok || operation.State != "unknown" || operation.WaitError != "RESULT_TIMEOUT" || operation.Attempts != 1 {
		t.Fatalf("operation=%+v", operation)
	}
	data, err := os.ReadFile(path)
	var document ledgerDocument
	if err != nil || json.Unmarshal(data, &document) != nil || document.Version != 1 {
		t.Fatalf("document=%+v err=%v", document, err)
	}
}
