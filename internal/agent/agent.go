package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"go.yaml.in/yaml/v4"
	"hlcp/internal/adapter"
	"hlcp/internal/command"
	"hlcp/internal/protocol"
	"hlcp/internal/session"
	"hlcp/internal/transport"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"os"
	"strings"
	"time"
)

const (
	initialReconnectDelay = time.Second
	maximumReconnectDelay = 30 * time.Second
)

type ResourceConfig struct {
	ID      string   `yaml:"id"`
	Type    string   `yaml:"type"`
	Adapter string   `yaml:"adapter"`
	Target  string   `yaml:"target"`
	Actions []string `yaml:"actions"`
}

func (config Config) NewDispatcher() (*command.Dispatcher, error) {
	for _, resource := range config.Resources {
		if err := resource.validate(); err != nil {
			return nil, err
		}
	}
	journal, err := command.OpenNodeJournal(config.JournalFile, config.JournalRetention, config.NodeID)
	if err != nil {
		return nil, err
	}
	dispatcher := command.NewDispatcher(journal)
	for _, resource := range config.Resources {
		if resource.Target == "" {
			return nil, fmt.Errorf("resource %q: target is required", resource.ID)
		}
		resource := resource
		dispatcher.Register(resource.ID, func(executionContext context.Context, action string) protocol.CommandResultPayload {
			defer config.requestRefresh()
			if !contains(resource.Actions, action) {
				return protocol.CommandResultPayload{Code: "ACTION_NOT_SUPPORTED", Message: "action is not enabled"}
			}
			switch resource.Adapter {
			case "systemd":
				return (adapter.Systemd{Unit: resource.Target, Runner: config.runner}).Execute(executionContext, action)
			case "docker", "podman":
				return (adapter.Container{Runtime: resource.Adapter, Name: resource.Target, Runner: config.runner}).Execute(executionContext, action)
			default:
				return protocol.CommandResultPayload{Code: "INVALID_CONFIGURATION", Message: "unsupported adapter"}
			}
		}, resource.Actions...)
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
	ControllerAddress   string           `yaml:"controller_address"`
	ServerName          string           `yaml:"server_name"`
	CertificateFile     string           `yaml:"certificate_file"`
	PrivateKeyFile      string           `yaml:"private_key_file"`
	CAFile              string           `yaml:"ca_file"`
	JournalFile         string           `yaml:"journal_file"`
	JournalRetention    int              `yaml:"journal_retention"`
	NodeID              string           `yaml:"node_id"`
	AgentVersion        string           `yaml:"agent_version"`
	Capabilities        []string         `yaml:"capabilities"`
	Resources           []ResourceConfig `yaml:"resources"`
	ObservationInterval time.Duration    `yaml:"observation_interval"`
	runner              adapter.CommandRunner
	refresh             chan struct{}
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
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
	}
	_, err := config.synchronization()
	return err
}

func (resource ResourceConfig) validate() error {
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
	for _, action := range resource.Actions {
		switch action {
		case "status", "start", "stop", "restart":
		default:
			return fmt.Errorf("resource %q: unsupported action %q", resource.ID, action)
		}
	}
	return nil
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
	config.refresh = make(chan struct{}, 1)
	tlsConfig, err := transport.ClientTLSConfig(transport.TLSFiles{CertificateFile: config.CertificateFile, PrivateKeyFile: config.PrivateKeyFile, CAFile: config.CAFile, ServerName: config.ServerName})
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
		stable, err := runSession(ctx, config, tlsConfig, sync, dispatcher)
		if ctx.Err() != nil {
			return nil
		}
		if stable {
			attempt = 0
		}
		delay := reconnectDelay(attempt, rand.Float64())
		slog.Warn("agent session ended; reconnecting", "error", err, "delay", delay)
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
	connection := tls.Client(raw, tlsConfig)
	handshakeContext, cancelHandshake := context.WithTimeout(ctx, 10*time.Second)
	err = connection.HandshakeContext(handshakeContext)
	cancelHandshake()
	if err != nil {
		return false, err
	}
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
