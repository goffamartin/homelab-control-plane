package cli

import "testing"

func TestParseProgressiveSelectors(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"-n"}, "nodes"},
		{[]string{"--node", "debian-01", "-a"}, "node-actions"},
		{[]string{"--node", "debian-01", "-a", "status"}, "node-status"},
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

func TestParseRequiresExplicitSingularValues(t *testing.T) {
	if _, err := Parse([]string{"--node"}); err == nil {
		t.Fatal("--node without value should fail")
	}
	request, err := Parse([]string{"--nodes"})
	if err != nil || Intent(request) != "nodes" {
		t.Fatalf("--nodes = %#v, %v", request, err)
	}
}
