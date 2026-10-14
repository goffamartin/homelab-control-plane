package onboarding

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"hlcp/internal/agent"
	"sort"
)

// ResourceReadinessHost inspects fixed runtime dependencies and configured script files,
// never executes a resource/CLI action, opens a journal or grants local rights.
type ResourceReadinessHost interface {
	RuntimeExecutable(context.Context, string) ([32]byte, error)
	DockerSocket(context.Context) ([32]byte, error)
	PodmanMappings(context.Context) ([32]byte, error)
	ScriptFiles(context.Context, agent.ScriptCommandConfig) ([32]byte, error)
}
type ResourceReadinessPreview struct {
	ReadOnly   bool     `json:"read_only"`
	Checks     []string `json:"checks"`
	Unverified []string `json:"unverified"`
}
type ResourceReadiness struct {
	preview ResourceReadinessPreview
	proof   [32]byte
}

func (r ResourceReadiness) Preview() ResourceReadinessPreview {
	return ResourceReadinessPreview{ReadOnly: true, Checks: append([]string{}, r.preview.Checks...), Unverified: append([]string{}, r.preview.Unverified...)}
}
func (r ResourceReadiness) MarshalJSON() ([]byte, error) { return json.Marshal(r.Preview()) }
func (r ResourceReadiness) String() string               { data, _ := r.MarshalJSON(); return string(data) }
func (r ResourceReadiness) GoString() string             { return r.String() }

// Native system-scope resources require inspection under the service sandbox,
// not root's filesystem/socket rights. They remain an explicit separate gate.
func PlanResourceReadiness(ctx context.Context, host ResourceReadinessHost, profile AgentServiceProfile, config agent.Config) (ResourceReadiness, error) {
	r := ResourceReadiness{preview: ResourceReadinessPreview{ReadOnly: true, Checks: []string{}, Unverified: []string{}}}
	if !validAgentServiceProfile(profile) || len(config.Resources) > 256 || config.Validate() != nil {
		return r, ErrAgentActivationResources
	}
	if err := ctx.Err(); err != nil {
		return r, err
	}
	if len(config.Resources) == 0 {
		return r, nil
	}
	if profile.Scope != "user" || host == nil {
		return r, ErrAgentActivationResources
	}
	required := map[string]bool{}
	scriptChecks := 0
	for _, resource := range config.Resources {
		required[resource.Adapter] = true
		if resource.Adapter == "systemd" {
			for _, action := range resource.Actions {
				if action != "status" {
					return r, ErrAgentActivationResources
				}
			}
		}
		if resource.Adapter == "script" {
			scriptChecks += len(resource.Actions)
		}
	}
	if scriptChecks > 128 {
		return r, ErrAgentActivationResources
	}
	proofs := [][32]byte{}
	add := func(code string, proof [32]byte, err error) error {
		if err != nil || proof == [32]byte{} {
			return ErrAgentActivationResources
		}
		proofs = append(proofs, proof)
		r.preview.Checks = append(r.preview.Checks, code)
		return nil
	}
	for _, name := range []string{"docker", "podman", "systemd"} {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		if !required[name] {
			continue
		}
		executable := name
		if name == "systemd" {
			executable = "systemctl"
		}
		proof, err := host.RuntimeExecutable(ctx, executable)
		if err = add(name+".executable", proof, err); err != nil {
			return r, err
		}
		if name == "docker" {
			if err := ctx.Err(); err != nil {
				return r, err
			}
			proof, err = host.DockerSocket(ctx)
			if err = add("docker.socket_access", proof, err); err != nil {
				return r, err
			}
		}
		if name == "podman" {
			if err := ctx.Err(); err != nil {
				return r, err
			}
			proof, err = host.PodmanMappings(ctx)
			if err = add("podman.id_mappings", proof, err); err != nil {
				return r, err
			}
		}
	}
	resources := append([]agent.ResourceConfig{}, config.Resources...)
	sort.Slice(resources, func(i, j int) bool { return resources[i].ID < resources[j].ID })
	for _, resource := range resources {
		if resource.Adapter != "script" {
			continue
		}
		actions := append([]string{}, resource.Actions...)
		sort.Strings(actions)
		for _, action := range actions {
			if err := ctx.Err(); err != nil {
				return r, err
			}
			proof, err := host.ScriptFiles(ctx, resource.Commands[action])
			if err = add("script.files", proof, err); err != nil {
				return r, err
			}
		}
	}
	r.preview.Unverified = []string{"runtime.environment_and_health", "resource_existence_and_execution", "acl_mac_mount_loader_effects"}
	if err := ctx.Err(); err != nil {
		return r, err
	}
	// Bind the dependency evidence to the exact validated configuration, including
	// fixed argv/environment. Neither these inputs nor the proof are public output.
	data, err := json.Marshal(struct {
		Profile AgentServiceProfile
		Config  agent.Config
		Proofs  [][32]byte
	}{profile, config, proofs})
	if err != nil {
		return r, ErrAgentActivationResources
	}
	r.proof = sha256.Sum256(data)
	return r, nil
}
