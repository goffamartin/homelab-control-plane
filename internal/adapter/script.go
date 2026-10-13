package adapter

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"hlcp/internal/protocol"
)

// ScriptCommand is an operator-configured command, never remote shell input.
type ScriptCommand struct {
	Executable, WorkingDirectory string
	Argv                         []string
	Environment                  map[string]string
	Timeout                      time.Duration
}

type Script struct{ Commands map[string]ScriptCommand }

func (script Script) Execute(parent context.Context, action string) protocol.CommandResultPayload {
	command, exists := script.Commands[action]
	if !exists {
		return failure("ACTION_NOT_SUPPORTED", "action is not enabled")
	}
	if command.Timeout <= 0 || command.Timeout > 20*time.Second {
		return failure("INVALID_CONFIGURATION", "script timeout must be positive and at most 20s")
	}
	ctx, cancel := context.WithTimeout(parent, command.Timeout)
	defer cancel()
	return executeScript(ctx, command)
}

// Do not inherit the agent's service environment (TLS/bootstrap credentials,
// tokens, HOME, SSH_AUTH_SOCK, LD_PRELOAD, etc.). Local overrides are deliberate.
func scriptEnvironment(overrides map[string]string) []string {
	values := map[string]string{"PATH": "/usr/bin:/bin", "LANG": "C.UTF-8", "LC_ALL": "C.UTF-8"}
	for name, value := range overrides {
		values[name] = value
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	environment := make([]string, 0, len(names))
	for _, name := range names {
		environment = append(environment, name+"="+values[name])
	}
	return environment
}

type scriptOutput struct {
	mutex     sync.Mutex
	data      []byte
	truncated bool
}

func (output *scriptOutput) Write(data []byte) (int, error) {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	remaining := 4096 - len(output.data)
	if len(data) > remaining {
		output.truncated = true
	} else {
		remaining = len(data)
	}
	output.data = append(output.data, data[:remaining]...)
	return len(data), nil // Always drain; exceeding the capture budget is not a pipe error.
}

func (output *scriptOutput) message(prefix string) string {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	message := prefix + strings.ToValidUTF8(string(output.data), "?")
	const marker = "\n[output truncated]"
	truncated := output.truncated || len(message) > 4096
	limit := 4096
	if truncated {
		limit -= len(marker)
	}
	if len(message) > limit {
		message = message[:limit]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	if truncated {
		message += marker
	}
	return message
}
