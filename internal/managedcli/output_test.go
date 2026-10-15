package managedcli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestAdministrativeResultContract(t *testing.T) {
	for _, status := range []string{"ok", "preview", "failed", "partial", "uncertain"} {
		var out bytes.Buffer
		code := emitResult(&out, true, Result{Operation: "test", Status: status, Phase: "validate", Code: "TEST", Message: "A safe message.", Changed: status == "partial", Uncertain: status == "uncertain"})
		decoder := json.NewDecoder(&out)
		var got Result
		if err := decoder.Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.SchemaVersion != 1 || got.Completed == nil || got.Status != status {
			t.Fatalf("invalid result: %+v", got)
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			t.Fatal("more than one result")
		}
		wantFailure := status == "failed" || status == "partial" || status == "uncertain"
		if (code != 0) != wantFailure {
			t.Fatal(status, code)
		}
	}
}

func TestRemovedResourceWritersHaveStructuredErrors(t *testing.T) {
	for _, action := range []string{"configure", "remove"} {
		var out bytes.Buffer
		code := Resources([]string{action, "--id", "SECRET", "--apply", "--yes", "--json"}, strings.NewReader(""), &out)
		var result Result
		if json.Unmarshal(out.Bytes(), &result) != nil || code == 0 || result.Changed || result.Uncertain || result.Code != "COMMAND_REMOVED" || strings.Contains(out.String(), "SECRET") {
			t.Fatal(out.String(), code)
		}
	}
}

func TestEditorArgumentsAreLiteral(t *testing.T) {
	cases := map[string][]string{
		`nano -w`: {"nano", "-w"},
		`"C:\Program Files\Editor\editor.exe" --wait`: {`C:\Program Files\Editor\editor.exe`, "--wait"},
		`vi 'a b' ""`:                 {"vi", "a b", ""},
		`vi $(touch-secret) > output`: {"vi", "$(touch-secret)", ">", "output"},
	}
	for input, want := range cases {
		got, err := editorArguments(input)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal(input, got, err)
		}
	}
	for _, input := range []string{"", `""`, "vi\nsecret", `vi 'unterminated`} {
		if _, err := editorArguments(input); err == nil {
			t.Fatal(input)
		}
	}
}

func TestEditorSelectionPrecedenceAndExplicitFailure(t *testing.T) {
	env := map[string]string{"VISUAL": "editor --wait", "EDITOR": "fallback"}
	lookup := func(name string) (string, error) {
		if name == "editor" {
			return "/bin/editor", nil
		}
		return "", errors.New("not found")
	}
	got, err := chooseEditor(func(key string) string { return env[key] }, lookup)
	if err != nil || !reflect.DeepEqual(got, []string{"/bin/editor", "--wait"}) {
		t.Fatal(got, err)
	}
	env["VISUAL"] = "missing"
	if _, err = chooseEditor(func(key string) string { return env[key] }, lookup); err == nil {
		t.Fatal("silently ignored explicit editor")
	}
}

func TestConfigurationOptionsJSONNeverEchoArguments(t *testing.T) {
	for _, check := range []bool{false, true} {
		var out bytes.Buffer
		code := configurationCommand([]string{"--json", "--SECRET=bad"}, nil, &out, check)
		var result Result
		if json.Unmarshal(out.Bytes(), &result) != nil || code == 0 || strings.Contains(out.String(), "SECRET") || result.Changed {
			t.Fatal(code, out.String())
		}
	}
}

func TestManagedCommandsHaveSingleJSONOptionErrors(t *testing.T) {
	commands := map[string]func(io.Writer) int{
		"backup":           func(out io.Writer) int { return PackageBackup([]string{"--SECRET=bad", "--json"}, out) },
		"join":             func(out io.Writer) int { return Join([]string{"--SECRET=bad", "--json"}, nil, out) },
		"controller.setup": func(out io.Writer) int { return ControllerSetup([]string{"--SECRET=bad", "--json"}, nil, out) },
		"certificates":     func(out io.Writer) int { return AgentCertificates([]string{"--SECRET=bad", "--json"}, out) },
		"prepare-user":     func(out io.Writer) int { return PrepareUser([]string{"--SECRET=bad", "--json"}, out) },
		"renew-issuer": func(out io.Writer) int {
			return ControllerPKI([]string{"renew-issuer", "--SECRET=bad", "--json"}, nil, out)
		},
		"finish-migration": func(out io.Writer) int {
			return ControllerPKI([]string{"finish-migration", "--SECRET=bad", "--json"}, nil, out)
		},
	}
	for name, command := range commands {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			if command(&out) == 0 {
				t.Fatal("invalid options accepted")
			}
			var result Result
			decoder := json.NewDecoder(&out)
			if decoder.Decode(&result) != nil || result.SchemaVersion != 1 || result.Status != "failed" || result.Changed || result.Uncertain || result.Code == "" {
				t.Fatal(out.String())
			}
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				t.Fatal("not exactly one result")
			}
		})
	}
}
