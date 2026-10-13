package main

import (
	"bytes"
	"hlcp/internal/cli"
	"hlcp/internal/localapi"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestConfigurePrivateDefaultsAndNoOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile", "cli.json")
	token := strings.Repeat("a", 64)
	var output bytes.Buffer
	args := []string{"--config", path, "--token-stdin"}
	if err := configure(args, strings.NewReader(token+"\n"), &output); err != nil {
		t.Fatal(err)
	}
	config, err := loadClientConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := localapi.LoadToken(config.TokenFile); err != nil || got != token {
		t.Fatal("token was not imported privately")
	}
	for _, file := range []string{path, config.TokenFile} {
		info, err := os.Stat(file)
		if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0600) {
			t.Fatal("unsafe file mode")
		}
	}
	if strings.Contains(output.String(), token) {
		t.Fatal("secret printed")
	}
	before, _ := os.ReadFile(path)
	if configure(args, strings.NewReader(strings.Repeat("b", 64)), &output) == nil {
		t.Fatal("overwrote configuration")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("changed existing configuration")
	}
	request := cli.Request{ConfigFile: path}
	if err := applyClientConfig(&request); err != nil || request.API != defaultAPI || request.APITokenFile != config.TokenFile {
		t.Fatal("saved defaults not applied")
	}
	request.API = "http://127.0.0.1:19444"
	request.APITokenFile = "explicit"
	if err := applyClientConfig(&request); err != nil || request.API != "http://127.0.0.1:19444" || request.APITokenFile != "explicit" {
		t.Fatal("explicit overrides lost")
	}
}

func TestClientConfigRejectsMalformedAndUnsafe(t *testing.T) {
	for _, body := range []string{
		`{"version":1,"api":"http://127.0.0.1:9444","api_token_file":"token","version":1}`,
		`{"Version":1,"api":"http://127.0.0.1:9444","api_token_file":"token"}`,
		`{"version":2,"api":"http://127.0.0.1:9444","api_token_file":"token"}`,
		`{"version":1,"api":"http://192.168.0.15:9444","api_token_file":"token"}`,
		`{"version":1,"api":"http://127.0.0.1:9444","api_token_file":"token"} {}`,
		strings.Repeat("x", 8193),
	} {
		path := filepath.Join(t.TempDir(), "cli.json")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadClientConfig(path); err == nil {
			t.Fatal("accepted malformed configuration")
		}
	}
	for _, token := range []string{"short", strings.Repeat("x", 257), strings.Repeat("x", 256) + "\r\nextra", strings.Repeat("x", 32) + "\x00"} {
		path := filepath.Join(t.TempDir(), "profile", "cli.json")
		if configure([]string{"--config", path, "--token-stdin"}, strings.NewReader(token), &bytes.Buffer{}) == nil {
			t.Fatal("accepted malformed token")
		}
		if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
			t.Fatal("invalid token created files")
		}
	}
}

func TestCLIConfiguredProcess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile", "cli.json")
	token := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/nodes" || r.Method != "GET" || r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("incorrect authenticated request")
		}
		w.Write([]byte("[]"))
	}))
	defer server.Close()
	child := exec.Command(os.Args[0], "-test.run=^TestCLIProcessHelper$", "--", "configure", "--config", path, "--api", server.URL, "--token-stdin")
	child.Env = append(os.Environ(), "HLCP_CLI_PROCESS_HELPER=1")
	child.Stdin = strings.NewReader(token)
	if output, err := child.CombinedOutput(); err != nil || strings.Contains(string(output), token) {
		t.Fatalf("configure process failed: %v", err)
	}
	child = exec.Command(os.Args[0], "-test.run=^TestCLIProcessHelper$", "--", "--config", path, "-n")
	child.Env = append(os.Environ(), "HLCP_CLI_PROCESS_HELPER=1")
	if output, err := child.CombinedOutput(); err != nil || !strings.Contains(string(output), "(none)") {
		t.Fatalf("configured discovery: %v %s", err, output)
	}
}

func TestAutomaticConfigDiscoveryAndLegacyFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("APPDATA", dir)
	if runtime.GOOS == "darwin" {
		t.Setenv("HOME", dir)
	}
	request := cli.Request{}
	if err := applyClientConfig(&request); err != nil || request.API != "" || request.APITokenFile != "" {
		t.Fatal("legacy defaults changed")
	}
	path, err := clientConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := configure([]string{"--token-stdin"}, strings.NewReader(strings.Repeat("a", 64)), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := applyClientConfig(&request); err != nil || request.API != defaultAPI || request.APITokenFile == "" {
		t.Fatal("automatic user config discovery failed")
	}
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if applyClientConfig(&cli.Request{}) == nil {
		t.Fatal("silently ignored invalid default configuration")
	}
	request = cli.Request{API: defaultAPI, APITokenFile: "explicit"}
	if err := applyClientConfig(&request); err != nil {
		t.Fatal("fully explicit transport did not bypass saved config")
	}
	if applyClientConfig(&cli.Request{ConfigFile: filepath.Join(dir, "missing")}) == nil {
		t.Fatal("ignored missing explicit config")
	}
}

func TestConfigureRejectsUnsafeDirectoryWithoutChangingIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows protection is managed by user ACL")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if configure([]string{"--config", filepath.Join(dir, "cli.json"), "--token-stdin"}, strings.NewReader(strings.Repeat("a", 64)), &bytes.Buffer{}) == nil {
		t.Fatal("accepted public directory")
	}
	info, _ := os.Stat(dir)
	if info.Mode().Perm() != 0755 {
		t.Fatal("changed existing directory mode")
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 0 {
		t.Fatal("left credentials in public directory")
	}
}
