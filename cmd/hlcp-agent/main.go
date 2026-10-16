package main

import (
	"context"
	"flag"
	"fmt"
	"hlcp/deploy/completion"
	"hlcp/internal/agent"
	"hlcp/internal/buildinfo"
	"hlcp/internal/doctor"
	"hlcp/internal/managedcli"
	"hlcp/internal/onboarding"
	"hlcp/internal/telemetry"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if handled, code := completion.Handle("hlcp-agent", os.Args[1:], os.Stdout); handled {
		os.Exit(code)
	}
	if len(os.Args) > 1 && os.Args[1] == "configure" {
		os.Exit(managedcli.Configure(os.Args[2:], os.Stdin, os.Stdout))
	}
	if len(os.Args) > 1 && os.Args[1] == "check-config" {
		os.Exit(managedcli.CheckConfig(os.Args[2:], os.Stdout))
	}
	if len(os.Args) > 1 && os.Args[1] == "certificates" {
		os.Exit(managedcli.AgentCertificates(os.Args[2:], os.Stdout))
	}
	if len(os.Args) > 1 && os.Args[1] == "resources" {
		os.Exit(managedcli.Resources(os.Args[2:], os.Stdin, os.Stdout))
	}
	if len(os.Args) > 1 && os.Args[1] == "join" {
		os.Exit(managedcli.Join(os.Args[2:], os.Stdin, os.Stdout))
	}
	if len(os.Args) > 1 && os.Args[1] == "prepare-user" {
		os.Exit(managedcli.PrepareUser(os.Args[2:], os.Stdout))
	}
	if len(os.Args) > 1 && os.Args[1] == "--internal-sandbox-inspector" {
		os.Exit(onboarding.SandboxInspectorCLI(os.Args[2:], os.Stdin, os.Stdout))
	}
	if len(os.Args) > 1 && (os.Args[1] == "start" || os.Args[1] == "restart" || os.Args[1] == "stop" || os.Args[1] == "status") {
		os.Exit(onboarding.AgentServiceCLI(os.Args[1], os.Args[2:], os.Stdout))
	}
	if len(os.Args) > 1 && os.Args[1] == "probe-resources" {
		os.Exit(onboarding.SandboxProbeCLI(os.Args[2:], os.Stdout))
	}
	if len(os.Args) > 1 && os.Args[1] == "setup" {
		os.Exit(onboarding.AgentCLI(os.Args[2:], os.Stdin, os.Stdout))
	}
	if len(os.Args) > 1 && os.Args[1] == "doctor" {
		os.Exit(doctor.CLI(os.Args[2:], "agent", os.Stdout))
	}
	configPath := flag.String("config", "/etc/hlcp/agent.yaml", "agent YAML configuration")
	checkConfig := flag.Bool("check-config", false, "validate configuration and exit")
	version := flag.Bool("version", false, "print build metadata and exit")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: hlcp-agent join|configure|check-config|resources|certificates|prepare-user|setup|start|restart|stop|status|probe-resources|doctor|completion bash [options]\nRuntime options:")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Unknown agent command. Use start, restart, stop, status or --help; activate has been removed.")
		os.Exit(2)
	}
	if *version {
		fmt.Println(buildinfo.String())
		return
	}
	config, err := agent.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if *checkConfig {
		fmt.Println("configuration valid")
		return
	}
	// Installed binary metadata, not an old provisioning label, describes the
	// running CLI agent after an upgrade. Embedded Go clients retain Config control.
	config.AgentVersion = buildinfo.Version
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	config.Telemetry = telemetry.New(telemetry.JSONSink(os.Stderr))
	defer func() {
		closeContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = config.Telemetry.Close(closeContext)
	}()
	if err := agent.Run(ctx, config); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
