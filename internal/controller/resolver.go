package controller

import (
	"hlcp/internal/liveness"
	"hlcp/internal/protocol"
	"net/http"
)

type ResolveError struct {
	Status        int
	Code, Message string
}

func (err *ResolveError) Error() string { return err.Code + ": " + err.Message }

type Resolution struct {
	Node              string                   `json:"node"`
	Resource          string                   `json:"resource,omitempty"`
	Action            string                   `json:"action"`
	ReadOnly          bool                     `json:"read_only"`
	KnownOperation    bool                     `json:"known_operation"`
	SessionID         string                   `json:"session_id,omitempty"`
	InventoryRevision uint64                   `json:"inventory_revision,omitempty"`
	Command           *protocol.CommandPayload `json:"command,omitempty"`
}

func resolveError(status int, code, message string) *ResolveError {
	return &ResolveError{status, code, message}
}
func hasCapability(capabilities []string, value string) bool {
	for _, capability := range capabilities {
		if capability == value {
			return true
		}
	}
	return false
}

// Resolve is the shared read-only authority for dry-run and fresh admission.
// It never touches a runner, a command writer or a ledger mutation method.
func (server *Server) Resolve(nodeID string, payload protocol.CommandPayload) (Resolution, *ResolveError) {
	plan := Resolution{Node: nodeID, Resource: payload.ResourceID, Action: payload.Action}
	hello := protocol.HelloPayload{NodeID: nodeID, AgentVersion: "resolver", ProtocolVersions: []uint8{protocol.ProtocolVersion}}
	if hello.Validate() != nil {
		return Resolution{}, resolveError(http.StatusBadRequest, "INVALID_REQUEST", "invalid node ID")
	}
	if payload.ResourceID != "" {
		if payload.Validate() != nil {
			return Resolution{}, resolveError(http.StatusBadRequest, "INVALID_REQUEST", "invalid command")
		}
		plan.Command = &payload
		if server.sessions != nil {
			operation, exists, err := server.sessions.tracker.Operation(nodeID, payload.CommandID)
			if err != nil {
				return Resolution{}, resolveError(http.StatusServiceUnavailable, "LEDGER_FAILURE", err.Error())
			}
			if exists {
				if operation.Command != payload {
					return Resolution{}, resolveError(http.StatusConflict, "COMMAND_CONFLICT", ErrCommandConflict.Error())
				}
				plan.KnownOperation, plan.ReadOnly = true, true
				return plan, nil
			}
		}
	} else {
		validation := payload
		validation.ResourceID = "node"
		if validation.CommandID == "" {
			validation.CommandID = "node-status"
		}
		if validation.Validate() != nil {
			return Resolution{}, resolveError(http.StatusBadRequest, "INVALID_REQUEST", "invalid node action")
		}
	}
	record, exists := server.registry.Get(nodeID)
	if !exists {
		return Resolution{}, resolveError(http.StatusNotFound, "NODE_NOT_FOUND", "node not found")
	}
	plan.SessionID, plan.InventoryRevision = record.SessionID, record.Inventory.Revision
	if payload.ResourceID == "" {
		if payload.Action != "status" {
			return Resolution{}, resolveError(http.StatusUnprocessableEntity, "ACTION_NOT_SUPPORTED", "only read-only node status is supported")
		}
		plan.ReadOnly = true
		return plan, nil
	}
	for _, resource := range record.Inventory.Resources {
		if resource.ID != payload.ResourceID {
			continue
		}
		if !hasCapability(resource.Actions, payload.Action) {
			return Resolution{}, resolveError(http.StatusUnprocessableEntity, "ACTION_NOT_SUPPORTED", "action is not supported by resource")
		}
		if record.State != liveness.NodeOnline {
			return Resolution{}, resolveError(http.StatusServiceUnavailable, "NODE_OFFLINE", "node is not online")
		}
		if !hasCapability(record.Capabilities, "resource.control") {
			return Resolution{}, resolveError(http.StatusUnprocessableEntity, "CAPABILITY_NOT_SUPPORTED", "node does not advertise resource.control")
		}
		supported := false
		switch resource.Type {
		case "container":
			supported = hasCapability(record.Capabilities, "docker") || hasCapability(record.Capabilities, "podman")
		case "service":
			supported = hasCapability(record.Capabilities, "systemd")
		}
		if !supported {
			return Resolution{}, resolveError(http.StatusUnprocessableEntity, "CAPABILITY_NOT_SUPPORTED", "node does not advertise a provider for this resource type")
		}
		plan.ReadOnly = payload.Action == "status"
		return plan, nil
	}
	return Resolution{}, resolveError(http.StatusNotFound, "RESOURCE_NOT_FOUND", "resource not found")
}

type ResourceDetail struct {
	protocol.ResourceDescriptor
	Observation *protocol.ResourceState `json:"observation"`
}

func (server *Server) ResourceDetail(nodeID, resourceID string) (ResourceDetail, *ResolveError) {
	record, exists := server.registry.Get(nodeID)
	if !exists {
		return ResourceDetail{}, resolveError(http.StatusNotFound, "NODE_NOT_FOUND", "node not found")
	}
	for _, resource := range record.Inventory.Resources {
		if resource.ID != resourceID {
			continue
		}
		detail := ResourceDetail{ResourceDescriptor: resource}
		for _, state := range record.Resources.Resources {
			if state.ResourceID == resourceID {
				copy := state
				detail.Observation = &copy
				break
			}
		}
		return detail, nil
	}
	return ResourceDetail{}, resolveError(http.StatusNotFound, "RESOURCE_NOT_FOUND", "resource not found")
}
