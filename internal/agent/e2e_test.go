package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hlcp/internal/command"
	"hlcp/internal/controller"
	"hlcp/internal/liveness"
	"hlcp/internal/transport"
)

// This gate is after the simulated external side effect, before journal/result.
// Observation calls are read-only and never increment the side-effect counter.
type e2eRunner struct {
	calls    atomic.Int32
	finished atomic.Bool
	started  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (runner *e2eRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	if name != "docker" {
		return "", fmt.Errorf("unexpected executable %q", name)
	}
	if len(args) > 0 && args[0] == "container" {
		return "running", nil
	}
	if len(args) != 2 || args[0] != "restart" || args[1] != "catalog" {
		return "", fmt.Errorf("unexpected action %v", args)
	}
	runner.calls.Add(1)
	defer runner.finished.Store(true)
	if runner.started != nil {
		runner.once.Do(func() { close(runner.started) })
		select {
		case <-runner.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return "catalog", nil
}

type e2eController struct {
	server   *controller.Server
	api      *httptest.Server
	cancel   context.CancelFunc
	done     chan error
	stopOnce sync.Once
}

const e2eToken = "01234567890123456789012345678901"

func startE2EController(t *testing.T, files transport.TLSFiles, ledger string) *e2eController {
	t.Helper()
	tlsConfig, err := transport.ServerTLSConfig(files)
	if err != nil {
		t.Fatal(err)
	}
	server, err := controller.Listen("127.0.0.1:0", controller.Config{
		TLSConfig: tlsConfig, LedgerFile: ledger,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		LivenessPolicy: liveness.Policy{HeartbeatInterval: time.Second, DegradedAfter: 2, OfflineAfter: 3},
		CommandRetry:   controller.RetryPolicy{ACKTimeout: time.Second, ResultTimeout: 3 * time.Second, MaxAttempts: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	fixture := &e2eController{server: server, api: httptest.NewServer(server.HTTPHandler(e2eToken)), cancel: cancel, done: make(chan error, 1)}
	go func() { fixture.done <- server.Serve(ctx) }()
	t.Cleanup(func() { fixture.stop(t); fixture.api.Close() })
	return fixture
}

func (fixture *e2eController) stop(t *testing.T) {
	t.Helper()
	fixture.stopOnce.Do(func() {
		fixture.cancel()
		select {
		case err := <-fixture.done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("controller did not join")
		}
	})
}

func startE2EAgent(t *testing.T, config Config) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, config) }()
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
				t.Fatal("agent did not join")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func e2eEventually(t *testing.T, description string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", description)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func e2eOperation(api, id string) (controller.CommandProgress, bool) {
	var progress controller.CommandProgress
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", api+"/v1/nodes/debian-01/commands/"+id, nil)
	request.Header.Set("Authorization", "Bearer "+e2eToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return progress, false
	}
	defer response.Body.Close()
	ok := response.StatusCode == 200 && json.NewDecoder(response.Body).Decode(&progress) == nil
	return progress, ok
}

type e2eCLIResult struct {
	code           int
	stdout, stderr string
}

func e2eCLI(binary, api, token string, args ...string) e2eCLIResult {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	arguments := append([]string{"--api", api, "--api-token-file", token, "--json", "-n", "debian-01"}, args...)
	child := exec.CommandContext(ctx, binary, arguments...)
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	err := child.Run()
	code := 0
	if err != nil {
		code = -1
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		}
	}
	return e2eCLIResult{code, stdout.String(), stderr.String()}
}

func requireE2ECLI(t *testing.T, result e2eCLIResult, code int) {
	t.Helper()
	if result.code != code {
		t.Fatalf("CLI exit=%d want=%d stdout=%s stderr=%s", result.code, code, result.stdout, result.stderr)
	}
	if code == 0 && !json.Valid([]byte(result.stdout)) {
		t.Fatalf("invalid CLI JSON: %s", result.stdout)
	}
}

func TestE2ECLIWithMTLSAndDurableRecovery(t *testing.T) {
	// Build the real CLI, not an HTTP imitation or test-only main helper.
	buildDirectory := t.TempDir()
	binary := filepath.Join(buildDirectory, "hlcp")
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		binary += ".exe"
		goBinary += ".exe"
	}
	buildContext, cancelBuild := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelBuild()
	build := exec.CommandContext(buildContext, goBinary, "build", "-trimpath", "-o", binary, "../../cmd/hlcp")
	build.Env = append(os.Environ(), "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v %s", err, output)
	}
	serverFiles, clientFiles := e2ePKI(t)
	for _, recovery := range []bool{false, true} {
		t.Run(fmt.Sprintf("lost-result-and-restart=%v", recovery), func(t *testing.T) {
			directory := t.TempDir()
			token := filepath.Join(directory, "api.token")
			if err := os.WriteFile(token, []byte(e2eToken), 0600); err != nil {
				t.Fatal(err)
			}
			ledger := filepath.Join(directory, "ledger.json")
			fixture := startE2EController(t, serverFiles, ledger)
			runner := new(e2eRunner)
			if recovery {
				runner.started = make(chan struct{})
				runner.release = make(chan struct{})
				t.Cleanup(func() {
					select {
					case <-runner.release:
					default:
						close(runner.release)
					}
				})
			}
			config := Config{ControllerAddress: fixture.server.Address().String(), ServerName: clientFiles.ServerName,
				CertificateFile: clientFiles.CertificateFile, PrivateKeyFile: clientFiles.PrivateKeyFile, CAFile: clientFiles.CAFile,
				NodeID: "debian-01", AgentVersion: "e2e", Capabilities: []string{"resource.control", "docker"},
				JournalFile: filepath.Join(directory, "agent.json"), runner: runner,
				Resources: []ResourceConfig{{ID: "catalog", Type: "container", Adapter: "docker", Target: "catalog", Actions: []string{"status", "restart"}}},
			}
			stopAgent := startE2EAgent(t, config)
			t.Cleanup(func() {
				if t.Failed() {
					data, err := os.ReadFile(config.JournalFile)
					t.Logf("agent journal after failure: %s (read error: %v)", data, err)
					t.Logf("runner calls=%d finished=%v", runner.calls.Load(), runner.finished.Load())
					stack := make([]byte, 1<<18)
					n := runtime.Stack(stack, true)
					for _, entry := range strings.Split(string(stack[:n]), "\n\n") {
						if strings.Contains(entry, "Dispatcher).work") {
							t.Log(entry)
						}
					}
				}
			})
			e2eEventually(t, "authenticated node observation", func() bool {
				node, ok := fixture.server.Registry().Get(config.NodeID)
				return ok && node.State == liveness.NodeOnline && len(node.Resources.Resources) == 1 && !node.Resources.Resources[0].Stale && node.Resources.Resources[0].State == "running"
			})
			args := []string{"-r", "catalog", "-a", "restart", "--command-id", "op-1"}
			beforeLedger, err := os.ReadFile(ledger)
			if err != nil {
				t.Fatal(err)
			}
			beforeJournal, err := os.ReadFile(config.JournalFile)
			if err != nil {
				t.Fatal(err)
			}
			requireE2ECLI(t, e2eCLI(binary, fixture.api.URL, token, append(args, "--dry-run")...), 0)
			afterLedger, _ := os.ReadFile(ledger)
			afterJournal, _ := os.ReadFile(config.JournalFile)
			if !bytes.Equal(beforeLedger, afterLedger) || !bytes.Equal(beforeJournal, afterJournal) || runner.calls.Load() != 0 {
				t.Fatal("dry-run admitted or executed a command")
			}
			if recovery {
				cliDone := make(chan e2eCLIResult, 1)
				go func() { cliDone <- e2eCLI(binary, fixture.api.URL, token, args...) }()
				select {
				case <-runner.started:
				case <-time.After(5 * time.Second):
					t.Fatal("action not started")
				}
				e2eEventually(t, "durable ACK before dropped result", func() bool {
					p, ok := e2eOperation(fixture.api.URL, "op-1")
					return ok && p.AckStatus == "accepted" && p.Attempts == 1 && p.Result == nil
				})
				fixture.stop(t) // TLS is closed while the result is still gated.
				runningFile, err := os.Stat(config.JournalFile)
				if err != nil {
					t.Fatal(err)
				}
				close(runner.release)
				// Stat observes replacement without opening/locking its destination.
				// Reading during Windows MoveFileEx can itself force persistence failure.
				e2eEventually(t, "agent journal replacement", func() bool {
					info, err := os.Stat(config.JournalFile)
					return err == nil && info.Size() != runningFile.Size()
				})
				e2eEventually(t, "agent result durable with no controller", func() bool {
					data, _ := os.ReadFile(config.JournalFile)
					var document struct {
						Entries []command.Entry `json:"entries"`
					}
					if json.Unmarshal(data, &document) != nil {
						return false
					}
					if len(document.Entries) == 1 && document.Entries[0].Result != nil && !document.Entries[0].Result.Success {
						t.Fatalf("unexpected durable agent failure: %+v", document.Entries[0].Result)
					}
					return len(document.Entries) == 1 && document.Entries[0].Result != nil && document.Entries[0].Result.Success
				})
				stopAgent()
				select {
				case result := <-cliDone:
					if result.code == 0 || result.code == -1 {
						t.Fatalf("lost result falsely succeeded: %+v", result)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("CLI did not finish")
				}
				fixture.api.Close()
				fixture = startE2EController(t, serverFiles, ledger)
				config.ControllerAddress = fixture.server.Address().String()
				startE2EAgent(t, config)
				e2eEventually(t, "read-only reconciliation after both restarts", func() bool {
					p, ok := e2eOperation(fixture.api.URL, "op-1")
					return ok && p.Result != nil && p.Result.Success && p.Attempts == 1
				})
			} else {
				requireE2ECLI(t, e2eCLI(binary, fixture.api.URL, token, args...), 0)
			}
			requireE2ECLI(t, e2eCLI(binary, fixture.api.URL, token, "--result", "op-1"), 0)
			// Concurrent duplicate CLI callers receive the retained result.
			done := make(chan e2eCLIResult, 4)
			for i := 0; i < 4; i++ {
				go func() { done <- e2eCLI(binary, fixture.api.URL, token, args...) }()
			}
			for i := 0; i < 4; i++ {
				requireE2ECLI(t, <-done, 0)
			}
			requireE2ECLI(t, e2eCLI(binary, fixture.api.URL, token, "-r", "catalog", "-a", "status", "--command-id", "op-1"), 2)
			if runner.calls.Load() != 1 {
				t.Fatalf("side effects=%d want=1", runner.calls.Load())
			}
		})
	}
}
