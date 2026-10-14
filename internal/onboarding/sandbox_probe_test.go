package onboarding

import (
	"context"
	"errors"
	"fmt"
	"hlcp/internal/agent"
	"strings"
	"sync"
	"testing"
)

type probeFake struct {
	snapshot             SandboxProbeSnapshot
	callerUID, callerGID uint32
	inspects, runs       int
	failInspect          int
	fault                string
	received             SandboxProbeInputs
	cancel               context.CancelFunc
}

func probeConfig() agent.Config {
	c := readinessConfig()
	c.Resources = c.Resources[3:]
	c.Capabilities = []string{"resource.control", "script"}
	return c
}
func freshProbeFake(t *testing.T) *probeFake {
	t.Helper()
	inputs, err := sandboxProbeInputs(probeConfig())
	if err != nil {
		t.Fatal(err)
	}
	return &probeFake{snapshot: SandboxProbeSnapshot{SourceProof: [32]byte{1}, ConfigProof: inputs.configProof, UID: 1000, GID: 1000, AgentInactive: true, ProbeAbsent: true}}
}
func (h *probeFake) Caller(context.Context) (uint32, uint32, error) {
	return h.callerUID, h.callerGID, nil
}
func (h *probeFake) InspectProbe(context.Context) (SandboxProbeSnapshot, error) {
	h.inspects++
	if h.inspects == h.failInspect {
		return SandboxProbeSnapshot{}, errors.New("SECRET")
	}
	return h.snapshot, nil
}
func (h *probeFake) RunProbe(ctx context.Context, s SandboxProbeSnapshot, inputs SandboxProbeInputs) (SandboxProbeEvidence, error) {
	h.runs++
	h.received = inputs
	if _, ok := ctx.Deadline(); !ok {
		panic("unbounded probe")
	}
	evidence := SandboxProbeEvidence{SourceProof: s.SourceProof, RequestProof: inputs.binding, DependencyProof: [32]byte{9}, UID: s.UID, GID: s.GID, InspectorExited: true, UnitCollected: true}
	switch h.fault {
	case "error":
		return evidence, errors.New("SECRET")
	case "source":
		evidence.SourceProof = [32]byte{2}
	case "request":
		evidence.RequestProof = [32]byte{2}
	case "dependency":
		evidence.DependencyProof = [32]byte{}
	case "uid":
		evidence.UID = 0
	case "gid":
		evidence.GID = 0
	case "exit":
		evidence.InspectorExited = false
	case "cleanup":
		evidence.UnitCollected = false
	case "post":
		h.snapshot.SourceProof = [32]byte{2}
	case "input-mutation":
		inputs.files[0].Executable = "SECRET"
	}
	if h.cancel != nil {
		h.cancel()
	}
	return evidence, nil
}
func TestSandboxProbePreviewConsentAndSuccessfulSingleUse(t *testing.T) {
	h := freshProbeFake(t)
	p, err := PlanSandboxProbe(context.Background(), h, probeConfig())
	if err != nil {
		t.Fatal(err)
	}
	preview := p.Preview()
	if !preview.ReadOnly || !preview.RequiresConsent || preview.FileChecks != 2 || h.runs != 0 || h.inspects != 1 {
		t.Fatal(preview)
	}
	copyPlan := *p
	for _, output := range []string{p.String(), fmt.Sprintf("%#v", copyPlan), fmt.Sprintf("%#v", p.inputs)} {
		if strings.Contains(output, "/opt") || strings.Contains(output, "SECRET") || strings.Contains(output, "Proof") {
			t.Fatal("private input exposed")
		}
	}
	if _, err := p.Apply(context.Background(), false); !errors.Is(err, ErrSandboxProbeConsent) || h.runs != 0 {
		t.Fatal(err)
	}
	r, err := p.Apply(context.Background(), true)
	if err != nil || !r.Completed || r.Uncertain || h.runs != 1 || h.inspects != 3 || p.dependencyProof != [32]byte{9} {
		t.Fatal(r, err)
	}
	if len(h.received.files) != 2 || h.received.files[0].Executable != "/opt/diagnose" {
		t.Fatal("wrong inspector input")
	}
	if _, err := copyPlan.Apply(context.Background(), true); !errors.Is(err, ErrSandboxProbeStale) || h.runs != 1 {
		t.Fatal("plan copies replayed", err)
	}
}
func TestSandboxProbeEligibilityBeforeInspection(t *testing.T) {
	for _, kind := range []string{"empty", "invalid", "container", "home", "tmp", "root", "run-user", "var-tmp"} {
		t.Run(kind, func(t *testing.T) {
			h := freshProbeFake(t)
			c := probeConfig()
			switch kind {
			case "empty":
				c.Resources = nil
			case "invalid":
				c.NodeID = ""
			case "container":
				c = readinessConfig()
			default:
				paths := map[string]string{"home": "/home/secret", "tmp": "/tmp/secret", "root": "/root/secret", "run-user": "/run/user/1000/secret", "var-tmp": "/var/tmp/secret"}
				cmd := c.Resources[0].Commands["status"]
				cmd.Executable = paths[kind]
				c.Resources[0].Commands["status"] = cmd
			}
			if _, err := PlanSandboxProbe(context.Background(), h, c); err == nil || h.inspects != 0 || h.runs != 0 {
				t.Fatal(kind, err)
			}
		})
	}
	for _, kind := range []string{"caller", "uid", "gid", "agent-active", "existing-probe", "config-mismatch", "source"} {
		h := freshProbeFake(t)
		switch kind {
		case "caller":
			h.callerUID = 1000
		case "uid":
			h.snapshot.UID = 0
		case "gid":
			h.snapshot.GID = 0
		case "agent-active":
			h.snapshot.AgentInactive = false
		case "existing-probe":
			h.snapshot.ProbeAbsent = false
		case "config-mismatch":
			h.snapshot.ConfigProof = [32]byte{3}
		case "source":
			h.snapshot.SourceProof = [32]byte{}
		}
		if _, err := PlanSandboxProbe(context.Background(), h, probeConfig()); !errors.Is(err, ErrSandboxProbeInvalid) || h.runs != 0 || (kind == "caller" && h.inspects != 0) {
			t.Fatal(kind, err)
		}
	}
}
func TestSandboxProbeStaleAndUncertainNeverRetry(t *testing.T) {
	for _, kind := range []string{"error", "source", "request", "dependency", "uid", "gid", "exit", "cleanup", "post", "post-error", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			h := freshProbeFake(t)
			h.fault = kind
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p, err := PlanSandboxProbe(ctx, h, probeConfig())
			if err != nil {
				t.Fatal(err)
			}
			if kind == "post-error" {
				h.failInspect = 3
			}
			if kind == "cancel" {
				h.cancel = cancel
			}
			r, err := p.Apply(ctx, true)
			if !errors.Is(err, ErrSandboxProbeIncomplete) || !r.Uncertain || r.Completed || h.runs != 1 || p.dependencyProof != [32]byte{} || strings.Contains(err.Error(), "SECRET") {
				t.Fatal(r, err)
			}
			if _, err := p.Apply(context.Background(), true); !errors.Is(err, ErrSandboxProbeStale) || h.runs != 1 {
				t.Fatal("repeat after dispatch", err)
			}
		})
	}
	for _, kind := range []string{"source", "caller", "inspect-error", "cancel"} {
		h := freshProbeFake(t)
		p, err := PlanSandboxProbe(context.Background(), h, probeConfig())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		switch kind {
		case "source":
			h.snapshot.SourceProof = [32]byte{2}
		case "caller":
			h.callerGID = 1000
		case "inspect-error":
			h.failInspect = 2
		case "cancel":
			cancel()
		}
		if _, err := p.Apply(ctx, true); err == nil || h.runs != 0 {
			t.Fatal(kind, err)
		}
		cancel()
	}
}
func TestSandboxProbeInputCloneAndConcurrentSingleUse(t *testing.T) {
	h := freshProbeFake(t)
	h.fault = "input-mutation"
	p, err := PlanSandboxProbe(context.Background(), h, probeConfig())
	if err != nil {
		t.Fatal(err)
	}
	original := p.inputs.files[0]
	var wg sync.WaitGroup
	wg.Add(2)
	outcomes := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { defer wg.Done(); _, err := p.Apply(context.Background(), true); outcomes <- err }()
	}
	wg.Wait()
	close(outcomes)
	successes, stale := 0, 0
	for err := range outcomes {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrSandboxProbeStale) {
			stale++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || stale != 1 || h.runs != 1 || p.inputs.files[0] != original {
		t.Fatal("mutable input or repeat")
	}
}
func TestSandboxProbeBindingAndDeduplication(t *testing.T) {
	c := probeConfig()
	inputs, err := sandboxProbeInputs(c)
	if err != nil {
		t.Fatal(err)
	}
	cmd := c.Resources[0].Commands["status"]
	cmd.Argv = []string{"changed"}
	c.Resources[0].Commands["status"] = cmd
	changed, err := sandboxProbeInputs(c)
	if err != nil || changed.binding == inputs.binding || changed.configProof == inputs.configProof {
		t.Fatal("argv not bound")
	}
	c.Resources[0].Commands["diagnose"] = cmd
	duplicate, err := sandboxProbeInputs(c)
	if err != nil || len(duplicate.files) != 1 {
		t.Fatal("repeated file pairs not deduplicated", err)
	}
}

