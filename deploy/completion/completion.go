// Package completion contains the same Bash template for manual and Debian use.
package completion

import (
	_ "embed"
	"fmt"
	"hlcp/internal/cli"
	"io"
	"strings"
)

//go:embed bash.tpl
var bashTemplate string

func Bash(command string) string {
	switch command {
	case "hlcp", "hlcp-agent", "hlcp-controller":
		return strings.ReplaceAll(bashTemplate, "@COMMAND@", command)
	}
	return ""
}

func Handle(command string, args []string, out io.Writer) (bool, int) {
	if len(args) == 0 {
		return false, 0
	}
	if args[0] == "completion" {
		if len(args) == 2 && args[1] == "bash" {
			_, err := io.WriteString(out, Bash(command))
			if err != nil {
				return true, 1
			}
			return true, 0
		}
		fmt.Fprintln(out, "Usage: "+command+" completion bash")
		return true, 2
	}
	if args[0] == "__complete" {
		for _, c := range cli.CompletionCandidates(command, args[1:], nil) {
			fmt.Fprintln(out, c)
		}
		return true, 0
	}
	return false, 0
}
