package protocol

import (
	"errors"
	"testing"
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
		Resources:         []ResourceState{{ResourceID: "minecraft", State: "running"}},
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

func TestStateRejectsUnknownResource(t *testing.T) {
	inventory := InventorySnapshot{Revision: 1}
	state := StateSnapshot{
		InventoryRevision: 1,
		Resources:         []ResourceState{{ResourceID: "not-configured", State: "running"}},
	}

	err := state.ValidateAgainst(inventory)
	if !errors.Is(err, ErrUnknownResource) {
		t.Fatalf("ValidateAgainst() error = %v, want ErrUnknownResource", err)
	}
}
