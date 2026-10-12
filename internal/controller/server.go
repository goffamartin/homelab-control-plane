package controller

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"hlcp/internal/liveness"
	"hlcp/internal/protocol"
	"hlcp/internal/session"
	"hlcp/internal/telemetry"
	"hlcp/internal/transport"
	"log/slog"
	"net"
	"sync"
	"time"
)

type Config struct {
	TLSConfig           *tls.Config
	LivenessPolicy      liveness.Policy
	HandshakeTimeout    time.Duration
	Logger              *slog.Logger
	Telemetry           *telemetry.Collector
	MaxConnections      int
	CommandRetry        RetryPolicy
	LedgerFile          string
	ResourceStateMaxAge time.Duration
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
	ownTelemetry    bool
	diagnosticsOnce sync.Once
	diagnostics     *diagnostics
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
	if config.ResourceStateMaxAge == 0 {
		config.ResourceStateMaxAge = time.Minute
	}
	if config.ResourceStateMaxAge < time.Second || config.ResourceStateMaxAge > 24*time.Hour {
		return nil, errors.New("resource state max age must be 1s..24h")
	}
	if config.HandshakeTimeout <= 0 {
		config.HandshakeTimeout = 10 * time.Second
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
	ownTelemetry := config.Telemetry == nil
	if ownTelemetry {
		var sink telemetry.Sink
		if config.Logger != nil {
			sink = func(ctx context.Context, e telemetry.Event) error {
				config.Logger.LogAttrs(ctx, slog.LevelInfo, "HLCP audit", slog.String("event", e.Name), slog.String("actor_id", e.ActorID), slog.String("node_id", e.NodeID), slog.String("resource_id", e.ResourceID), slog.String("command_id", e.CommandID), slog.String("session_id", e.SessionID), slog.String("action", e.Action), slog.String("code", e.Code), slog.Int("attempt", e.Attempt), slog.Int64("duration_ms", e.DurationMillis))
				return nil
			}
		}
		config.Telemetry = telemetry.New(sink)
	}
	tracker.telemetry = config.Telemetry
	tracker.updateGaugesLocked()
	config.Telemetry.SetGauge(telemetry.ConnectionLimit, config.MaxConnections)
	sessions.policy = config.CommandRetry
	sessions.registry.stateMaxAge = config.ResourceStateMaxAge
	return &Server{listener: listener, config: config, registry: sessions.registry, sessions: sessions, connections: make(map[net.Conn]struct{}), ownTelemetry: ownTelemetry}, nil
}
func (server *Server) Telemetry() *telemetry.Collector { return server.config.Telemetry }
func (server *Server) Address() net.Addr               { return server.listener.Addr() }
func (server *Server) Registry() *Registry             { return server.registry }
func (server *Server) Sessions() *ActiveSessions       { return server.sessions }
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
	if server.ownTelemetry {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = server.config.Telemetry.Close(ctx)
		cancel()
	}
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
			server.config.Telemetry.Record(telemetry.Event{Kind: telemetry.ConnectionRejected, Code: "CONNECTION_CAPACITY"})
			_ = rawConnection.Close()
			continue
		}
		server.connections[rawConnection] = struct{}{}
		server.config.Telemetry.SetGauge(telemetry.Connections, len(server.connections))
		server.workers.Add(1)
		server.connectionMutex.Unlock()
		go func() {
			defer server.workers.Done()
			defer func() {
				server.connectionMutex.Lock()
				delete(server.connections, rawConnection)
				server.config.Telemetry.SetGauge(telemetry.Connections, len(server.connections))
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
	tlsConnection := tls.Server(raw, server.config.TLSConfig)
	if err := tlsConnection.SetDeadline(time.Now().Add(server.config.HandshakeTimeout)); err != nil {
		return
	}
	if err := tlsConnection.Handshake(); err != nil {
		server.config.Telemetry.Record(telemetry.Event{Kind: telemetry.ConnectionRejected, Code: "TLS_HANDSHAKE"})
		return
	}
	_ = tlsConnection.SetDeadline(time.Time{})
	state := tlsConnection.ConnectionState()
	if state.NegotiatedProtocol != transport.ALPN || len(state.VerifiedChains) == 0 {
		server.config.Telemetry.Record(telemetry.Event{Kind: telemetry.ConnectionRejected, Code: "TLS_PEER"})
		return
	}
	nodeID, err := transport.AgentNodeID(state.VerifiedChains[0][0])
	if err != nil {
		server.config.Telemetry.Record(telemetry.Event{Kind: telemetry.ConnectionRejected, Code: "TLS_IDENTITY"})
		return
	}
	server.config.Telemetry.Record(telemetry.Event{Kind: telemetry.Authenticated, ActorID: nodeID, NodeID: nodeID, Code: "MTLS"})
	connection := telemetry.Wrap(tlsConnection, server.config.Telemetry)
	registration, err := session.Accept(connection, session.ControllerConfig{ExpectedNodeID: nodeID, HeartbeatInterval: server.config.LivenessPolicy.HeartbeatInterval, MissedHeartbeatThreshold: server.config.LivenessPolicy.OfflineAfter, HandshakeTimeout: server.config.HandshakeTimeout})
	if err != nil {
		server.config.Telemetry.Record(telemetry.Event{Kind: telemetry.ConnectionRejected, NodeID: nodeID, Code: "REGISTRATION"})
		return
	}
	synchronization, err := session.ReceiveInitialSynchronization(connection, server.config.HandshakeTimeout)
	if err != nil {
		server.config.Telemetry.Record(telemetry.Event{Kind: telemetry.ConnectionRejected, NodeID: nodeID, Code: "SYNCHRONIZATION"})
		return
	}
	registeredAt := time.Now()
	tracker, _ := liveness.NewTracker(server.config.LivenessPolicy, registeredAt)
	writer := session.NewWriter(connection, 4)
	_, previouslyRegistered := server.registry.Get(registration.NodeID)
	server.sessions.Publish(registration, synchronization, registeredAt, writer, cancel)
	if previouslyRegistered {
		server.config.Telemetry.Record(telemetry.Event{Kind: telemetry.Reconnect, NodeID: registration.NodeID, SessionID: registration.SessionID, Code: "RECONNECTED"})
	}
	defer server.sessions.Remove(registration.NodeID, writer)
	reconcileDone := make(chan struct{})
	go func() { defer close(reconcileDone); server.sessions.Reconcile(ctx, registration.NodeID, writer) }()
	defer func() { cancel(); <-reconcileDone }()
	inbound := session.StartInbound(ctx, connection)
	defer func() { cancel(); _ = raw.Close(); <-inbound.Done }()
	server.config.Telemetry.Record(telemetry.Event{Kind: telemetry.SessionStarted, NodeID: registration.NodeID, SessionID: registration.SessionID})
	err = session.RunInboundLoop(ctx, inbound, tracker, func(change session.StateChange) {
		if server.registry.UpdateLiveness(registration.NodeID, registration.SessionID, change.State, change.LastActivity) {
			server.config.Telemetry.Record(telemetry.Event{Kind: telemetry.LivenessChanged, NodeID: registration.NodeID, SessionID: registration.SessionID, Code: string(change.State)})
		}
	}, func(ack protocol.CommandACKPayload) bool {
		return server.sessions.HandleAck(registration.NodeID, writer, ack)
	}, func(result protocol.CommandResultPayload) bool {
		return server.sessions.HandleResult(registration.NodeID, writer, result)
	}, session.InboundHandlers{CommandState: func(state protocol.CommandStatePayload) bool {
		return server.sessions.HandleCommandState(registration.NodeID, writer, state)
	}, StateUpdate: func(update protocol.StateUpdate) bool {
		return server.sessions.HandleStateUpdate(registration.NodeID, writer, update)
	}})
	code := "SESSION_CLOSED"
	if errors.Is(err, session.ErrNodeOffline) {
		code = "LIVENESS_TIMEOUT"
	}
	server.config.Telemetry.Record(telemetry.Event{Kind: telemetry.SessionEnded, NodeID: registration.NodeID, SessionID: registration.SessionID, Code: code, DurationMillis: time.Since(registeredAt).Milliseconds()})
}
