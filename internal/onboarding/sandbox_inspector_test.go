package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"hlcp/internal/agent"
	"io"
	"strings"
	"testing"
)

type inspectorFake struct {
	identities, files int
	fault             string
	cancel            context.CancelFunc
}

func (h *inspectorFake) Identity(context.Context, uint32, uint32) error {
	h.identities++
	if h.fault == "identity" || (h.fault == "post-identity" && h.identities == 2) {
		return errors.New("SECRET")
	}
	return nil
}
func (h *inspectorFake) ScriptFiles(_ context.Context, c agent.ScriptCommandConfig) ([32]byte, error) {
	if len(c.Argv) != 0 || len(c.Environment) != 0 || c.Timeout != 0 {
		panic("resource execution input reached inspector")
	}
	h.files++
	if h.cancel != nil {
		h.cancel()
	}
	if h.fault == "file" {
		return [32]byte{}, errors.New("SECRET")
	}
	if h.fault == "zero" {
		return [32]byte{}, nil
	}
	return [32]byte{1}, nil
}
func inspectorFixture(t *testing.T) ([]byte, SandboxProbeSnapshot, SandboxProbeInputs) {
	t.Helper()
	h := freshProbeFake(t)
	inputs, err := sandboxProbeInputs(probeConfig())
	if err != nil {
		t.Fatal(err)
	}
	data, err := encodeSandboxInspectorRequest(h.snapshot, inputs)
	if err != nil {
		t.Fatal(err)
	}
	return data, h.snapshot, inputs
}
func TestSandboxInspectorIPCAndSeparateLifecycleEvidence(t *testing.T) {
	input, snapshot, inputs := inspectorFixture(t)
	h := &inspectorFake{}
	var out bytes.Buffer
	if runSandboxInspector(context.Background(), nil, bytes.NewReader(input), &out, h) != 0 || h.files != 2 || h.identities != 2 {
		t.Fatal(out.String())
	}
	for _, secret := range []string{"SECRET", "/opt/", "working_directory", "argv", "environment"} {
		if strings.Contains(string(input), secret) && secret != "/opt/" && secret != "working_directory" {
			t.Fatal("private execution data in input")
		}
		if strings.Contains(out.String(), secret) {
			t.Fatal("private output leaked")
		}
	}
	evidence, err := decodeSandboxInspectorResponse(out.Bytes(), snapshot, inputs)
	if err != nil || evidence.DependencyProof == [32]byte{} || evidence.InspectorExited || evidence.UnitCollected {
		t.Fatal("IPC forged lifecycle acceptance", err)
	}
	var repeated bytes.Buffer
	if runSandboxInspector(context.Background(), nil, bytes.NewReader(input), &repeated, &inspectorFake{}) != 0 || !bytes.Equal(out.Bytes(), repeated.Bytes()) {
		t.Fatal("unstable response")
	}
	var request inspectorRequest
	json.Unmarshal(input, &request)
	request.Files[0].WorkingDirectory = "/usr"
	changed, _ := json.Marshal(request)
	repeated.Reset()
	runSandboxInspector(context.Background(), nil, bytes.NewReader(changed), &repeated, &inspectorFake{})
	if bytes.Equal(out.Bytes(), repeated.Bytes()) {
		t.Fatal("paths not bound to dependency evidence")
	}
}
func TestSandboxInspectorMalformedInputNeverInspects(t *testing.T) {
	data, _, _ := inspectorFixture(t)
	var request inspectorRequest
	json.Unmarshal(data, &request)
	cases := [][]byte{nil, []byte("null"), []byte("{}"), append(bytes.Clone(data), []byte("{}")...), []byte(strings.Repeat("x", inspectorInputLimit+1)), []byte(`{"version":1,"version":1}`), []byte(`{"files":[{"executable":"/opt/a","executable":"/opt/b"}]}`), []byte(`{"files":[[[[[[]]]]]]}`)}
	for _, kind := range []string{"version", "uid", "gid", "source", "request", "files", "path", "hidden", "duplicate-pair", "unsorted", "unknown", "unknown-nested", "null-uid"} {
		copyRequest := request
		copyRequest.Files = append([]inspectorFile{}, request.Files...)
		switch kind {
		case "version":
			copyRequest.Version = 2
		case "uid":
			copyRequest.UID = 0
		case "gid":
			copyRequest.GID = 0
		case "source":
			copyRequest.SourceProof = strings.Repeat("0", 64)
		case "request":
			copyRequest.RequestProof = strings.Repeat("A", 64)
		case "files":
			copyRequest.Files = nil
		case "path":
			copyRequest.Files[0].Executable = "/opt/../root/SECRET"
		case "hidden":
			copyRequest.Files[0].WorkingDirectory = "/home/SECRET"
		case "duplicate-pair":
			copyRequest.Files[1] = copyRequest.Files[0]
		case "unsorted":
			copyRequest.Files[0], copyRequest.Files[1] = copyRequest.Files[1], copyRequest.Files[0]
		}
		encoded, _ := json.Marshal(copyRequest)
		if kind == "unknown" {
			encoded = append([]byte(`{"argv":["SECRET"],`), encoded[1:]...)
		}
		if kind == "unknown-nested" {
			encoded = bytes.Replace(encoded, []byte(`"executable":`), []byte(`"environment":{"SECRET":"private"},"executable":`), 1)
		}
		if kind == "null-uid" {
			encoded = bytes.Replace(encoded, []byte(`"uid":1000`), []byte(`"uid":null`), 1)
		}
		cases = append(cases, encoded)
	}
	for _, input := range cases {
		h := &inspectorFake{}
		var out bytes.Buffer
		if runSandboxInspector(context.Background(), nil, bytes.NewReader(input), &out, h) != 2 || h.files != 0 || h.identities != 0 || !strings.Contains(out.String(), "INPUT_REFUSED") || strings.Contains(out.String(), "SECRET") {
			t.Fatal("malformed input accepted", out.String())
		}
	}
	for _, input := range [][]byte{bytes.Replace(data, []byte(`"version"`), []byte(`"Version"`), 1), append([]byte(`{"Version":1,`), data[1:]...), {0xff}} {
		h := &inspectorFake{}
		var out bytes.Buffer
		if runSandboxInspector(context.Background(), nil, bytes.NewReader(input), &out, h) != 2 || h.identities != 0 {
			t.Fatal("ambiguous field case/UTF-8 accepted")
		}
	}
}

