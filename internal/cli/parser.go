package cli

import (
	"fmt"
	"hlcp/internal/protocol"
	"strconv"
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
		case "--follow", "--tail", "--since", "--until", "--contains", "--stream", "--cursor", "--limit":
			if request.Logs == nil {
				request.Logs = &LogOptions{Query: protocol.DefaultLogQuery(), Limit: 100}
			}
			if argument == "--follow" {
				request.Logs.Query.Follow = true
				break
			}
			if index+1 >= len(args) {
				return Request{}, fmt.Errorf("log option requires a value")
			}
			index++
			value := args[index]
			switch argument {
			case "--tail":
				n, err := strconv.Atoi(value)
				if err != nil {
					return Request{}, fmt.Errorf("invalid log tail")
				}
				request.Logs.Query.Tail = n
			case "--limit":
				n, err := strconv.Atoi(value)
				if err != nil {
					return Request{}, fmt.Errorf("invalid log page limit")
				}
				request.Logs.Limit = n
			case "--since":
				request.Logs.Query.Since = value
			case "--until":
				request.Logs.Query.Until = value
			case "--contains":
				request.Logs.Query.Contains = value
			case "--stream":
				request.Logs.Query.Stream = value
			case "--cursor":
				request.Logs.Cursor = value
			}
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
	if request.Logs != nil {
		l := request.Logs
		if request.Node == "" || request.Resource == "" || request.Action != "logs" || request.Lookup || request.CommandID != "" || l.Query.Validate() != nil || l.Limit < 1 || l.Limit > 200 || len(l.Cursor) > 160 {
			return Request{}, fmt.Errorf("invalid log options; select a container and action logs")
		}
		if l.Query.Follow && (l.Cursor != "" || seen["--limit"]) {
			return Request{}, fmt.Errorf("streaming does not accept a cursor or page limit")
		}
		if l.Cursor != "" && (seen["--tail"] || seen["--since"] || seen["--until"] || seen["--contains"] || seen["--stream"]) {
			return Request{}, fmt.Errorf("cursor pages reuse the original filters")
		}
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
