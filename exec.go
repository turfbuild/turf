package main

import (
	"context"
	"strings"

	"github.com/spf13/cobra"
)

// newExecCmd wires the `exec` subcommand: a single, headless agent request with
// no TUI. Unlike `up`/`destroy` (which carry fixed /up and /destroy prompts),
// `exec` runs an arbitrary natural-language message against the real model
// selected by --model, streaming the run to stdout.
//
// It is the intended surface for driving turf from a script or another agent
// (e.g. Claude Code exercising a real model end-to-end): the message is the
// caller's prompt, --json emits one runtime event per line for machine parsing,
// and the process exit code reflects whether the run succeeded. It always runs
// non-interactively, so plan-approval elicitation is auto-confirmed (see
// autoconfirm.go); the mutation gate still asks on stdin unless --yes is set.
func newExecCmd() *cobra.Command {
	var (
		autoApprove   bool
		outputJSON    bool
		hideToolCalls bool
	)

	cmd := &cobra.Command{
		Use:   "exec [message]",
		Short: "Run a single agent request without the TUI",
		Long: "Run one natural-language request against the model (set with --model) without launching the interactive TUI, " +
			"streaming the agent's output to stdout.\n\n" +
			"The request comes from the [message] argument(s), from stdin when [message] is \"-\", or from piped stdin when no " +
			"argument is given (with a terminal and no argument, a plain readline prompt loop starts instead). Multiple " +
			"positional words are joined into a single message, so quoting is optional; use \"--\" to pass a message that " +
			"begins with a dash (e.g. turf exec -- --dry-run the plan).\n\n" +
			"Use --json to emit each runtime event (agent text, tool calls, tool results, errors) as one JSON object per " +
			"line for scripting, and --yes to auto-approve changes so the run proceeds unattended. The process exits " +
			"non-zero if the run fails. Use -C/--chdir to target another directory.",
		// Arbitrary args: the positionals form one message (joined below), so an
		// unquoted multi-word prompt and `exec -- <prompt>` both work.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			// exec is always headless: create the worktree (when --worktree is
			// set) here and, like the non-interactive up/destroy path, leave it in
			// place on teardown (interactive=false never auto-removes it).
			wt, err := setupRunWorktree(ctx)
			if err != nil {
				return err
			}
			defer cleanupRunWorktree(context.WithoutCancel(ctx), wt, false)

			rt, cleanup, err := createAgentRuntime(ctx, agentOpts{
				model:          flagModel,
				baseURL:        flagModelBaseURL,
				tmpDir:         flagTmpDir,
				pluginCacheDir: flagPluginCacheDir,
				memoryPath:     flagMemoryPath,
				noMemory:       flagNoMemory,
				logFile:        flagLogFile,
				logLevel:       flagLogLevel,
				logFormat:      flagLogFormat,
				// Headless run: no TUI to render a user_prompt dialog, so wire the
				// auto-confirming stand-in (see agent.go / autoconfirm.go).
				interactive: false,
			})
			if err != nil {
				return err
			}
			defer cleanup()

			// The session carries no seeded message — the prompt IS the user
			// turn (see execMessages), so it is not double-sent.
			sess := newSession("", autoApprove)
			return runExecWith(ctx, rt, sess, execConfig{
				autoApprove:   autoApprove,
				outputJSON:    outputJSON,
				hideToolCalls: hideToolCalls,
				messages:      execMessages(args),
			})
		},
	}

	cmd.Flags().BoolVar(&autoApprove, "yes", false, "Auto-approve changes without confirmation")
	cmd.Flags().BoolVar(&autoApprove, "auto-approve", false, "Auto-approve changes without confirmation")
	cmd.Flags().BoolVar(&outputJSON, "json", false, "Emit each runtime event as one JSON object per line (for scripting)")
	cmd.Flags().BoolVar(&hideToolCalls, "hide-tool-calls", false, "Suppress tool-call and tool-result lines, printing only the agent's prose")

	return cmd
}

// execMessages turns the exec positionals into the user turn(s) for cli.Run:
//   - no args        → nil (cli.Run reads piped stdin, or starts a readline loop on a TTY)
//   - a lone "-"     → ["-"] (cli.Run reads the whole message from stdin)
//   - one or more    → one message, words joined by spaces
//
// Joining is what makes both `exec create a bucket` (unquoted) and
// `exec -- create a bucket` (after the flag terminator) resolve to the same
// single message, so quoting is optional.
func execMessages(args []string) []string {
	switch {
	case len(args) == 1 && args[0] == "-":
		return []string{"-"}
	case len(args) > 0:
		return []string{strings.Join(args, " ")}
	default:
		return nil
	}
}
