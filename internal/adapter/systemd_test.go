package adapter

import (
	"context"
	"errors"
	"testing"
)

type fakeRunner struct {
	name   string
	args   []string
	output string
	err    error
}

func (runner *fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	runner.name = name
	runner.args = args
	return runner.output, runner.err
}
func TestSystemdRestartsConfiguredUnit(t *testing.T) {
	runner := &fakeRunner{}
	result := Systemd{Unit: "minecraft.service", Runner: runner}.Execute(context.Background(), "restart")
	if !result.Success || runner.name != "systemctl" || len(runner.args) != 2 || runner.args[1] != "minecraft.service" {
		t.Fatalf("result=%#v runner=%#v", result, runner)
	}
}
func TestSystemdRejectsUnsupportedAction(t *testing.T) {
	result := Systemd{Unit: "minecraft.service"}.Execute(context.Background(), "exec")
	if result.Code != "ACTION_NOT_SUPPORTED" {
		t.Fatalf("result=%#v", result)
	}
}
func TestSystemdReportsExecutionFailure(t *testing.T) {
	result := Systemd{Unit: "minecraft.service", Runner: &fakeRunner{output: "failed", err: errors.New("exit")}}.Execute(context.Background(), "restart")
	if result.Success || result.Code != "EXECUTION_FAILED" {
		t.Fatalf("result=%#v", result)
	}
}
