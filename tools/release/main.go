// Release builds are offline, deterministic and never overwrite an output tree.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"hlcp/internal/releaseauth"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

var metadataPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)
var commands = []string{"hlcp", "hlcp-agent", "hlcp-controller"}

type artifact struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}
type manifest struct {
	Version         string     `json:"version"`
	Revision        string     `json:"revision"`
	GoVersion       string     `json:"go_version"`
	ProtocolVersion int        `json:"protocol_version"`
	StateVersion    int        `json:"state_version"`
	Artifacts       []artifact `json:"artifacts"`
}

func main() {
	out := flag.String("out", "dist/release", "new output directory (must not exist)")
	version := flag.String("version", "dev", "release version label")
	targets := flag.String("targets", "linux/amd64,linux/arm64,windows/amd64", "supported OS/architecture pairs")
	allowDirty := flag.Bool("allow-dirty", false, "local validation only; mark revision with -dirty")
	signingKey := flag.String("signing-key", "", "explicit private Ed25519 signing key; must match pinned public key")
	flag.Parse()
	if err := release(*out, *version, *targets, *allowDirty); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *signingKey != "" {
		public, err := os.ReadFile("deploy/install/release.pub")
		if err != nil || releaseauth.SignDirectory(*out, *signingKey, public) != nil {
			fmt.Fprintln(os.Stderr, "Release signing failed; unsigned artifacts must not be published as verified releases.")
			os.Exit(1)
		}
	}
}

func parseTargets(input string) ([]string, error) {
	seen := map[string]bool{}
	result := strings.Split(input, ",")
	for _, target := range result {
		if target != "linux/amd64" && target != "linux/arm64" && target != "windows/amd64" {
			return nil, fmt.Errorf("unsupported target %q", target)
		}
		if seen[target] {
			return nil, fmt.Errorf("duplicate target %q", target)
		}
		seen[target] = true
	}
	sort.Strings(result)
	return result, nil
}

func sourceRevision(allowDirty bool) (string, error) {
	sha, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	status, err := exec.Command("git", "status", "--porcelain", "--untracked-files=normal").Output()
	if err != nil {
		return "", err
	}
	revision := strings.TrimSpace(string(sha))
	if len(status) > 0 {
		if !allowDirty {
			return "", fmt.Errorf("release requires a clean worktree; --allow-dirty is for local validation only")
		}
		revision += "-dirty"
	}
	return revision, nil
}

// Remove inherited build modifiers and replace environment keys portably.
func buildEnv(target string) []string {
	parts := strings.Split(target, "/")
	overrides := map[string]string{"GOOS": parts[0], "GOARCH": parts[1], "CGO_ENABLED": "0", "GOTOOLCHAIN": "local", "GOFLAGS": "", "GOEXPERIMENT": "", "GOWORK": "off", "GOAMD64": "v1", "GOARM64": "v8.0", "GOPROXY": "off", "GOSUMDB": "off"}
	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if _, replaced := overrides[strings.ToUpper(key)]; !replaced {
			env = append(env, item)
		}
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+overrides[key])
	}
	return env
}

func checksum(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func release(out, version, targetList string, allowDirty bool) error {
	if !metadataPattern.MatchString(version) {
		return fmt.Errorf("invalid version label")
	}
	targets, err := parseTargets(targetList)
	if err != nil {
		return err
	}
	pinned, err := os.ReadFile(".go-version")
	if err != nil {
		return err
	}
	if runtime.Version() != "go"+strings.TrimSpace(string(pinned)) {
		return fmt.Errorf("release requires Go %s, running %s", strings.TrimSpace(string(pinned)), runtime.Version())
	}
	revision, err := sourceRevision(allowDirty)
	if err != nil {
		return err
	}
	if !metadataPattern.MatchString(revision) {
		return fmt.Errorf("invalid source revision")
	}
	if out == "" {
		return fmt.Errorf("output directory is required")
	}
	if err := os.MkdirAll(filepath.Dir(out), 0700); err != nil {
		return err
	}
	if err := os.Mkdir(out, 0700); err != nil {
		return fmt.Errorf("output must be new: %w", err)
	}
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	result := manifest{Version: version, Revision: revision, GoVersion: runtime.Version(), ProtocolVersion: 1, StateVersion: 1}
	ldflags := "-s -w -buildid= -X hlcp/internal/buildinfo.Version=" + version + " -X hlcp/internal/buildinfo.Revision=" + revision
	for _, target := range targets {
		for _, name := range commands {
			filename := name + "-" + strings.ReplaceAll(target, "/", "-")
			if strings.HasPrefix(target, "windows/") {
				filename += ".exe"
			}
			path := filepath.Join(out, filename)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			build := exec.CommandContext(ctx, goBinary, "build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-ldflags", ldflags, "-o", path, "./cmd/"+name)
			build.Env = buildEnv(target)
			output, err := build.CombinedOutput()
			cancel()
			if err != nil {
				return fmt.Errorf("build %s: %w\n%s", filename, err, output)
			}
			digest, err := checksum(path)
			if err != nil {
				return err
			}
			result.Artifacts = append(result.Artifacts, artifact{filename, digest})
			fmt.Println(filename)
		}
	}
	deployment, err := packageDeployment(out)
	if err != nil {
		return err
	}
	result.Artifacts = append(result.Artifacts, deployment)
	fmt.Println(deployment.Name)
	for _, target := range targets {
		if !strings.HasPrefix(target, "linux/") {
			continue
		}
		packaged, err := packageControllerDeb(out, version, revision, strings.TrimPrefix(target, "linux/"))
		if err != nil {
			return err
		}
		result.Artifacts = append(result.Artifacts, packaged)
		fmt.Println(packaged.Name)
		agent, err := packageAgentDeb(out, version, revision, strings.TrimPrefix(target, "linux/"))
		if err != nil {
			return err
		}
		result.Artifacts = append(result.Artifacts, agent)
		fmt.Println(agent.Name)
	}
	current, err := sourceRevision(allowDirty)
	if err != nil {
		return err
	}
	if current != revision {
		return fmt.Errorf("source revision changed during build")
	}
	sort.Slice(result.Artifacts, func(i, j int) bool { return result.Artifacts[i].Name < result.Artifacts[j].Name })
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "build-manifest.json"), append(data, '\n'), 0600); err != nil {
		return err
	}
	digest, err := checksum(filepath.Join(out, "build-manifest.json"))
	if err != nil {
		return err
	}
	var sums strings.Builder
	for _, artifact := range result.Artifacts {
		fmt.Fprintf(&sums, "%s  %s\n", artifact.SHA256, artifact.Name)
	}
	fmt.Fprintf(&sums, "%s  build-manifest.json\n", digest)
	return os.WriteFile(filepath.Join(out, "SHA256SUMS"), []byte(sums.String()), 0600)
}
