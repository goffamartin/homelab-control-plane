package adapter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestRunnerOutputHelper(t *testing.T) {
	if os.Getenv("HLCP_TEST_OUTPUT_HELPER") != "1" {
		return
	}
	fmt.Print(strings.Repeat("x", 20000))
	fmt.Fprint(os.Stderr, strings.Repeat("y", 20000))
	os.Exit(0)
}

func TestSystemRunnerBoundsCombinedOutput(t *testing.T) {
	t.Setenv("HLCP_TEST_OUTPUT_HELPER", "1")
	output, err := (systemRunner{}).Run(context.Background(), os.Args[0], "-test.run=^TestRunnerOutputHelper$")
	if err != nil || len(output) != 4096 {
		t.Fatalf("output bytes=%d err=%v", len(output), err)
	}
}

func TestExecutionFailureDistinguishesUnavailableAndUncertain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, action := range []string{"restart", "status"} {
		result := executionFailure(ctx, context.Canceled, "", action)
		want := "EXECUTION_UNKNOWN"
		if action == "status" {
			want = "TIMEOUT"
		}
		if result.Code != want {
			t.Fatalf("action=%s result=%+v", action, result)
		}
	}
	result := executionFailure(context.Background(), &exec.Error{Name: "missing", Err: exec.ErrNotFound}, "", "restart")
	if result.Code != "RUNTIME_UNAVAILABLE" {
		t.Fatalf("result=%+v", result)
	}
	_, err := (systemRunner{}).Run(ctx, os.Args[0])
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
