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
	MaxConnections   int
	CommandRetry     RetryPolicy
	LedgerFile       string
}
type Server struct {
	listener        net.Listener
	config          Config
	registry        *Registry
	sessions        *ActiveSessions
	connectionMutex sync.Mutex
	connections     map[net.Conn]struct{}
	closed          bool
	workers         sync.WaitGroup
}

func Listen(address string, config Config) (*Server, error) {
	if config.TLSConfig == nil {
		return nil, errors.New("hlcp controller: TLS configuration is required")
	}
	if err := config.LivenessPolicy.Validate(); err != nil {
		return nil, err
	}
	if config.CommandRetry == (RetryPolicy{}) {
		config.CommandRetry = DefaultRetryPolicy()
	}
	if err := config.CommandRetry.Validate(); err != nil {
		return nil, err
	}
	if config.HandshakeTimeout <= 0 {
		config.HandshakeTimeout = 10 * time.Second
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.MaxConnections < 0 {
		return nil, errors.New("max connections must not be negative")
	}
	if config.MaxConnections == 0 {
		config.MaxConnections = 128
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", address, err)
	}
	tracker := NewCommandTracker()
	if config.LedgerFile != "" {
		tracker, err = OpenCommandTracker(config.LedgerFile)
		if err != nil {
			listener.Close()
			return nil, err
		}
	}
	sessions := NewActiveSessions(tracker)
	sessions.policy = config.CommandRetry
	return &Server{listener: listener, config: config, registry: sessions.registry, sessions: sessions, connections: make(map[net.Conn]struct{})}, nil
}
func (server *Server) Address() net.Addr         { return server.listener.Addr() }
func (server *Server) Registry() *Registry       { return server.registry }
func (server *Server) Sessions() *ActiveSessions { return server.sessions }
func (server *Server) Close() error {
	server.connectionMutex.Lock()
	server.closed = true
	err := server.listener.Close()
	for connection := range server.connections {
		_ = connection.Close()
	}
	server.connectionMutex.Unlock()
	server.workers.Wait()
	ledgerErr := server.sessions.tracker.Close()
	if errors.Is(err, net.ErrClosed) {
		return ledgerErr
	}
	if err == nil {
		return ledgerErr
	}
	return err
}

func (server *Server) Serve(ctx context.Context) error {
	stop := context.AfterFunc(ctx, func() { _ = server.Close() })
	defer stop()
	defer server.Close()
	for {
		rawConnection, err := server.listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept agent connection: %w", err)
		}
		server.connectionMutex.Lock()
		if server.closed || len(server.connections) >= server.config.MaxConnections {
			server.connectionMutex.Unlock()
			_ = rawConnection.Close()
			continue
		}
		server.connections[rawConnection] = struct{}{}
		server.workers.Add(1)
		server.connectionMutex.Unlock()
		go func() {
			defer server.workers.Done()
			defer func() {
				server.connectionMutex.Lock()
				delete(server.connections, rawConnection)
				server.connectionMutex.Unlock()
			}()
			server.handleConnection(ctx, rawConnection)
		}()
	}
}

func (server *Server) handleConnection(parent context.Context, raw net.Conn) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
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
	tracker, _ := liveness.NewTracker(server.config.LivenessPolicy, registeredAt)
	writer := session.NewWriter(connection, 4)
	server.sessions.Publish(registration, synchronization, registeredAt, writer, cancel)
	defer server.sessions.Remove(registration.NodeID, writer)
	reconcileDone := make(chan struct{})
	go func() { defer close(reconcileDone); server.sessions.Reconcile(ctx, registration.NodeID, writer) }()
	defer func() { cancel(); <-reconcileDone }()
	inbound := session.StartInbound(ctx, connection)
	defer func() { cancel(); _ = raw.Close(); <-inbound.Done }()
	server.config.Logger.Info("agent registered", "node", registration.NodeID, "session", registration.SessionID)
	err = session.RunInboundLoop(ctx, inbound, tracker, func(change session.StateChange) {
		server.registry.UpdateLiveness(registration.NodeID, registration.SessionID, change.State, change.LastActivity)
	}, func(ack protocol.CommandACKPayload) bool {
		return server.sessions.HandleAck(registration.NodeID, writer, ack)
	}, func(result protocol.CommandResultPayload) bool {
		return server.sessions.HandleResult(registration.NodeID, writer, result)
	}, func(state protocol.CommandStatePayload) bool {
		return server.sessions.HandleCommandState(registration.NodeID, writer, state)
	})
	server.config.Logger.Info("agent session ended", "node", registration.NodeID, "session", registration.SessionID, "error", err)
}
