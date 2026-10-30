package cli

import (
	"reflect"
	"strings"
	"testing"
)

func TestTagFiltersAreReadOnlyANDResourceLists(t *testing.T) {
	for _, args := range [][]string{{"-n", "n", "-r", "-t", "kostka", "--tag", "production", "--json"}, {"--tag", "kostka", "--node", "n", "--resources", "-t", "production"}} {
		r, err := Parse(args)
		if err != nil || Intent(r) != "resources" || !reflect.DeepEqual(r.Tags, []string{"kostka", "production"}) {
			t.Fatal(args, r, err)
		}
	}
	for _, suffix := range [][]string{{"-t"}, {"-t", ""}, {"-t", "Production"}, {"-t", "a", "--tag", "a"}, {"-t", "a", "-a", "restart"}, {"-t", "a", "--result", "op"}, {"-t", "a", "--dry-run"}, {"-t", "a", "--follow"}, {"-t", "a", "--command-id", "op"}} {
		if _, err := Parse(append([]string{"-n", "n", "-r"}, suffix...)); err == nil {
			t.Fatal("invalid/mutating tag selector accepted", suffix)
		}
	}
	for _, args := range [][]string{{"-t", "a"}, {"-n", "n", "-t", "a"}, {"-n", "n", "-r", "app", "-t", "a"}} {
		if _, err := Parse(args); err == nil {
			t.Fatal("tags accepted outside resource list", args)
		}
	}
	args := []string{"-n", "n", "-r"}
	for i := 0; i < 17; i++ {
		args = append(args, "-t", strings.Repeat("a", i+1))
	}
	if _, err := Parse(args); err == nil {
		t.Fatal("unbounded tag filters")
	}
}

func TestTagCompletionAndSelectorParsing(t *testing.T) {
	var calls int
	lookup := func(kind, node, resource string) []string {
		calls++
		if kind != "tags" || node != "n" {
			t.Fatal(kind, node, resource)
		}
		return []string{"kostka", "production", "production", "Bad", "bad\nword", "$(whoami)"}
	}
	args := []string{"-n", "n", "-r", "-t", "kostka", "--tag", "p"}
	if got := CompletionCandidates("hlcp", args, lookup); !reflect.DeepEqual(got, []string{"production"}) || calls != 1 {
		t.Fatal(got, calls)
	}
	r, ok := CompletionRequest(args)
	if !ok || r.Node != "n" || r.Resource != "" || !reflect.DeepEqual(r.Tags, []string{"kostka"}) {
		t.Fatal(r, ok)
	}
	CompletionCandidates("hlcp-agent", []string{"-t", ""}, lookup)
	CompletionCandidates("hlcp", []string{"-n", "n", "-t", "kostka", "-r", ""}, lookup)
	if calls != 1 {
		t.Fatal("agent or filtered-list completion performed an invalid lookup")
	}
}
