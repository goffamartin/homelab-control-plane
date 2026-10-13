package cli

import (
	"fmt"
	"hlcp/internal/protocol"
)

// Parse accepts selectors with optional values: -n lists nodes, -n ID selects one.
func Parse(args []string) (Request, error) {
	var request Request
	seen := make(map[string]bool)
	for index := 0; index < len(args); index++ {
		argument := args[index]
		group := argument
		switch argument {
		case "-n", "--node", "--nodes":
			group = "node selector"
		case "-r", "--resource", "--resources":
			group = "resource selector"
		case "-a", "--action", "--actions":
			group = "action selector"
		case "--command-id", "--result":
			group = "command ID"
		case "-h", "--help":
			group = "help"
		}
		if seen[group] {
			return Request{}, fmt.Errorf("duplicate or conflicting %s", group)
		}
		seen[group] = true
		var target *string
		optional := false
		switch argument {
		case "-n":
			target = &request.Node
			request.NodeSpecified = true
			optional = true
		case "-r":
			target = &request.Resource
			request.ResourceSpecified = true
			optional = true
		case "-a":
			target = &request.Action
			request.ActionSpecified = true
			optional = true
		case "--node":
			target = &request.Node
			request.NodeSpecified = true
		case "--resource":
			target = &request.Resource
			request.ResourceSpecified = true
		case "--action":
			target = &request.Action
			request.ActionSpecified = true
		case "--nodes":
			request.NodeSpecified = true
		case "--resources":
			request.ResourceSpecified = true
		case "--actions":
			request.ActionSpecified = true
		case "-h", "--help":
			request.Help = true
		case "--version":
			request.Version = true
		case "--json":
			request.Output = "json"
		case "--dry-run":
			request.DryRun = true
		case "--api":
			target = &request.API
		case "--api-token-file":
			target = &request.APITokenFile
		case "--config":
			target = &request.ConfigFile
		case "--command-id", "--result":
			target = &request.CommandID
			request.Lookup = argument == "--result"
		default:
			return Request{}, fmt.Errorf("unknown argument %q", argument)
		}
		if target != nil {
			if index+1 < len(args) && len(args[index+1]) > 0 && args[index+1][0] != '-' {
				index++
				*target = args[index]
			} else if !optional {
				return Request{}, fmt.Errorf("%s requires a value", argument)
			}
		}
	}
	if request.Help || request.Version {
		return request, nil
	}
	if (request.ResourceSpecified || request.ActionSpecified || request.Lookup) && request.Node == "" {
		return Request{}, fmt.Errorf("resource, action and result selectors require a selected node")
	}
	if request.ResourceSpecified && request.Resource == "" && request.ActionSpecified {
		return Request{}, fmt.Errorf("an action requires a selected resource, not a resource list")
	}
	if request.Lookup && (request.ResourceSpecified || request.ActionSpecified || request.DryRun) {
		return Request{}, fmt.Errorf("--result cannot be combined with resource, action or dry-run")
	}
	if request.CommandID != "" && !request.Lookup && (request.Resource == "" || request.Action == "") {
		return Request{}, fmt.Errorf("--command-id requires a resource action")
	}
	if request.DryRun && request.Action == "" {
		return Request{}, fmt.Errorf("--dry-run requires an action")
	}
	if request.CommandID != "" {
		if err := (protocol.CommandPayload{CommandID: request.CommandID, ResourceID: "resource", Action: "status"}).Validate(); err != nil {
			return Request{}, fmt.Errorf("invalid command ID")
		}
	}
	return request, nil
}

// Intent turns the progressive-discovery syntax into one explicit operation.
func Intent(request Request) string {
	if request.Help {
		return "help"
	}
	if request.Version {
		return "version"
	}
	if request.Lookup {
		return "result"
	}
	if !request.NodeSpecified || request.Node == "" {
		return "nodes"
	}
	if request.ResourceSpecified && request.Resource == "" {
		return "resources"
	}
	if request.Resource == "" && request.ActionSpecified && request.Action == "" {
		return "node-actions"
	}
	if request.Resource == "" && request.Action == "status" {
		return "node-status"
	}
	if request.Resource == "" {
		if request.Action != "" {
			return "node-command"
		}
		return "node"
	}
	if request.ActionSpecified && request.Action == "" {
		return "actions"
	}
	if request.Action == "" {
		return "resource"
	}
	return "command"
}
