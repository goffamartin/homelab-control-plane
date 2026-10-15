package agent

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"hlcp/internal/protocol"
)

const (
	DefaultScriptTimeout            = 10 * time.Second
	MaximumScriptTimeout            = 20 * time.Second
	maximumScriptArguments          = 32
	maximumScriptArgumentBytes      = 8 * 1024
	maximumScriptEnvironmentEntries = 32
	maximumScriptEnvironmentBytes   = 8 * 1024
)

// ScriptCommandConfig describes a local Linux executable, not a shell command
// received over HLCP. Nothing in this contract executes or probes the file.
type ScriptCommandConfig struct {
	Executable       string            `yaml:"executable"`
	Argv             []string          `yaml:"argv"`
	WorkingDirectory string            `yaml:"working_directory"`
	Timeout          time.Duration     `yaml:"timeout"`
	Environment      map[string]string `yaml:"environment"`
}

func (command ScriptCommandConfig) ExecutionTimeout() time.Duration {
	if command.Timeout == 0 {
		return DefaultScriptTimeout
	}
	return command.Timeout
}

func (resource ResourceConfig) validateScript() error {
	if resource.Type != "task" || resource.Target != "" {
		return fmt.Errorf("resource %q: script requires type task and no target", resource.ID)
	}
	return resource.validateCommands()
}

func nativeAction(action string) bool {
	switch action {
	case "status", "start", "stop", "restart":
		return true
	default:
		return false
	}
}

// ScriptActions includes only explicitly enabled local commands. Native status
// observation never traverses this list or executes a custom action.
func (resource ResourceConfig) ScriptActions() []string {
	var actions []string
	for _, action := range resource.Actions {
		if _, ok := resource.Commands[action]; ok {
			actions = append(actions, action)
		}
	}
	return actions
}

func (resource ResourceConfig) validateCommands() error {
	inventory := protocol.InventorySnapshot{Revision: 1, Resources: []protocol.ResourceDescriptor{{ID: resource.ID, Type: resource.Type, Actions: resource.Actions}}}
	if err := inventory.Validate(); err != nil {
		return err
	}
	for action, command := range resource.Commands {
		if !contains(resource.Actions, action) {
			return fmt.Errorf("resource %q: command %q is not enabled in actions", resource.ID, action)
		}
		if resource.Adapter != "script" && nativeAction(action) {
			return fmt.Errorf("resource %q: command %q conflicts with a reserved native action", resource.ID, action)
		}
		if err := command.validate(); err != nil {
			return fmt.Errorf("resource %q action %q: %w", resource.ID, action, err)
		}
	}
	for _, action := range resource.Actions {
		if _, exists := resource.Commands[action]; !exists && (resource.Adapter == "script" || !nativeAction(action)) {
			return fmt.Errorf("resource %q: action %q has no local command", resource.ID, action)
		}
	}
	return nil
}

func (command ScriptCommandConfig) validate() error {
	if !validScriptPath(command.Executable) || !validScriptPath(command.WorkingDirectory) {
		return errors.New("executable and working_directory must be clean absolute Linux paths (at most 4096 bytes)")
	}
	if command.Timeout < 0 || command.Timeout > MaximumScriptTimeout {
		return errors.New("timeout must be positive and at most 20s (zero uses 10s)")
	}
	if len(command.Argv) > maximumScriptArguments {
		return errors.New("argv exceeds 32 arguments")
	}
	argumentBytes := 0
	for _, argument := range command.Argv {
		if !validScriptString(argument) {
			return errors.New("argv must contain UTF-8 strings without NUL")
		}
		argumentBytes += len(argument) + 1 // Include the terminator in the budget.
	}
	if argumentBytes > maximumScriptArgumentBytes {
		return errors.New("argv exceeds 8 KiB")
	}
	if len(command.Environment) > maximumScriptEnvironmentEntries {
		return errors.New("environment exceeds 32 entries")
	}
	environmentBytes := 0
	for name, value := range command.Environment {
		if !validEnvironmentName(name) || !validScriptString(value) {
			return errors.New("environment requires POSIX variable names and UTF-8 values without NUL")
		}
		environmentBytes += len(name) + len(value) + 2 // '=' and terminator.
	}
	if environmentBytes > maximumScriptEnvironmentBytes {
		return errors.New("environment exceeds 8 KiB")
	}
	return nil
}

func validScriptPath(value string) bool {
	return len(value) <= 4096 && path.IsAbs(value) && path.Clean(value) == value && validScriptString(value)
}

func validScriptString(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func validEnvironmentName(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if character == '_' || character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || index > 0 && character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}
