package onboarding

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"hlcp/deploy/systemd"
	"hlcp/internal/agent"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrSandboxProbeInvalid     = errors.New("sandbox probe evidence or inputs are invalid")
	ErrSandboxProbeUnsupported = errors.New("sandbox probe only supports fixed script metadata in the initial system profile")
	ErrSandboxProbeConsent     = errors.New("sandbox probe requires explicit consent")
	ErrSandboxProbeStale       = errors.New("sandbox probe plan is stale or already used")
	ErrSandboxProbeIncomplete  = errors.New("sandbox probe outcome is uncertain; inspect state before retry")
)

const sandboxProbeUnit = "hlcp-agent-resource-probe.service"
const sandboxProbeTimeout = 30 * time.Second

// SourceProof binds installed binary/manager/profile, imported config/TLS,
// registered service identity and state metadata without reading the journal.
// The initial probe never runs alongside an active agent or an existing probe.
type SandboxProbeSnapshot struct {
	SourceProof                [32]byte
	ConfigProof                [32]byte
	UID, GID                   uint32
	AgentInactive, ProbeAbsent bool
}
type sandboxProbeFile struct{ Executable, WorkingDirectory string }

// Inputs carry only immutable executable/workdir pairs, never argv, environment,
// PEM, resource/action names, journal paths or arbitrary unit/command options.
type SandboxProbeInputs struct {
	files                []sandboxProbeFile
	binding, configProof [32]byte
}

func (i SandboxProbeInputs) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Files int `json:"file_checks"`
	}{len(i.files)})
}
func (i SandboxProbeInputs) String() string   { data, _ := i.MarshalJSON(); return string(data) }
func (i SandboxProbeInputs) GoString() string { return i.String() }

// Evidence is supplied by a trusted native adapter, not a network client.
// It must confirm the fixed inspector finished AND its transient unit was
// collected, with actual service identity/sandbox and the exact request binding.
type SandboxProbeEvidence struct {
	SourceProof, RequestProof, DependencyProof [32]byte
	UID, GID                                   uint32
	InspectorExited, UnitCollected             bool
}
type SandboxProbeHost interface {
	Caller(context.Context) (uint32, uint32, error)
	InspectProbe(context.Context) (SandboxProbeSnapshot, error)
	// This is the only mutation. Launch only the installed metadata inspector in
	// the fixed sandbox; never scripts/runtimes, start/enable the main agent,
	// create grants or read/lock the journal. Must honor timeout and cancellation.
	RunProbe(context.Context, SandboxProbeSnapshot, SandboxProbeInputs) (SandboxProbeEvidence, error)
}
type SandboxProbePreview struct {
	ReadOnly        bool   `json:"read_only"`
	Unit            string `json:"unit"`
	FileChecks      int    `json:"file_checks"`
	RequiresConsent bool   `json:"requires_consent"`
}
type SandboxProbeResult struct {
	Completed bool `json:"completed"`
	Uncertain bool `json:"uncertain"`
}
type SandboxProbe struct{ *sandboxProbeState }
type sandboxProbeState struct {
	mutex           sync.Mutex
	host            SandboxProbeHost
	snapshot        SandboxProbeSnapshot
	inputs          SandboxProbeInputs
	used            bool
	dependencyProof [32]byte
}

