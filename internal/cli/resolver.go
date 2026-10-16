package cli

import "hlcp/internal/protocol"

type LogOptions struct {
	Query  protocol.LogQuery
	Cursor string
	Limit  int
}

// Request contains syntax and transport options only. The controller owns
// resource/action resolution and admission policy.
type Request struct {
	Node              string
	Resource          string
	Action            string
	NodeSpecified     bool
	ResourceSpecified bool
	ActionSpecified   bool
	Help              bool
	Version           bool
	DryRun            bool
	Output            string
	API               string
	APITokenFile      string
	CommandID         string
	Lookup            bool
	ConfigFile        string
	Logs              *LogOptions
}
