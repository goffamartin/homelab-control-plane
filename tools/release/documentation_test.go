package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"hlcp/internal/agent"
)

// Check the actual allowlisted manual: links must also work after Debian installation.
func TestOperatorDocumentationLinks(t *testing.T) {
	root := filepath.Join("..", "..", "docs")
	allowed := make(map[string]bool)
	for _, name := range operatorDocumentation {
		allowed[filepath.Clean(filepath.FromSlash(name))] = true
	}
	link := regexp.MustCompile(`\[[^\]\r\n]*\]\(([^\s)]+)\)`)
	for _, name := range operatorDocumentation {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range link.FindAllSubmatch(data, -1) {
			target := strings.SplitN(string(match[1]), "#", 2)[0]
			if target == "" || strings.HasPrefix(target, "https://") {
				continue
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(filepath.FromSlash(name)), filepath.FromSlash(target)))
			if !allowed[resolved] {
				t.Errorf("%s: local link %q is not shipped in the operator manual", name, target)
			}
		}
	}
}

func TestOperatorDocumentationYAMLExamples(t *testing.T) {
	root := filepath.Join("..", "..", "docs")
	block := regexp.MustCompile("(?s)```yaml\\r?\\n(.*?)\\r?\\n```")
	// Guides deliberately show resource fragments, not replacement identity files.
	prefix := "controller_address: controller.example:9443\nserver_name: controller.example\n" +
		"certificate_file: /private/agent.crt\nprivate_key_file: /private/agent.key\nca_file: /private/ca.crt\n" +
		"journal_file: /private/journal.json\nnode_id: debian-01\nagent_version: v1.0.0\n"
	count := 0
	for _, name := range []string{"getting-started.md", "configuration.md", "reference/56-container-logs.md"} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range block.FindAllSubmatch(data, -1) {
			count++
			if _, err := agent.Parse([]byte(prefix + string(match[1]) + "\n")); err != nil {
				t.Errorf("%s: invalid documented YAML fragment: %v", name, err)
			}
		}
	}
	if count < 4 {
		t.Fatal("expected getting-started, container, custom-action and log YAML examples")
	}
}

func TestOperatorDocumentationPackageContents(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	for _, packageName := range []string{"hlcp-agent", "hlcp-controller"} {
		entries, err := appendOperatorDocumentation(nil, packageName)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != len(operatorDocumentation)+1 {
			t.Fatal("incomplete operator manual")
		}
		for _, entry := range entries {
			if strings.Contains(entry.name, "/archive/") || strings.Contains(entry.name, "implementation-plan") {
				t.Fatal("historical plans must not ship as an operator manual")
			}
		}
	}
}

func TestPublicInstallationGuide(t *testing.T) {
	checked := 0
	for _, name := range []string{"getting-started.md", "operations.md"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "docs", name))
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ReplaceAll(string(data), "\r\n", "\n")
		text = strings.ReplaceAll(text, "\\\n", " ")
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "gh auth login") || strings.HasPrefix(line, "gh release download") {
				t.Errorf("%s: public quick start must not require authenticated GitHub CLI", name)
			}
			if !strings.HasPrefix(line, "sh hlcp-install.sh ") {
				continue
			}
			checked++
			public := false
			for _, flag := range strings.Fields(line) {
				public = public || flag == "--public"
			}
			if !public {
				t.Errorf("%s: installer example requires explicit --public", name)
			}
		}
	}
	if checked < 7 {
		t.Fatal("expected fresh controller/agent and preview/apply update examples")
	}
}
