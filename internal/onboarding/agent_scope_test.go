package onboarding

import (
	"context"
	"errors"
	"hlcp/internal/setup"
	"path/filepath"
	"testing"
)

func TestAgentSetupScopeRefusesMismatchedIdentity(t *testing.T) {
	options := AgentOptions{NodeID: "debian-01", ControllerAddress: "controller.test:9443", ServerName: "controller.test", JournalFile: filepath.Join(t.TempDir(), "history.json"), AgentVersion: "test"}
	for _, test := range []struct {
		scope string
		owner setup.Owner
	}{{"unknown", setup.Owner{}}, {"system", setup.Owner{UID: 1000, GID: 1000}}, {"system", setup.Owner{UID: 0, GID: 1000}}, {"user", setup.Owner{}}, {"user", setup.Owner{UID: 1000, GID: 0}}} {
		options.Scope = test.scope
		if _, err := PlanAgent(context.Background(), t.TempDir(), test.owner, options, ImportedTLS{}, testNow); !errors.Is(err, ErrAgentOptions) {
			t.Fatal(test.scope, err)
		}
	}
}
