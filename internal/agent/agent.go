package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"go.yaml.in/yaml/v4"
	"hlcp/internal/adapter"
	"hlcp/internal/command"
	"hlcp/internal/managed"
	"hlcp/internal/protocol"
	"hlcp/internal/session"
	"hlcp/internal/telemetry"
	"hlcp/internal/transport"
	"io"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	initialReconnectDelay = time.Second
	maximumReconnectDelay = 30 * time.Second
)

type ResourceConfig struct {
	ID       string                         `yaml:"id"`
	Type     string                         `yaml:"type"`
	Adapter  string                         `yaml:"adapter"`
	Target   string                         `yaml:"target"`
	Actions  []string                       `yaml:"actions"`
	Commands map[string]ScriptCommandConfig `yaml:"commands,omitempty"`
}

func (config Config) NewDispatcher() (*command.Dispatcher, error) {
	for _, resource := range config.Resources {
		if err := resource.validate(); err != nil {
			return nil, err
		}
		if len(resource.Commands) > 0 && runtime.GOOS != "linux" {
			return nil, errors.New("script execution is supported only on Linux")
		}
	}
	journal, err := command.OpenNodeJournal(config.JournalFile, config.JournalRetention, config.NodeID)
	if err != nil {
		return nil, err
	}
	dispatcher := command.NewObservedDispatcher(journal, config.Telemetry, config.NodeID)
	for _, resource := range config.Resources {
		commands := make(map[string]adapter.ScriptCommand, len(resource.Commands))
		for action, spec := range resource.Commands {
			environment := make(map[string]string, len(spec.Environment))
			for name, value := range spec.Environment {
				environment[name] = value
			}
			commands[action] = adapter.ScriptCommand{Executable: spec.Executable, Argv: append([]string(nil), spec.Argv...), WorkingDirectory: spec.WorkingDirectory, Environment: environment, Timeout: spec.ExecutionTimeout()}
		}
		scripts := adapter.Script{Commands: commands}
		resource := resource
		execute := func(executionContext context.Context, action string) protocol.CommandResultPayload {
			defer config.requestRefresh()
			if !contains(resource.Actions, action) {
				return protocol.CommandResultPayload{Code: "ACTION_NOT_SUPPORTED", Message: "action is not enabled"}
			}
			if _, custom := scripts.Commands[action]; custom {
				return scripts.Execute(executionContext, action)
			}
			switch resource.Adapter {
			case "systemd":
				return (adapter.Systemd{Unit: resource.Target, Runner: config.runner}).Execute(executionContext, action)
			case "docker", "podman":
				return (adapter.Container{Runtime: resource.Adapter, Name: resource.Target, Runner: config.runner}).Execute(executionContext, action)
			default:
				return protocol.CommandResultPayload{Code: "INVALID_CONFIGURATION", Message: "unsupported adapter"}
			}
		}
		// Only native status is read-only. Script-only resources may call a
		// mutating command "status"; mixed resources reserve all native names.
		if resource.Adapter == "script" {
			dispatcher.RegisterMutating(resource.ID, execute, resource.Actions...)
		} else {
			dispatcher.Register(resource.ID, execute, resource.Actions...)
		}
	}
	return dispatcher, nil
}
func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

type Config struct {
	ControllerAddress   string               `yaml:"controller_address"`
	ServerName          string               `yaml:"server_name"`
	ManagedDirectory    string               `yaml:"managed_directory,omitempty"`
	CertificateFile     string               `yaml:"certificate_file"`
	PrivateKeyFile      string               `yaml:"private_key_file"`
	CAFile              string               `yaml:"ca_file"`
	JournalFile         string               `yaml:"journal_file"`
	JournalRetention    int                  `yaml:"journal_retention"`
	NodeID              string               `yaml:"node_id"`
	AgentVersion        string               `yaml:"agent_version"`
	Capabilities        []string             `yaml:"capabilities"`
	Resources           []ResourceConfig     `yaml:"resources"`
	ObservationInterval time.Duration        `yaml:"observation_interval"`
	Telemetry           *telemetry.Collector `yaml:"-"`
	runner              adapter.CommandRunner
	refresh             chan struct{}
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return Parse(data)
}

