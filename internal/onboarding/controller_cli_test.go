package onboarding

import (
	"bytes"
	"context"
	"errors"
	"hlcp/internal/setup"
	"strings"
	"testing"
	"time"
)

type controllerCLIPlan struct {
	calls int
	err   error
}

func (*controllerCLIPlan) Preview() setup.Preview {
	return setup.Preview{ReadOnly: true, Changes: []setup.Change{{Name: "api.token", Action: "create", Mode: 0600}}}
}
func (p *controllerCLIPlan) Apply(context.Context, bool, time.Time) (setup.ApplyResult, error) {
	p.calls++
	return setup.ApplyResult{Changed: 9}, p.err
}
func (*controllerCLIPlan) Close() error { return nil }

type controllerCLIHost struct {
	material      ImportedTLS
	plan          controllerCLIPlan
	reads, owners int
	uid           uint32
	options       ControllerOptions
}

func (h *controllerCLIHost) Owner() (setup.Owner, error) {
	h.owners++
	return setup.Owner{UID: h.uid}, nil
}
func (h *controllerCLIHost) ReadFile(name string, private bool) ([]byte, error) {
	h.reads++
	switch name {
	case "/cert":
		return h.material.certificate, nil
	case "/key":
		if private {
			return h.material.key, nil
		}
	case "/peer":
		return h.material.peerTrust, nil
	case "/issuer":
		return h.material.issuerTrust, nil
	}
	return nil, errors.New("SECRET")
}
func (h *controllerCLIHost) ControllerPlan(_ context.Context, _ string, _ setup.Owner, options ControllerOptions, _ ImportedTLS, _ time.Time) (controllerPlan, error) {
	h.options = options
	return &h.plan, nil
}
func controllerCLIArgs() []string {
	return []string{"--listen", "127.0.0.1:9443", "--server-name", "controller.test", "--cert", "/cert", "--key", "/key", "--peer-ca", "/peer", "--issuer-ca", "/issuer"}
}
func TestControllerCLIOptInTTYAndSanitizedFailure(t *testing.T) {
	h := &controllerCLIHost{material: controllerMaterial(t)}
	var out bytes.Buffer
	clock := func() time.Time { return testNow }
	if runControllerCLI(append(controllerCLIArgs(), "--json"), nil, &out, h, false, clock) != 0 || h.plan.calls != 0 {
		t.Fatal("preview write")
	}
	out.Reset()
	if runControllerCLI(append(controllerCLIArgs(), "--apply", "--yes", "--admit", "debian-01", "--json"), nil, &out, h, false, clock) != 0 || h.plan.calls != 1 || len(h.options.AdmittedNodes) != 1 {
		t.Fatal("apply")
	}
	h.plan.err = errors.New("SECRET disk failure")
	out.Reset()
	if runControllerCLI(append(controllerCLIArgs(), "--apply", "--yes", "--json"), nil, &out, h, false, clock) != 1 || strings.Contains(out.String(), "SECRET") {
		t.Fatal("failure leak")
	}
	for _, args := range [][]string{{"--help"}, {"--interactive"}, {"--yes"}, {"--SECRET=value"}} {
		host := &controllerCLIHost{}
		out.Reset()
		runControllerCLI(args, panicReader{}, &out, host, false, clock)
		if host.owners != 0 || host.reads != 0 {
			t.Fatal("help/usage/non-TTY read")
		}
	}
	h = &controllerCLIHost{uid: 1000}
	out.Reset()
	if runControllerCLI(controllerCLIArgs(), nil, &out, h, false, clock) != 1 || h.reads != 0 {
		t.Fatal("nonroot")
	}
	h = &controllerCLIHost{material: controllerMaterial(t)}
	out.Reset()
	if runControllerCLI(append(controllerCLIArgs(), "--interactive"), strings.NewReader("no\n"), &out, h, true, clock) != 0 || h.plan.calls != 0 {
		t.Fatal("cancel")
	}
}
