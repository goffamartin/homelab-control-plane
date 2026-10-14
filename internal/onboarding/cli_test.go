package onboarding

import (
	"bytes"
	"context"
	"errors"
	"hlcp/internal/setup"
	"io"
	"strings"
	"testing"
	"time"
)

type fixturePlan struct {
	applies int
	err     error
}

func (*fixturePlan) Preview() setup.Preview {
	return setup.Preview{ReadOnly: true, Changes: []setup.Change{{Name: "agent.key", Action: "create", Mode: 0600}}}
}
func (p *fixturePlan) Apply(context.Context, bool) (setup.ApplyResult, error) {
	p.applies++
	return setup.ApplyResult{Changed: 1, BackupRetained: true}, p.err
}
func (*fixturePlan) Close() error { return nil }

type fixtureSetupHost struct {
	material                 ImportedTLS
	plan                     fixturePlan
	ownerCalls, reads, plans int
	planErr                  error
}

func (h *fixtureSetupHost) ReadFile(path string, private bool) ([]byte, error) {
	h.reads++
	switch path {
	case "/cert":
		return h.material.certificate, nil
	case "/key":
		if !private {
			return nil, errors.New("SECRET key not private")
		}
		return h.material.key, nil
	case "/peer":
		return h.material.peerTrust, nil
	case "/issuer":
		return h.material.issuerTrust, nil
	}
	return nil, errors.New("SECRET path")
}
func (h *fixtureSetupHost) Owner() (setup.Owner, error) {
	h.ownerCalls++
	return setup.Owner{UID: 1000, GID: 1000}, nil
}
func (h *fixtureSetupHost) Plan(ctx context.Context, directory string, owner setup.Owner, options AgentOptions, material ImportedTLS, now time.Time) (agentPlan, error) {
	h.plans++
	if _, err := material.Validate(Identity{Role: "agent", NodeID: options.NodeID}, now); err != nil {
		return nil, err
	}
	return &h.plan, h.planErr
}
func setupArgs() []string {
	return []string{"--node", "debian-01", "--controller", "controller.test:9443", "--server-name", "controller.test",
		"--directory", "/cfg", "--journal", "/state/journal", "--cert", "/cert", "--key", "/key", "--peer-ca", "/peer", "--issuer-ca", "/issuer"}
}

func TestSetupPreviewConsentAndSecretFreeOutput(t *testing.T) {
	host := &fixtureSetupHost{material: agentMaterial(t)}
	var out bytes.Buffer
	if runAgentCLI(append(setupArgs(), "--json"), nil, &out, host, false, testNow) != 0 || host.plan.applies != 0 ||
		!strings.Contains(out.String(), `"status":"preview"`) {
		t.Fatalf("%s", out.String())
	}
	out.Reset()
	if runAgentCLI(append(setupArgs(), "--apply", "--yes", "--json"), nil, &out, host, false, testNow) != 0 || host.plan.applies != 1 ||
		!strings.Contains(out.String(), `"read_only":false`) || strings.Contains(out.String(), "BEGIN") {
		t.Fatalf("%s", out.String())
	}
	host.planErr = errors.New("SECRET config error")
	out.Reset()
	if runAgentCLI(setupArgs(), nil, &out, host, false, testNow) != 1 || strings.Contains(out.String(), "SECRET") || host.plan.applies != 1 {
		t.Fatal("error leak/mutation")
	}
}

func TestHelpInvalidOptionsAndNonTTYNeverReadInput(t *testing.T) {
	host := &fixtureSetupHost{}
	var out bytes.Buffer
	for _, row := range []struct {
		args []string
		code int
	}{
		{[]string{"--help"}, 0}, {[]string{"--SECRET=value"}, 2}, {[]string{"--apply"}, 2}, {[]string{"--yes"}, 2}, {[]string{"--interactive"}, 1},
	} {
		out.Reset()
		if code := runAgentCLI(row.args, panicReader{}, &out, host, false, testNow); code != row.code {
			t.Fatalf("%v: %d", row.args, code)
		}
		if host.ownerCalls != 0 || host.reads != 0 || strings.Contains(out.String(), "SECRET") {
			t.Fatal("unexpected read/leak")
		}
	}
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("non-TTY input read") }

func TestInteractiveConfirmationAndBounds(t *testing.T) {
	for _, answer := range []string{"apply", "no"} {
		host := &fixtureSetupHost{material: agentMaterial(t)}
		answers := "debian-01\ncontroller.test:9443\ncontroller.test\n/cfg\n/state/journal\n/cert\n/key\n/peer\n/issuer\n" + answer + "\n"
		var out bytes.Buffer
		if runAgentCLI([]string{"--interactive"}, strings.NewReader(answers), &out, host, true, testNow) != 0 {
			t.Fatalf("%s", out.String())
		}
		if (host.plan.applies == 1) != (answer == "apply") || strings.Contains(out.String(), "BEGIN") {
			t.Fatal("confirmation")
		}
	}
	host := &fixtureSetupHost{}
	var out bytes.Buffer
	if runAgentCLI([]string{"--interactive"}, strings.NewReader(strings.Repeat("x", 4097)+"\n"), &out, host, true, testNow) != 1 ||
		host.reads != 0 || host.plan.applies != 0 {
		t.Fatal("unbounded interactive input")
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestOutputFailureBeforeWriteAndTransactionFailure(t *testing.T) {
	host := &fixtureSetupHost{material: agentMaterial(t)}
	if runAgentCLI(append(setupArgs(), "--apply", "--yes"), nil, failedWriter{}, host, false, testNow) != 2 || host.plan.applies != 0 {
		t.Fatal("write without preview output")
	}
	host.plan.err = errors.New("SECRET disk failure")
	var out bytes.Buffer
	if runAgentCLI(append(setupArgs(), "--apply", "--yes", "--json"), nil, &out, host, false, testNow) != 1 ||
		!strings.Contains(out.String(), "TRANSACTION_INCOMPLETE") || strings.Contains(out.String(), "SECRET") {
		t.Fatal("transaction result")
	}
}

func TestCertificateExpiryDuringConfirmationPreventsWrite(t *testing.T) {
	host := &fixtureSetupHost{material: agentMaterial(t)}
	calls := 0
	clock := func() time.Time {
		calls++
		if calls == 1 {
			return testNow
		}
		return testNow.AddDate(1, 0, 0)
	}
	var out bytes.Buffer
	if runAgentCLIClock(append(setupArgs(), "--interactive"), strings.NewReader("apply\n"), &out, host, true, clock) != 1 ||
		host.plan.applies != 0 || !strings.Contains(out.String(), "TLS_RECHECK_FAILED") {
		t.Fatalf("%s", out.String())
	}
}
