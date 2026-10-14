package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"hlcp/internal/cli"
	"hlcp/internal/localapi"
	"hlcp/internal/setup"
	"path/filepath"
	"strings"
)

var ErrClientOptions = errors.New("onboarding: client provisioning options or local account are invalid")

// ClientCredential holds an already authorized bearer, never a new identity.
// It must cross any root-to-user boundary privately, not through args/env/logs.
type ClientCredential struct{ token []byte }

func NewClientCredential(token []byte) (ClientCredential, error) {
	value, err := localapi.ParseToken(token)
	if err != nil {
		return ClientCredential{}, ErrClientOptions
	}
	return ClientCredential{token: []byte(value + "\n")}, nil
}
func (ClientCredential) MarshalJSON() ([]byte, error) {
	return []byte(`{"client_credential":"redacted"}`), nil
}
func (c ClientCredential) String() string   { data, _ := c.MarshalJSON(); return string(data) }
func (c ClientCredential) GoString() string { return c.String() }

// ClientSetup is a private file/directory transaction, not service activation.
type ClientSetup struct {
	plan  *AgentSetup
	owner setup.Owner
}

func (p *ClientSetup) Preview() setup.Preview {
	if p == nil || p.plan == nil {
		return setup.Preview{ReadOnly: true}
	}
	return p.plan.Preview()
}
func (p ClientSetup) MarshalJSON() ([]byte, error) { return json.Marshal(p.Preview()) }
func (p ClientSetup) String() string               { data, _ := p.MarshalJSON(); return string(data) }
func (p ClientSetup) GoString() string             { return p.String() }
func (p *ClientSetup) Close() error {
	if p == nil || p.plan == nil {
		return nil
	}
	return p.plan.Close()
}
func (p *ClientSetup) Apply(ctx context.Context, confirmed bool) (setup.ApplyResult, error) {
	if p == nil || p.plan == nil {
		return setup.ApplyResult{}, ErrClientOptions
	}
	// Never allow a privileged caller to apply a plan made under another account.
	if err := validateClientOwner(p.owner); err != nil {
		return setup.ApplyResult{}, err
	}
	return p.plan.Apply(ctx, confirmed)
}

// PlanClient runs only as the explicitly selected, already dropped non-root
// account. No root reads or writes to user-owned paths, chown, HOME/SUDO_USER
// inference, recursive mkdir, permission repair or controller policy changes.
func PlanClient(ctx context.Context, directory string, owner setup.Owner, api string, credential ClientCredential) (*ClientSetup, error) {
	if err := validateClientOwner(owner); err != nil {
		return nil, err
	}
	intents, err := clientIntents(directory, api, credential)
	if err != nil {
		return nil, err
	}
	plan, err := planFiles(ctx, directory, owner, intents)
	if err != nil {
		return nil, err
	}
	// Use the same pinned file plan, not an independently reopened snapshot.
	// A partial pair is not a fresh install; never complete it automatically.
	changes := plan.plan.Preview().Changes
	if len(changes) != 2 || changes[0].Action != changes[1].Action {
		plan.Close()
		return nil, setup.ErrRecoveryRequired
	}
	return &ClientSetup{plan: plan, owner: owner}, nil
}

func validateClientOwner(owner setup.Owner) error {
	actual, err := (nativeSetupHost{}).Owner()
	if err != nil || owner.UID == 0 || actual != owner {
		return ErrClientOptions
	}
	return nil
}

func clientIntents(directory, api string, credential ClientCredential) ([]setup.Intent, error) {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || directory == string(filepath.Separator) || len(directory) > 4096 || strings.ContainsRune(directory, 0) || localapi.ValidateURL(api) != nil {
		return nil, ErrClientOptions
	}
	normalized, err := NewClientCredential(credential.token)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(cli.ClientConfig{Version: 1, API: api, TokenFile: "cli.token"})
	if err != nil {
		return nil, ErrClientOptions
	}
	return []setup.Intent{
		{Name: "cli.json", Data: append(data, '\n'), Mode: 0600, Validate: func(data []byte) error {
			_, err := cli.ParseClientConfig(data, filepath.Join(directory, "cli.json"))
			return err
		}},
		{Name: "cli.token", Data: normalized.token, Mode: 0600, Validate: func(data []byte) error { _, err := localapi.ParseToken(data); return err }},
	}, nil
}
