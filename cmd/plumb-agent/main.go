// Command plumb-agent: see docs/ARCHITECTURE.md. This file parses arguments only.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Bernotese/plumb/internal/agent"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "version" {
		fmt.Fprintln(stdout, "plumb-agent", version)
		return 0
	}
	if len(args) >= 1 && args[0] == "run" {
		fs := flag.NewFlagSet("run", flag.ContinueOnError)
		fs.SetOutput(stderr)
		config := fs.String("config", agent.DefaultConfigPath, "agent configuration file")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		return serve(*config, stderr)
	}
	fmt.Fprintln(stderr, "usage: plumb-agent version | plumb-agent run [-config path]")
	return 2
}

func serve(config string, stderr io.Writer) int {
	log := slog.New(slog.NewTextHandler(stderr, nil))
	cfg, err := agent.LoadConfig(config)
	if err != nil {
		log.Error("config", "err", err)
		return 1
	}
	a, err := agent.New(agent.Options{Config: cfg, Version: version, Log: log})
	if err != nil {
		log.Error("start", "err", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := a.Run(ctx); err != nil && ctx.Err() == nil {
		log.Error("agent stopped", "err", err)
		return 1
	}
	return 0
}
