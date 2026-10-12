package cli

import (
	"reflect"
	"strings"
	"testing"
)

func FuzzProgressiveParser(f *testing.F) {
	f.Add("-n\x00debian-01\x00-r\x00catalog\x00-a\x00restart")
	f.Add("--nodes\x00--node\x00n")
	f.Add("--node\x00n\x00--result\x00op-1")
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 8192 {
			t.Skip()
		}
		args := strings.Split(input, "\x00")
		request, err := Parse(args)
		if err != nil {
			return
		}
		again, err := Parse(args)
		if err != nil || !reflect.DeepEqual(request, again) {
			t.Fatal("non-deterministic parse")
		}
		intent := Intent(request)
		switch intent {
		case "help", "nodes":
		case "node", "resources", "node-actions", "node-status", "node-command":
			if request.Node == "" {
				t.Fatal("missing selected node")
			}
		case "resource", "actions", "command":
			if request.Node == "" || request.Resource == "" {
				t.Fatal("missing selected parent")
			}
		case "result":
			if request.Node == "" || request.CommandID == "" || request.ActionSpecified || request.ResourceSpecified || request.DryRun {
				t.Fatal("lookup can mutate")
			}
		default:
			t.Fatalf("unknown intent %q", intent)
		}
	})
}
