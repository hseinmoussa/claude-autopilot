package main

import (
	"os"

	"github.com/hseinmoussa/claude-autopilot/cmd"
)

// version is injected at build time via -ldflags "-X main.version=<value>".
var version = "dev"

func main() {
	cmd.SetVersion(version)
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
