package main

import (
	"context"
	"flag"
	"fmt"
	"hlcp/internal/agent"
	"hlcp/internal/buildinfo"
	"hlcp/internal/doctor"
	"hlcp/internal/onboarding"
	"hlcp/internal/telemetry"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "setup" {
		os.Exit(onboarding.AgentCLI(os.Args[2:], os.Stdin, os.Stdout))
	}
	if len(os.Args) > 1 && os.Args[1] == "doctor" {
		os.Exit(doctor.CLI(os.Args[2:], "agent", os.Stdout))
	}
	configPath := flag.String("config", "/etc/hlcp/agent.yaml", "agent YAML configuration")
	checkConfig := flag.Bool("check-config", false, "validate configuration and exit")
	version := flag.Bool("version", false, "print build metadata and exit")
	flag.Parse()
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
