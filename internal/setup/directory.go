package setup

import (
	"context"
	"errors"
	"sync"
)

var ErrMissingParent = errors.New("setup: parent directory must already exist")

type directorySource interface {
	create(context.Context) (Source, error)
	close() error
}

// PlanChildDirectory pins an absent direct child to this planner's original
// private directory descriptor. It cannot adopt a replacement parent by path.
func (planner *Planner) PlanChildDirectory(name string) (*DirectoryPlan, error) {
	planner.mutex.Lock()
	defer planner.mutex.Unlock()
	if planner.closed {
		return nil, ErrClosed
	}
	if !validName(name) {
		return nil, ErrInvalidInput
	}
	parent, ok := planner.source.(interface {
		planChildDirectory(string) (directorySource, error)
	})
	if !ok {
		return nil, ErrApplyUnavailable
	}
	source, err := parent.planChildDirectory(name)
	if err != nil {
		return nil, err
	}
	return &DirectoryPlan{source: source}, nil
}

// DirectoryPlan pins an existing safe parent. There is no recursive creation,
// permission repair or implicit consent. Published targets and unknown staging
// are retained; only a known empty pre-publish staging inode may be removed.
type DirectoryPlan struct {
	mutex        sync.Mutex
	source       directorySource
	closed, used bool
}

func PlanDirectory(directory string, owner Owner) (*DirectoryPlan, error) {
	source, err := planDirectory(directory, owner)
	if err != nil {
		return nil, err
	}
	return &DirectoryPlan{source: source}, nil
}
func PlanDirectoryTrustingOwnerGroup(directory string, owner Owner) (*DirectoryPlan, error) {
	source, err := planDirectoryWithTrust(directory, owner, true)
	if err != nil {
		return nil, err
	}
	return &DirectoryPlan{source: source}, nil
}
func (p *DirectoryPlan) Create(ctx context.Context, confirmed bool) (*Planner, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if !confirmed {
		return nil, ErrConsentRequired
	}
	if p.closed {
		return nil, ErrClosed
	}
	if p.used {
		return nil, ErrStalePlan
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.used = true
	source, err := p.source.create(ctx)
	if err != nil {
		return nil, err
	}
	planner, err := NewPlanner(source)
	if err != nil {
		source.Close()
		return nil, err
	}
	return planner, nil
}
func (p *DirectoryPlan) Close() error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	return p.source.close()
}
