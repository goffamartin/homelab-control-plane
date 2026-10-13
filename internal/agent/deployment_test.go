package agent

import (
	"context"
	"errors"
	"hlcp/internal/controller"
	"hlcp/internal/liveness"
	"hlcp/internal/localapi"
	"hlcp/internal/telemetry"
	"hlcp/internal/transport"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type noRuntimeRunner struct{ calls atomic.Int32 }

func (runner *noRuntimeRunner) Run(context.Context, string, ...string) (string, error) {
	runner.calls.Add(1)
	return "", errors.New("no runtime is installed")
}

func TestRuntimeIndependentAgentConnectsWithoutAdapters(t *testing.T) {
	serverFiles, clientFiles := e2ePKI(t)
	dir := t.TempDir()
	server := startE2EController(t, serverFiles, filepath.Join(dir, "ledger.json"))
	runner := &noRuntimeRunner{}
	stopAgent := startE2EAgent(t, Config{
		ControllerAddress: server.server.Address().String(), ServerName: clientFiles.ServerName,
		CertificateFile: clientFiles.CertificateFile, PrivateKeyFile: clientFiles.PrivateKeyFile, CAFile: clientFiles.CAFile,
		JournalFile: filepath.Join(dir, "journal.json"), NodeID: "debian-01", AgentVersion: "generic-test",
		runner: runner, ObservationInterval: time.Second,
	})
	defer stopAgent()
	e2eEventually(t, "generic node online", func() bool {
		node, ok := server.server.Registry().Get("debian-01")
		return ok && node.State == "online" && node.Inventory.Revision == 1
	})
	first, _ := server.server.Registry().Get("debian-01")
	e2eEventually(t, "generic node heartbeat without runtime", func() bool {
		node, ok := server.server.Registry().Get("debian-01")
		return ok && node.State == "online" && node.LastActivity.After(first.LastActivity)
	})
	last, _ := server.server.Registry().Get("debian-01")
	if len(last.Capabilities) != 0 || len(last.Inventory.Resources) != 0 || len(last.Resources.Resources) != 0 || runner.calls.Load() != 0 {
		t.Fatal("generic node invented resources or invoked an unavailable runtime")
	}
}

func TestNodeRemovalOnControllerRestart(t *testing.T) {
	serverFiles, clientFiles := e2ePKI(t)
	tlsConfig, err := transport.ServerTLSConfig(serverFiles)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	start := func(address string, nodes map[string]bool) (*controller.Server, func()) {
		server, err := controller.Listen(address, controller.Config{TLSConfig: tlsConfig,
			AllowedNodeIDs: nodes, LedgerFile: filepath.Join(dir, "ledger.json"),
			LivenessPolicy: liveness.Policy{HeartbeatInterval: time.Second, DegradedAfter: 2, OfflineAfter: 3}})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- server.Serve(ctx) }()
		var once sync.Once
		stop := func() {
			once.Do(func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("controller did not stop")
				}
			})
		}
		t.Cleanup(stop)
		return server, stop
	}
	nodes := map[string]bool{"debian-01": true}
	server, stop := start("127.0.0.1:0", nodes)
	delete(nodes, "debian-01") // The running controller must own a copy.
	address := server.Address().String()
	stopAgent := startE2EAgent(t, Config{ControllerAddress: address, ServerName: clientFiles.ServerName,
		CertificateFile: clientFiles.CertificateFile, PrivateKeyFile: clientFiles.PrivateKeyFile, CAFile: clientFiles.CAFile,
		NodeID: "debian-01", AgentVersion: "allowlist-test", Capabilities: []string{"resource.control", "docker"},
		JournalFile: filepath.Join(dir, "journal.json"), runner: &e2eRunner{},
		Resources: []ResourceConfig{{ID: "catalog", Type: "container", Adapter: "docker", Target: "catalog", Actions: []string{"status"}}}})
	e2eEventually(t, "allowed node online after caller map change", func() bool {
		node, ok := server.Registry().Get("debian-01")
		return ok && node.State == "online"
	})
	stop()
	if server.Telemetry().Snapshot().Gauges["connections"] != 0 {
		t.Fatal("active connection survived stop")
	}
	denied, _ := start(address, map[string]bool{})
	e2eEventually(t, "removed identity rejected after restart", func() bool {
		for _, event := range denied.Telemetry().Events() {
			if event.Kind == telemetry.ConnectionRejected && event.Code == "NODE_NOT_ALLOWED" && event.NodeID == "debian-01" {
				return true
			}
		}
		return false
	})
	if _, ok := denied.Registry().Get("debian-01"); ok {
		t.Fatal("denied identity registered")
	}
	stopAgent()
}

func TestControllerCheckConfigDoesNotBindOrCreateLedger(t *testing.T) {
	files, _ := e2ePKI(t)
	dir := t.TempDir()
	binary := filepath.Join(dir, "controller")
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		binary += ".exe"
		goBinary += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, goBinary, "build", "-trimpath", "-o", binary, "../../cmd/hlcp-controller")
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	token := filepath.Join(dir, "api.token")
	nodes := filepath.Join(dir, "nodes.json")
	ledger := filepath.Join(dir, "missing", "ledger.json")
	if err := os.WriteFile(token, []byte(e2eToken), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nodes, []byte(`["debian-01"]`), 0600); err != nil {
		t.Fatal(err)
	}
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	args := []string{"-check-config", "-listen", busy.Addr().String(), "-cert", files.CertificateFile,
		"-key", files.PrivateKeyFile, "-ca", files.CAFile, "-api-token-file", token,
		"-node-allowlist-file", nodes, "-ledger-file", ledger}
	if output, err := exec.CommandContext(ctx, binary, args...).CombinedOutput(); err != nil {
		t.Fatalf("check: %v %s", err, output)
	}
	if _, err := os.Stat(filepath.Dir(ledger)); !os.IsNotExist(err) {
		t.Fatal("configuration check touched ledger directory")
	}
	for _, flags := range [][]string{
		{"-key", filepath.Join(dir, "missing.key")}, {"-node-allowlist-file", token},
		{"-api-token-file", nodes}, {"-heartbeat-interval", "0s"}, {"-api-listen", "0.0.0.0:9444"},
		{"-listen", "127.0.0.1:not-a-port"},
	} {
		negative := append(append([]string{}, args...), flags...)
		if _, err := exec.CommandContext(ctx, binary, negative...).CombinedOutput(); err == nil {
			t.Fatalf("accepted invalid flags: %v", flags)
		}
	}
}

func TestDeploymentAgentTemplates(t *testing.T) {
	for _, name := range []string{"agent-docker.yaml", "agent-podman.yaml", "agent-user.yaml", "agent-system.yaml", "agent-script.yaml"} {
		if _, err := Load(filepath.Join("..", "..", "deploy", "config", name)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "config", "api-policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string][]byte{
		"api-policy.json": data, "api.token": []byte(e2eToken),
		"observer.token": []byte("observer-token-distinct-01234567890123456789"),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), contents, 0400); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := localapi.LoadPolicy(filepath.Join(dir, "api-policy.json")); err != nil {
		t.Fatalf("credential snapshot policy: %v", err)
	}
}
