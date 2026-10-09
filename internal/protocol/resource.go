package protocol

import (
	"errors"
	"fmt"
	"time"
)

const (
	maxResourcesPerNode   = 256
	maxActionsPerResource = 32
	maxResourceIDLength   = 128
	maxResourceTypeLength = 128
	maxActionLength       = 64
	maxStateLength        = 64
)

var (
	ErrTooManyResources         = errors.New("hlcp: too many resources")
	ErrDuplicateResource        = errors.New("hlcp: duplicate resource ID")
	ErrUnknownResource          = errors.New("hlcp: state references an unknown resource")
	ErrInvalidResource          = errors.New("hlcp: invalid resource")
	ErrInvalidResourceState     = errors.New("hlcp: invalid resource state")
	ErrInvalidInventoryRevision = errors.New("hlcp: invalid inventory revision")
	ErrIncompleteStateSnapshot  = errors.New("hlcp: incomplete state snapshot")
)

// ResourceDescriptor describes a controller-visible resource. Adapter and
// implementation target stay agent-local and are deliberately absent.
type ResourceDescriptor struct {
	ID      string   `cbor:"id" json:"id"`
	Type    string   `cbor:"type" json:"type"`
	Actions []string `cbor:"actions" json:"actions"`
}

func (resource ResourceDescriptor) validate() error {
	if !isIdentifier(resource.ID, maxResourceIDLength) || !isIdentifier(resource.Type, maxResourceTypeLength) {
		return ErrInvalidResource
	}
	if len(resource.Actions) == 0 || len(resource.Actions) > maxActionsPerResource {
		return ErrInvalidResource
	}

	actions := make(map[string]struct{}, len(resource.Actions))
	for _, action := range resource.Actions {
		if !isIdentifier(action, maxActionLength) {
			return ErrInvalidResource
		}
		if _, exists := actions[action]; exists {
			return fmt.Errorf("%w: duplicate action %q", ErrInvalidResource, action)
		}
		actions[action] = struct{}{}
	}
	return nil
}

// InventorySnapshot replaces the controller's resource inventory for one node.
// Revision increases whenever the agent's resource configuration changes.
type InventorySnapshot struct {
	Revision  uint64               `cbor:"revision" json:"revision"`
	Resources []ResourceDescriptor `cbor:"resources" json:"resources"`
}

func (snapshot InventorySnapshot) Validate() error {
	if snapshot.Revision == 0 {
		return ErrInvalidInventoryRevision
	}
	if len(snapshot.Resources) > maxResourcesPerNode {
		return ErrTooManyResources
	}

	resources := make(map[string]struct{}, len(snapshot.Resources))
	for _, resource := range snapshot.Resources {
		if err := resource.validate(); err != nil {
			return err
		}
		if _, exists := resources[resource.ID]; exists {
			return fmt.Errorf("%w: %s", ErrDuplicateResource, resource.ID)
		}
		resources[resource.ID] = struct{}{}
	}
	return nil
}

// ResourceState is a current agent observation, not a desired state. State is
// limited to running/stopped/unknown/error. Stale is a controller-only projection.
type ResourceState struct {
	ResourceID string    `cbor:"resource_id" json:"resource_id"`
	State      string    `cbor:"state" json:"state"`
	ObservedAt time.Time `cbor:"observed_at" json:"observed_at"`
	Code       string    `cbor:"code,omitempty" json:"code,omitempty"`
	Message    string    `cbor:"message,omitempty" json:"message,omitempty"`
	Stale      bool      `cbor:"-" json:"stale"`
}

func (state ResourceState) validate() error {
	if !isIdentifier(state.ResourceID, maxResourceIDLength) || !isIdentifier(state.State, maxStateLength) {
		return ErrInvalidResourceState
	}
	switch state.State {
	case "running", "stopped", "unknown", "error":
	default:
		return ErrInvalidResourceState
	}
	if state.Code != "" && !isIdentifier(state.Code, maxActionLength) || len(state.Message) > maxDiagnosticLength {
		return ErrInvalidResourceState
	}
	if state.ObservedAt.IsZero() && (state.State != "unknown" || state.Code != "NOT_OBSERVED") {
		return ErrInvalidResourceState
	}
	if state.ObservedAt.Year() < 1 || state.ObservedAt.Year() > 9999 {
		return ErrInvalidResourceState
	}
	return nil
}

type StateUpdate struct {
	SessionID         string        `cbor:"session_id" json:"session_id"`
	InventoryRevision uint64        `cbor:"inventory_revision" json:"inventory_revision"`
	Revision          uint64        `cbor:"revision" json:"revision"`
	Resource          ResourceState `cbor:"resource" json:"resource"`
}

func (update StateUpdate) Validate() error {
	if !isIdentifier(update.SessionID, maxSessionIDLength) || update.InventoryRevision == 0 || update.Revision == 0 {
		return ErrInvalidResourceState
	}
	return update.Resource.validate()
}

// StateSnapshot replaces dynamic resource state for the stated inventory
// revision. A controller must not combine it with an unrelated inventory.
type StateSnapshot struct {
	InventoryRevision uint64          `cbor:"inventory_revision" json:"inventory_revision"`
	Resources         []ResourceState `cbor:"resources" json:"resources"`
}

func (snapshot StateSnapshot) ValidateAgainst(inventory InventorySnapshot) error {
	if err := inventory.Validate(); err != nil {
		return err
	}
	if snapshot.InventoryRevision != inventory.Revision {
		return fmt.Errorf("hlcp: state snapshot revision %d does not match inventory revision %d", snapshot.InventoryRevision, inventory.Revision)
	}
	if len(snapshot.Resources) > maxResourcesPerNode {
		return ErrTooManyResources
	}

	knownResources := make(map[string]struct{}, len(inventory.Resources))
	for _, resource := range inventory.Resources {
		knownResources[resource.ID] = struct{}{}
	}

	states := make(map[string]struct{}, len(snapshot.Resources))
	for _, state := range snapshot.Resources {
		if err := state.validate(); err != nil {
			return err
		}
		if _, exists := knownResources[state.ResourceID]; !exists {
			return fmt.Errorf("%w: %s", ErrUnknownResource, state.ResourceID)
		}
		if _, exists := states[state.ResourceID]; exists {
			return fmt.Errorf("%w: %s", ErrDuplicateResource, state.ResourceID)
		}
		states[state.ResourceID] = struct{}{}
	}
	if len(states) != len(knownResources) {
		return ErrIncompleteStateSnapshot
	}
	return nil
}
