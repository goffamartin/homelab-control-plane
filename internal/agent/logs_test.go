package agent

import (
	"context"
	"hlcp/internal/protocol"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNativeLogsAreExplicitAndNeverCommandDispatched(t *testing.T) {
	for _, actions := range []string{"logs", "status, logs"} {
		c, err := Load(writeConfig(t, strings.Replace(validConfigYAML, "status, restart", actions, 1)))
		if err != nil {
			t.Fatal(err)
		}
		c.JournalFile = filepath.Join(t.TempDir(), "journal.json")
		d, err := c.NewDispatcher()
		if err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(c.JournalFile)
		admission := d.Admit(protocol.CommandPayload{CommandID: "wrong-channel", ResourceID: "example-catalog", Action: "logs"})
		if admission.Job != nil || admission.Result == nil || admission.Ack.Status != "rejected" {
			t.Fatal("native logs admitted as command", admission)
		}
		after, _ := os.ReadFile(c.JournalFile)
		if string(before) != string(after) {
			t.Fatal("wrong-channel request changed journal")
		}
		d.Close()
		caps := c.logCapabilities()
		if contains(caps, protocol.LogCapability) != (runtime.GOOS == "linux") {
			t.Fatal(caps)
		}
		if contains(c.Capabilities, protocol.LogCapability) {
			t.Fatal("derived capability mutated config")
		}
		called := false
		c.logReader = func(context.Context, string, string, protocol.LogQuery, func(protocol.LogRecord) error) string {
			called = true
			return "OK"
		}
		request := protocol.LogRequest{RequestID: "r", ResourceID: "other", Query: protocol.DefaultLogQuery()}
		if code := c.logHandler(context.Background(), request, nil); code != "NOT_ALLOWED" || called {
			t.Fatal(code, called)
		}
		request.ResourceID = "example-catalog"
		if code := c.logHandler(context.Background(), request, nil); code != "OK" || !called {
			t.Fatal(code, called)
		}
		called = false
		c.Resources[0].Actions = []string{"status"}
		if code := c.logHandler(context.Background(), request, nil); code != "NOT_ALLOWED" || called {
			t.Fatal(code, called)
		}
	}
}
