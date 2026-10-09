package protocol

import "errors"

// COMMAND_QUERY uses CommandPayload as its immutable fingerprint. It never
// admits an unknown command and never grants permission to execute anything.
type CommandStatePayload struct {
	CommandID string                `cbor:"command_id" json:"command_id"`
	State     string                `cbor:"state" json:"state"`
	Result    *CommandResultPayload `cbor:"result,omitempty" json:"result,omitempty"`
}

func (payload CommandStatePayload) Validate() error {
	if !isIdentifier(payload.CommandID, 128) {
		return errors.New("invalid command state ID")
	}
	if payload.State == "completed" {
		if payload.Result == nil || payload.Result.CommandID != payload.CommandID {
			return errors.New("missing or mismatched command state result")
		}
		return payload.Result.Validate()
	}
	if payload.Result != nil {
		return errors.New("nonterminal command state has a result")
	}
	switch payload.State {
	case "not_found", "queued", "running", "unknown":
		return nil
	default:
		return errors.New("invalid command state")
	}
}
