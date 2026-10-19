package main

import "path/filepath"

// Both packages ship the same current manual, never historical implementation plans.
var operatorDocumentation = []string{
	"README.md", "getting-started.md", "configuration.md", "cli.md",
	"operations.md", "security.md", "validation.md", "development.md",
	"reference/09-telemetry-and-audit.md",
	"reference/10-scoped-api-credentials.md",
	"reference/11-bounded-diagnostics-api.md",
	"reference/18-script-resource-contract.md",
	"reference/55-administrative-output.md",
	"reference/56-container-logs.md",
	"reference/57-agent-service-and-bash-completion.md",
}

func operatorDocumentationSources() []string {
	paths := make([]string, 0, len(operatorDocumentation))
	for _, name := range operatorDocumentation {
		paths = append(paths, "docs/"+name)
	}
	return paths
}

func appendOperatorDocumentation(data []debEntry, packageName string) ([]debEntry, error) {
	base := "usr/share/doc/" + packageName + "/"
	data = append(data, debEntry{base + "reference/", 0755, nil})
	for _, name := range operatorDocumentation {
		contents, err := debSource(filepath.Join("docs", filepath.FromSlash(name)), true)
		if err != nil {
			return nil, err
		}
		data = append(data, debEntry{base + name, 0644, contents})
	}
	return data, nil
}
