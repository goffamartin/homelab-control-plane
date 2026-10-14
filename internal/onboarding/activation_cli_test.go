package onboarding

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestActivationCLIHelpInvalidOptionsAndPreview(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"--server-name", "controller.test", "--yes"}, {"--server-name", "controller.test", "--apply"}, {"--server-name", "bad name"}, {"--server-name", "controller.test", "unexpected"}, {"--unknown"}} {
		called := false
		var output bytes.Buffer
		code := runActivationCLI(args, &output, func(string) ControllerActivationHost { called = true; return freshActivationHost() })
		if called || (args[0] == "--help" && code != 0) || (args[0] != "--help" && code != 2) {
			t.Fatal(args, code, called)
		}
	}
	h := freshActivationHost()
	var out bytes.Buffer
	if runActivationCLI([]string{"--server-name", "controller.test", "--json", "--enable"}, &out, func(name string) ControllerActivationHost {
		if name != "controller.test" {
			t.Fatal(name)
		}
		return h
	}) != 0 {
		t.Fatal(out.String())
	}
	var r activationReport
	if json.Unmarshal(out.Bytes(), &r) != nil || r.Status != "preview" || !r.ReadOnly || len(r.Preview.Actions) != 2 || len(h.changes) != 0 {
		t.Fatal(out.String())
	}
}
func TestActivationCLIConfirmedAndPartialOutcomes(t *testing.T) {
	for _, fail := range []ControllerServiceAction{"", ControllerStart} {
		h := freshActivationHost()
		h.failAction = fail
		var out bytes.Buffer
		code := runActivationCLI([]string{"--server-name", "controller.test", "--enable", "--apply", "--yes", "--json"}, &out, func(string) ControllerActivationHost { return h })
		var r activationReport
		if json.Unmarshal(out.Bytes(), &r) != nil || r.ReadOnly || len(h.changes) != 2 || strings.Contains(out.String(), "SECRET") {
			t.Fatal(out.String())
		}
		if fail == "" {
			if code != 0 || r.Status != "applied" || len(r.Result.Completed) != 2 || r.Result.Uncertain {
				t.Fatal(out.String())
			}
		} else {
			if code != 1 || r.Code != "ACTIVATION_INCOMPLETE" || len(r.Result.Completed) != 1 || !r.Result.Uncertain {
				t.Fatal(out.String())
			}
		}
	}
	h := freshActivationHost()
	h.snapshot.Active = "failed"
	var out bytes.Buffer
	if runActivationCLI([]string{"--server-name", "controller.test", "--apply", "--yes", "--json"}, &out, func(string) ControllerActivationHost { return h }) != 1 || len(h.changes) != 0 || !strings.Contains(out.String(), "PREFLIGHT_REFUSED") {
		t.Fatal(out.String())
	}
}
