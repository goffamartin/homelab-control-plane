package cli

import "testing"

func TestLogSelectorsAndOptions(t *testing.T) {
	request, err := Parse([]string{"-n", "node", "-r", "app", "-a", "logs", "--follow", "--tail", "0", "--stream", "stderr", "--contains", "error", "--json"})
	if err != nil || request.Logs == nil || !request.Logs.Query.Follow || request.Logs.Query.Tail != 0 || request.Logs.Query.Stream != "stderr" || Intent(request) != "command" {
		t.Fatal(request, err)
	}
	base := []string{"-n", "node", "-r", "app", "-a", "logs"}
	for _, extra := range [][]string{{"--tail", "-1"}, {"--tail", "1001"}, {"--since", "10m"}, {"--since", "--follow"}, {"--stream", "shell"}, {"--follow", "--cursor", "x"}, {"--follow", "--limit", "100"}, {"--cursor", "x", "--contains", "y"}, {"--cursor", "x", "--tail", "100"}, {"--tail", "10", "--tail", "20"}, {"--follow", "--command-id", "c"}} {
		args := append(append([]string{}, base...), extra...)
		if _, err := Parse(args); err == nil {
			t.Fatal("invalid log selectors accepted", extra)
		}
	}
	if _, err := Parse([]string{"-n", "node", "-r", "app", "-a", "restart", "--follow"}); err == nil {
		t.Fatal("log options used for mutation")
	}
}
