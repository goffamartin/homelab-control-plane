package managedcli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestSecretIsBoundedAndNotEchoed(t *testing.T) {
	for _, test := range []struct {
		input string
		ok    bool
	}{{"private pairing code\n", true}, {strings.Repeat("x", 8192) + "\n", false}, {"", false}} {
		var output bytes.Buffer
		ui := newTerminal(strings.NewReader(test.input), &output)
		_, err := ui.secret(context.Background(), "Password: ")
		if (err == nil) != test.ok {
			t.Fatal("unexpected result")
		}
		if strings.Contains(output.String(), "private pairing code") {
			t.Fatal("secret echoed")
		}
	}
}
func TestManagedSetupRouting(t *testing.T) {
	if !ManagedSetup(nil) || !ManagedSetup([]string{"--managed"}) || ManagedSetup([]string{"--imported"}) || ManagedSetup([]string{"--cert=/private/cert"}) {
		t.Fatal("routing")
	}
}
func TestCLIHelpNeverRequiresAccountOrState(t *testing.T) {
	for _, run := range []func() int{func() int { return Join([]string{"--help"}, strings.NewReader(""), &bytes.Buffer{}) }, func() int { return Resources([]string{"--help"}, strings.NewReader(""), &bytes.Buffer{}) }, func() int { return ControllerSetup([]string{"--help"}, strings.NewReader(""), &bytes.Buffer{}) }, func() int { return AgentCertificates([]string{"--help"}, &bytes.Buffer{}) }} {
		if run() != 0 {
			t.Fatal("help refused")
		}
	}
}
