package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/docker-agent/pkg/worktree"
	"github.com/spf13/cobra"
)

// Git-worktree support: --worktree runs a session in a fresh git worktree of the
// working directory, isolating synthesized HCL on a throwaway branch so the
// user's checkout stays untouched. This is the narrow "HCL-authoring mode"
// exception CLAUDE.md reserved for wiring cagent's pkg/worktree.
//
// The mechanism hinges on one fact: turf launches turf-mcp-server with an empty
// subprocess cwd (see agent.go), so the server inherits the CLI process cwd. That
// same cwd also drives the filesystem tool's allow-list and the memory-db default.
// So a single os.Chdir(worktree.Dir) before the runtime is built redirects all
// three — the server writes HCL into the worktree, and the agent's file tools and
// memory follow — with no other change.

var (
	flagWorktree     string // --worktree (NoOptDefVal "auto"); "" means not requested
	flagWorktreeBase string // --worktree-base
)

// worktreeAutoName is stored when --worktree is given with no value; it maps to
// an empty name so pkg/worktree generates a friendly random one.
const worktreeAutoName = "auto"

// addWorktreeFlags registers the persistent worktree flags on the root command.
func addWorktreeFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVar(&flagWorktree, "worktree", "",
		"Run in a fresh git worktree of the working directory, isolating synthesized HCL on a throwaway branch. Optionally name it: --worktree=my-name")
	cmd.PersistentFlags().Lookup("worktree").NoOptDefVal = worktreeAutoName
	cmd.PersistentFlags().StringVar(&flagWorktreeBase, "worktree-base", "",
		"Branch the --worktree from this ref instead of the current HEAD (e.g. main, origin/main); a remote-tracking ref is fetched first")
}

// setupRunWorktree creates the git worktree requested by --worktree and chdirs
// into it, so the runtime, the filesystem tool, memory, and the inherited
// turf-mcp-server cwd all land inside the worktree. It returns (nil, "", nil)
// when --worktree was not given. It MUST be called before createAgentRuntime,
// which captures the working directory when it builds toolsets and starts the
// server.
//
// The second return value is the anchor: the launch/--chdir working directory
// captured *before* the chdir into the worktree. Session history is anchored
// there (see createAgentRuntime / agentOpts.sessionDBDir) so a session's
// transcript lives with the real project across throwaway worktrees, while
// memory, the recorded WorkingDir, and the server all follow cwd into the
// worktree. It is "" when no worktree was created (the anchor is then just cwd,
// which createAgentRuntime already defaults to).
func setupRunWorktree(ctx context.Context) (*worktree.Worktree, string, error) {
	if flagWorktree == "" {
		if flagWorktreeBase != "" {
			return nil, "", errors.New("--worktree-base requires --worktree")
		}
		return nil, "", nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", err
	}

	name := flagWorktree
	if name == worktreeAutoName {
		name = ""
	}

	// Store worktrees under turf's own home (<TURF_HOME>/worktrees/<name>), not
	// cagent's data dir: applyTurfTheme()'s paths.SetDataDir runs later (in the
	// TUI), so pkg/worktree's default root would still point at cagent's dir here.
	// Resolve the root to an absolute path so wt.Dir stays valid after we chdir
	// into it — a relative TURF_HOME would otherwise make cleanup's `git -C` fail.
	root := turfHome()
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	wt, err := worktree.Create(ctx, cwd, name,
		worktree.WithBase(flagWorktreeBase), worktree.WithRoot(root))
	if err != nil {
		return nil, "", friendlyWorktreeErr(err, cwd)
	}

	if err := os.Chdir(wt.Dir); err != nil {
		return nil, "", fmt.Errorf("entering git worktree %s: %w", wt.Dir, err)
	}
	fmt.Fprintf(os.Stderr, "Using git worktree %s (branch %s)\n", wt.Dir, wt.Branch)
	// cwd is the anchor: the launch dir captured before the chdir above. The
	// session store is pinned there so history survives the throwaway worktree.
	return wt, cwd, nil
}

