package cli

import (
	"errors"
	"fmt"
	"hlcp/internal/controller"
	"hlcp/internal/liveness"
	"hlcp/internal/protocol"
)

var (
	ErrNodeNotFound       = errors.New("NODE_NOT_FOUND")
	ErrNodeOffline        = errors.New("NODE_OFFLINE")
	ErrResourceNotFound   = errors.New("RESOURCE_NOT_FOUND")
	ErrActionNotSupported = errors.New("ACTION_NOT_SUPPORTED")
)

type Request struct {
	Node              string
	Resource          string
	Action            string
	NodeSpecified     bool
	ResourceSpecified bool
	ActionSpecified   bool
	Help              bool
	DryRun            bool
	Output            string
	API               string
	APITokenFile      string
}
type Resolved struct {
	Node     string                   `json:"node"`
	Resource string                   `json:"resource,omitempty"`
	Action   string                   `json:"action"`
	DryRun   bool                     `json:"dry_run"`
	Command  *protocol.CommandPayload `json:"command,omitempty"`
}

func Resolve(registry *controller.Registry, request Request, commandID string) (Resolved, error) {
	if request.Node == "" || request.Action == "" {
		return Resolved{}, fmt.Errorf("INVALID_REQUEST: node and action are required")
	}
	record, found := registry.Get(request.Node)
	if !found {
		return Resolved{}, ErrNodeNotFound
	}
	if record.State != liveness.NodeOnline {
		return Resolved{}, ErrNodeOffline
	}
	resolved := Resolved{Node: request.Node, Resource: request.Resource, Action: request.Action, DryRun: request.DryRun}
	if request.Resource == "" {
		if request.Action != "status" {
			return Resolved{}, ErrActionNotSupported
		}
		return resolved, nil
	}
	for _, resource := range record.Inventory.Resources {
		if resource.ID == request.Resource {
			for _, action := range resource.Actions {
				if action == request.Action {
					resolved.Command = &protocol.CommandPayload{CommandID: commandID, ResourceID: request.Resource, Action: request.Action}
					return resolved, nil
				}
			}
			return Resolved{}, ErrActionNotSupported
		}
	}
	return Resolved{}, ErrResourceNotFound
}
