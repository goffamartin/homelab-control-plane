package cli

import "fmt"

// Parse accepts selectors with optional values: -n lists nodes, -n ID selects one.
func Parse(args []string) (Request, error) {
	var request Request
	for index := 0; index < len(args); index++ {
		argument := args[index]
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
		case "--json":
			request.Output = "json"
		case "--dry-run":
			request.DryRun = true
		case "--api":
			if index+1 >= len(args) {
				return Request{}, fmt.Errorf("--api requires a value")
			}
			index++
			request.API = args[index]
		case "--api-token-file":
			if index+1 >= len(args) || len(args[index+1]) == 0 || args[index+1][0] == '-' {
				return Request{}, fmt.Errorf("--api-token-file requires a value")
			}
			index++
			request.APITokenFile = args[index]
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
	return request, nil
}

// Intent turns the progressive-discovery syntax into one explicit operation.
func Intent(request Request) string {
	if request.Help {
		return "help"
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