// worktreeBranch returns the worktree's branch for the TUI status bar, or "" when
// no worktree is active.
func worktreeBranch(wt *worktree.Worktree) string {
	if wt == nil {
		return ""
	}
	return wt.Branch
}

// versionWithBranch appends the worktree branch to the version string shown in
// the full TUI status bar (title = "<appName> <version>"). Returns Version
// unchanged when no worktree is active.
func versionWithBranch(branch string) string {
	if branch == "" {
		return Version
	}
	return Version + " · " + branch
}

// appNameWithBranch appends the worktree branch to the app name shown in the lean
// TUI status footer (which has no version slot). Returns appName unchanged when no
// worktree is active.
func appNameWithBranch(branch string) string {
	if branch == "" {
		return appName
	}
	return appName + " · " + branch
}

// friendlyWorktreeErr translates pkg/worktree's sentinel errors into actionable
// messages naming the flag the user set.
func friendlyWorktreeErr(err error, dir string) error {
	switch {
	case errors.Is(err, worktree.ErrNotGitRepository):
		return fmt.Errorf("--worktree requires %s to be inside a git repository (run `git init` first)", dir)
	case errors.Is(err, worktree.ErrInvalidName):
		return fmt.Errorf("invalid --worktree name: %w", err)
	case errors.Is(err, worktree.ErrInvalidBase):
		return fmt.Errorf("invalid --worktree-base: %w", err)
	default:
		return err
	}
}

// cleanupRunWorktree removes a worktree created for an interactive run once it
// ends, mirroring cagent's cleanupWorktree. It is a no-op when no worktree was
// created or the run was non-interactive (exec up/destroy leave the worktree in
// place for inspection, matching cagent's --exec). A clean worktree is removed
// automatically; a dirty one (uncommitted changes, untracked files, or new
// commits) is kept unless the user confirms removal, so work is never discarded
// silently. Failures are reported but never abort — the run already finished.
func cleanupRunWorktree(ctx context.Context, wt *worktree.Worktree, interactive bool) {
	if wt == nil || !interactive {
		return
	}

	st, err := wt.Status(ctx)
	if err != nil {
		fmt.Printf("Could not inspect git worktree %s: %s\n", wt.Dir, err.Error())
		fmt.Printf("Leaving it in place. Remove it manually with: git -C %s worktree remove %s\n", wt.SourceDir, wt.Dir)
		return
	}

	if st.IsDirty() {
		if !promptRemoveDirtyWorktree(wt, st) {
			fmt.Printf("Keeping git worktree %s (branch %s).\n", wt.Dir, wt.Branch)
			return
		}
	}

	// The process cwd is inside the worktree (setupRunWorktree chdir'd there);
	// step out to the source repo before removing so the removal doesn't run from
	// a directory git is about to delete.
	if err := os.Chdir(wt.SourceDir); err != nil {
		fmt.Printf("Could not leave git worktree %s before removal: %s\n", wt.Dir, err.Error())
		return
	}
	if err := wt.Remove(ctx); err != nil {
		fmt.Printf("Failed to remove git worktree %s: %s\n", wt.Dir, err.Error())
		return
	}
	fmt.Printf("Removed git worktree %s (branch %s).\n", wt.Dir, wt.Branch)
}

// promptRemoveDirtyWorktree asks whether to discard a worktree that still holds
// work. It defaults to keeping (returns false) on any non-yes answer or read
// error, so uncommitted work is never lost by accident.
func promptRemoveDirtyWorktree(wt *worktree.Worktree, st worktree.Status) bool {
	var held []string
	if st.Modified {
		held = append(held, "uncommitted changes")
	}
	if st.Untracked {
		held = append(held, "untracked files")
	}
	if st.NewCommits {
		held = append(held, "new commits")
	}

	fmt.Printf("\nThe git worktree %s (branch %s) still has %s.\n", wt.Dir, wt.Branch, strings.Join(held, ", "))
	fmt.Print("Remove it and discard this work? Keeping preserves the directory and branch so you can return later. (y/N): ")

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false
	}
	response := strings.TrimSpace(strings.ToLower(line))
	return response == "y" || response == "yes"
}
