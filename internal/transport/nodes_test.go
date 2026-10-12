package transport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllowedNodesStrict(t *testing.T) {
	for _, input := range []string{`null`, `{}`, `["a","a"]`, `["bad node"]`, `["a"] []`, `[1]`, strings.Repeat(" ", 65537)} {
		t.Run(input[:min(len(input), 30)], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "nodes.json")
			if err := os.WriteFile(path, []byte(input), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadAllowedNodes(path); err == nil {
				t.Fatal("accepted invalid allowlist")
			}
		})
	}
	for _, input := range []string{`[]`, `["debian-01","node:02"]`} {
		path := filepath.Join(t.TempDir(), "nodes.json")
		if err := os.WriteFile(path, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		nodes, err := LoadAllowedNodes(path)
		if err != nil || nodes == nil {
			t.Fatalf("explicit map: %v", err)
		}
		if input != "[]" && !nodes["debian-01"] {
			t.Fatal("missing node")
		}
	}
}
