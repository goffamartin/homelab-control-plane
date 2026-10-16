package cli

import (
	"reflect"
	"slices"
	"testing"
)

func TestCompletionStaticAndNoActivateAlias(t *testing.T) {
	for _, command := range []string{"hlcp", "hlcp-agent", "hlcp-controller"} {
		values := CompletionCandidates(command, []string{""}, nil)
		if len(values) == 0 {
			t.Fatal(command)
		}
		if command == "hlcp-agent" && (!slices.Contains(values, "start") || !slices.Contains(values, "restart") || slices.Contains(values, "activate")) {
			t.Fatal(values)
		}
	}
	for _, row := range []struct {
		cmd        string
		args, want []string
	}{
		{"hlcp-agent", []string{"res"}, []string{"resources", "restart"}},
		{"hlcp-agent", []string{"start", "--scope", "s"}, []string{"system"}},
		{"hlcp-agent", []string{"restart", "--en"}, []string{}},
		{"hlcp", []string{"--stream", "s"}, []string{"stdout", "stderr"}},
		{"hlcp", []string{"--api-token-file", ""}, nil},
	} {
		if got := CompletionCandidates(row.cmd, row.args, nil); !reflect.DeepEqual(got, row.want) {
			t.Fatal(row, got)
		}
	}
}
func TestCompletionOnlyLooksUpSelectorsAndFiltersUntrustedWords(t *testing.T) {
	calls := 0
	lookup := func(kind, node, resource string) []string {
		calls++
		if kind != "actions" || node != "n" || resource != "app" {
			t.Fatal(kind, node, resource)
		}
		return []string{"status", "restart", "logs", "$(touch pwn)", "bad\nword", "logs"}
	}
	got := CompletionCandidates("hlcp", []string{"-n", "n", "-r", "app", "-a", ""}, lookup)
	if !reflect.DeepEqual(got, []string{"status", "restart", "logs"}) || calls != 1 {
		t.Fatal(got, calls)
	}
	CompletionCandidates("hlcp", []string{"-n", "n", "-r", "app", "-a", "restart", "--j"}, lookup)
	CompletionCandidates("hlcp-agent", []string{"start", ""}, lookup)
	CompletionCandidates("hlcp", []string{"--contains", "-n", ""}, lookup)
	if calls != 1 {
		t.Fatal("completion fetched on nonselector input")
	}
	if _, ok := CompletionRequest([]string{"--contains", "secret\nline", ""}); ok {
		t.Fatal("unbounded/multiline words accepted")
	}
}
