// Command plumb: see docs/ARCHITECTURE.md. This file parses arguments only.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "version" {
		fmt.Fprintln(stdout, "plumb", version)
		return 0
	}
	if len(args) >= 1 && args[0] == "serve" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return serve(ctx, args[1:], stdout, stderr)
	}
	if len(args) >= 1 && args[0] == "key" {
		return keyCmd(args[1:], osKeyIO(stdout, stderr))
	}
	fmt.Fprintln(stderr, "usage: plumb version | plumb serve [--dev] [flags] | plumb key init|restore [flags]")
	return 2
}
