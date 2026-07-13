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

			// Interactive up/destroy always use the lean TUI. Seed nothing on
			// this path: the first-message mechanism echoes and sends the prompt,
			// so also seeding it into the session would render it twice. The exec
			// path does need the seed (cli.Run replays it as history).
			if interactive {
				sess := newSession("", autoApprove)
				return runTUI(ctx, rt, sess, prompt, true, worktreeBranch(wt))
			}
			sess := newSession(prompt, autoApprove)
			return runExec(ctx, rt, sess, autoApprove)
		},
	}

	cmd.Flags().BoolVar(&autoApprove, "yes", false, "Auto-approve changes without confirmation")
	cmd.Flags().BoolVar(&autoApprove, "auto-approve", false, "Auto-approve changes without confirmation")

	return cmd
}
