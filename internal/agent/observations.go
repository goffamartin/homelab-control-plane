package agent

import (
	"context"
	"sync"
	"time"

	"hlcp/internal/adapter"
	"hlcp/internal/protocol"
	"hlcp/internal/session"
)

const observationWorkers = 4
const observationTimeout = 3 * time.Second

func (config Config) requestRefresh() {
	if config.refresh != nil {
		select {
		case config.refresh <- struct{}{}:
		default:
		}
	}
}
func (config Config) observe(ctx context.Context, resource ResourceConfig) protocol.ResourceState {
	ctx, cancel := context.WithTimeout(ctx, observationTimeout)
	defer cancel()
	var state protocol.ResourceState
	switch resource.Adapter {
	case "docker", "podman":
		state = (adapter.Container{Runtime: resource.Adapter, Name: resource.Target, Runner: config.runner}).Observe(ctx)
	case "systemd":
		state = (adapter.Systemd{Unit: resource.Target, Runner: config.runner}).Observe(ctx)
	default:
		state = protocol.ResourceState{State: "error", Code: "INVALID_CONFIGURATION", ObservedAt: time.Now().UTC()}
	}
	state.ResourceID = resource.ID
	return state
}
func (config Config) observationRound(parent context.Context, writer *session.Writer, sessionID string, inventoryRevision uint64, revision *uint64) error {
	ctx, cancel := context.WithCancel(parent)
	jobs := make(chan ResourceConfig, len(config.Resources))
	for _, resource := range config.Resources {
		jobs <- resource
	}
	close(jobs)
	results := make(chan protocol.ResourceState, observationWorkers)
	var workers sync.WaitGroup
	for i := 0; i < observationWorkers; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for resource := range jobs {
				if ctx.Err() != nil {
					return
				}
				state := config.observe(ctx, resource)
				select {
				case results <- state:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	done := make(chan struct{})
	go func() { workers.Wait(); close(results); close(done) }()
	defer func() { cancel(); <-done }()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case state, ok := <-results:
			if !ok {
				return nil
			}
			*revision = *revision + 1
			update := protocol.StateUpdate{SessionID: sessionID, InventoryRevision: inventoryRevision, Revision: *revision, Resource: state}
			if err := update.Validate(); err != nil {
				return err
			}
			if err := writer.SendContext(ctx, protocol.MessageStateUpdate, update); err != nil {
				return err
			}
		}
	}
}
func (config Config) runObservations(ctx context.Context, writer *session.Writer, sessionID string, inventoryRevision uint64) error {
	interval := config.ObservationInterval
	if interval == 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var revision uint64
	for {
		if err := config.observationRound(ctx, writer, sessionID, inventoryRevision, &revision); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		case <-config.refresh:
		}
	}
}
