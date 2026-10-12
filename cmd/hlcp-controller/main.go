package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"hlcp/internal/buildinfo"
	"hlcp/internal/controller"
	"hlcp/internal/liveness"
	"hlcp/internal/localapi"
	"hlcp/internal/telemetry"
	"hlcp/internal/transport"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	version := flag.Bool("version", false, "print build metadata and exit")
	listen := flag.String("listen", ":9443", "agent TCP address")
	apiListen := flag.String("api-listen", "127.0.0.1:9444", "local controller API address")
	apiTokenFile := flag.String("api-token-file", localapi.DefaultTokenFile, "private local API bearer token file")
	apiPolicyFile := flag.String("api-policy-file", "", "private scoped API policy JSON (exclusive with explicit api-token-file)")
	certificate := flag.String("cert", "", "controller certificate PEM")
	privateKey := flag.String("key", "", "controller private key PEM")
	ca := flag.String("ca", "", "trusted agent CA bundle PEM")
	interval := flag.Duration("heartbeat-interval", 5*time.Second, "agent heartbeat interval")
	degraded := flag.Uint("degraded-after", 2, "missed intervals before degraded")
	offline := flag.Uint("offline-after", 3, "missed intervals before offline")
	retryDefaults := controller.DefaultRetryPolicy()
	ackTimeout := flag.Duration("command-ack-timeout", retryDefaults.ACKTimeout, "maximum time for each command ACK attempt")
	resultTimeout := flag.Duration("command-result-timeout", retryDefaults.ResultTimeout, "maximum command execution/result wait")
	maxAttempts := flag.Int("command-max-attempts", retryDefaults.MaxAttempts, "maximum delivery attempts before ACK")
	ledgerFile := flag.String("ledger-file", "/var/lib/hlcp/controller-operations.json", "durable controller operation ledger")
	stateMaxAge := flag.Duration("resource-state-max-age", time.Minute, "maximum age of a received resource observation")
	flag.Parse()
	if *version {
		fmt.Println(buildinfo.String())
		return
	}
	if *stateMaxAge < time.Second || *stateMaxAge > 24*time.Hour {
		fmt.Fprintln(os.Stderr, "resource-state-max-age must be 1s..24h")
		os.Exit(2)
	}
	if *ledgerFile == "" {
		fmt.Fprintln(os.Stderr, "ledger-file is required")
		os.Exit(2)
	}
	commandRetry := controller.RetryPolicy{ACKTimeout: *ackTimeout, ResultTimeout: *resultTimeout, MaxAttempts: *maxAttempts}
	if err := commandRetry.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := localapi.ValidateAddress(*apiListen); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	explicitToken := false
	flag.Visit(func(value *flag.Flag) {
		if value.Name == "api-token-file" {
			explicitToken = true
		}
	})
	authorizer, err := loadAPIAuthorizer(*apiPolicyFile, *apiTokenFile, explicitToken)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if *degraded > 255 || *offline > 255 {
		fmt.Fprintln(os.Stderr, "thresholds must not exceed 255")
		os.Exit(2)
	}
	tlsConfig, err := transport.ServerTLSConfig(transport.TLSFiles{CertificateFile: *certificate, PrivateKeyFile: *privateKey, CAFile: *ca})
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid TLS configuration: %v\n", err)
		os.Exit(2)
	}
	diagnostics := telemetry.New(telemetry.JSONSink(os.Stderr))
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = diagnostics.Close(ctx)
	}()
	server, err := controller.Listen(*listen, controller.Config{TLSConfig: tlsConfig, LivenessPolicy: liveness.Policy{HeartbeatInterval: *interval, DegradedAfter: uint8(*degraded), OfflineAfter: uint8(*offline)}, CommandRetry: commandRetry, LedgerFile: *ledgerFile, ResourceStateMaxAge: *stateMaxAge, Telemetry: diagnostics})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot start controller: %v\n", err)
		os.Exit(1)
	}
	defer server.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *apiPolicyFile != "" {
		hup := make(chan os.Signal, 1)
		signal.Notify(hup, syscall.SIGHUP)
		reloadContext, cancelReload := context.WithCancel(ctx)
		reloadDone := make(chan struct{})
		go func() {
			defer close(reloadDone)
			runPolicyReloads(reloadContext, hup, authorizer, *apiPolicyFile, diagnostics)
		}()
		defer func() { signal.Stop(hup); cancelReload(); <-reloadDone }()
	}
	apiListener, err := net.Listen("tcp", *apiListen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot bind local API:", err)
		os.Exit(1)
	}
	defer apiListener.Close()
	api := &http.Server{Addr: *apiListen, Handler: server.HTTPHandlerWithAuthorizer(authorizer), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: *resultTimeout + 5*time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 8192}
	fmt.Printf("HLCP controller listening on %s (%s)\n", server.Address(), transport.ALPN)
	fmt.Printf("HLCP local API listening on %s\n", *apiListen)
	if err := serveProcesses(ctx, server, api, apiListener); err != nil {
		fmt.Fprintf(os.Stderr, "controller failed: %v\n", err)
		os.Exit(1)
	}
}

func loadAPIAuthorizer(policyFile, tokenFile string, explicitToken bool) (*localapi.Authorizer, error) {
	if policyFile != "" {
		if explicitToken {
			return nil, errors.New("api-policy-file and api-token-file cannot be combined")
		}
		p, err := localapi.LoadPolicy(policyFile)
		if err != nil {
			return nil, err
		}
		return localapi.NewAuthorizer(p), nil
	}
	token, err := localapi.LoadToken(tokenFile)
	if err != nil {
		return nil, err
	}
	return localapi.LegacyAuthorizer(token), nil
}

func runPolicyReloads(ctx context.Context, signals <-chan os.Signal, authorizer *localapi.Authorizer, path string, diagnostics *telemetry.Collector) {
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-signals:
			if !ok {
				return
			}
			code := "POLICY_RELOADED"
			if authorizer.Reload(path) != nil {
				code = "POLICY_RELOAD_REJECTED"
			}
			diagnostics.Record(telemetry.Event{Kind: telemetry.PolicyReload, ActorID: "controller", Code: code})
		}
	}
}

func serveProcesses(parent context.Context, server *controller.Server, api *http.Server, listener net.Listener) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	done := make(chan error, 2)
	go func() { done <- server.Serve(ctx) }()
	go func() { done <- api.Serve(listener) }()
	var failure error
	completed := 0
	select {
	case <-parent.Done():
	case err := <-done:
		completed++
		if parent.Err() == nil {
			failure = err
			if failure == nil || errors.Is(failure, http.ErrServerClosed) {
				failure = errors.New("controller service stopped unexpectedly")
			}
		}
	}
	cancel()
	_ = server.Close()
	shutdownContext, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := api.Shutdown(shutdownContext); err != nil {
		_ = api.Close()
		if failure == nil {
			failure = err
		}
	}
	for completed < 2 {
		<-done
		completed++
	}
	return failure
}
