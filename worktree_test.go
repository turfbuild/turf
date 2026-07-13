package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initGitRepo creates a temp git repository with one commit and returns its path.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Resolve symlinks so the repo root matches what git reports (macOS /var →
	// /private/var), keeping path comparisons in tests exact.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// Deterministic identity + no signing so `git commit` works in CI.
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=turf", "GIT_AUTHOR_EMAIL=turf@example.com",
			"GIT_COMMITTER_NAME=turf", "GIT_COMMITTER_EMAIL=turf@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init")
	run("commit", "--allow-empty", "-m", "init")
	return dir
}

// setWorktreeFlags sets the package-level worktree flag vars for a test and
// restores them afterward.
func setWorktreeFlags(t *testing.T, name, base string) {
	t.Helper()
	origName, origBase := flagWorktree, flagWorktreeBase
	flagWorktree, flagWorktreeBase = name, base
	t.Cleanup(func() { flagWorktree, flagWorktreeBase = origName, origBase })
}

// enterDir chdirs to dir and restores the original cwd when the test ends. Uses a
// defer-style restore registered before any t.TempDir cleanup so the process
// leaves the temp tree before it is removed.
func enterDir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

func TestSetupRunWorktreeNotRequested(t *testing.T) {
	setWorktreeFlags(t, "", "")
	wt, err := setupRunWorktree(context.Background())
	if err != nil {
		t.Fatalf("setupRunWorktree: %v", err)
	}
	if wt != nil {
		t.Fatalf("expected nil worktree when --worktree unset, got %+v", wt)
	}
}

func TestSetupRunWorktreeBaseRequiresWorktree(t *testing.T) {
	setWorktreeFlags(t, "", "main")
	_, err := setupRunWorktree(context.Background())
	if err == nil || !strings.Contains(err.Error(), "--worktree-base requires --worktree") {
		t.Fatalf("expected base-requires-worktree error, got %v", err)
	}
}

func TestSetupRunWorktreeCreatesAndChdirs(t *testing.T) {
	home := t.TempDir()
	// Resolve symlinks (macOS /var → /private/var) so the expected dir matches the
	// symlink-resolved cwd after setupRunWorktree chdirs.
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	t.Setenv("TURF_HOME", home)
	repo := initGitRepo(t)
	enterDir(t, repo)
	setWorktreeFlags(t, "authoring", "")

	wt, err := setupRunWorktree(context.Background())
	if err != nil {
		t.Fatalf("setupRunWorktree: %v", err)
	}
	if wt == nil {
		t.Fatal("expected a worktree, got nil")
	}
	t.Cleanup(func() { _ = os.Chdir(repo); _ = wt.Remove(context.Background()) })

	wantDir := filepath.Join(home, "worktrees", "authoring")
	if wt.Dir != wantDir {
		t.Errorf("worktree Dir = %q, want %q", wt.Dir, wantDir)
	}
	if wt.Branch != "worktree-authoring" {
		t.Errorf("worktree Branch = %q, want worktree-authoring", wt.Branch)
	}
	// setupRunWorktree must chdir into the worktree so the server (which inherits
	// cwd), the filesystem tool, and memory all follow.
	cwd, _ := os.Getwd()
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	if cwd != wt.Dir {
		t.Errorf("cwd = %q after setup, want worktree dir %q", cwd, wt.Dir)
	}
}

func TestSetupRunWorktreeNonGitRepo(t *testing.T) {
	t.Setenv("TURF_HOME", t.TempDir())
	enterDir(t, t.TempDir())
	setWorktreeFlags(t, "auto", "")

	_, err := setupRunWorktree(context.Background())
	if err == nil || !strings.Contains(err.Error(), "git repository") {
		t.Fatalf("expected not-a-git-repository error, got %v", err)
	}
}

func TestCleanupRunWorktreeNonInteractiveKeeps(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TURF_HOME", home)
	repo := initGitRepo(t)
	enterDir(t, repo)
	setWorktreeFlags(t, "keepme", "")

	wt, err := setupRunWorktree(context.Background())
	if err != nil {
		t.Fatalf("setupRunWorktree: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(repo); _ = wt.Remove(context.Background()) })

	// Non-interactive runs never remove the worktree.
	cleanupRunWorktree(context.Background(), wt, false)
	if _, err := os.Stat(wt.Dir); err != nil {
		t.Fatalf("worktree should survive a non-interactive run, stat err: %v", err)
	}
}

func TestCleanupRunWorktreeCleanRemoves(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TURF_HOME", home)
	repo := initGitRepo(t)
	enterDir(t, repo)
	setWorktreeFlags(t, "throwaway", "")

	wt, err := setupRunWorktree(context.Background())
	if err != nil {
		t.Fatalf("setupRunWorktree: %v", err)
	}

	// A clean, interactive worktree is auto-removed (no prompt path taken).
	cleanupRunWorktree(context.Background(), wt, true)
	if _, err := os.Stat(wt.Dir); !os.IsNotExist(err) {
		t.Fatalf("clean worktree should be removed, stat err: %v", err)
	}
}

func TestCleanupRunWorktreeDirtyKeepsOnDecline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TURF_HOME", home)
	repo := initGitRepo(t)
	enterDir(t, repo)
	setWorktreeFlags(t, "dirty", "")

	wt, err := setupRunWorktree(context.Background())
	if err != nil {
		t.Fatalf("setupRunWorktree: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(repo); _ = wt.Remove(context.Background()) })

	// Make the worktree dirty (untracked file) so cleanup takes the prompt path.
	if err := os.WriteFile(filepath.Join(wt.Dir, "main.tf"), []byte("# hcl\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Feed EOF on stdin so the prompt reads no "yes" and defaults to keep.
	origStdin := os.Stdin
	r, w, _ := os.Pipe()
	_ = w.Close() // immediate EOF
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = origStdin })

	cleanupRunWorktree(context.Background(), wt, true)
	if _, err := os.Stat(wt.Dir); err != nil {
		t.Fatalf("dirty worktree should be kept on a non-yes answer, stat err: %v", err)
	}
}
