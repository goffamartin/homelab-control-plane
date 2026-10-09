package protocol

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestInventoryAndStateSnapshotValidate(t *testing.T) {
	inventory := InventorySnapshot{
		Revision: 7,
		Resources: []ResourceDescriptor{
			{ID: "minecraft", Type: "service", Actions: []string{"status", "start", "stop", "restart"}},
		},
	}
	state := StateSnapshot{
		InventoryRevision: 7,
		Resources:         []ResourceState{{ResourceID: "minecraft", State: "running", ObservedAt: time.Now().UTC()}},
	}

	if err := inventory.Validate(); err != nil {
		t.Fatalf("inventory.Validate() error = %v", err)
	}
	if err := state.ValidateAgainst(inventory); err != nil {
		t.Fatalf("state.ValidateAgainst() error = %v", err)
	}
}

func TestInventoryRejectsDuplicateResource(t *testing.T) {
	inventory := InventorySnapshot{
		Revision: 1,
		Resources: []ResourceDescriptor{
			{ID: "minecraft", Type: "service", Actions: []string{"status"}},
			{ID: "minecraft", Type: "service", Actions: []string{"status"}},
		},
	}

	err := inventory.Validate()
	if !errors.Is(err, ErrDuplicateResource) {
		t.Fatalf("Validate() error = %v, want ErrDuplicateResource", err)
	}
}

func TestInventoryWithSixtyFiveResourcesRoundTrips(t *testing.T) {
	inventory := InventorySnapshot{Revision: 1}
	for index := 0; index < 65; index++ {
		inventory.Resources = append(inventory.Resources, ResourceDescriptor{ID: fmt.Sprintf("resource-%d", index), Type: "container", Actions: []string{"status"}})
	}
	encoded, err := EncodePayload(inventory)
	if err != nil {
		t.Fatal(err)
	}
	var decoded InventorySnapshot
	if err := DecodePayload(encoded, &decoded); err != nil {
		t.Fatalf("DecodePayload() error = %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestInventoryRejectsMoreThanMaximumResources(t *testing.T) {
	inventory := InventorySnapshot{Revision: 1, Resources: make([]ResourceDescriptor, maxResourcesPerNode+1)}
	if err := inventory.Validate(); !errors.Is(err, ErrTooManyResources) {
		t.Fatalf("Validate() error = %v, want ErrTooManyResources", err)
	}
}

func TestStateRejectsUnknownResource(t *testing.T) {
	inventory := InventorySnapshot{Revision: 1}
	state := StateSnapshot{
		InventoryRevision: 1,
		Resources:         []ResourceState{{ResourceID: "not-configured", State: "running", ObservedAt: time.Now().UTC()}},
	}

	err := state.ValidateAgainst(inventory)
	if !errors.Is(err, ErrUnknownResource) {
		t.Fatalf("ValidateAgainst() error = %v, want ErrUnknownResource", err)
	}
}
