// Command turf is the Terraform-compatible infrastructure engine built for AI
// agents: full support for Terraform HCL and the module registry, plus an AI
// agent that plans, applies, and destroys cloud infrastructure across any
// OpenTofu provider. Built on the turf MCP server.
//
// It provides interactive (chat) and non-interactive (up, destroy) commands
// for managing cloud infrastructure via OpenTofu providers. The CLI launches
// the turf-mcp-server binary (which must be available on PATH) as a subprocess
// and connects to it over MCP.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := execute(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
