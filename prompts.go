package main

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/docker/docker-agent/pkg/app"
	"github.com/docker/docker-agent/pkg/runtime"
	"github.com/docker/docker-agent/pkg/tui/commands"
	"github.com/docker/docker-agent/pkg/tui/core"
	"github.com/docker/docker-agent/pkg/tui/messages"
)

// renderServerPrompt fetches and renders one of the turf MCP server's authored
// prompts ("up"/"destroy") into the initial user message, so the CLI subcommands
// deliver the exact same runbook the TUI's /up and /destroy slash commands do
// (both go through the runtime's ExecuteMCPPrompt → MCP prompts/get path).
//
// config_path is passed explicitly (resolved after any --worktree chdir): the
// server defaults none, and without one its prompt asks; instructions, when
// non-empty, are woven into the prompt's User Instructions section server-side.
// autoApprove (--yes/--auto-approve) sends the prompt's auto_approve argument: the
// user pre-approved every plan of this run, so the server's runbook has up/destroy
// run to the end without pausing at each round's approval point.
func renderServerPrompt(ctx context.Context, rt runtime.Runtime, name, configPath, instructions string, autoApprove bool) (string, error) {
	text, err := rt.ExecuteMCPPrompt(ctx, name, serverPromptArgs(configPath, instructions, autoApprove))
	if err != nil {
		return "", fmt.Errorf("render %q prompt from turf-mcp-server: %w", name, err)
	}
	return text, nil
}

// serverPromptArgs builds the arguments of the server's "up"/"destroy" prompts,
// by name: config_path is the directory the CLI runs in, and the user's words
// go to instructions. The subcommands (renderServerPrompt) and the TUI's /up and
// /destroy (turfPromptCommands) both build them here, so the two can't drift.
func serverPromptArgs(configPath, instructions string, autoApprove bool) map[string]string {
	args := map[string]string{"config_path": configPath}
	if autoApprove {
		args["auto_approve"] = "true"
	}
	if instructions = strings.TrimSpace(instructions); instructions != "" {
		args["instructions"] = instructions
	}
	return args
}

// turfPromptCommands builds the TUI's command palette with /up and /destroy
// taking the same arguments as `turf up` and `turf destroy`: the working
// directory as config_path and the typed text as instructions. cagent's stock
// slash command hands the whole text to a prompt's first declared argument,
// which for these two is config_path, so "/up use env/staging.tfvars" would
// name a directory that doesn't exist. Every other prompt (/guide's topic,
// another server's) keeps the stock mapping.
func turfPromptCommands(a *app.App, wd string) func(context.Context, tea.Model) []commands.Category {
	return func(ctx context.Context, _ tea.Model) []commands.Category {
		return withTurfPromptArgs(commands.BuildCommandCategories(ctx, a), wd)
	}
}

// withTurfPromptArgs rewires the up and destroy prompt items of cats in place
// and returns cats. The TUI has no --yes, so auto_approve is never sent.
func withTurfPromptArgs(cats []commands.Category, wd string) []commands.Category {
	for ci := range cats {
		for ii := range cats[ci].Commands {
			item := &cats[ci].Commands[ii]
			name, ok := strings.CutPrefix(item.ID, "mcp.prompt.")
			if !ok || (name != "up" && name != "destroy") {
				continue
			}
			item.Execute = func(arg string) tea.Cmd {
				return core.CmdHandler(messages.MCPPromptMsg{
					PromptName: name,
					Arguments:  serverPromptArgs(wd, arg, false),
				})
			}
		}
	}
	return cats
}
