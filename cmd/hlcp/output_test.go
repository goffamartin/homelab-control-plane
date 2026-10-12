package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestTextRendering(t *testing.T) {
	for _, test := range []struct{ body, want string }{
		{`[]`, "(none)"}, {`null`, "(none)"}, {`["status","restart"]`, "ACTION"},
		{`[{"node_id":"n","state":"offline","agent_version":"dev"}]`, "offline"},
		{`[{"id":"catalog","type":"container","actions":["status"]}]`, "catalog"},
		{`{"id":"catalog","observation":{"state":"stopped","stale":true}}`, "observation.stale"},
		{`{"message":"bad\u001b[31m\nrow\tvalue"}`, "bad [31m row value"},
	} {
		var value any
		if err := json.Unmarshal([]byte(test.body), &value); err != nil {
			t.Fatal(err)
		}
		var buffer bytes.Buffer
		if err := renderText(&buffer, value); err != nil || !strings.Contains(buffer.String(), test.want) {
			t.Fatalf("%s => %s %v", test.body, buffer.String(), err)
		}
		if strings.Contains(buffer.String(), "\x1b") {
			t.Fatal("terminal escape leaked")
		}
	}
}

func TestCommandResultExitCodes(t *testing.T) {
	for _, test := range []struct {
		body, intent string
		code         int
	}{
		{`{"success":true,"code":"OK"}`, "command", 0},
		{`{"success":false,"code":"EXECUTION_FAILED"}`, "command", 3},
		{`{"success":false,"code":"EXECUTION_UNKNOWN"}`, "command", 5},
		{`{"state":"accepted"}`, "result", 5}, {`{"state":"running"}`, "result", 5},
		{`{"state":"unknown"}`, "result", 5}, {`{"state":"expired"}`, "result", 5},
		{`{"state":"completed","result":{"success":false,"code":"EXECUTION_FAILED"}}`, "result", 3},
		{`{"state":"completed","result":{"success":true,"code":"OK"}}`, "result", 0},
		{`{"read_only":false}`, "resolve", 0}, {`{}`, "command", 4},
	} {
		var value any
		_ = json.Unmarshal([]byte(test.body), &value)
		if got := resultExit(value, test.intent); got != test.code {
			t.Errorf("%s => %d want %d", test.body, got, test.code)
		}
	}
}