func FuzzSandboxInspectorInput(f *testing.F) {
	valid, _ := json.Marshal(inspectorRequest{Version: 1, UID: 1000, GID: 1000, SourceProof: strings.Repeat("a", 64), RequestProof: strings.Repeat("b", 64), Files: []inspectorFile{{"/opt/probe", "/opt"}}})
	f.Add(valid)
	f.Add([]byte(`{"version":1}`))
	f.Add([]byte(`{"version":1,"version":2}`))
	f.Add([]byte("null"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > inspectorInputLimit+1 {
			t.Skip()
		}
		var out bytes.Buffer
		code := runSandboxInspector(context.Background(), nil, bytes.NewReader(data), &out, &inspectorFake{})
		if code != 0 && code != 2 {
			t.Fatal("unexpected exit code")
		}
		if out.Len() > inspectorOutputLimit {
			t.Fatal("unbounded output")
		}
		var response inspectorResponse
		if json.Unmarshal(out.Bytes(), &response) != nil {
			t.Fatal("invalid inspector output")
		}
	})
}

type inspectorPanicReader struct{}

func (inspectorPanicReader) Read([]byte) (int, error) { panic("invalid options read stdin") }

type inspectorBrokenWriter struct{}

func (inspectorBrokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestSandboxInspectorFailuresAreControlled(t *testing.T) {
	data, _, _ := inspectorFixture(t)
	if runSandboxInspector(context.Background(), []string{"--config", "SECRET"}, inspectorPanicReader{}, io.Discard, &inspectorFake{}) != 2 {
		t.Fatal("options accepted")
	}
	for _, fault := range []string{"identity", "post-identity", "file", "zero", "cancel"} {
		h := &inspectorFake{fault: fault}
		ctx, cancel := context.WithCancel(context.Background())
		if fault == "cancel" {
			h.cancel = cancel
		}
		var out bytes.Buffer
		if runSandboxInspector(ctx, nil, bytes.NewReader(data), &out, h) != 2 || strings.Contains(out.String(), "SECRET") {
			t.Fatal(out.String())
		}
		cancel()
	}
	if runSandboxInspector(context.Background(), nil, bytes.NewReader(data), inspectorBrokenWriter{}, &inspectorFake{}) != 2 {
		t.Fatal("output failure accepted")
	}
}
func TestSandboxInspectorResponseRefusesMismatches(t *testing.T) {
	data, snapshot, inputs := inspectorFixture(t)
	var out bytes.Buffer
	runSandboxInspector(context.Background(), nil, bytes.NewReader(data), &out, &inspectorFake{})
	var response inspectorResponse
	json.Unmarshal(out.Bytes(), &response)
	for _, kind := range []string{"version", "status", "code", "uid", "gid", "source", "request", "dependency", "count", "trailing", "unknown", "size", "duplicate"} {
		copyResponse := response
		switch kind {
		case "version":
			copyResponse.Version = 2
		case "status":
			copyResponse.Status = "failed"
		case "code":
			copyResponse.Code = "SECRET"
		case "uid":
			copyResponse.UID = 0
		case "gid":
			copyResponse.GID = 0
		case "source":
			copyResponse.SourceProof = strings.Repeat("b", 64)
		case "request":
			copyResponse.RequestProof = strings.Repeat("b", 64)
		case "dependency":
			copyResponse.DependencyProof = strings.Repeat("0", 64)
		case "count":
			copyResponse.Checks++
		}
		encoded, _ := json.Marshal(copyResponse)
		switch kind {
		case "trailing":
			encoded = append(encoded, []byte("{}")...)
		case "unknown":
			encoded = append([]byte(`{"argv":"SECRET",`), encoded[1:]...)
		case "size":
			encoded = []byte(strings.Repeat("x", inspectorOutputLimit+1))
		case "duplicate":
			encoded = append([]byte(`{"version":1,`), encoded[1:]...)
		}
		if _, err := decodeSandboxInspectorResponse(encoded, snapshot, inputs); !errors.Is(err, ErrSandboxProbeInvalid) {
			t.Fatal("invalid response accepted", kind)
		}
	}
}
