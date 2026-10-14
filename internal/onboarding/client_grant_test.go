package onboarding

import (
	"bytes"
	"context"
	"hlcp/internal/setup"
	"strings"
	"testing"
)

func TestClientGrantCLIHelpValidationAndConsent(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"--user", "martin", "--yes"}, {"--user", "martin", "--apply"}, {"--user", "martin", "--role", "admin"}, {"--apply", "--yes"}, {"--unknown"}} {
		called := false
		var out bytes.Buffer
		code := runClientGrantCLI(args, &out, func(context.Context, string, string, bool) clientGrantReport {
			called = true
			return clientGrantReport{}
		})
		if called || (args[0] == "--help" && code != 0) || (args[0] != "--help" && code != 2) {
			t.Fatal(args, called, code)
		}
	}
	for _, apply := range []bool{false, true} {
		var out bytes.Buffer
		args := []string{"--user", "martin", "--role", "observer", "--json"}
		if apply {
			args = append(args, "--apply", "--yes")
		}
		code := runClientGrantCLI(args, &out, func(_ context.Context, name, role string, confirmed bool) clientGrantReport {
			if name != "martin" || role != "observer" || confirmed != apply {
				t.Fatal("wrong grant request")
			}
			return grantFailure("SOURCE_REFUSED", true)
		})
		if code != 1 || !strings.Contains(out.String(), "SOURCE_REFUSED") {
			t.Fatal(code, out.String())
		}
	}
}
func TestGrantPreviewStrictOutput(t *testing.T) {
	// Cross-platform validation of the parent-facing preview is Linux specific;
	// public CLI only ever prints report fields, never credentials.
	var out bytes.Buffer
	preview := setup.Preview{ReadOnly: true, Changes: []setup.Change{{Name: "cli.json", Action: "create", Mode: 0600}, {Name: "cli.token", Action: "create", Mode: 0600}}}
	if runClientGrantCLI([]string{"--user", "martin", "--json"}, &out, func(context.Context, string, string, bool) clientGrantReport {
		return clientGrantReport{Status: "preview", ReadOnly: true, Preview: &preview}
	}) != 0 || !strings.Contains(out.String(), "cli.json") {
		t.Fatal(out.String())
	}
}
