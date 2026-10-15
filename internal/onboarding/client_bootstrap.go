package onboarding

import (
	"context"
	"errors"
	"hlcp/internal/setup"
	"path/filepath"
)

// This orchestration adds only the known .config -> hlcp chain, not recursive
// arbitrary mkdir. Each publication is separately pinned and no-clobber.
type grantClientSetup struct {
	client    *ClientSetup
	parent    *setup.DirectoryPlan
	directory string
	owner     setup.Owner
}

func sameGrantChanges(a, b setup.Preview) bool {
	if len(a.Changes) != len(b.Changes) {
		return false
	}
	for index := range a.Changes {
		if a.Changes[index] != b.Changes[index] {
			return false
		}
	}
	return true
}

func planGrantClient(ctx context.Context, directory string, owner setup.Owner, credential ClientCredential) (*grantClientSetup, error) {
	return planGrantClientWithTrust(ctx, directory, owner, credential, false)
}
func planGrantClientWithTrust(ctx context.Context, directory string, owner setup.Owner, credential ClientCredential, trust bool) (*grantClientSetup, error) {
	client, err := planClientWithTrust(ctx, directory, owner, "http://127.0.0.1:9444", credential, trust)
	if err == nil {
		return &grantClientSetup{client: client, directory: directory, owner: owner}, nil
	}
	if !errors.Is(err, setup.ErrMissingParent) || filepath.Base(directory) != "hlcp" || filepath.Base(filepath.Dir(directory)) != ".config" {
		return nil, err
	}
	planDirectory := setup.PlanDirectory
	if trust {
		planDirectory = setup.PlanDirectoryTrustingOwnerGroup
	}
	parent, err := planDirectory(filepath.Dir(directory), owner)
	if err != nil {
		return nil, err
	}
	intents, err := clientIntents(directory, "http://127.0.0.1:9444", credential)
	if err != nil {
		parent.Close()
		return nil, err
	}
	planner, err := setup.NewPlanner(absentAgentSource{owner})
	if err != nil {
		parent.Close()
		return nil, err
	}
	plan, err := planner.Build(ctx, intents)
	if err != nil {
		planner.Close()
		parent.Close()
		return nil, err
	}
	return &grantClientSetup{client: &ClientSetup{owner: owner, plan: &AgentSetup{planner: planner, plan: plan}}, parent: parent, directory: directory, owner: owner}, nil
}
func (p *grantClientSetup) Close() {
	p.client.Close()
	if p.parent != nil {
		p.parent.Close()
	}
}
func (p *grantClientSetup) Preview() setup.Preview {
	preview := p.client.Preview()
	if p.parent != nil {
		preview.Changes = append([]setup.Change{{Name: "user-config-directory", Action: "create-directory", Mode: 0700}, {Name: "configuration-directory", Action: "create-directory", Mode: 0700}}, preview.Changes...)
	}
	return preview
}
func (p *grantClientSetup) Apply(ctx context.Context, confirmed bool, credential ClientCredential) (setup.ApplyResult, error) {
	if !confirmed {
		return setup.ApplyResult{}, setup.ErrConsentRequired
	}
	if err := validateClientOwner(p.owner); err != nil {
		return setup.ApplyResult{}, err
	}
	base := p.client.plan
	if err := base.planner.Recheck(ctx, base.plan); err != nil {
		return setup.ApplyResult{}, err
	}
	intents, err := clientIntents(p.directory, "http://127.0.0.1:9444", credential)
	if err != nil {
		return setup.ApplyResult{}, err
	}
	final, err := base.planner.Build(ctx, intents)
	if err != nil {
		return setup.ApplyResult{}, err
	}
	if !sameGrantChanges(base.plan.Preview(), final.Preview()) {
		return setup.ApplyResult{}, setup.ErrStalePlan
	}
	if p.parent == nil {
		ready := &ClientSetup{owner: p.owner, plan: &AgentSetup{planner: base.planner, plan: final, directory: base.directory, intents: intents}}
		return ready.Apply(ctx, true)
	}
	// The existing home was pinned during preview. Newly published .config is
	// returned as a pinned planner, not reopened/adopted through its pathname.
	parent, err := p.parent.Create(ctx, true)
	if err != nil {
		return setup.ApplyResult{}, err
	}
	defer parent.Close()
	child, err := parent.PlanChildDirectory("hlcp")
	if err != nil {
		return setup.ApplyResult{}, err
	}
	defer child.Close()
	planner, err := child.Create(ctx, true)
	if err != nil {
		return setup.ApplyResult{}, err
	}
	defer planner.Close()
	plan, err := planner.Build(ctx, intents)
	if err != nil {
		return setup.ApplyResult{}, err
	}
	if !sameGrantChanges(final.Preview(), plan.Preview()) {
		return setup.ApplyResult{}, setup.ErrStalePlan
	}
	return planner.Apply(ctx, plan, true)
}
