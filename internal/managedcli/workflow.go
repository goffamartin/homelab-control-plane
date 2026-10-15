package managedcli

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// operationPhases is used serially by a wizard. Human input has no deadline,
// but remains interruptible. Each technical phase gets a fresh bounded context;
// an expired phase never cancels the parent or a later phase.
type operationPhases struct {
	parent  context.Context
	stop    context.CancelFunc
	cancel  context.CancelFunc
	timeout time.Duration
}

func newWorkflowContext() *operationPhases {
	parent, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return &operationPhases{parent: parent, stop: stop, timeout: 2 * time.Minute}
}

func (p *operationPhases) input() context.Context {
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	return p.parent
}

func (p *operationPhases) operation() context.Context {
	p.input()
	ctx, cancel := context.WithTimeout(p.parent, p.timeout)
	p.cancel = cancel
	return ctx
}

func (p *operationPhases) close() {
	p.input()
	p.stop()
}
