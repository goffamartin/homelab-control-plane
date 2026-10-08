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
	"net"
	"os"
	"time"
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
	dispatcher := command.NewDispatcher()
	for _, resource := range config.Resources {
		if resource.Adapter != "systemd" || resource.Target == "" {
			return nil, fmt.Errorf("resource %q: unsupported adapter configuration", resource.ID)
		}
		resource := resource
		dispatcher.Register(resource.ID, func(action string) protocol.CommandResultPayload {
			if !contains(resource.Actions, action) {
				return protocol.CommandResultPayload{Code: "ACTION_NOT_SUPPORTED", Message: "action is not enabled"}
			}
			return (adapter.Systemd{Unit: resource.Target}).Execute(context.Background(), action)
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
	if err := yaml.Unmarshal(data, &config); err != nil {
		return Config{}, err
	}
	return config, config.Validate()
}
func (config Config) Validate() error {
	if config.ControllerAddress == "" || config.ServerName == "" || config.CertificateFile == "" || config.PrivateKeyFile == "" || config.CAFile == "" {
		return errors.New("agent TLS/controller configuration is required")
	}
	hello := protocol.HelloPayload{ProtocolVersions: []uint8{protocol.ProtocolVersion}, NodeID: config.NodeID, AgentVersion: config.AgentVersion, Capabilities: config.Capabilities}
	if err := hello.Validate(); err != nil {
		return err
	}
	_, err := config.synchronization()
	return err
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
	dialer := net.Dialer{}
	raw, err := dialer.DialContext(ctx, "tcp", config.ControllerAddress)
	if err != nil {
		return err
	}
	defer raw.Close()
	connection := tls.Client(raw, tlsConfig)
	if err := connection.HandshakeContext(ctx); err != nil {
		return err
	}
	defer connection.Close()
	welcome, err := session.Start(connection, config.hello(), 10*time.Second)
	if err != nil {
		return err
	}
	if err := session.SendInitialSynchronization(connection, sync, 10*time.Second); err != nil {
		return err
	}
	dispatcher, err := config.NewDispatcher()
	if err != nil {
		return err
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
			return nil
		case err := <-commandErrors:
			return fmt.Errorf("command loop: %w", err)
		case <-ticker.C:
			sequence++
			if err := writer.Send(protocol.MessageHeartbeat, protocol.HeartbeatPayload{Sequence: sequence}); err != nil {
				return fmt.Errorf("send heartbeat: %w", err)
			}
		}
	}
}
