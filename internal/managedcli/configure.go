package managedcli

import (
	"bytes"
	"errors"
	"flag"
	"hlcp/internal/agent"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Configure edits only the existing YAML, as the invoking configuration owner.
// JSON is a read-only inspection: it never launches another program.
func Configure(args []string, input io.Reader, out io.Writer) int {
	return configurationCommand(args, input, out, false)
}

func CheckConfig(args []string, out io.Writer) int {
	return configurationCommand(args, nil, out, true)
}

func configurationCommand(args []string, input io.Reader, out io.Writer, check bool) int {
	operation := "agent.configure"
	if check {
		operation = "agent.check-config"
	}
	jsonMode := has(args, "--json")
	f := flag.NewFlagSet(operation, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	scopeDefault := "user"
	if os.Geteuid() == 0 {
		scopeDefault = "system"
	}
	scope := f.String("scope", scopeDefault, "user or system configuration owner")
	f.BoolVar(&jsonMode, "json", jsonMode, "versioned read-only JSON; never open an editor")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return emitResult(out, jsonMode, Result{Operation: operation, Status: "ok", Code: "HELP", Phase: "options", Message: "Usage: hlcp-agent configure|check-config [--scope user|system] [--json]. Configure opens existing YAML using VISUAL, EDITOR, nano or vi. No automatic restart, permission repair or YAML rewriting."})
		}
		return resultError(out, jsonMode, operation, "options", "INVALID_OPTIONS", "Invalid configuration command. Nothing changed.", "Use --help.")
	}
	if f.NArg() != 0 {
		return resultError(out, jsonMode, operation, "options", "INVALID_OPTIONS", "Unexpected arguments. Nothing changed.", "Use --help.")
	}
	p, err := pathsFor(*scope)
	if err != nil {
		return resultError(out, jsonMode, operation, "account", "SCOPE_OR_ACCOUNT_INVALID", "Cannot access the selected configuration owner.", "Run user scope as the actual agent owner without sudo.")
	}
	path := filepath.Join(p.directory, "agent.yaml")
	before, err := readPrivate(path, p.owner)
	if err != nil {
		return resultError(out, jsonMode, operation, "read", "CONFIG_UNAVAILABLE", "Cannot read the private agent YAML. Nothing changed.", "Check path, ownership and permissions. If not paired yet, use hlcp-agent join.")
	}
	data := map[string]any{"path": path, "scope": *scope, "service_restarted": false}
	if check {
		if _, err = agent.Parse(before); err != nil {
			return resultError(out, jsonMode, operation, "validate", "CONFIG_INVALID", "Agent YAML did not pass syntax/schema validation. Nothing changed.", "Edit with hlcp-agent configure. YAML values are omitted from errors to protect secrets.")
		}
		data["validation"] = "syntax_and_schema"
		data["runtime_verified"] = false
		return emitResult(out, jsonMode, Result{Operation: operation, Status: "ok", Code: "CONFIG_VALID", Phase: "validate", Message: "Agent YAML syntax and schema are valid. TLS files, service readiness and resource execution were not checked.", Data: data})
	}
	if jsonMode {
		return emitResult(out, true, Result{Operation: operation, Status: "preview", Code: "CONFIG_LOCATION", Phase: "inspect", Message: "Configuration location inspected. No editor opened and nothing changed.", Data: data})
	}
	argv, err := chooseEditor(os.Getenv, exec.LookPath)
	if err != nil {
		return resultError(out, false, operation, "editor", "EDITOR_UNAVAILABLE", "No usable editor selected. Nothing changed.", "Set VISUAL or EDITOR to an executable and optional quoted arguments; no shell expressions.")
	}
	cmd := exec.Command(argv[0], append(argv[1:], path)...)
	cmd.Stdin = input
	cmd.Stdout = out
	cmd.Stderr = out
	editErr := cmd.Run()
	after, readErr := readPrivate(path, p.owner)
	changed := readErr == nil && !bytes.Equal(before, after)
	r := Result{Operation: operation, Status: "ok", Code: "EDITOR_CLOSED", Phase: "editor", Changed: changed, Message: "Editor closed; YAML contents are unchanged.", Data: data, Next: "Run hlcp-agent restart to validate and load changes, or restart --dry-run for a read-only plan. Use the same --scope/--unit as your installed service."}
	if changed {
		r.Message = "Agent YAML was edited. No validation or service restart was performed."
		r.Completed = []string{"yaml_edited"}
	}
	if editErr != nil || readErr != nil {
		r.Status = "uncertain"
		r.Code = "EDITOR_OUTCOME_UNVERIFIED"
		r.Uncertain = true
		r.Message = "Editor did not complete cleanly, or the edited file no longer passes private-file checks. Edits were not rolled back."
		if readErr == nil {
			r.Uncertain = false
			r.Status = "failed"
			if changed {
				r.Status = "partial"
			}
			r.Code = "EDITOR_FAILED"
			r.Message = "Editor exited with an error. Existing edits were retained; no service restart was performed."
		}
	}
	return emitResult(out, false, r)
}

func chooseEditor(getenv func(string) string, lookPath func(string) (string, error)) ([]string, error) {
	for _, key := range []string{"VISUAL", "EDITOR"} {
		if value := strings.TrimSpace(getenv(key)); value != "" {
			argv, err := editorArguments(value)
			if err != nil {
				return nil, err
			}
			exe, err := lookPath(argv[0])
			if err != nil {
				return nil, err
			}
			argv[0] = exe
			return argv, nil
		}
	}
	for _, name := range []string{"nano", "vi"} {
		if exe, err := lookPath(name); err == nil {
			return []string{exe}, nil
		}
	}
	return nil, errors.New("editor unavailable")
}

// Parse quotes for argv grouping only. Never invoke a shell, expand variables,
// execute substitutions, or interpret redirections from editor settings.
func editorArguments(value string) ([]string, error) {
	var args []string
	var word strings.Builder
	var quote rune
	started := false
	for _, c := range value {
		if quote != 0 {
			if c == quote {
				quote = 0
			} else {
				word.WriteRune(c)
			}
			started = true
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			started = true
		case ' ', '\t':
			if started {
				args = append(args, word.String())
				word.Reset()
				started = false
			}
		case '\n', '\r', 0:
			return nil, errors.New("invalid editor")
		default:
			word.WriteRune(c)
			started = true
		}
	}
	if quote != 0 {
		return nil, errors.New("unterminated editor quote")
	}
	if started {
		args = append(args, word.String())
	}
	if len(args) == 0 || args[0] == "" {
		return nil, errors.New("empty editor")
	}
	return args, nil
}
