package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestCheckScriptConfigDoesNotExecute(t *testing.T) {
	if os.Getenv("HLCP_CHECK_CONFIG_TEST_HELPER") == "1" {
		flag.CommandLine = flag.NewFlagSet("hlcp-agent", flag.ExitOnError)
		os.Args = []string{"hlcp-agent", "-config", os.Getenv("HLCP_CHECK_CONFIG_TEST_PATH"), "-check-config"}
		main()
		os.Exit(0)
	}
	directory := t.TempDir()
	marker := filepath.Join(directory, "executed")
	script := filepath.Join(directory, "probe.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf executed > \"$1\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(directory, "journal.json")
	configPath := filepath.Join(directory, "agent.yaml")
	config := fmt.Sprintf(`controller_address: nonexistent.test:9443
server_name: nonexistent.test
certificate_file: /nonexistent/agent.crt
private_key_file: /nonexistent/agent.key
ca_file: /nonexistent/ca.crt
journal_file: %s
node_id: test-node
agent_version: test
capabilities: [resource.control, script]
resources:
  - id: diagnostics
    type: task
    adapter: script
    actions: [diagnose]
    commands:
      diagnose:
        executable: /bin/sh
        argv: [%s, %s]
        working_directory: /
`, strconv.Quote(journal), strconv.Quote(script), strconv.Quote(marker))
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestCheckScriptConfigDoesNotExecute$")
	command.Env = append(os.Environ(), "HLCP_CHECK_CONFIG_TEST_HELPER=1", "HLCP_CHECK_CONFIG_TEST_PATH="+configPath)
	output, err := command.CombinedOutput()
	if err != nil || string(output) != "configuration valid\n" {
		t.Fatalf("output=%q err=%v", output, err)
	}
	for _, path := range []string{marker, journal} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("check-config touched %s: %v", path, err)
		}
	}
}
