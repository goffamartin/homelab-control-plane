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