// Parse validates configuration without opening journals, TLS files or resources.
func Parse(data []byte) (Config, error) {
	var config Config
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return Config{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Config{}, errors.New("agent configuration must contain exactly one YAML document")
		}
		return Config{}, err
	}
	return config, config.Validate()
}
func (config Config) Validate() error {
	if config.ManagedDirectory != "" && (!filepath.IsAbs(config.ManagedDirectory) || filepath.Clean(config.ManagedDirectory) != config.ManagedDirectory || len(config.ManagedDirectory) > 4096) {
		return errors.New("managed_directory must be a clean absolute private directory")
	}
	if config.ControllerAddress == "" || config.ServerName == "" || config.CertificateFile == "" || config.PrivateKeyFile == "" || config.CAFile == "" || config.JournalFile == "" {
		return errors.New("agent TLS/controller and command journal configuration is required")
	}
	hello := protocol.HelloPayload{ProtocolVersions: []uint8{protocol.ProtocolVersion}, NodeID: config.NodeID, AgentVersion: config.AgentVersion, Capabilities: config.Capabilities}
	if err := hello.Validate(); err != nil {
		return err
	}
	if config.JournalRetention < 0 || config.JournalRetention > 1_000_000 {
		return errors.New("journal_retention must be between 0 and 1000000")
	}
	if config.ObservationInterval < 0 || config.ObservationInterval > 5*time.Minute || (config.ObservationInterval > 0 && config.ObservationInterval < time.Second) {
		return errors.New("observation_interval must be 1s..5m (zero uses 10s)")
	}
	for _, resource := range config.Resources {
		if err := resource.validate(); err != nil {
			return err
		}
		if !contains(config.Capabilities, "resource.control") || !contains(config.Capabilities, resource.Adapter) {
			return fmt.Errorf("resource %q: capabilities must include resource.control and %s", resource.ID, resource.Adapter)
		}
		if len(resource.Commands) > 0 && !contains(config.Capabilities, "script") {
			return fmt.Errorf("resource %q: custom commands require the script capability", resource.ID)
		}
	}
	_, err := config.synchronization()
	return err
}

func (resource ResourceConfig) validate() error {
	if resource.Adapter == "script" {
		return resource.validateScript()
	}
	if resource.Target == "" || resource.Target != strings.TrimSpace(resource.Target) || strings.HasPrefix(resource.Target, "-") {
		return fmt.Errorf("resource %q: target is invalid", resource.ID)
	}
	for _, character := range resource.Target {
		if character < 0x21 || character > 0x7e {
			return fmt.Errorf("resource %q: target contains unsupported characters", resource.ID)
		}
	}
	switch resource.Adapter {
	case "docker", "podman":
		if resource.Type != "container" {
			return fmt.Errorf("resource %q: %s adapter requires type container", resource.ID, resource.Adapter)
		}
	case "systemd":
		if resource.Type != "service" {
			return fmt.Errorf("resource %q: systemd adapter requires type service", resource.ID)
		}
	default:
		return fmt.Errorf("resource %q: unsupported adapter %q", resource.ID, resource.Adapter)
	}
	return resource.validateCommands()
}
func (config Config) hello() protocol.HelloPayload {
	return protocol.HelloPayload{ProtocolVersions: []uint8{protocol.ProtocolVersion}, NodeID: config.NodeID, AgentVersion: config.AgentVersion, Capabilities: config.Capabilities}
}
func (config Config) synchronization() (session.Synchronization, error) {
	inventory := protocol.InventorySnapshot{Revision: 1}
	state := protocol.StateSnapshot{InventoryRevision: 1}
	for _, r := range config.Resources {
		inventory.Resources = append(inventory.Resources, protocol.ResourceDescriptor{ID: r.ID, Type: r.Type, Actions: r.Actions})
		state.Resources = append(state.Resources, protocol.ResourceState{ResourceID: r.ID, State: "unknown", Code: "NOT_OBSERVED"})
	}
	if err := inventory.Validate(); err != nil {
		return session.Synchronization{}, err
	}
	if err := state.ValidateAgainst(inventory); err != nil {
		return session.Synchronization{}, err
	}
	return session.Synchronization{Inventory: inventory, State: state}, nil
}
func Run(ctx context.Context, config Config) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if config.Telemetry == nil {
		config.Telemetry = telemetry.New(nil)
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = config.Telemetry.Close(ctx)
		}()
	}
	config.refresh = make(chan struct{}, 1)
	var tlsConfig *tls.Config
	var err error
	if config.ManagedDirectory != "" {
		store, e := managed.OpenStore(config.ManagedDirectory)
		if e != nil {
			return e
		}
		defer store.Close()
		identity, e := managed.OpenAgentIdentity(store, config.NodeID)
		if e != nil {
			return e
		}
		tlsConfig, err = identity.TLSConfig(config.ServerName)
		if err == nil {
			worker, stop := context.WithCancel(ctx)
			done := make(chan struct{})
			go func() { defer close(done); identity.Maintain(worker, managed.StatusReporter(os.Stderr)) }()
			defer func() { stop(); <-done }()
		}
	} else {
		tlsConfig, err = transport.ClientTLSConfig(transport.TLSFiles{CertificateFile: config.CertificateFile, PrivateKeyFile: config.PrivateKeyFile, CAFile: config.CAFile, ServerName: config.ServerName})
	}
	if err != nil {
		return err
	}
	sync, err := config.synchronization()
	if err != nil {
		return err
	}
	dispatcher, err := config.NewDispatcher()
	if err != nil {
		return err
	}
	defer dispatcher.Close()
	for attempt := 0; ; attempt++ {
		stable, _ := runSession(ctx, config, tlsConfig, sync, dispatcher)
		if ctx.Err() != nil {
			return nil
		}
		if stable {
			attempt = 0
		}
		delay := reconnectDelay(attempt, rand.Float64())
		config.Telemetry.Record(telemetry.Event{Kind: telemetry.Reconnect, NodeID: config.NodeID, Code: "RECONNECT_SCHEDULED", Attempt: attempt + 1, DurationMillis: delay.Milliseconds()})
		if !waitForReconnect(ctx, delay) {
			return nil
		}
	}
}

