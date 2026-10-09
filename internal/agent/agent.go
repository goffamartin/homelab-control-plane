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
	State   string   `yaml:"state"`
}

func (config Config) NewDispatcher() (*command.Dispatcher, error) {
	journal, err := command.OpenFileJournal(config.JournalFile, config.JournalRetention)
	if err != nil {
		return nil, err
	}
	dispatcher := command.NewDispatcher(journal)
	for _, resource := range config.Resources {
		if resource.Target == "" {
			return nil, fmt.Errorf("resource %q: target is required", resource.ID)
		}
		resource := resource
		dispatcher.Register(resource.ID, func(action string) protocol.CommandResultPayload {
			if !contains(resource.Actions, action) {
				return protocol.CommandResultPayload{Code: "ACTION_NOT_SUPPORTED", Message: "action is not enabled"}
			}
			switch resource.Adapter {
			case "systemd":
				return (adapter.Systemd{Unit: resource.Target}).Execute(context.Background(), action)
			case "docker", "podman":
				return (adapter.Container{Runtime: resource.Adapter, Name: resource.Target}).Execute(context.Background(), action)
			default:
				return protocol.CommandResultPayload{Code: "INVALID_CONFIGURATION", Message: "unsupported adapter"}
			}
		})
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
	ControllerAddress string           `yaml:"controller_address"`
	ServerName        string           `yaml:"server_name"`
	CertificateFile   string           `yaml:"certificate_file"`
	PrivateKeyFile    string           `yaml:"private_key_file"`
	CAFile            string           `yaml:"ca_file"`
	JournalFile       string           `yaml:"journal_file"`
	JournalRetention  int              `yaml:"journal_retention"`
	NodeID            string           `yaml:"node_id"`
	AgentVersion      string           `yaml:"agent_version"`
	Capabilities      []string         `yaml:"capabilities"`
	Resources         []ResourceConfig `yaml:"resources"`
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
	for _, resource := range config.Resources {
		if err := resource.validate(); err != nil {
			return err
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
		state.Resources = append(state.Resources, protocol.ResourceState{ResourceID: r.ID, State: r.State})
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
	for attempt := 0; ; attempt++ {
		established, err := runSession(ctx, config, tlsConfig, sync, dispatcher)
		if ctx.Err() != nil {
			return nil
		}
		if established {
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
	dialer := net.Dialer{}
	raw, err := dialer.DialContext(ctx, "tcp", config.ControllerAddress)
	if err != nil {
		return false, err
	}
	defer raw.Close()
	connection := tls.Client(raw, tlsConfig)
	if err := connection.HandshakeContext(ctx); err != nil {
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
	commandErrors := make(chan error, 1)
	go func() { commandErrors <- session.RunAgentCommandLoop(connection, writer, dispatcher) }()
	ticker := time.NewTicker(time.Duration(welcome.HeartbeatIntervalMillis) * time.Millisecond)
	defer ticker.Stop()
	var sequence uint64
	for {
		select {
		case <-ctx.Done():
			return true, nil
		case err := <-commandErrors:
			return true, fmt.Errorf("command loop: %w", err)
		case <-ticker.C:
			sequence++
			if err := writer.Send(protocol.MessageHeartbeat, protocol.HeartbeatPayload{Sequence: sequence}); err != nil {
				return true, fmt.Errorf("send heartbeat: %w", err)
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
	return time.Duration(float64(delay) * (0.8 + 0.4*random))
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
