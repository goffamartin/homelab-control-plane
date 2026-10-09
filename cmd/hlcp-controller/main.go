package main

import (
	"context"
	"flag"
	"fmt"
	"hlcp/internal/controller"
	"hlcp/internal/liveness"
	"hlcp/internal/localapi"
	"hlcp/internal/transport"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	listen := flag.String("listen", ":9443", "agent TCP address")
	apiListen := flag.String("api-listen", "127.0.0.1:9444", "local controller API address")
	apiTokenFile := flag.String("api-token-file", localapi.DefaultTokenFile, "private local API bearer token file")
	certificate := flag.String("cert", "", "controller certificate PEM")
	privateKey := flag.String("key", "", "controller private key PEM")
	ca := flag.String("ca", "", "trusted agent CA bundle PEM")
	interval := flag.Duration("heartbeat-interval", 5*time.Second, "agent heartbeat interval")
	degraded := flag.Uint("degraded-after", 1, "missed intervals before degraded")
	offline := flag.Uint("offline-after", 3, "missed intervals before offline")
	flag.Parse()
	if err := localapi.ValidateAddress(*apiListen); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	apiToken, err := localapi.LoadToken(*apiTokenFile)
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
	server, err := controller.Listen(*listen, controller.Config{TLSConfig: tlsConfig, LivenessPolicy: liveness.Policy{HeartbeatInterval: *interval, DegradedAfter: uint8(*degraded), OfflineAfter: uint8(*offline)}})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot start controller: %v\n", err)
		os.Exit(1)
	}
	defer server.Close()
	context, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	apiListener, err := net.Listen("tcp", *apiListen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot bind local API:", err)
		os.Exit(1)
	}
	defer apiListener.Close()
	api := &http.Server{Addr: *apiListen, Handler: server.HTTPHandler(apiToken), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 8192}
	go func() { <-context.Done(); _ = api.Shutdown(context) }()
	go func() {
		if err := api.Serve(apiListener); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "controller API failed: %v\n", err)
			stop()
		}
	}()
	fmt.Printf("HLCP controller listening on %s (%s)\n", server.Address(), transport.ALPN)
	fmt.Printf("HLCP local API listening on %s\n", *apiListen)
	if err := server.Serve(context); err != nil {
		fmt.Fprintf(os.Stderr, "controller failed: %v\n", err)
		os.Exit(1)
	}
}
