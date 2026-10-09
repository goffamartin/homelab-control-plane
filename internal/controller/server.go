package controller

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"hlcp/internal/liveness"
	"hlcp/internal/protocol"
	"hlcp/internal/session"
	"hlcp/internal/transport"
	"log/slog"
	"net"
	"sync"
	"time"
)

type Config struct {
	TLSConfig        *tls.Config
	LivenessPolicy   liveness.Policy
	HandshakeTimeout time.Duration
	Logger           *slog.Logger
}
type Server struct {
	listener net.Listener
	config   Config
	registry *Registry
	sessions *ActiveSessions
}

func Listen(address string, config Config) (*Server, error) {
	if config.TLSConfig == nil {
		return nil, errors.New("hlcp controller: TLS configuration is required")
	}
	if err := config.LivenessPolicy.Validate(); err != nil {
		return nil, err
	}
	if config.HandshakeTimeout <= 0 {
		config.HandshakeTimeout = 10 * time.Second
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", address, err)
	}
	tracker := NewCommandTracker()
	return &Server{listener: listener, config: config, registry: NewRegistry(), sessions: NewActiveSessions(tracker)}, nil
}
func (server *Server) Address() net.Addr         { return server.listener.Addr() }
func (server *Server) Registry() *Registry       { return server.registry }
func (server *Server) Sessions() *ActiveSessions { return server.sessions }
func (server *Server) Close() error              { return server.listener.Close() }

func (server *Server) Serve(ctx context.Context) error {
	var closeOnce sync.Once
	go func() { <-ctx.Done(); closeOnce.Do(func() { _ = server.Close() }) }()
	for {
		rawConnection, err := server.listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept agent connection: %w", err)
		}
		go server.handleConnection(rawConnection)
	}
}

func (server *Server) handleConnection(raw net.Conn) {
	defer raw.Close()
	connection := tls.Server(raw, server.config.TLSConfig)
	if err := connection.SetDeadline(time.Now().Add(server.config.HandshakeTimeout)); err != nil {
		return
	}
	if err := connection.Handshake(); err != nil {
		server.config.Logger.Warn("agent TLS handshake failed", "remote", raw.RemoteAddr(), "error", err)
		return
	}
	_ = connection.SetDeadline(time.Time{})
	state := connection.ConnectionState()
	if state.NegotiatedProtocol != transport.ALPN || len(state.VerifiedChains) == 0 {
		server.config.Logger.Warn("agent TLS peer is not verified for HLCP")
		return
	}
	nodeID, err := transport.AgentNodeID(state.VerifiedChains[0][0])
	if err != nil {
		server.config.Logger.Warn("agent certificate identity failed", "error", err)
		return
	}
	registration, err := session.Accept(connection, session.ControllerConfig{ExpectedNodeID: nodeID, HeartbeatInterval: server.config.LivenessPolicy.HeartbeatInterval, MissedHeartbeatThreshold: server.config.LivenessPolicy.OfflineAfter, HandshakeTimeout: server.config.HandshakeTimeout})
	if err != nil {
		server.config.Logger.Warn("agent registration failed", "error", err)
		return
	}
	synchronization, err := session.ReceiveInitialSynchronization(connection, server.config.HandshakeTimeout)
	if err != nil {
		server.config.Logger.Warn("agent synchronization failed", "node", registration.NodeID, "error", err)
		return
	}
	registeredAt := time.Now()
	server.registry.Register(registration, synchronization, registeredAt)
	tracker, _ := liveness.NewTracker(server.config.LivenessPolicy, registeredAt)
	writer := session.NewWriter(connection, 4)
	server.sessions.Register(registration.NodeID, writer)
	defer server.sessions.Remove(registration.NodeID, writer)
	inbound := session.StartInbound(connection)
	server.config.Logger.Info("agent registered", "node", registration.NodeID, "session", registration.SessionID)
	err = session.RunInboundLoop(inbound, tracker, func(change session.StateChange) {
		server.registry.UpdateLiveness(registration.NodeID, registration.SessionID, change.State, change.LastActivity)
	}, func(result protocol.CommandResultPayload) { server.sessions.HandleResult(result) })
	server.registry.UpdateLiveness(registration.NodeID, registration.SessionID, liveness.NodeOffline, time.Now())
	server.config.Logger.Info("agent session ended", "node", registration.NodeID, "session", registration.SessionID, "error", err)
}
