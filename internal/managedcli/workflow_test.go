package managedcli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

type delayedInput struct {
	value string
	delay time.Duration
}

func (r *delayedInput) Read(p []byte) (int, error) {
	time.Sleep(r.delay)
	if r.value == "" {
		return 0, io.EOF
	}
	n := copy(p, r.value)
	r.value = r.value[n:]
	return n, nil
}

func TestWorkflowDefaultDeadlineStartsOnlyForOperation(t *testing.T) {
	phases := newWorkflowContext()
	defer phases.close()
	if _, ok := phases.input().Deadline(); ok {
		t.Fatal("human input has a deadline")
	}
	op := phases.operation()
	deadline, ok := op.Deadline()
	remaining := time.Until(deadline)
	if !ok || remaining <= time.Minute || remaining > 2*time.Minute {
		t.Fatal("technical deadline missing or incorrect")
	}
	phases.close()
	if !errors.Is(op.Err(), context.Canceled) || !errors.Is(phases.input().Err(), context.Canceled) {
		t.Fatal("workflow cleanup must cancel parent and operation")
	}
}

func TestWizardWaitDoesNotConsumeOperationDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		parent, stop := context.WithCancel(context.Background())
		phases := &operationPhases{parent: parent, stop: stop, timeout: 2 * time.Minute}
		defer phases.close()
		started := time.Now()
		// Configuration/consent, both password prompts and off-host backup
		// acknowledgement may each take longer than the technical timeout.
		for _, secret := range []bool{false, true, true, false} {
			previous := phases.operation()
			input := phases.input()
			if _, ok := input.Deadline(); ok || previous.Err() != context.Canceled {
				t.Fatal("human input must suspend the operation deadline")
			}
			var output bytes.Buffer
			ui := newTerminal(&delayedInput{value: "private answer\n", delay: 3 * time.Minute}, &output)
			var value string
			var err error
			if secret {
				value, err = ui.secret(input, "Password: ")
			} else {
				value, err = ui.lineContext(input, "Confirm: ")
			}
			if err != nil || value != "private answer" || strings.Contains(output.String(), value) {
				t.Fatal("delayed input failed or was echoed")
			}
			next := phases.operation()
			deadline, ok := next.Deadline()
			if !ok || next.Err() != nil || deadline.Sub(time.Now()) != 2*time.Minute {
				t.Fatal("operation after input needs its full fresh deadline")
			}
		}
		if time.Since(started) != 12*time.Minute {
			t.Fatal("long wizard pauses were not exercised")
		}
	})
}

func TestOperationTimeoutAndParentCancellationRemainEnforced(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		parent, stop := context.WithCancel(context.Background())
		phases := &operationPhases{parent: parent, stop: stop, timeout: 2 * time.Minute}
		defer phases.close()
		op := phases.operation()
		time.Sleep(3 * time.Minute)
		if !errors.Is(op.Err(), context.DeadlineExceeded) || phases.input().Err() != nil {
			t.Fatal("technical phase must time out without cancelling human input")
		}
		op = phases.operation()
		if op.Err() != nil {
			t.Fatal("new technical phase inherited old expiry")
		}
		stop()
		if !errors.Is(op.Err(), context.Canceled) || !errors.Is(phases.input().Err(), context.Canceled) || !errors.Is(phases.operation().Err(), context.Canceled) {
			t.Fatal("parent interruption must cancel input and all later operations")
		}
	})
}

func TestWizardInputCancellationAndBounds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for _, secret := range []bool{false, true} {
			ctx, cancel := context.WithCancel(context.Background())
			ui := newTerminal(&delayedInput{value: "later\n", delay: 3 * time.Minute}, io.Discard)
			go func() { time.Sleep(time.Second); cancel() }()
			var err error
			if secret {
				_, err = ui.secret(ctx, "")
			} else {
				_, err = ui.lineContext(ctx, "")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("input ignored interruption")
			}
		}
		// The command exits after cancellation; let the synthetic readers
		// finish as well so the test bubble retains no blocked goroutines.
		time.Sleep(3 * time.Minute)
		synctest.Wait()
	})
	for _, value := range []string{"", strings.Repeat("x", 8192) + "\n"} {
		if _, err := newTerminal(strings.NewReader(value), io.Discard).lineContext(context.Background(), ""); err == nil {
			t.Fatal("empty/oversized input accepted")
		}
	}
}
