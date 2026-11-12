// Command plumb-agent: see docs/ARCHITECTURE.md. This file parses arguments only.
package main

import (
	"context"
	"errors"
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
	if len(args) >= 1 && (args[0] == "run" || args[0] == "self-test" || args[0] == "guard") {
		fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
		fs.SetOutput(stderr)
		config := fs.String("config", agent.DefaultConfigPath, "agent configuration file")
		if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
			return 2
		}
		switch args[0] {
		case "self-test":
			// The old agent runs this before it swaps in this binary (D-258).
			if err := agent.SelfTest(*config, version, stdout); err != nil {
				fmt.Fprintln(stderr, "self-test:", err)
				return 1
			}
			return 0
		case "guard":
			return guard(*config, stderr)
		}
		if crashAtStart {
			fmt.Fprintln(stderr, "plumb-agent: test build that exits at start (plumbtest_crash)")
			return 1
		}
		return serve(*config, stderr)
	}
	fmt.Fprintln(stderr, "usage: plumb-agent version | plumb-agent run|self-test|guard [-config path]")
	return 2
}

// guard is one tick of the update guard (D-271), run by
// plumb-agent-guard.service as the previous binary.
func guard(config string, stderr io.Writer) int {
	log := slog.New(slog.NewTextHandler(stderr, nil))
	if err := agent.RunGuard(context.Background(), agent.GuardOptions{ConfigPath: config, Log: log}); err != nil {
		log.Error("update guard", "err", err)
		return 1
	}
	return 0
}

func serve(config string, stderr io.Writer) int {
	log := slog.New(slog.NewTextHandler(stderr, nil))
	cfg, err := agent.LoadConfig(config)
	if err != nil {
		log.Error("config", "err", err)
		return 1
	}
	a, err := agent.New(agent.Options{Config: cfg, ConfigPath: config, Version: version, Log: log})
	if err != nil {
		log.Error("start", "err", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = a.Run(ctx)
	if errors.Is(err, agent.ErrRestart) {
		return 0 // systemd (Restart=always) starts the new binary
	}
	if err != nil && ctx.Err() == nil {
		log.Error("agent stopped", "err", err)
		return 1
	}
	return 0
}
