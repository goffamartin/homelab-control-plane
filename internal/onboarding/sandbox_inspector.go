package onboarding

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hlcp/internal/agent"
	"io"
	"path"
	"strings"
	"time"
	"unicode/utf8"
)

const inspectorInputLimit = 1 << 20
const inspectorOutputLimit = 4096

type inspectorFile struct {
	Executable       string `json:"executable"`
	WorkingDirectory string `json:"working_directory"`
}
type inspectorRequest struct {
	Version      int             `json:"version"`
	UID          uint32          `json:"uid"`
	GID          uint32          `json:"gid"`
	SourceProof  string          `json:"source_proof"`
	RequestProof string          `json:"request_proof"`
	Files        []inspectorFile `json:"files"`
}
type inspectorResponse struct {
	Version         int    `json:"version"`
	Status          string `json:"status"`
	Code            string `json:"code,omitempty"`
	UID             uint32 `json:"uid,omitempty"`
	GID             uint32 `json:"gid,omitempty"`
	SourceProof     string `json:"source_proof,omitempty"`
	RequestProof    string `json:"request_proof,omitempty"`
	Checks          int    `json:"checks,omitempty"`
	DependencyProof string `json:"dependency_proof,omitempty"`
}
type sandboxInspectorHost interface {
	Identity(context.Context, uint32, uint32) error
	ScriptFiles(context.Context, agent.ScriptCommandConfig) ([32]byte, error)
}

// Private IPC only, never a user resource action. Early CLI dispatch prevents
// agent.Load/Run, controller connection, telemetry and journal initialization.
func SandboxInspectorCLI(args []string, input io.Reader, output io.Writer) int {
	return runSandboxInspector(context.Background(), args, input, output, nativeSandboxInspectorHost{})
}
func runSandboxInspector(parent context.Context, args []string, input io.Reader, output io.Writer, host sandboxInspectorHost) int {
	fail := func(code string) int {
		_ = json.NewEncoder(output).Encode(inspectorResponse{Version: 1, Status: "failed", Code: code})
		return 2
	}
	if len(args) != 0 || input == nil || host == nil {
		return fail("INVALID_OPTIONS")
	}
	// The launcher must close/bound stdin and enforce a whole-process timeout.
	// A context alone cannot interrupt a blocking arbitrary io.Reader.
	data, err := io.ReadAll(io.LimitReader(input, inspectorInputLimit+1))
	if err != nil || len(data) > inspectorInputLimit {
		return fail("INPUT_REFUSED")
	}
	var request inspectorRequest
	if strictInspectorDecode(data, &request) != nil || !validInspectorRequest(request) {
		return fail("INPUT_REFUSED")
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	if ctx.Err() != nil || host.Identity(ctx, request.UID, request.GID) != nil {
		return fail("IDENTITY_REFUSED")
	}
	proofs := make([][32]byte, 0, len(request.Files))
	for _, file := range request.Files {
		if ctx.Err() != nil {
			return fail("INSPECTION_REFUSED")
		}
		proof, err := host.ScriptFiles(ctx, agent.ScriptCommandConfig{Executable: file.Executable, WorkingDirectory: file.WorkingDirectory})
		if err != nil || proof == [32]byte{} {
			return fail("INSPECTION_REFUSED")
		}
		proofs = append(proofs, proof)
	}
	if ctx.Err() != nil || host.Identity(ctx, request.UID, request.GID) != nil {
		return fail("IDENTITY_REFUSED")
	}
	encoded, err := json.Marshal(struct {
		Request inspectorRequest
		Proofs  [][32]byte
	}{request, proofs})
	if err != nil {
		return fail("INSPECTION_REFUSED")
	}
	proof := sha256.Sum256(encoded)
	response := inspectorResponse{Version: 1, Status: "inspected", UID: request.UID, GID: request.GID, SourceProof: request.SourceProof, RequestProof: request.RequestProof, Checks: len(proofs), DependencyProof: hex.EncodeToString(proof[:])}
	if json.NewEncoder(output).Encode(response) != nil {
		return 2
	}
	return 0
}
func inspectorDigest(value string) ([32]byte, bool) {
	result := [32]byte{}
	if len(value) != 64 || strings.ToLower(value) != value {
		return result, false
	}
	data, err := hex.DecodeString(value)
	if err != nil {
		return result, false
	}
	copy(result[:], data)
	return result, result != [32]byte{}
}
func validInspectorPath(value string) bool {
	if underSystemPath(value, "/run/credentials") || value == "/var/lib/hlcp/command-journal.json" || strings.HasPrefix(value, "/var/lib/hlcp/command-journal.json.") {
		return false
	}
	if len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) || !path.IsAbs(value) || path.Clean(value) != value {
		return false
	}
	for _, hidden := range []string{"/home", "/root", "/run/user", "/tmp", "/var/tmp"} {
		if underSystemPath(value, hidden) {
			return false
		}
	}
	return true
}
func validInspectorRequest(r inspectorRequest) bool {
	if r.Version != 1 || r.UID == 0 || r.GID == 0 || len(r.Files) == 0 || len(r.Files) > 128 {
		return false
	}
	if _, ok := inspectorDigest(r.SourceProof); !ok {
		return false
	}
	if _, ok := inspectorDigest(r.RequestProof); !ok {
		return false
	}
	for index, file := range r.Files {
		if !validInspectorPath(file.Executable) || !validInspectorPath(file.WorkingDirectory) {
			return false
		}
		if index > 0 {
			previous := r.Files[index-1]
			if previous.Executable > file.Executable || (previous.Executable == file.Executable && previous.WorkingDirectory >= file.WorkingDirectory) {
				return false
			}
		}
	}
	return true
}
func encodeSandboxInspectorRequest(snapshot SandboxProbeSnapshot, inputs SandboxProbeInputs) ([]byte, error) {
	if !validSandboxProbeSnapshot(snapshot) || snapshot.ConfigProof != inputs.configProof || inputs.binding == [32]byte{} {
		return nil, ErrSandboxProbeInvalid
	}
	request := inspectorRequest{Version: 1, UID: snapshot.UID, GID: snapshot.GID, SourceProof: hex.EncodeToString(snapshot.SourceProof[:]), RequestProof: hex.EncodeToString(inputs.binding[:])}
	for _, file := range inputs.files {
		request.Files = append(request.Files, inspectorFile{file.Executable, file.WorkingDirectory})
	}
	if !validInspectorRequest(request) {
		return nil, ErrSandboxProbeInvalid
	}
	data, err := json.Marshal(request)
	if err != nil || len(data) > inspectorInputLimit {
		return nil, ErrSandboxProbeInvalid
	}
	return data, nil
}

