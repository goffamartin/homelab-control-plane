package main

import (
	"flag"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestServiceCommandEntryPointsNeverFallIntoRuntime(t *testing.T) {
	if os.Getenv("HLCP_SERVICE_MAIN_HELPER") == "1" {
		for i, a := range os.Args {
			if a == "--" {
				os.Args = append([]string{"hlcp-agent"}, os.Args[i+1:]...)
				break
			}
		}
		flag.CommandLine = flag.NewFlagSet("hlcp-agent", flag.ExitOnError)
		main()
		os.Exit(0)
	}
	for _, row := range []struct {
		args []string
		want string
		code int
	}{
		{[]string{"activate"}, "activate has been removed", 2},
		{[]string{"start", "--help", "--json"}, `"operation":"agent.start"`, 0},
		{[]string{"restart", "--help", "--json"}, `"operation":"agent.restart"`, 0},
		{[]string{"stop", "--enable", "--json"}, "INVALID_OPTIONS", 2},
		{[]string{"status", "--help", "--json"}, `"operation":"agent.status"`, 0},
		{[]string{"completion", "bash"}, "complete -F _hlcp_complete hlcp-agent", 0},
		{[]string{"__complete", "rest"}, "restart", 0},
	} {
		args := append([]string{"-test.run=^TestServiceCommandEntryPointsNeverFallIntoRuntime$", "--"}, row.args...)
		child := exec.Command(os.Args[0], args...)
		child.Env = append(os.Environ(), "HLCP_SERVICE_MAIN_HELPER=1")
		out, err := child.CombinedOutput()
		code := 0
		if e, ok := err.(*exec.ExitError); ok {
			code = e.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if code != row.code || !strings.Contains(string(out), row.want) || strings.Contains(string(out), "open /etc/hlcp") || strings.Contains(string(out), "agent.yaml:") {
			t.Fatal(row.args, code, string(out))
		}
	}
}
