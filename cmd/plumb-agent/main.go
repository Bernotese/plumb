// Command plumb-agent: see docs/ARCHITECTURE.md. This file parses arguments only.
package main

import (
	"fmt"
	"io"
	"os"
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
	fmt.Fprintln(stderr, "usage: plumb-agent version")
	return 2
}