// IPC output is not exit/reap/cleanup evidence. Flags remain false until the
// future native launcher independently verifies process exit and unit collection.
func decodeSandboxInspectorResponse(data []byte, snapshot SandboxProbeSnapshot, inputs SandboxProbeInputs) (SandboxProbeEvidence, error) {
	var response inspectorResponse
	if _, err := encodeSandboxInspectorRequest(snapshot, inputs); err != nil {
		return SandboxProbeEvidence{}, ErrSandboxProbeInvalid
	}
	if len(data) > inspectorOutputLimit || !validSandboxProbeSnapshot(snapshot) || snapshot.ConfigProof != inputs.configProof || strictInspectorDecode(data, &response) != nil {
		return SandboxProbeEvidence{}, ErrSandboxProbeInvalid
	}
	source, ok1 := inspectorDigest(response.SourceProof)
	request, ok2 := inspectorDigest(response.RequestProof)
	dependency, ok3 := inspectorDigest(response.DependencyProof)
	if !ok1 || !ok2 || !ok3 || response.Version != 1 || response.Status != "inspected" || response.Code != "" || response.UID != snapshot.UID || response.GID != snapshot.GID || source != snapshot.SourceProof || request != inputs.binding || response.Checks != len(inputs.files) || response.Checks == 0 {
		return SandboxProbeEvidence{}, ErrSandboxProbeInvalid
	}
	return SandboxProbeEvidence{SourceProof: source, RequestProof: request, DependencyProof: dependency, UID: response.UID, GID: response.GID}, nil
}

// Reject duplicate keys at every nesting level, unknown fields, excessive
// nesting and trailing documents. Only one bounded JSON object is accepted.
func strictInspectorDecode(data []byte, target any) error {
	if !utf8.Valid(data) {
		return ErrSandboxProbeInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := inspectorJSONValue(decoder, 0); err != nil {
		return ErrSandboxProbeInvalid
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrSandboxProbeInvalid
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return ErrSandboxProbeInvalid
	}
	return nil
}
func inspectorJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 4 {
		return ErrSandboxProbeInvalid
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] || name != strings.ToLower(name) {
				return ErrSandboxProbeInvalid
			}
			seen[name] = true
			if err := inspectorJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return ErrSandboxProbeInvalid
		}
	case '[':
		for decoder.More() {
			if err := inspectorJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return ErrSandboxProbeInvalid
		}
	default:
		return ErrSandboxProbeInvalid
	}
	return nil
}
