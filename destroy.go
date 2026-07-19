package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

func newDestroyCmd() *cobra.Command {
	var autoApprove bool

	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Destroy all managed infrastructure",
		Long:  "Destroy all infrastructure resources managed by the HCL configuration in the current directory. Use -C/--chdir to target another directory.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			absPath, err := filepath.Abs(".")
			if err != nil {
				return err
			}

			interactive := isatty.IsTerminal(os.Stdout.Fd())

			wt, err := setupRunWorktree(ctx)
			if err != nil {
				return err
			}
			defer cleanupRunWorktree(context.WithoutCancel(ctx), wt, interactive)

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
				interactive:    interactive,
			})
			if err != nil {
				return err
			}
			defer cleanup()

			prompt := generateDestroyPrompt(absPath)

			// Both paths start from an empty session and deliver the prompt as
			// the active user turn — the TUI via its first-message mechanism
			// (which echoes and sends it), the exec path via cli.Run's user
			// messages. Seeding the prompt into the session too would double it.
			sess := newSession(autoApprove)
			if interactive {
				return runTUI(ctx, rt, sess, prompt, true, worktreeBranch(wt))
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
