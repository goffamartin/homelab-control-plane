package managedcli

import (
	"encoding/json"
	"fmt"
	"io"
)

// Result is the versioned administrative CLI result, not a protocol/API payload.
// changed describes this invocation only; uncertainty must never imply rollback.
type Result struct {
	SchemaVersion int      `json:"schema_version"`
	Operation     string   `json:"operation"`
	Status        string   `json:"status"`
	Code          string   `json:"code"`
	Phase         string   `json:"phase"`
	Message       string   `json:"message"`
	Changed       bool     `json:"changed"`
	Uncertain     bool     `json:"uncertain"`
	Completed     []string `json:"completed"`
	Next          string   `json:"next,omitempty"`
	Data          any      `json:"data,omitempty"`
}

func emitResult(out io.Writer, jsonMode bool, r Result) int {
	r.SchemaVersion = 1
	if r.Completed == nil {
		r.Completed = []string{}
	}
	if jsonMode {
		if json.NewEncoder(out).Encode(r) != nil {
			return 1
		}
	} else {
		if _, err := fmt.Fprintf(out, "%s\n", r.Message); err != nil {
			return 1
		}
		if r.Status == "failed" || r.Status == "partial" || r.Status == "uncertain" {
			if _, err := fmt.Fprintf(out, "Reason: %s (step: %s).\n", r.Code, r.Phase); err != nil {
				return 1
			}
		}
		if len(r.Completed) > 0 && r.Status != "ok" {
			if _, err := fmt.Fprintf(out, "Completed: %v\n", r.Completed); err != nil {
				return 1
			}
		}
		if r.Uncertain {
			if _, err := fmt.Fprintln(out, "The last operation's outcome is unverified. Inspect actual state before retrying; no rollback was performed."); err != nil {
				return 1
			}
		}
		if r.Next != "" {
			if _, err := fmt.Fprintln(out, "Next:", r.Next); err != nil {
				return 1
			}
		}
	}
	if r.Status == "failed" || r.Status == "partial" || r.Status == "uncertain" {
		return 1
	}
	return 0
}

func resultError(out io.Writer, jsonMode bool, operation, phase, code, message, next string) int {
	return emitResult(out, jsonMode, Result{Operation: operation, Phase: phase, Status: "failed", Code: code, Message: message, Next: next})
}
