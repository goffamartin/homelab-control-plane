package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"unicode"
)

// JSON is the API contract; the text view is intentionally presentation-only.
func renderText(out io.Writer, value any) error {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	switch value := value.(type) {
	case nil:
		fmt.Fprintln(w, "(none)")
	case []any:
		if len(value) == 0 {
			fmt.Fprintln(w, "(none)")
			break
		}
		first, objects := value[0].(map[string]any)
		if !objects {
			fmt.Fprintln(w, "ACTION")
			for _, item := range value {
				fmt.Fprintln(w, display(item))
			}
			break
		}
		keys := []string{"id", "type", "actions"}
		if _, nodes := first["node_id"]; nodes {
			keys = []string{"node_id", "state", "agent_version", "last_activity"}
		}
		fmt.Fprintln(w, strings.ToUpper(strings.Join(keys, "\t")))
		for _, item := range value {
			row, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("inconsistent API list")
			}
			cells := make([]string, len(keys))
			for i, key := range keys {
				cells[i] = display(row[key])
			}
			fmt.Fprintln(w, strings.Join(cells, "\t"))
		}
	case map[string]any:
		fmt.Fprintln(w, "FIELD\tVALUE")
		textFields(w, "", value)
	default:
		fmt.Fprintln(w, display(value))
	}
	return w.Flush()
}

func textFields(w io.Writer, prefix string, value map[string]any) {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if nested, ok := value[key].(map[string]any); ok {
			textFields(w, prefix+key+".", nested)
		} else {
			fmt.Fprintf(w, "%s\t%s\n", display(prefix+key), display(value[key]))
		}
	}
}

func display(value any) string {
	if value == nil {
		return "-"
	}
	var text string
	if s, ok := value.(string); ok {
		text = s
	} else {
		encoded, _ := json.Marshal(value)
		text = string(encoded)
	}
	// Runtime diagnostics must not inject terminal escapes or table rows.
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
}

func resultExit(value any, intent string) int {
	if intent != "command" && intent != "result" {
		return 0
	}
	object, ok := value.(map[string]any)
	if !ok {
		return 4
	}
	if intent == "result" {
		if object["state"] != "completed" {
			return 5
		}
		object, ok = object["result"].(map[string]any)
		if !ok {
			return 4
		}
	}
	if object["code"] == "EXECUTION_UNKNOWN" {
		return 5
	}
	if success, ok := object["success"].(bool); ok {
		if success {
			return 0
		}
		return 3
	}
	return 4
}
