package protocol

import "testing"

func TestCommandStateValidationAndRoundTrip(t *testing.T) {
	for _, state := range []string{"not_found", "queued", "running", "unknown", "completed"} {
		payload := CommandStatePayload{CommandID: "c", State: state}
		if state == "completed" {
			payload.Result = &CommandResultPayload{CommandID: "c", Success: true, Code: "OK"}
		}
		encoded, err := EncodePayload(payload)
		var decoded CommandStatePayload
		if err != nil || DecodePayload(encoded, &decoded) != nil || decoded.Validate() != nil {
			t.Fatalf("state=%s err=%v", state, err)
		}
	}
	for _, payload := range []CommandStatePayload{{CommandID: "c", State: "completed"}, {CommandID: "c", State: "invalid"}, {CommandID: "c", State: "completed", Result: &CommandResultPayload{CommandID: "other", Success: true, Code: "OK"}}, {CommandID: "c", State: "running", Result: &CommandResultPayload{CommandID: "c", Success: true, Code: "OK"}}} {
		if payload.Validate() == nil {
			t.Fatalf("accepted=%+v", payload)
		}
	}
}
