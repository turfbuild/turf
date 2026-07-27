package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/docker-agent/pkg/session"
)

// newTestStore opens a real SQLite session store in a temp dir. Using the real
// store (not the in-memory one) exercises the same code path production runs do.
func newTestStore(t *testing.T) session.Store {
	t.Helper()
	store, err := session.NewSQLiteSessionStore(context.Background(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("opening test session store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// assertTurfPolicy checks that a session carries turf's detailed tool-view policy.
// This is the session-level property resolveTurfSession must guarantee on both
// fresh and resumed sessions. Tool pre-approval is NOT a session property anymore —
// it is installed at the team level (see createAgentRuntime / preApprovedTools), so
// it is not stamped onto (nor asserted on) the session object here.
func assertTurfPolicy(t *testing.T, sess *session.Session) {
	t.Helper()
	if sess.HideToolResults {
		t.Error("HideToolResults = true, want false (detailed tool views)")
	}
}

func TestResolveTurfSessionEmptyRefIsFresh(t *testing.T) {
	store := newTestStore(t)
	sess, err := resolveTurfSession(context.Background(), store, "", false)
	if err != nil {
		t.Fatalf("resolveTurfSession: %v", err)
	}
	assertTurfPolicy(t, sess)
	if len(sess.Messages) != 0 {
		t.Errorf("fresh session has %d messages, want 0", len(sess.Messages))
	}
}

func TestResolveTurfSessionNilStoreIsFresh(t *testing.T) {
	// A resume ref with no store still yields a fresh session rather than
	// erroring — persistence disabled shouldn't crash a run.
	sess, err := resolveTurfSession(context.Background(), nil, "-1", false)
	if err != nil {
		t.Fatalf("resolveTurfSession: %v", err)
	}
	assertTurfPolicy(t, sess)
}

func TestResolveTurfSessionUnknownExplicitIDCreatesWithID(t *testing.T) {
	store := newTestStore(t)
	sess, err := resolveTurfSession(context.Background(), store, "caller-owned-id", false)
	if err != nil {
		t.Fatalf("resolveTurfSession: %v", err)
	}
	if sess.ID != "caller-owned-id" {
		t.Errorf("session ID = %q, want %q", sess.ID, "caller-owned-id")
	}
	assertTurfPolicy(t, sess)
}

func TestResolveTurfSessionResumeReappliesPolicy(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Seed a session with HideToolResults=true — the opposite of turf's policy.
	// cagent's resume path re-applies tools-approved and hide-tool-results, but the
	// guard here is that resolveTurfSession re-stamps turf's session flags (detailed
	// tool views) on load regardless. (Tool pre-approval is team-level now, so it
	// isn't a session flag and isn't asserted here.)
	seed := session.New(session.WithID("seed-1"))
	session.WithHideToolResults(true)(seed)
	if err := store.AddSession(ctx, seed); err != nil {
		t.Fatalf("AddSession: %v", err)
	}
	if _, err := store.AddMessage(ctx, seed.ID, session.UserMessage("hello")); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}

	// Resume the most recent session.
	sess, err := resolveTurfSession(ctx, store, "-1", false)
	if err != nil {
		t.Fatalf("resolveTurfSession: %v", err)
	}
	if sess.ID != "seed-1" {
		t.Errorf("resumed session ID = %q, want %q", sess.ID, "seed-1")
	}
	if len(sess.Messages) == 0 {
		t.Error("resumed session has no messages; history not loaded")
	}
	assertTurfPolicy(t, sess) // the guard: policy re-applied on the loaded session
}

// TestResolveTurfSessionResumePreservesTitle guards that a title the curator
// persisted (via store.UpdateSessionTitle) survives resume — applyTurfSessionPolicy
// re-stamps permissions/tool-view but must not wipe the loaded title.
func TestResolveTurfSessionResumePreservesTitle(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	seed := session.New(session.WithID("titled-1"))
	if err := store.AddSession(ctx, seed); err != nil {
		t.Fatalf("AddSession: %v", err)
	}
	if _, err := store.AddMessage(ctx, seed.ID, session.UserMessage("set up a bucket")); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}
	// Simulate the curator writing a milestone title through the store.
	if err := store.UpdateSessionTitle(ctx, seed.ID, "Provisioned myapp bucket"); err != nil {
		t.Fatalf("UpdateSessionTitle: %v", err)
	}

	sess, err := resolveTurfSession(ctx, store, "titled-1", false)
	if err != nil {
		t.Fatalf("resolveTurfSession: %v", err)
	}
	if sess.Title != "Provisioned myapp bucket" {
		t.Errorf("resumed title = %q, want %q", sess.Title, "Provisioned myapp bucket")
	}
	assertTurfPolicy(t, sess) // policy re-applied without clobbering the title
}

func TestResolveTurfSessionContinueEmptyStoreIsFresh(t *testing.T) {
	// `turf --continue` (ref "-1") in a new/empty directory has nothing to
	// resume, so it must start a fresh session rather than erroring with
	// "session offset 1 out of range (have 0 sessions)".
	store := newTestStore(t)
	sess, err := resolveTurfSession(context.Background(), store, "-1", false)
	if err != nil {
		t.Fatalf("resolveTurfSession(-1) against empty store: %v", err)
	}
	assertTurfPolicy(t, sess)
	if len(sess.Messages) != 0 {
		t.Errorf("fresh session has %d messages, want 0", len(sess.Messages))
	}
}

func TestResolveTurfSessionRelativeOutOfRangeNonEmpty(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	// One session exists; asking for the 2nd-most-recent is an out-of-range
	// offset against a non-empty store — a likely typo, so it stays an error
	// rather than silently starting fresh.
	seed := session.New(session.WithID("only-1"))
	if err := store.AddSession(ctx, seed); err != nil {
		t.Fatalf("AddSession: %v", err)
	}
	if _, err := store.AddMessage(ctx, seed.ID, session.UserMessage("hi")); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}
	if _, err := resolveTurfSession(ctx, store, "-2", false); err == nil {
		t.Fatal("expected error resolving -2 against a 1-session store, got nil")
	}
}

func TestResumeRef(t *testing.T) {
	defer func() { flagNoSession = false }()

	tests := []struct {
		name        string
		sessionFlag string
		continueF   bool
		noSession   bool
		want        string
		wantErr     bool
	}{
		{name: "none", want: ""},
		{name: "explicit session", sessionFlag: "abc", want: "abc"},
		{name: "continue is -1", continueF: true, want: "-1"},
		{name: "session wins over continue", sessionFlag: "abc", continueF: true, want: "abc"},
		{name: "no-session with ref errors", sessionFlag: "abc", noSession: true, wantErr: true},
		{name: "no-session without ref is fine", noSession: true, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flagNoSession = tc.noSession
			got, err := resumeRef(tc.sessionFlag, tc.continueF)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("resumeRef: %v", err)
			}
			if got != tc.want {
				t.Errorf("resumeRef = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCreateAgentRuntimeWiresSessionStore is an integration check that
// createAgentRuntime actually opens the SQLite store, creates its file, and hands
// it to the runtime. It needs turf-mcp-server on PATH (createAgentRuntime launches
// it), so it skips when the binary is absent — mirroring examples_test.go, keeping
// CI without the server green. The openai provider is used with a dummy key and a
// bogus base URL because it constructs lazily and createAgentRuntime never dials
// the model (only a real turn would), so no network is touched.
func TestCreateAgentRuntimeWiresSessionStore(t *testing.T) {
	if _, err := resolveMCPServer(); err != nil {
		t.Skipf("turf-mcp-server not available: %v", err)
	}
	t.Setenv("OPENAI_API_KEY", "test-key")
	ctx := context.Background()

	t.Run("store wired and file created", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "sessions.db")
		rt, store, _, cleanup, err := createAgentRuntime(ctx, agentOpts{
			model:         "openai/gpt-4o",
			baseURL:       "http://127.0.0.1:1/v1",
			noMemory:      true,
			sessionDBPath: dbPath,
		})
		if err != nil {
			t.Fatalf("createAgentRuntime: %v", err)
		}
		defer cleanup()

		if store == nil {
			t.Fatal("createAgentRuntime returned a nil session store with persistence enabled")
		}
		if _, err := os.Stat(dbPath); err != nil {
			t.Errorf("session database not created at %s: %v", dbPath, err)
		}
		if rt.SessionStore() != store {
			t.Error("runtime.SessionStore() is not the store createAgentRuntime opened")
		}
	})

	t.Run("sessionDBDir anchors the default path", func(t *testing.T) {
		// With no explicit --session-db, the store defaults to .turf/sessions.db in
		// the anchor dir (opts.sessionDBDir) rather than cwd — the --worktree case,
		// where the anchor is the launch dir so history lives with the real project.
		anchor := t.TempDir()
		_, store, _, cleanup, err := createAgentRuntime(ctx, agentOpts{
			model:        "openai/gpt-4o",
			baseURL:      "http://127.0.0.1:1/v1",
			noMemory:     true,
			sessionDBDir: anchor, // no sessionDBPath: exercise the default resolution
		})
		if err != nil {
			t.Fatalf("createAgentRuntime: %v", err)
		}
		defer cleanup()

		if store == nil {
			t.Fatal("createAgentRuntime returned a nil session store with persistence enabled")
		}
		want := filepath.Join(anchor, ".turf", "sessions.db")
		if _, err := os.Stat(want); err != nil {
			t.Errorf("session database not created at anchored path %s: %v", want, err)
		}
	})

	t.Run("no-session leaves store nil", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "sessions.db")
		_, store, _, cleanup, err := createAgentRuntime(ctx, agentOpts{
			model:         "openai/gpt-4o",
			baseURL:       "http://127.0.0.1:1/v1",
			noMemory:      true,
			noSession:     true,
			sessionDBPath: dbPath,
		})
		if err != nil {
			t.Fatalf("createAgentRuntime: %v", err)
		}
		defer cleanup()

		if store != nil {
			t.Error("expected nil session store with --no-session")
		}
		if _, err := os.Stat(dbPath); err == nil {
			t.Error("session database was created despite --no-session")
		}
	})
}

// TestSessionSummariesReflectTurfSessions reproduces the data path the full
// TUI's /sessions browser reads: it persists turf-created sessions to the store
// (as a run does) and calls GetSessionSummaries. It guards two things the browser
// needs: turf sessions show up at all, and they carry a WorkingDir so the browser
// can group them under "This workspace" (empty WorkingDir was why turf sessions
// never grouped).
func TestSessionSummariesReflectTurfSessions(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	// Two sessions, each with content so they persist (empty sessions are lazy).
	for range 2 {
		sess := newSession(false)
		if err := store.AddSession(ctx, sess); err != nil {
			t.Fatalf("AddSession: %v", err)
		}
		if _, err := store.AddMessage(ctx, sess.ID, session.UserMessage("do a thing")); err != nil {
			t.Fatalf("AddMessage: %v", err)
		}
	}

	summaries, err := store.GetSessionSummaries(ctx)
	if err != nil {
		t.Fatalf("GetSessionSummaries: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("GetSessionSummaries returned %d sessions, want 2", len(summaries))
	}
	for _, s := range summaries {
		if s.WorkingDir != cwd {
			t.Errorf("summary WorkingDir = %q, want cwd %q (browser can't group it under \"This workspace\")", s.WorkingDir, cwd)
		}
		if s.NumMessages == 0 {
			t.Errorf("summary %s has 0 messages", s.ID)
		}
	}
}

func TestApplyTurfSessionPolicyAutoApprove(t *testing.T) {
	sess := session.New()
	applyTurfSessionPolicy(sess, true)
	assertTurfPolicy(t, sess)
	if !sess.ToolsApproved {
		t.Error("ToolsApproved = false, want true under autoApprove")
	}
}
