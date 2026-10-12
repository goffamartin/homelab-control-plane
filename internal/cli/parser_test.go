package cli

import "testing"

func TestParseProgressiveSelectors(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"-n"}, "nodes"},
		{nil, "nodes"},
		{[]string{"--nodes"}, "nodes"},
		{[]string{"--node", "debian-01"}, "node"},
		{[]string{"--node", "debian-01", "--resources"}, "resources"},
		{[]string{"--node", "debian-01", "--actions"}, "node-actions"},
		{[]string{"--node", "debian-01", "--resource", "catalog", "--actions"}, "actions"},
		{[]string{"--node", "debian-01", "--resource", "catalog", "--action", "restart", "--command-id", "op-1"}, "command"},
		{[]string{"--node", "debian-01", "--result", "op-1"}, "result"},
		{[]string{"--help"}, "help"},
		{[]string{"--node", "debian-01", "-a"}, "node-actions"},
		{[]string{"--node", "debian-01", "-a", "status"}, "node-status"},
		{[]string{"-n", "debian-01", "-a", "restart"}, "node-command"},
		{[]string{"-n", "debian-01", "-r", "catalog"}, "resource"},
		{[]string{"--node", "debian-01", "-r"}, "resources"},
		{[]string{"-n", "debian-01", "-r", "catalog", "-a"}, "actions"},
		{[]string{"-n", "debian-01", "-r", "catalog", "-a", "restart"}, "command"},
	}
	for _, test := range cases {
		request, err := Parse(test.args)
		if err != nil || Intent(request) != test.want {
			t.Fatalf("%v => %#v %v", test.args, request, err)
		}
	}
}

func TestParseRejectsAmbiguousOrIncompleteRequests(t *testing.T) {
	for _, args := range [][]string{
		{"--resource"}, {"--action"}, {"--api"}, {"--api", "--json"}, {"--api-token-file"},
		{"-r"}, {"-a"}, {"--resources"}, {"--actions"}, {"-n", "-r"},
		{"--nodes", "--node", "n"}, {"-n", "n", "-n", "n"},
		{"-n", "n", "--resources", "--resource", "r"},
		{"-n", "n", "--actions", "--action", "status"},
		{"-n", "n", "-r", "-a", "status"}, {"--dry-run"},
		{"--result"}, {"--result", "op"}, {"-n", "n", "--result", "op", "-a", "status"},
		{"-n", "n", "--result", "op", "-r"}, {"-n", "n", "--result", "op", "--dry-run"},
		{"-n", "n", "--command-id", "op"}, {"-n", "n", "--result", "op", "--command-id", "op"},
		{"-n", "n", "--result", "../op"}, {"--json", "--json"}, {"--api", ""},
	} {
		if request, err := Parse(args); err == nil {
			t.Errorf("accepted %v: %#v", args, request)
		}
	}
}

func TestParseRequiresExplicitSingularValues(t *testing.T) {
	if _, err := Parse([]string{"--node"}); err == nil {
		t.Fatal("--node without value should fail")
	}
	request, err := Parse([]string{"--nodes"})
	if err != nil || Intent(request) != "nodes" {
		t.Fatalf("--nodes = %#v, %v", request, err)
	}
}
