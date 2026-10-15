package releaseauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Artifact struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}
type Manifest struct {
	Version         string     `json:"version"`
	Revision        string     `json:"revision"`
	GoVersion       string     `json:"go_version"`
	ProtocolVersion int        `json:"protocol_version"`
	StateVersion    int        `json:"state_version"`
	Artifacts       []Artifact `json:"artifacts"`
}

var versionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.]+)?$`)
var revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var filenamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,200}$`)

func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, ErrSignature
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, ErrSignature
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, ErrSignature
	}
	return data, nil
}
func VerifyDirectory(directory, version, role, arch string, public []byte) (string, error) {
	if !filepath.IsAbs(directory) || !versionPattern.MatchString(version) || (role != "controller" && role != "agent") || (arch != "amd64" && arch != "arm64") {
		return "", ErrSignature
	}
	data, err := readBounded(filepath.Join(directory, "build-manifest.json"), 1<<20)
	if err != nil {
		return "", ErrSignature
	}
	sig, err := readBounded(filepath.Join(directory, "build-manifest.json.sig"), 64)
	if err != nil || Verify(public, data, sig) != nil {
		return "", ErrSignature
	}
	var m Manifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&m) != nil || decoder.Decode(new(any)) != io.EOF || m.Version != version || !revisionPattern.MatchString(m.Revision) || m.ProtocolVersion != 1 || m.StateVersion != 1 || len(m.Artifacts) == 0 || len(m.Artifacts) > 64 {
		return "", ErrSignature
	}
	packageName := "hlcp-" + role + "_" + strings.TrimPrefix(version, "v") + "_" + arch + ".deb"
	required := map[string]bool{packageName: false, "hlcp-linux-" + arch: false}
	seen := map[string]bool{}
	for _, a := range m.Artifacts {
		digest, e := hex.DecodeString(a.SHA256)
		if e != nil || len(digest) != 32 || !filenamePattern.MatchString(a.Name) || seen[a.Name] {
			return "", ErrSignature
		}
		seen[a.Name] = true
		if _, wanted := required[a.Name]; !wanted {
			continue
		}
		content, e := readBounded(filepath.Join(directory, a.Name), 64<<20)
		if e != nil {
			return "", ErrSignature
		}
		actual := sha256.Sum256(content)
		if hex.EncodeToString(actual[:]) != a.SHA256 {
			return "", ErrSignature
		}
		required[a.Name] = true
	}
	for _, valid := range required {
		if !valid {
			return "", ErrSignature
		}
	}
	return packageName, nil
}
