package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/docker/docker-agent/pkg/runtime"
)

// renderServerPrompt fetches and renders one of the turf MCP server's authored
// prompts ("up"/"destroy") into the initial user message, so the CLI subcommands
// deliver the exact same runbook the TUI's /up and /destroy slash commands do
// (both go through the runtime's ExecuteMCPPrompt → MCP prompts/get path).
//
// config_path is passed explicitly (resolved after any --worktree chdir) so the
// server never has to fall back to its inherited cwd; instructions, when
// non-empty, are woven into the prompt's User Instructions section server-side.
// autoApprove (--yes/--auto-approve) sends the prompt's auto_approve argument: the
// user pre-approved every plan of this run, so the server's runbook has up/destroy
// run to the end without pausing at each round's approval point.
func renderServerPrompt(ctx context.Context, rt runtime.Runtime, name, configPath, instructions string, autoApprove bool) (string, error) {
	args := map[string]string{"config_path": configPath}
	if autoApprove {
		args["auto_approve"] = "true"
	}
	if instructions = strings.TrimSpace(instructions); instructions != "" {
		args["instructions"] = instructions
	}
	text, err := rt.ExecuteMCPPrompt(ctx, name, args)
	if err != nil {
		return "", fmt.Errorf("render %q prompt from turf-mcp-server: %w", name, err)
	}
	return text, nil
}