func TestSandboxProbeBoundsAndZeroValues(t *testing.T) {
	for _, count := range []int{128, 129, 257} {
		c := probeConfig()
		template := c.Resources[0]
		c.Resources = nil
		for i := 0; i < count; i++ {
			resource := template
			resource.ID = fmt.Sprintf("task-%d", i)
			resource.Actions = []string{"status"}
			resource.Commands = map[string]agent.ScriptCommandConfig{"status": template.Commands["status"]}
			c.Resources = append(c.Resources, resource)
		}
		inputs, err := sandboxProbeInputs(c)
		if count == 128 {
			if err != nil || len(inputs.files) != 1 {
				t.Fatal(count, err)
			}
		} else if err == nil {
			t.Fatal("unbounded script checks", count)
		}
	}
	large := probeConfig()
	template := large.Resources[0]
	large.Resources = nil
	command := template.Commands["status"]
	command.Argv = []string{strings.Repeat("x", 7000)}
	command.Environment = map[string]string{"SAFE_VALUE": strings.Repeat("y", 7000)}
	for i := 0; i < 128; i++ {
		resource := template
		resource.ID = fmt.Sprintf("large-%d", i)
		resource.Actions = []string{"status"}
		resource.Commands = map[string]agent.ScriptCommandConfig{"status": command}
		large.Resources = append(large.Resources, resource)
	}
	if err := large.Validate(); err != nil {
		t.Fatal("invalid size fixture", err)
	}
	if _, err := sandboxProbeInputs(large); !errors.Is(err, ErrSandboxProbeInvalid) {
		t.Fatal("oversized canonical binding accepted", err)
	}
	h := freshProbeFake(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PlanSandboxProbe(ctx, h, probeConfig()); !errors.Is(err, context.Canceled) || h.inspects != 0 {
		t.Fatal(err)
	}
	if _, err := PlanSandboxProbe(context.Background(), nil, probeConfig()); !errors.Is(err, ErrSandboxProbeInvalid) {
		t.Fatal(err)
	}
	var nilPlan *SandboxProbe
	if _, err := nilPlan.Apply(context.Background(), true); !errors.Is(err, ErrSandboxProbeInvalid) {
		t.Fatal(err)
	}
	zero := SandboxProbe{}
	if _, err := zero.Apply(context.Background(), true); !errors.Is(err, ErrSandboxProbeInvalid) {
		t.Fatal(err)
	}
	if zero.Preview().Unit != "" || !zero.Preview().ReadOnly {
		t.Fatal("zero plan claimed readiness")
	}
}
