package main

import (
	"context"
	"flag"
	"fmt"
	"hlcp/internal/agent"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	configPath := flag.String("config", "/etc/hlcp/agent.yaml", "agent YAML configuration")
	checkConfig := flag.Bool("check-config", false, "validate configuration and exit")
	flag.Parse()
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
	if err := agent.Run(ctx, config); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
