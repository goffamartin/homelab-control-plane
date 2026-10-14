package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go.yaml.in/yaml/v4"
	"hlcp/internal/agent"
	"hlcp/internal/setup"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var ErrAgentOptions = errors.New("onboarding: agent setup options are invalid")

type AgentOptions struct{ NodeID, ControllerAddress, ServerName, JournalFile, AgentVersion string }

// AgentSetup contains a private transaction plan. It cannot start services or
// admit a node, and never writes a journal or enables resources.
type AgentSetup struct {
	planner   *setup.Planner
	plan      *setup.Plan
	directory *setup.DirectoryPlan
	intents   []setup.Intent
}

func (s *AgentSetup) Preview() setup.Preview {
	if s == nil {
		return setup.Preview{ReadOnly: true}
	}
	preview := s.plan.Preview()
	if s.directory != nil {
		preview.Changes = append([]setup.Change{{Name: "configuration-directory", Action: "create-directory", Mode: 0700}}, preview.Changes...)
	}
	return preview
}
func (s AgentSetup) MarshalJSON() ([]byte, error) { return json.Marshal(s.Preview()) }
func (s AgentSetup) String() string               { data, _ := s.MarshalJSON(); return string(data) }
func (s AgentSetup) GoString() string             { return s.String() }
func (s *AgentSetup) Close() error {
	if s == nil || s.planner == nil {
		return nil
	}
	err := s.planner.Close()
	if s.directory != nil {
		if other := s.directory.Close(); err == nil {
			err = other
		}
	}
	return err
}
func (s *AgentSetup) Apply(ctx context.Context, confirmed bool) (setup.ApplyResult, error) {
	if s == nil || s.planner == nil || s.plan == nil {
		return setup.ApplyResult{}, ErrAgentOptions
	}
	if s.directory != nil {
		planner, err := s.directory.Create(ctx, confirmed)
		if err != nil {
			return setup.ApplyResult{}, err
		}
		defer planner.Close()
		plan, err := planner.Build(ctx, s.intents)
		if err != nil {
			return setup.ApplyResult{}, err
		}
		for _, change := range plan.Preview().Changes {
			if change.Action != "create" {
				return setup.ApplyResult{}, setup.ErrStalePlan
			}
		}
		return planner.Apply(ctx, plan, true)
	}
	return s.planner.Apply(ctx, s.plan, confirmed)
}

// PlanAgent binds generated TLS paths to the exact target planner directory.
// Existing directories require private owner/mode; a missing final directory
// is previewed and created only on confirmed Apply under an existing safe parent.
// Existing different config is a conflict: this fresh-install core has no force.
func PlanAgent(ctx context.Context, directory string, owner setup.Owner, options AgentOptions, material ImportedTLS, now time.Time) (*AgentSetup, error) {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || directory == "/" ||
		len(directory) > 4096 || strings.ContainsRune(directory, 0) || !filepath.IsAbs(options.JournalFile) ||
		filepath.Clean(options.JournalFile) != options.JournalFile || options.JournalFile == "/" ||
		len(options.JournalFile) > 4096 || strings.ContainsRune(options.JournalFile, 0) {
		return nil, ErrAgentOptions
	}
	relative, err := filepath.Rel(directory, options.JournalFile)
	if err != nil || relative == "." || !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, ErrAgentOptions
	}
	host, port, err := net.SplitHostPort(options.ControllerAddress)
	number, portErr := strconv.Atoi(port)
	if err != nil || !validHost(host) || !validHost(options.ServerName) || portErr != nil || number < 1 || number > 65535 {
		return nil, ErrAgentOptions
	}
	identity := Identity{Role: "agent", NodeID: options.NodeID}
	if _, err := material.Validate(identity, now); err != nil {
		return nil, err
	}
	config := agent.Config{ControllerAddress: options.ControllerAddress, ServerName: options.ServerName, NodeID: options.NodeID,
		CertificateFile: filepath.Join(directory, "agent.crt"), PrivateKeyFile: filepath.Join(directory, "agent.key"),
		CAFile: filepath.Join(directory, "ca.crt"), JournalFile: options.JournalFile, AgentVersion: options.AgentVersion,
		Capabilities: []string{}, Resources: []agent.ResourceConfig{}}
	if config.Validate() != nil {
		return nil, ErrAgentOptions
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		return nil, ErrAgentOptions
	}
	validateCert := func(data []byte) error {
		certs, err := parseCertificates(data)
		if err != nil {
			return err
		}
		return validateLeaf(certs[0], identity, now)
	}
	validateCA := func(data []byte) error { _, err := validCA(data, now); return err }
	intents := []setup.Intent{
		{Name: "agent.yaml", Data: data, Mode: 0600, Validate: func(data []byte) error { _, err := agent.Parse(data); return err }},
		{Name: "agent.crt", Data: material.certificate, Mode: 0600, Validate: validateCert},
		{Name: "agent.key", Data: material.key, Mode: 0600, Validate: validatePrivateKey},
		{Name: "ca.crt", Data: material.peerTrust, Mode: 0600, Validate: validateCA},
		{Name: "issuer-ca.crt", Data: material.issuerTrust, Mode: 0600, Validate: validateCA},
	}
	planner, err := setup.OpenPlanner(directory, owner)
	if err != nil {
		if !errors.Is(err, setup.ErrMissingDirectory) {
			return nil, err
		}
		directoryPlan, err := setup.PlanDirectory(directory, owner)
		if err != nil {
			return nil, err
		}
		previewPlanner, err := setup.NewPlanner(absentAgentSource{owner})
		if err != nil {
			directoryPlan.Close()
			return nil, err
		}
		preview, err := previewPlanner.Build(ctx, intents)
		if err != nil {
			previewPlanner.Close()
			directoryPlan.Close()
			return nil, err
		}
		for index := range intents {
			intents[index].Data = bytes.Clone(intents[index].Data)
		}
		return &AgentSetup{planner: previewPlanner, plan: preview, directory: directoryPlan, intents: intents}, nil
	}
	plan, err := planner.Build(ctx, intents)
	if err != nil {
		planner.Close()
		return nil, err
	}
	return &AgentSetup{planner: planner, plan: plan}, nil
}

type absentAgentSource struct{ owner setup.Owner }

func (s absentAgentSource) Owner() setup.Owner                       { return s.owner }
func (absentAgentSource) Read(string, int64) (setup.Snapshot, error) { return setup.Snapshot{}, nil }
func (absentAgentSource) Close() error                               { return nil }

func validHost(value string) bool {
	if net.ParseIP(value) != nil {
		return true
	}
	if len(value) == 0 || len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(value, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range label {
			if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-') {
				return false
			}
		}
	}
	return true
}
