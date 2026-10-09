package protocol

import (
	"strings"
	"testing"
	"time"
)

func TestStateUpdateRoundTripAndLimits(t *testing.T) {
	update := StateUpdate{SessionID: "s", InventoryRevision: 1, Revision: 1, Resource: ResourceState{ResourceID: "catalog", State: "running", ObservedAt: time.Now().UTC(), Code: "OK", Stale: true}}
	encoded, err := EncodePayload(update)
	var decoded StateUpdate
	if err != nil || DecodePayload(encoded, &decoded) != nil || decoded.Validate() != nil || decoded.Resource.ObservedAt.IsZero() || decoded.Resource.Stale {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	for _, change := range []func(*StateUpdate){func(u *StateUpdate) { u.SessionID = "" }, func(u *StateUpdate) { u.Revision = 0 }, func(u *StateUpdate) { u.InventoryRevision = 0 }, func(u *StateUpdate) { u.Resource.State = "invented" }, func(u *StateUpdate) { u.Resource.ObservedAt = time.Time{} }, func(u *StateUpdate) { u.Resource.ObservedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }, func(u *StateUpdate) { u.Resource.Message = strings.Repeat("x", 4097) }} {
		invalid := update
		change(&invalid)
		if invalid.Validate() == nil {
			t.Fatalf("accepted=%+v", invalid)
		}
	}
}
