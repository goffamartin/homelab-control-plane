package doctor

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net/url"
	"strings"
	"testing"
	"time"
)

type fakeHost struct {
	files       map[string][]byte
	reads       []string
	executables []string
}

func (h *fakeHost) ReadFile(path string, private bool) ([]byte, error) {
	h.reads = append(h.reads, path)
	data, ok := h.files[path]
	if !ok {
		return nil, errors.New("SECRET OS ERROR")
	}
	return data, nil
}
func (h *fakeHost) Executable(name string) bool {
	h.executables = append(h.executables, name)
	return true
}
func (*fakeHost) Platform() (string, string) { return "linux", "amd64" }

var testNow = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func fixture(t *testing.T, agentRole bool, expires time.Time) *fakeHost {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: testNow.Add(-time.Hour), NotAfter: testNow.Add(365 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: testNow.Add(-time.Hour),
		NotAfter: expires, DNSNames: []string{"controller.test"}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if agentRole {
		uri, _ := url.Parse("urn:hlcp:node:debian-01")
		leaf.URIs = []*url.URL{uri}
		leaf.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return &fakeHost{files: map[string][]byte{
		"/cert": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		"/ca":   pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		"/key":  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	}}
}

func controllerOptions() Options {
	return Options{Role: "controller", Certificate: "/cert", Key: "/key", CA: "/ca", ServerName: "controller.test"}
}

func stateFor(r Report, code string) string {
	for _, check := range r.Checks {
		if check.Code == code {
			return check.State
		}
	}
	return ""
}

func TestCertificateBoundariesAndOutput(t *testing.T) {
	for _, test := range []struct {
		delta time.Duration
		want  string
	}{
		{31 * 24 * time.Hour, "ok"}, {30 * 24 * time.Hour, "warn"}, {time.Second, "warn"}, {0, "fail"}, {-time.Second, "fail"},
	} {
		host := fixture(t, false, testNow.Add(test.delta))
		report := Run(host, controllerOptions(), testNow)
		if got := stateFor(report, "tls.leaf.validity"); got != test.want {
			t.Fatalf("%v: %s", test.delta, got)
		}
		if !report.Offline || !report.ReadOnly {
			t.Fatal("report contract")
		}
	}
	host := fixture(t, false, testNow.Add(90*24*time.Hour))
	options := controllerOptions()
	options.ServerName = "wrong.test"
	if stateFor(Run(host, options, testNow), "tls.server_name") != "fail" {
		t.Fatal("hostname mismatch accepted")
	}
	if stateFor(Run(host, controllerOptions(), testNow.Add(-2*time.Hour)), "tls.leaf.validity") != "fail" {
		t.Fatal("not-before")
	}
	host.files["/key"] = []byte("SECRET KEY")
	var output bytes.Buffer
	if runCLI([]string{"--cert", "/cert", "--key", "/key", "--ca", "/ca", "--json"}, "controller", &output, host, testNow) != 1 {
		t.Fatal("exit")
	}
	if strings.Contains(output.String(), "SECRET") || strings.Contains(output.String(), "BEGIN") {
		t.Fatal("secret leaked")
	}
}

const minimalAgent = "controller_address: controller.test:9443\nserver_name: controller.test\ncertificate_file: /cert\nprivate_key_file: /key\nca_file: /ca\njournal_file: /never-open-journal\nnode_id: debian-01\nagent_version: test\ncapabilities: []\nresources: []\n"

func TestGenericAgentAndIdentity(t *testing.T) {
	host := fixture(t, true, testNow.Add(90*24*time.Hour))
	host.files["/config"] = []byte(minimalAgent)
	report := Run(host, Options{Role: "agent", Config: "/config"}, testNow)
	if report.Failed() {
		t.Fatalf("%+v", report)
	}
	if len(host.executables) != 0 {
		t.Fatal("generic agent required a runtime")
	}
	for _, path := range host.reads {
		if path == "/never-open-journal" {
			t.Fatal("journal opened")
		}
	}
	host.files["/config"] = []byte(strings.ReplaceAll(minimalAgent, "node_id: debian-01", "node_id: other"))
	if stateFor(Run(host, Options{Role: "agent", Config: "/config"}, testNow), "tls.node_identity") != "fail" {
		t.Fatal("identity mismatch accepted")
	}
}

func TestConfiguredRuntimeLookupAndInvalidPort(t *testing.T) {
	host := fixture(t, true, testNow.Add(90*24*time.Hour))
	config := strings.ReplaceAll(minimalAgent, "capabilities: []", "capabilities: [resource.control, podman]")
	config = strings.ReplaceAll(config, "resources: []", "resources:\n  - id: example\n    type: container\n    adapter: podman\n    target: example\n    actions: [status]")
	host.files["/config"] = []byte(config)
	report := Run(host, Options{Role: "agent", Config: "/config"}, testNow)
	if report.Failed() || len(host.executables) != 1 || host.executables[0] != "podman" {
		t.Fatalf("configured lookup: %+v", report)
	}
	host.files["/config"] = []byte(strings.ReplaceAll(config, "controller.test:9443", "controller.test:99999"))
	if stateFor(Run(host, Options{Role: "agent", Config: "/config"}, testNow), "agent.endpoint") != "fail" {
		t.Fatal("invalid port accepted")
	}
}

func TestInvalidAndOversizedInputs(t *testing.T) {
	for _, input := range [][]byte{[]byte("SECRET unknown: value"), bytes.Repeat([]byte("x"), MaximumFileSize+1)} {
		host := &fakeHost{files: map[string][]byte{"/config": input}}
		var out bytes.Buffer
		if runCLI([]string{"--config", "/config", "--json"}, "agent", &out, host, testNow) != 1 {
			t.Fatal("exit")
		}
		if len(host.reads) != 1 || strings.Contains(out.String(), "SECRET") {
			t.Fatal("unexpected read/leak")
		}
	}
	host := &fakeHost{}
	var out bytes.Buffer
	if runCLI([]string{"--help"}, "agent", &out, host, testNow) != 0 || len(host.reads) != 0 {
		t.Fatal("help accessed files")
	}
	out.Reset()
	if runCLI([]string{"--SECRET=value"}, "agent", &out, host, testNow) != 2 || strings.Contains(out.String(), "SECRET") {
		t.Fatal("usage leak")
	}
}

func TestTrustBundleStrictness(t *testing.T) {
	host := fixture(t, false, testNow.Add(90*24*time.Hour))
	for _, extra := range [][]byte{[]byte("SECRET"), host.files["/key"], host.files["/cert"]} {
		data := append(append([]byte{}, host.files["/ca"]...), extra...)
		if _, ok := parseBundle(data); ok {
			t.Fatal("non-CA content accepted")
		}
	}
	host.files["/key"] = fixture(t, false, testNow.Add(90*24*time.Hour)).files["/key"]
	if stateFor(Run(host, controllerOptions(), testNow), "tls.key_match") != "fail" {
		t.Fatal("mismatching key accepted")
	}
}