func runSession(ctx context.Context, config Config, tlsConfig *tls.Config, synchronization session.Synchronization, dispatcher *command.Dispatcher) (bool, error) {
	dialer := net.Dialer{Timeout: 10 * time.Second}
	raw, err := dialer.DialContext(ctx, "tcp", config.ControllerAddress)
	if err != nil {
		return false, err
	}
	defer raw.Close()
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stop()
	tlsConnection := tls.Client(raw, tlsConfig)
	handshakeContext, cancelHandshake := context.WithTimeout(ctx, 10*time.Second)
	err = tlsConnection.HandshakeContext(handshakeContext)
	cancelHandshake()
	if err != nil {
		config.Telemetry.Record(telemetry.Event{Kind: telemetry.ConnectionRejected, NodeID: config.NodeID, Code: "TLS_HANDSHAKE"})
		return false, err
	}
	if peer := tlsConnection.ConnectionState().PeerCertificates; len(peer) > 0 {
		expiry := time.AfterFunc(time.Until(peer[0].NotAfter), func() { _ = raw.Close() })
		defer expiry.Stop()
	}
	config.Telemetry.Record(telemetry.Event{Kind: telemetry.Authenticated, NodeID: config.NodeID, Code: "MTLS"})
	connection := telemetry.Wrap(tlsConnection, config.Telemetry)
	defer connection.Close()
	welcome, err := session.Start(connection, config.hello(), 10*time.Second)
	if err != nil {
		return false, err
	}
	if err := session.SendInitialSynchronization(connection, synchronization, 10*time.Second); err != nil {
		return false, err
	}
	writer := session.NewWriter(connection, 4)
	observationContext, cancelObservations := context.WithCancel(ctx)
	observationErrors := make(chan error, 1)
	observationDone := make(chan struct{})
	go func() {
		defer close(observationDone)
		observationErrors <- config.runObservations(observationContext, writer, welcome.SessionID, synchronization.Inventory.Revision)
	}()
	defer func() { cancelObservations(); _ = raw.Close(); <-observationDone }()
	readyAt := time.Now()
	config.Telemetry.Record(telemetry.Event{Kind: telemetry.SessionStarted, NodeID: config.NodeID, SessionID: welcome.SessionID})
	defer func() {
		config.Telemetry.Record(telemetry.Event{Kind: telemetry.SessionEnded, NodeID: config.NodeID, SessionID: welcome.SessionID, Code: "SESSION_CLOSED", DurationMillis: time.Since(readyAt).Milliseconds()})
	}()
	stable := func() bool { return time.Since(readyAt) >= time.Minute }
	commandErrors := make(chan error, 1)
	commandDone := make(chan struct{})
	go func() {
		defer close(commandDone)
		commandErrors <- session.RunAgentCommandLoop(ctx, connection, writer, dispatcher)
	}()
	defer func() { _ = raw.Close(); <-commandDone }()
	ticker := time.NewTicker(time.Duration(welcome.HeartbeatIntervalMillis) * time.Millisecond)
	defer ticker.Stop()
	var sequence uint64
	for {
		select {
		case <-ctx.Done():
			return stable(), nil
		case err := <-commandErrors:
			return stable(), fmt.Errorf("command loop: %w", err)
		case err := <-observationErrors:
			return stable(), fmt.Errorf("observation loop: %w", err)
		case <-ticker.C:
			sequence++
			if err := writer.SendContext(ctx, protocol.MessageHeartbeat, protocol.HeartbeatPayload{Sequence: sequence}); err != nil {
				return stable(), fmt.Errorf("send heartbeat: %w", err)
			}
		}
	}
}

func reconnectDelay(attempt int, random float64) time.Duration {
	delay := initialReconnectDelay
	for index := 0; index < attempt && delay < maximumReconnectDelay; index++ {
		delay *= 2
		if delay > maximumReconnectDelay {
			delay = maximumReconnectDelay
		}
	}
	if random < 0 {
		random = 0
	}
	if random > 1 {
		random = 1
	}
	jittered := time.Duration(float64(delay) * (0.8 + 0.4*random))
	if jittered > maximumReconnectDelay {
		return maximumReconnectDelay
	}
	return jittered
}

func waitForReconnect(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
