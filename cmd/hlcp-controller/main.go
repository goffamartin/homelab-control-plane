package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"hlcp/deploy/completion"
	"hlcp/internal/buildinfo"
	"hlcp/internal/controller"
	"hlcp/internal/doctor"
	"hlcp/internal/liveness"
	"hlcp/internal/localapi"
	"hlcp/internal/managed"
	"hlcp/internal/managedcli"
	"hlcp/internal/onboarding"
	"hlcp/internal/telemetry"
	"hlcp/internal/transport"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	if handled, code := completion.Handle("hlcp-controller", os.Args[1:], os.Stdout); handled {
		os.Exit(code)
	}
	if len(os.Args) > 1 && os.Args[1] == "pki" {
		os.Exit(managedcli.ControllerPKI(os.Args[2:], os.Stdin, os.Stdout))
	}
	if len(os.Args) > 1 && os.Args[1] == "configure-client" {
		os.Exit(onboarding.ClientGrantCLI(os.Args[2:], os.Stdout))
	}
	if len(os.Args) == 2 && os.Args[1] == "__client-provision" {
		os.Exit(onboarding.ClientProvisionChild(os.Stdin, os.Stdout))
	}
	if len(os.Args) > 1 && os.Args[1] == "activate" {
		os.Exit(onboarding.ControllerActivationCLI(os.Args[2:], os.Stdout))
	}
	if len(os.Args) > 1 && os.Args[1] == "setup" {
		if managedcli.ManagedSetup(os.Args[2:]) {
			os.Exit(managedcli.ControllerSetup(os.Args[2:], os.Stdin, os.Stdout))
		}
		os.Exit(onboarding.ControllerCLI(managedcli.WithoutImported(os.Args[2:]), os.Stdin, os.Stdout))
	}
	if len(os.Args) > 1 && os.Args[1] == "doctor" {
		os.Exit(doctor.CLI(os.Args[2:], "controller", os.Stdout))
	}
	version := flag.Bool("version", false, "print build metadata and exit")
	managedState := flag.String("managed-state", os.Getenv("HLCP_MANAGED_STATE"), "private managed controller state; no fallback on invalid state")
	checkConfig := flag.Bool("check-config", false, "validate configuration without binding sockets or opening the ledger")
	nodesFile := flag.String("node-allowlist-file", "", "startup JSON array of admitted certificate node identities; restart to apply")
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
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: hlcp-controller setup|pki|configure-client|activate|doctor|completion bash [options]\nManaged setup is the default; setup --imported retains external PKI.\nRuntime options:")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *version {
		fmt.Println(buildinfo.String())
		return
	}
	if *managedState == "" {
		if _, e := os.Lstat("/var/lib/hlcp-controller/managed"); e == nil || !os.IsNotExist(e) {
			fmt.Fprintln(os.Stderr, "managed state exists or is inaccessible; refusing imported-mode fallback without explicit managed-state")
			os.Exit(2)
		}
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
	policy := liveness.Policy{HeartbeatInterval: *interval, DegradedAfter: uint8(*degraded), OfflineAfter: uint8(*offline)}
	if err := policy.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	_, port, addressErr := net.SplitHostPort(*listen)
	portNumber, portErr := strconv.Atoi(port)
	if addressErr != nil || portErr != nil || portNumber < 0 || portNumber > 65535 {
		fmt.Fprintln(os.Stderr, "invalid agent listen address")
		os.Exit(2)
	}
	var allowedNodes map[string]bool
	if *nodesFile != "" && *managedState == "" {
		allowedNodes, err = transport.LoadAllowedNodes(*nodesFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	var tlsConfig *tls.Config
	var managedRuntime *managed.Runtime
	var legacyExpires time.Time
	if *managedState != "" {
		managedRuntime, err = managed.OpenRuntime(*managedState)
		if err == nil {
			defer managedRuntime.Store.Close()
			if managedRuntime.Config.Listen != *listen {
				fmt.Fprintln(os.Stderr, "managed listener/config mismatch")
				os.Exit(2)
			}
			tlsConfig, err = managedRuntime.Identity.TLSConfig()
			if err == nil && managedRuntime.Config.LegacyServer {
				chain, e1 := os.ReadFile(*certificate)
				key, e2 := os.ReadFile(*privateKey)
				if e1 != nil || e2 != nil || managed.VerifyLegacyServer(chain, key, []byte(managedRuntime.Config.LegacyTrust), managedRuntime.Config.ServerName, time.Now()) != nil {
					err = managed.ErrPKI
				} else {
					pair, e := tls.X509KeyPair(chain, key)
					err = e
					if err == nil {
						leaf, e := x509.ParseCertificate(pair.Certificate[0])
						if e != nil {
							fmt.Fprintln(os.Stderr, "invalid legacy server leaf")
							os.Exit(2)
						}
						legacyExpires = leaf.NotAfter
						roots := append(managedRuntime.Authority.RootPEM(), []byte(managedRuntime.Config.LegacyTrust)...)
						tlsConfig, err = transport.ServerTLSConfigDynamic(roots, func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &pair, nil })
					}
				}
			}
		}
	} else {
		tlsConfig, err = transport.ServerTLSConfig(transport.TLSFiles{CertificateFile: *certificate, PrivateKeyFile: *privateKey, CAFile: *ca})
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid TLS configuration: %v\n", err)
		os.Exit(2)
	}
	if *checkConfig {
		fmt.Println("configuration valid (listener availability and ledger contents not checked)")
		return
	}
	if managedRuntime != nil {
		if err := managedRuntime.Identity.Renew(context.Background(), time.Now()); err != nil {
			fmt.Fprintln(os.Stderr, "managed server certificate renewal failed")
			os.Exit(2)
		}
	}
	diagnostics := telemetry.New(telemetry.JSONSink(os.Stderr))
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = diagnostics.Close(ctx)
	}()
	serverConfig := controller.Config{TLSConfig: tlsConfig, LivenessPolicy: policy, AllowedNodeIDs: allowedNodes, CommandRetry: commandRetry, LedgerFile: *ledgerFile, ResourceStateMaxAge: *stateMaxAge, Telemetry: diagnostics}
	if managedRuntime != nil {
		serverConfig.ManagedRegistry = managedRuntime.Registry
		serverConfig.EnrollmentURL = managedRuntime.Config.EnrollmentURL
		serverConfig.CertificateStatus = func() managed.CertificateStatus {
			s := managedRuntime.Identity.Status(time.Now())
			s.Migration = managedRuntime.Config.LegacyServer
			s.LegacyExpires = legacyExpires
			return s
		}
	}
	server, err := controller.Listen(*listen, serverConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot start controller: %v\n", err)
		os.Exit(1)
	}
	defer server.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var extra []httpService
	if managedRuntime != nil {
		service, e := managed.NewEnrollmentService(managedRuntime.Registry, managedRuntime.Config.Listen, managedRuntime.Config.ServerName)
		if e != nil {
			fmt.Fprintln(os.Stderr, "managed enrollment configuration invalid")
			os.Exit(2)
		}
		if managedRuntime.Config.LegacyServer {
			service.PeerCA = append(managedRuntime.Authority.RootPEM(), []byte(managedRuntime.Config.LegacyTrust)...)
		}
		enrollmentTLS, e := managedRuntime.Identity.EnrollmentTLSConfig()
		if e != nil {
			fmt.Fprintln(os.Stderr, "managed enrollment TLS invalid")
			os.Exit(2)
		}
		listener, e := net.Listen("tcp", managedRuntime.Config.EnrollmentListen)
		if e != nil {
			fmt.Fprintln(os.Stderr, "cannot bind enrollment listener")
			os.Exit(1)
		}
		defer listener.Close()
		https := &http.Server{Handler: service, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 8192}
		extra = append(extra, httpService{https, tls.NewListener(listener, enrollmentTLS)})
		worker, stopWorker := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { defer close(done); managedRuntime.Identity.Maintain(worker, managed.StatusReporter(os.Stderr)) }()
		defer func() { stopWorker(); <-done }()
	}
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
	if err := serveProcesses(ctx, server, api, apiListener, extra...); err != nil {
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

type httpService struct {
	server   *http.Server
	listener net.Listener
}

func serveProcesses(parent context.Context, server *controller.Server, api *http.Server, listener net.Listener, extra ...httpService) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	total := 2 + len(extra)
	done := make(chan error, total)
	go func() { done <- server.Serve(ctx) }()
	go func() { done <- api.Serve(listener) }()
	for _, service := range extra {
		go func(service httpService) { done <- service.server.Serve(service.listener) }(service)
	}
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
	for _, service := range extra {
		if err := service.server.Shutdown(shutdownContext); err != nil {
			_ = service.server.Close()
			if failure == nil {
				failure = err
			}
		}
	}
	for completed < total {
		<-done
		completed++
	}
	return failure
}
