package onboarding

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"hlcp/internal/agent"
	"hlcp/internal/setup"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type nativeSandboxProbeHost struct{}

var nativeProbeCLIHost sandboxProbeCLIHost = nativeSandboxProbeHost{}

func (nativeSandboxProbeHost) Caller(ctx context.Context) (uint32, uint32, error) {
	return (nativeAgentServiceHost{}).Caller(ctx)
}

// No resource executable is inspected as root. Source config/TLS validation is
// shared with activation, but script metadata is checked only in the sandbox.
func (nativeSandboxProbeHost) source(ctx context.Context) (SandboxProbeSnapshot, agent.Config, error) {
	snapshot, config, err := inspectNativeSandboxSource(ctx)
	if err == nil && !snapshot.AgentInactive {
		return SandboxProbeSnapshot{}, agent.Config{}, ErrSandboxProbeInvalid
	}
	return snapshot, config, err
}

// Post-start inspection may observe active; all probe callers still require
// inactive through source(). Normalize lifecycle values in the fingerprint;
// actual flags remain separately bound in every snapshot comparison.
func inspectNativeSandboxSource(ctx context.Context) (SandboxProbeSnapshot, agent.Config, error) {
	fail := func() (SandboxProbeSnapshot, agent.Config, error) {
		return SandboxProbeSnapshot{}, agent.Config{}, ErrSandboxProbeInvalid
	}
	if nativeSandboxLauncherReady(ctx) != nil {
		return fail()
	}
	var config agent.Config
	var inputs SandboxProbeInputs
	profile := AgentServiceProfile{Scope: "system", Unit: "hlcp-agent.service"}
	source, err := (nativeAgentServiceHost{}).inspectAgentSource(ctx, profile, func(c agent.Config) ([32]byte, error) {
		var e error
		inputs, e = sandboxProbeInputs(c)
		config = c
		return inputs.configProof, e
	})
	if err != nil {
		if err == ErrSandboxProbeUnsupported {
			return SandboxProbeSnapshot{}, agent.Config{}, err
		}
		return fail()
	}
	inactive := source.Service.Active == "inactive"
	enabled := source.Service.Enabled
	source.Service.Active = "inactive"
	source.Service.Enabled = false
	account, err := serviceActivationAccount("hlcp")
	if err != nil {
		return fail()
	}
	fields := strings.Split(account, ":")
	uid, e1 := strconv.ParseUint(fields[2], 10, 32)
	gid, e2 := strconv.ParseUint(fields[3], 10, 32)
	if e1 != nil || e2 != nil || uid == 0 || gid == 0 {
		return fail()
	}
	runner, err := activationFile("/usr/bin/systemd-run", 64<<20, true)
	if err != nil {
		return fail()
	}
	data, err := (nativeSandboxLaunchTransport{}).Unit(ctx)
	if err != nil {
		return fail()
	}
	properties, err := sandboxUnitProperties(data)
	if err != nil || !sandboxUnitAbsent(properties) {
		return fail()
	}
	evidence, err := json.Marshal(struct {
		Source  ControllerActivationSnapshot
		Account string
		Runner  setup.Snapshot
	}{source.Service, account, runner})
	if err != nil || ctx.Err() != nil {
		return fail()
	}
	return SandboxProbeSnapshot{SourceProof: sha256.Sum256(evidence), ConfigProof: inputs.configProof,
		UID: uint32(uid), GID: uint32(gid), AgentInactive: inactive, ProbeAbsent: true, AgentEnabled: enabled}, config, nil
}

func (h nativeSandboxProbeHost) Config(ctx context.Context) (agent.Config, error) {
	_, config, err := h.source(ctx)
	return config, err
}
func (h nativeSandboxProbeHost) InspectProbe(ctx context.Context) (SandboxProbeSnapshot, error) {
	snapshot, _, err := h.source(ctx)
	return snapshot, err
}
func (h nativeSandboxProbeHost) RunProbe(ctx context.Context, snapshot SandboxProbeSnapshot, inputs SandboxProbeInputs) (SandboxProbeEvidence, error) {
	current, config, err := h.source(ctx)
	if err != nil || current != snapshot {
		return SandboxProbeEvidence{}, ErrSandboxProbeStale
	}
	expected, err := sandboxProbeInputs(config)
	if err != nil || expected.binding != inputs.binding || expected.configProof != inputs.configProof || !reflect.DeepEqual(expected.files, inputs.files) {
		return SandboxProbeEvidence{}, ErrSandboxProbeInvalid
	}
	return launchSandboxInspector(ctx, nativeSandboxLaunchTransport{}, snapshot, inputs)
}

func (h nativeSandboxProbeHost) startVerifiedSandboxAgent(ctx context.Context, snapshot SandboxProbeSnapshot, proof [32]byte, expiresAt time.Time) error {
	if proof == [32]byte{} || !validSandboxProbeSnapshot(snapshot) || expiresAt.IsZero() || !time.Now().Before(expiresAt) {
		return ErrSandboxProbeInvalid
	}
	current, _, err := h.source(ctx)
	if err != nil || current != snapshot || ctx.Err() != nil || expiresAt.IsZero() || !time.Now().Before(expiresAt) {
		return ErrSandboxProbeStale
	}
	profile := AgentServiceProfile{Scope: "system", Unit: "hlcp-agent.service"}
	if _, err = agentServiceProcess(ctx, profile, "start", profile.Unit, 20*time.Second); err != nil {
		return ErrSandboxProbeIncomplete
	}
	after, _, err := inspectNativeSandboxSource(ctx)
	if err != nil || ctx.Err() != nil || !sandboxStartedSourceMatches(snapshot, after) {
		return ErrSandboxProbeIncomplete
	}
	return nil
}

func (nativeSandboxProbeHost) enableVerifiedSandboxAgent(ctx context.Context, snapshot SandboxProbeSnapshot, proof [32]byte, expiresAt time.Time) error {
	inactive := snapshot
	inactive.AgentInactive = true
	if snapshot.AgentInactive || !validSandboxProbeSnapshot(inactive) || proof == [32]byte{} || expiresAt.IsZero() || !time.Now().Before(expiresAt) {
		return ErrSandboxProbeInvalid
	}
	current, _, err := inspectNativeSandboxSource(ctx)
	if err != nil || current != snapshot || ctx.Err() != nil || !time.Now().Before(expiresAt) {
		return ErrSandboxProbeStale
	}
	if current.AgentEnabled {
		return nil
	}
	profile := AgentServiceProfile{Scope: "system", Unit: "hlcp-agent.service"}
	if _, err = agentServiceProcess(ctx, profile, "enable", profile.Unit, 10*time.Second); err != nil {
		return ErrSandboxProbeIncomplete
	}
	after, _, err := inspectNativeSandboxSource(ctx)
	if err != nil || ctx.Err() != nil || !sandboxEnabledSourceMatches(snapshot, after) {
		return ErrSandboxProbeIncomplete
	}
	return nil
}
