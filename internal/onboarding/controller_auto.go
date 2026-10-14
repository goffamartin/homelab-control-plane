package onboarding

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hlcp/internal/setup"
	"hlcp/internal/transport"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// AutoControllerSetup owns a preview only; placeholder bytes are never applied.
// Fresh bearer generation and final plan construction happen only after consent.
type AutoControllerSetup struct{ *autoControllerState }
type autoControllerState struct {
	mutex        sync.Mutex
	preview      *ControllerSetup
	directory    string
	options      ControllerOptions
	material     ImportedTLS
	credentials  ControllerCredentials
	generate     bool
	used, closed bool
	random       io.Reader
}

func PlanControllerAuto(ctx context.Context, directory string, owner setup.Owner, options ControllerOptions, material ImportedTLS, now time.Time) (*AutoControllerSetup, error) {
	if options.AdmittedNodes != nil {
		options.AdmittedNodes = append([]string{}, options.AdmittedNodes...)
	}
	var err error
	material, err = NewImportedTLS(material.certificate, material.key, material.peerTrust, material.issuerTrust)
	if err != nil {
		return nil, err
	}
	standins, _ := NewControllerCredentials([]byte(strings.Repeat("0", 64)), []byte(strings.Repeat("1", 64)))
	if _, err := controllerIntents(directory, options, material, standins, now); err != nil {
		return nil, err
	}
	credentials := ControllerCredentials{}
	generate := true
	planner, err := setup.OpenPlanner(directory, owner)
	if err == nil {
		operator, e1 := planner.Snapshot("api.token", 257)
		observer, e2 := planner.Snapshot("observer.token", 257)
		if e1 != nil || e2 != nil {
			planner.Close()
			return nil, setup.ErrInvalidExisting
		}
		if operator.Exists != observer.Exists {
			planner.Close()
			return nil, setup.ErrRecoveryRequired
		}
		if operator.Exists {
			if operator.Mode != 0600 || observer.Mode != 0600 {
				planner.Close()
				return nil, setup.ErrUnsafePath
			}
			credentials, err = NewControllerCredentials(operator.Data, observer.Data)
			if err != nil {
				planner.Close()
				return nil, setup.ErrInvalidExisting
			}
			generate = false
		}
		if options.AdmittedNodes == nil {
			nodes, err := planner.Snapshot("nodes.json", 64*1024)
			if err != nil {
				planner.Close()
				return nil, setup.ErrInvalidExisting
			}
			if nodes.Exists {
				admitted, err := transport.ParseAllowedNodes(nodes.Data)
				if err != nil {
					planner.Close()
					return nil, setup.ErrInvalidExisting
				}
				options.AdmittedNodes = []string{}
				for node := range admitted {
					options.AdmittedNodes = append(options.AdmittedNodes, node)
				}
				sort.Strings(options.AdmittedNodes)
			}
		}
		planner.Close()
	} else if !errors.Is(err, setup.ErrMissingDirectory) {
		return nil, err
	}
	if generate {
		// Non-secret stand-ins validate shape only. Never call preview.Apply.
		credentials = standins
	}
	preview, err := PlanController(ctx, directory, owner, options, material, credentials, now)
	if err != nil {
		return nil, err
	}
	preview.previewOnly = generate
	return &AutoControllerSetup{&autoControllerState{preview: preview, directory: directory, options: options, material: material,
		credentials: credentials, generate: generate, random: rand.Reader}}, nil
}
func (s AutoControllerSetup) Preview() setup.Preview {
	if s.autoControllerState == nil {
		return setup.Preview{ReadOnly: true}
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.preview.Preview()
}
func (s AutoControllerSetup) MarshalJSON() ([]byte, error) { return json.Marshal(s.Preview()) }
func (s AutoControllerSetup) String() string               { data, _ := s.MarshalJSON(); return string(data) }
func (s AutoControllerSetup) GoString() string             { return s.String() }
func (s *AutoControllerSetup) Close() error {
	if s == nil || s.autoControllerState == nil {
		return nil
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.preview.Close()
}
func (s *AutoControllerSetup) Apply(ctx context.Context, confirmed bool, now time.Time) (setup.ApplyResult, error) {
	if s == nil || s.autoControllerState == nil {
		return setup.ApplyResult{}, ErrControllerOptions
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if !confirmed {
		return setup.ApplyResult{}, setup.ErrConsentRequired
	}
	if s.closed {
		return setup.ApplyResult{}, setup.ErrClosed
	}
	if s.used {
		return setup.ApplyResult{}, setup.ErrStalePlan
	}
	if err := ctx.Err(); err != nil {
		return setup.ApplyResult{}, err
	}
	if _, err := s.material.Validate(Identity{Role: "controller", ServerName: s.options.ServerName}, now); err != nil {
		return setup.ApplyResult{}, err
	}
	base := s.preview.plan
	if err := base.planner.Recheck(ctx, base.plan); err != nil {
		return setup.ApplyResult{}, err
	}
	tokens := s.credentials
	if s.generate {
		a, b := make([]byte, 32), make([]byte, 32)
		if _, err := io.ReadFull(s.random, a); err != nil {
			return setup.ApplyResult{}, ErrControllerOptions
		}
		if _, err := io.ReadFull(s.random, b); err != nil {
			return setup.ApplyResult{}, ErrControllerOptions
		}
		var err error
		tokens, err = NewControllerCredentials([]byte(hex.EncodeToString(a)), []byte(hex.EncodeToString(b)))
		if err != nil {
			return setup.ApplyResult{}, err
		}
	}
	intents, err := controllerIntents(s.directory, s.options, s.material, tokens, now)
	if err != nil {
		return setup.ApplyResult{}, err
	}
	final, err := base.planner.Build(ctx, intents)
	if err != nil {
		return setup.ApplyResult{}, err
	}
	before, after := base.plan.Preview().Changes, final.Preview().Changes
	if len(before) != len(after) {
		return setup.ApplyResult{}, setup.ErrStalePlan
	}
	for index := range before {
		if before[index] != after[index] {
			return setup.ApplyResult{}, setup.ErrStalePlan
		}
	}
	s.used = true
	// Retain the original pinned directory plan; never reopen/adopt a raced target.
	finalSetup := &AgentSetup{planner: base.planner, plan: final, directory: base.directory, intents: intents}
	return finalSetup.Apply(ctx, true)
}
