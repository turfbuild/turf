package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

func newUpCmd() *cobra.Command {
	var autoApprove bool

	cmd := &cobra.Command{
		Use:   "up [instructions...]",
		Short: "Deploy infrastructure from HCL configuration",
		Long: "Deploy or reconcile infrastructure by running an AI agent that plans and applies changes from HCL configuration files in the current directory. Use -C/--chdir to target another directory.\n\n" +
			"Optionally pass freeform instructions to steer the plan (e.g. `turf up replace aws_s3_bucket.assets`, or `turf up -- -target=module.network` when the text starts with a dash). Instructions are honored within the normal plan/approve flow and never bypass approval.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			interactive := isatty.IsTerminal(os.Stdout.Fd())

			wt, sessionDir, err := setupRunWorktree(ctx)
			if err != nil {
				return err
			}
			defer cleanupRunWorktree(context.WithoutCancel(ctx), wt, interactive)

			// Resolve the configuration path AFTER any --worktree chdir so the
			// explicit path handed to the server's /up prompt (and from there to
			// config_init) lands inside the worktree — the server never relies
			// on its inherited cwd for the configuration.
			absPath, err := filepath.Abs(".")
			if err != nil {
				return err
			}

			// up persists to the session store (so the run shows up in the
			// /sessions browser and can be continued later from chat) but takes
			// no resume flag: each up run delivers a fresh /up runbook, so the
			// store is only ever the sink here. The store handle is unused.
			rt, _, curator, cleanup, err := createAgentRuntime(ctx, agentOpts{
				model:          flagModel,
				baseURL:        flagModelBaseURL,
				tmpDir:         flagTmpDir,
				allowPaths:     flagAllowPaths,
				pluginCacheDir: flagPluginCacheDir,
				memoryPath:     flagMemoryPath,
				noMemory:       flagNoMemory,
				sessionDBPath:  flagSessionDB,
				noSession:      flagNoSession,
				sessionDBDir:   sessionDir,
				logFile:        flagLogFile,
				logLevel:       flagLogLevel,
				logFormat:      flagLogFormat,
				interactive:    interactive,
			})
			if err != nil {
				return err
			}
			defer cleanup()

			// Render the server's authored /up runbook as the first message —
			// the same content the TUI's /up slash command produces — with any
			// positional args joined into the optional instructions argument.
			prompt, err := renderServerPrompt(ctx, rt, "up", absPath, strings.Join(args, " "))
			if err != nil {
				return err
			}

			// Both paths start from an empty session and deliver the prompt as
			// the active user turn — the TUI via its first-message mechanism
			// (which echoes and sends it), the exec path via cli.Run's user
			// messages. Seeding the prompt into the session too would double it.
			sess := newSession(autoApprove)
			if interactive {
				return runTUI(ctx, rt, sess, curator, prompt, true, worktreeBranch(wt))
			}
			return runExecWith(ctx, rt, sess, execConfig{
				autoApprove: autoApprove,
				messages:    []string{prompt},
			})
		},
	}

	cmd.Flags().BoolVar(&autoApprove, "yes", false, "Auto-approve changes without confirmation")
	cmd.Flags().BoolVar(&autoApprove, "auto-approve", false, "Auto-approve changes without confirmation")

	return cmd
}
