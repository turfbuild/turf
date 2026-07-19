package main

import (
	"context"

	"github.com/spf13/cobra"
)

func newChatCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "chat",
		Short: "Interactive infrastructure management",
		Long:  "Start an interactive TUI session for ad-hoc infrastructure management. Type freely to create, update, or inspect resources.",
		Args:  cobra.NoArgs,
		RunE:  runChat,
	}
}

// runChat launches the interactive chat TUI. It backs both the `chat`
// subcommand and the bare `turf` invocation (wired as the root command's RunE),
// so running turf with no subcommand is equivalent to `turf chat`.
func runChat(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	// chat is always an interactive TUI session, so the worktree (when --worktree
	// is set) is created here and cleaned up when the session ends.
	wt, err := setupRunWorktree(ctx)
	if err != nil {
		return err
	}
	defer cleanupRunWorktree(context.WithoutCancel(ctx), wt, true)

	rt, cleanup, err := createAgentRuntime(ctx, agentOpts{
		model:          flagModel,
		baseURL:        flagModelBaseURL,
		tmpDir:         flagTmpDir,
		pluginCacheDir: flagPluginCacheDir,
		memoryPath:     flagMemoryPath,
		noMemory:       flagNoMemory,
		welcomeMessage: welcomeMessage,
		logFile:        flagLogFile,
		logLevel:       flagLogLevel,
		logFormat:      flagLogFormat,
		interactive:    true, // chat is always a TUI session
	})
	if err != nil {
		return err
	}
	defer cleanup()

	sess := newSession(false)
	return runTUI(ctx, rt, sess, "", flagLean, worktreeBranch(wt))
}
