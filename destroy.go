package main

import (
	"os"
	"path/filepath"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

func newDestroyCmd() *cobra.Command {
	var autoApprove bool

	cmd := &cobra.Command{
		Use:   "destroy [path]",
		Short: "Destroy all managed infrastructure",
		Long:  "Destroy all infrastructure resources managed by the HCL configuration at the given path.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			configPath := "."
			if len(args) > 0 {
				configPath = args[0]
			}
			absPath, err := filepath.Abs(configPath)
			if err != nil {
				return err
			}

			interactive := isatty.IsTerminal(os.Stdout.Fd())

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
				return runTUI(ctx, rt, sess, prompt, true)
			}
			sess := newSession(prompt, autoApprove)
			return runExec(ctx, rt, sess, autoApprove)
		},
	}

	cmd.Flags().BoolVar(&autoApprove, "yes", false, "Auto-approve changes without confirmation")
	cmd.Flags().BoolVar(&autoApprove, "auto-approve", false, "Auto-approve changes without confirmation")

	return cmd
}