// Planning only reads host evidence. Probe success does not open the system
// resource activation gate or establish resource execution/VM readiness.
func PlanSandboxProbe(ctx context.Context, host SandboxProbeHost, config agent.Config) (*SandboxProbe, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	inputs, err := sandboxProbeInputs(config)
	if err != nil {
		return nil, err
	}
	if host == nil {
		return nil, ErrSandboxProbeInvalid
	}
	uid, gid, err := host.Caller(ctx)
	if err != nil || uid != 0 || gid != 0 {
		return nil, ErrSandboxProbeInvalid
	}
	inspectCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	snapshot, err := host.InspectProbe(inspectCtx)
	if err != nil || inspectCtx.Err() != nil || !validSandboxProbeSnapshot(snapshot) || snapshot.ConfigProof != inputs.configProof {
		return nil, ErrSandboxProbeInvalid
	}
	return &SandboxProbe{&sandboxProbeState{host: host, snapshot: snapshot, inputs: inputs}}, nil
}
func validSandboxProbeSnapshot(s SandboxProbeSnapshot) bool {
	return s.SourceProof != [32]byte{} && s.ConfigProof != [32]byte{} && s.UID != 0 && s.GID != 0 && s.AgentInactive && s.ProbeAbsent
}
func sandboxProbeInputs(config agent.Config) (SandboxProbeInputs, error) {
	if len(config.Resources) == 0 || len(config.Resources) > 256 || config.Validate() != nil {
		return SandboxProbeInputs{}, ErrSandboxProbeInvalid
	}
	unit, ok := systemd.PackagedAgentUnit("system", "hlcp-agent.service")
	if !ok || !systemResourceProfileMatches(unit) {
		return SandboxProbeInputs{}, ErrSandboxProbeUnsupported
	}
	files := []sandboxProbeFile{}
	count := 0
	for _, resource := range config.Resources {
		if resource.Adapter != "script" {
			return SandboxProbeInputs{}, ErrSandboxProbeUnsupported
		}
		for _, action := range resource.Actions {
			count++
			if count > 128 {
				return SandboxProbeInputs{}, ErrSandboxProbeUnsupported
			}
			command := resource.Commands[action]
			for _, path := range []string{command.Executable, command.WorkingDirectory} {
				if path == config.CertificateFile || path == config.PrivateKeyFile || path == config.CAFile {
					return SandboxProbeInputs{}, ErrSandboxProbeUnsupported
				}
				if !validInspectorPath(path) || path == config.JournalFile || strings.HasPrefix(path, config.JournalFile+".") {
					return SandboxProbeInputs{}, ErrSandboxProbeUnsupported
				}
			}
			files = append(files, sandboxProbeFile{command.Executable, command.WorkingDirectory})
		}
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].Executable == files[j].Executable {
			return files[i].WorkingDirectory < files[j].WorkingDirectory
		}
		return files[i].Executable < files[j].Executable
	})
	unique := files[:0]
	for _, file := range files {
		if len(unique) == 0 || file != unique[len(unique)-1] {
			unique = append(unique, file)
		}
	}
	// Bind the complete config (including fixed argv/env) without giving those
	// values to the inspector or public output. Config is not persisted here.
	encoded, err := json.Marshal(struct {
		Config  agent.Config
		Profile string
		Files   []sandboxProbeFile
	}{config, unit, unique})
	if err != nil || len(encoded) > 1<<20 {
		return SandboxProbeInputs{}, ErrSandboxProbeInvalid
	}
	configData, err := json.Marshal(config)
	if err != nil {
		return SandboxProbeInputs{}, ErrSandboxProbeInvalid
	}
	return SandboxProbeInputs{append([]sandboxProbeFile{}, unique...), sha256.Sum256(encoded), sha256.Sum256(configData)}, nil
}
func (p SandboxProbe) Preview() SandboxProbePreview {
	if p.sandboxProbeState == nil {
		return SandboxProbePreview{ReadOnly: true, RequiresConsent: true}
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return SandboxProbePreview{ReadOnly: true, Unit: sandboxProbeUnit, FileChecks: len(p.inputs.files), RequiresConsent: true}
}
func (p SandboxProbe) MarshalJSON() ([]byte, error) { return json.Marshal(p.Preview()) }
func (p SandboxProbe) String() string               { data, _ := p.MarshalJSON(); return string(data) }
func (p SandboxProbe) GoString() string             { return p.String() }
func (p *SandboxProbe) Apply(ctx context.Context, confirmed bool) (SandboxProbeResult, error) {
	result := SandboxProbeResult{}
	if p == nil || p.sandboxProbeState == nil {
		return result, ErrSandboxProbeInvalid
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if !confirmed {
		return result, ErrSandboxProbeConsent
	}
	if p.used {
		return result, ErrSandboxProbeStale
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	uid, gid, err := p.host.Caller(ctx)
	if err != nil || uid != 0 || gid != 0 {
		return result, ErrSandboxProbeStale
	}
	bounded, cancel := context.WithTimeout(ctx, sandboxProbeTimeout)
	defer cancel()
	current, err := p.host.InspectProbe(bounded)
	if err != nil || bounded.Err() != nil || current != p.snapshot || !validSandboxProbeSnapshot(current) {
		return result, ErrSandboxProbeStale
	}
	// Consume immediately before dispatch; no implicit retries or rollback.
	p.used = true
	inputs := SandboxProbeInputs{append([]sandboxProbeFile{}, p.inputs.files...), p.inputs.binding, p.inputs.configProof}
	evidence, err := p.host.RunProbe(bounded, current, inputs)
	if err != nil || bounded.Err() != nil || !evidence.InspectorExited || !evidence.UnitCollected || evidence.SourceProof != current.SourceProof || evidence.RequestProof != p.inputs.binding || evidence.UID != current.UID || evidence.GID != current.GID || evidence.DependencyProof == [32]byte{} {
		result.Uncertain = true
		return result, ErrSandboxProbeIncomplete
	}
	after, err := p.host.InspectProbe(bounded)
	if err != nil || bounded.Err() != nil || after != current {
		result.Uncertain = true
		return result, ErrSandboxProbeIncomplete
	}
	p.dependencyProof = evidence.DependencyProof
	result.Completed = true
	return result, nil
}
