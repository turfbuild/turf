package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/docker/docker-agent/pkg/runtime"
	"github.com/docker/docker-agent/pkg/session"
	"github.com/docker/docker-agent/pkg/tools"
)

// respEvent builds a completed tool-call response event carrying output JSON,
// as the runtime hands it to an EventObserver.
func respEvent(tool, output string) *runtime.ToolCallResponseEvent {
	return &runtime.ToolCallResponseEvent{
		ToolDefinition: tools.Tool{Name: tool},
		Response:       output,
	}
}

// initOut is init's result for a configuration at path, in the workspace the server
// always reports (an unselected directory's "default").
func initOut(path string) string {
	return fmt.Sprintf(`{"config_alias":"","path":%q,"workspace":"default"}`, path)
}

// awaiting is an up/destroy/apply result paused at a round's approval point, with
// the round's plan counts.
func awaiting(kind, id string, round, add, change, destroy int) string {
	return fmt.Sprintf(`{"convergence_id":%q,"workspace_alias":"default","kind":%q,"outcome":"awaiting_approval","round":%d,
		"plan":{"path":"/tmp/p.json","digest":"sha256:ab","add":%d,"change":%d,"destroy":%d},
		"rounds":[{"round":%d,"status":"awaiting_approval","approval":"asked","planned":%d}],"next":"…"}`,
		id, kind, round, add, change, destroy, round, add+change+destroy)
}

// ended is a convergence result that has ended with outcome.
func ended(kind, id, outcome string) string {
	return fmt.Sprintf(`{"convergence_id":%q,"workspace_alias":"default","kind":%q,"outcome":%q,"rounds":[],"next":"…"}`,
		id, kind, outcome)
}

func TestTitleDigestTitle(t *testing.T) {
	cases := []struct {
		name  string
		setup func(d *titleDigest)
		want  string
	}{
		{
			name:  "pre-plan init",
			setup: func(d *titleDigest) { d.label = "myapp" },
			want:  "myapp · init",
		},
		{
			name:  "plan add and change",
			setup: func(d *titleDigest) { d.label = "myapp"; d.stage = stagePlan; d.added, d.changed = 3, 1 },
			want:  "myapp · plan +3 ~1 -0",
		},
		{
			name:  "plan no changes",
			setup: func(d *titleDigest) { d.label = "myapp"; d.stage = stagePlan },
			want:  "myapp · plan no changes",
		},
		{
			name:  "planned teardown",
			setup: func(d *titleDigest) { d.label = "myapp"; d.stage = stagePlan; d.destroyed = 3 },
			want:  "myapp · planned teardown -3",
		},
		{
			name:  "applied",
			setup: func(d *titleDigest) { d.label = "myapp"; d.stage = stageApplied; d.added = 2; d.destroyed = 2 },
			want:  "myapp · applied +2 ~0 -2",
		},
		{
			name:  "destroyed",
			setup: func(d *titleDigest) { d.label = "myapp"; d.stage = stageApplied; d.destroyed = 4 },
			want:  "myapp · destroyed -4",
		},
		{
			name:  "no label yet",
			setup: func(d *titleDigest) { d.stage = stagePlan; d.added = 1 },
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var d titleDigest
			tc.setup(&d)
			if got := d.title(); got != tc.want {
				t.Errorf("title() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A convergence titles its paused round with that round's counts, and once it
// converges with everything its rounds applied — a round re-vended after a restart
// counted once.
func TestIngestConvergenceRounds(t *testing.T) {
	c := &sessionTitleCurator{}
	c.digest.label = "core"
	feed := func(tool, out string) bool { return c.ingest(tool, out) }

	feed("turf_up", awaiting("up", "p-1", 1, 4, 0, 0))
	if got, want := c.digest.title(), "core · plan +4 ~0 -0"; got != want {
		t.Fatalf("round 1: %q, want %q", got, want)
	}
	feed("turf_apply", awaiting("up", "p-1", 2, 1, 1, 1))
	feed("turf_apply", awaiting("up", "p-1", 2, 1, 1, 1)) // re-vended, same round
	if got, want := c.digest.title(), "core · plan +1 ~1 -1"; got != want {
		t.Fatalf("round 2: %q, want %q", got, want)
	}
	feed("turf_apply", ended("up", "p-1", "converged"))
	if got, want := c.digest.title(), "core · applied +5 ~1 -1"; got != want {
		t.Fatalf("converged: %q, want %q", got, want)
	}

	// A new convergence starts over; one that fails leaves the title on its plan.
	feed("turf_up", awaiting("up", "p-2", 1, 0, 2, 0))
	if handled := feed("turf_apply", ended("up", "p-2", "failed")); handled {
		t.Error("a failed convergence should not retitle")
	}
	if got, want := c.digest.title(), "core · plan +0 ~2 -0"; got != want {
		t.Fatalf("after a failure: %q, want %q", got, want)
	}

	// An auto-approved run (turf up --yes) converges in one call, so no round's plan
	// was ever seen: a destroy still knows it deleted what it applied, and a deploy
	// says how many changes it applied rather than claiming none.
	autoUp := `{"convergence_id":"p-3","workspace_alias":"default","kind":"up","outcome":"converged",
		"rounds":[{"round":1,"status":"applied","approval":"auto","planned":4,"deferred":1,"applied":4},
			{"round":2,"status":"applied","approval":"auto","planned":2,"deferred":0,"applied":2}],"next":"…"}`
	feed("turf_up", autoUp)
	if got, want := c.digest.title(), "core · applied 6 change(s)"; got != want {
		t.Fatalf("auto-approved up: %q, want %q", got, want)
	}
	autoDestroy := `{"convergence_id":"d-3","workspace_alias":"default","kind":"destroy","outcome":"converged",
		"rounds":[{"round":1,"status":"applied","approval":"auto","planned":6,"deferred":0,"applied":6}],"next":"…"}`
	feed("turf_destroy", autoDestroy)
	if got, want := c.digest.title(), "core · destroyed -6"; got != want {
		t.Fatalf("auto-approved destroy: %q, want %q", got, want)
	}
	// And a deploy that applied nothing really did change nothing.
	feed("turf_up", upNoChange)
	if got, want := c.digest.title(), "core · applied no changes"; got != want {
		t.Fatalf("no-change up: %q, want %q", got, want)
	}
	feed("turf_up", awaiting("up", "p-2", 1, 0, 2, 0))

	// Upkeep runs — refresh, import, invoke — do not title the session.
	for _, kind := range []string{"refresh", "import", "invoke"} {
		if feed("turf_apply", awaiting(kind, "x-"+kind, 1, 1, 0, 0)) {
			t.Errorf("a %s convergence should not retitle", kind)
		}
	}
	if got, want := c.digest.title(), "core · plan +0 ~2 -0"; got != want {
		t.Fatalf("after upkeep: %q, want %q", got, want)
	}
}

func TestAutoTitlePattern(t *testing.T) {
	// Every shape title() can produce must be recognized (or be the bare label /
	// empty) so the auto-detect regex can't drift from the format.
	digests := []titleDigest{
		{label: "a"},
		{label: "a", stage: stagePlan, added: 2, changed: 1, destroyed: 3},
		{label: "a", stage: stagePlan}, // no changes
		{label: "a", stage: stagePlan, destroyed: 4},
		{label: "a", stage: stageApplied, added: 2, destroyed: 3},
		{label: "a", stage: stageApplied, destroyed: 4},
		{label: "a", stage: stageApplied, appliedOnly: 6},
	}
	for _, d := range digests {
		got := d.title()
		if got == "" || got == d.label || isAutoTitle(got) {
			continue
		}
		t.Errorf("title() = %q is neither empty, the label, nor matched by isAutoTitle", got)
	}

	// A legacy "· promoted" title (from a turf that still had config_promote) stays
	// curator-owned, so resuming such a session keeps updating its title.
	if !isAutoTitle("a · promoted") {
		t.Error(`isAutoTitle("a · promoted") = false, want true (legacy auto-title shape)`)
	}

	// Human titles must NOT be mistaken for auto titles.
	for _, human := range []string{"My deploy", "prod rollout", "actions", "fix the bug · later"} {
		if isAutoTitle(human) {
			t.Errorf("isAutoTitle(%q) = true, want false", human)
		}
	}
}

func TestSessionLabel(t *testing.T) {
	cases := []struct {
		name                                  string
		wsAlias, cfgAlias, wsName, path, want string
	}{
		// The rungs, each claimed in turn.
		{"workspace alias wins", "prod", "app", "production", "/some/dir", "prod"},
		{"config alias next", "", "app", "production", "/some/dir", "app"},
		{"real workspace name next", "", "", "production", "/some/dir", "production"},
		{"path base", "", "", "", "/Users/x/envs/actions", "actions"},
		{"relative path base", "", "", "", "envs/prod/myapp", "myapp"},
		{"dot path falls back to cwd base", "", "", "", ".", expectedCwdAlias(t)},

		// The rung-3 exclusion, which is the whole point: the server sends
		// workspace on every init and it is almost always "default",
		// so treating it as a label would title every session identically and
		// leave the path fallbacks unreachable.
		{"default workspace name is not a label", "", "", "default", "envs/prod/myapp", "myapp"},
		{"default name with no path falls to cwd", "", "", "default", ".", expectedCwdAlias(t)},
		{"a real alias still beats the default name", "staging", "", "default", "envs/prod/myapp", "staging"},

		// The alias is what tells two workspaces on one configuration apart —
		// exactly the case where the server's own fixtures leave the name "default".
		{"two workspaces, one config", "prod", "app", "default", "envs/prod/myapp", "prod"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sessionLabel(tc.wsAlias, tc.cfgAlias, tc.wsName, tc.path)
			if got != tc.want {
				t.Errorf("sessionLabel(%q,%q,%q,%q) = %q, want %q",
					tc.wsAlias, tc.cfgAlias, tc.wsName, tc.path, got, tc.want)
			}
		})
	}
}

// expectedCwdAlias returns the expected cwd-base fallback for the dot-path case.
func expectedCwdAlias(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return cleanBase(cwd)
}

// TestIngestTracksTools asserts ingest reports the tools it handles (and only
// those), and that the digest evolves through a full deploy.
func TestIngestTracksTools(t *testing.T) {
	c := &sessionTitleCurator{}

	steps := []struct {
		tool    string
		output  string
		handled bool
	}{
		{"turf_init", initOut("envs/prod/myapp"), true},
		// Realistic: the server echoes both fields, and the name is the default
		// slot even when the caller named the workspace. The label must NOT move —
		// init already set it, which is what the `label == ""` guard is for.
		{"turf_open", `{"workspace_alias":"prod","workspace_name":"default","config_alias":"app"}`, true},
		{"turf_up", awaiting("up", "p-1", 1, 3, 1, 0), true},
		{"turf_apply", ended("up", "p-1", "converged"), true},
		{"turf_state_list", `{}`, false}, // untracked tool
	}
	for i, s := range steps {
		if got := c.ingest(s.tool, s.output); got != s.handled {
			t.Errorf("step %d (%s): handled = %v, want %v", i, s.tool, got, s.handled)
		}
	}
	if c.digest.label != "myapp" {
		t.Errorf("label = %q, want %q", c.digest.label, "myapp")
	}
	if got, want := c.digest.title(), "myapp · applied +3 ~1 -0"; got != want {
		t.Errorf("title() = %q, want %q", got, want)
	}
}

// The configuration's own alias outranks the config directory's base name: it is
// what the caller chose to call this workflow, and init always sends it.
func TestCuratorPrefersConfigAlias(t *testing.T) {
	c := newSessionTitleCurator(nil)
	var titles []string
	c.setEmitter(func(title string) { titles = append(titles, title) })

	sess := session.New()
	ctx := context.Background()
	c.OnRunStart(ctx, sess)

	c.OnEvent(ctx, sess, respEvent("turf_init",
		`{"config_alias":"demo","workspace":"default","path":"/x/tmpdir001"}`))

	if got, want := sess.Title, "demo · init"; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
}

// The turf_open branch is the RESUME path: the workspace is already open
// server-side and the turn goes straight to reopening it, so init never
// runs and the branch is the only thing that can label the session. It is also
// the one place the workspace alias is available — and the case that motivates
// preferring it, since the state slot is "default" for every workspace here.
func TestCuratorLabelsFromWorkspaceOpenOnResume(t *testing.T) {
	for _, tc := range []struct {
		name, output, want string
	}{
		{
			"alias wins over the default state slot",
			`{"workspace_alias":"prod","workspace_name":"default","config_alias":"app","config_path":"/x/myapp"}`,
			"prod · init",
		},
		{
			"no alias falls through to the config alias",
			`{"workspace_alias":"","workspace_name":"default","config_alias":"app","config_path":"/x/myapp"}`,
			"app · init",
		},
		{
			"a real tofu workspace still names the session",
			`{"workspace_alias":"","workspace_name":"production","config_alias":"","config_path":"/x/myapp"}`,
			"production · init",
		},
		{
			"nothing but the path",
			`{"workspace_alias":"","workspace_name":"default","config_alias":"","config_path":"/x/myapp"}`,
			"myapp · init",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newSessionTitleCurator(nil)
			sess := session.New()
			ctx := context.Background()
			c.OnRunStart(ctx, sess)

			c.OnEvent(ctx, sess, respEvent("turf_open", tc.output))

			if got := sess.Title; got != tc.want {
				t.Errorf("title = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCuratorRetitlesEndToEnd drives OnEvent through fixture core's recorded
// deploy — two rounds, the second planning what the first deferred — and asserts the
// exact title lands via the emitter at each step.
func TestCuratorRetitlesEndToEnd(t *testing.T) {
	c := newSessionTitleCurator(nil) // nil store: emitter is the observable sink
	var titles []string
	c.setEmitter(func(title string) { titles = append(titles, title) })

	sess := session.New()
	ctx := context.Background()
	c.OnRunStart(ctx, sess)

	feed := func(tool, out string) { c.OnEvent(ctx, sess, respEvent(tool, out)) }
	feed("turf_init", initOut("/x/core"))
	feed("turf_up", upRound1)
	feed("turf_apply", applyRound2)
	feed("turf_apply", applyConverged)

	want := []string{
		"core · init",
		"core · plan +4 ~0 -0",
		"core · plan +2 ~0 -0",
		"core · applied +6 ~0 -0",
	}
	if strings.Join(titles, "|") != strings.Join(want, "|") {
		t.Fatalf("emitted titles = %v, want %v", titles, want)
	}
	if sess.Title != want[len(want)-1] {
		t.Errorf("session title = %q, want %q", sess.Title, want[len(want)-1])
	}
}

func TestCuratorTitlesDestroy(t *testing.T) {
	c := newSessionTitleCurator(nil)
	var last string
	c.setEmitter(func(title string) { last = title })

	sess := session.New()
	ctx := context.Background()
	c.OnRunStart(ctx, sess)
	c.OnEvent(ctx, sess, respEvent("turf_init", initOut("/x/core")))
	c.OnEvent(ctx, sess, respEvent("turf_destroy", destroyRound1))
	if last != "core · planned teardown -6" {
		t.Errorf("after destroy plan: %q", last)
	}
	c.OnEvent(ctx, sess, respEvent("turf_apply", destroyConverged))
	if last != "core · destroyed -6" {
		t.Errorf("after destroy apply: %q", last)
	}
}

// TestCuratorRespectsUserTitle: once the user sets a title, a later milestone
// must not overwrite it.
func TestCuratorRespectsUserTitle(t *testing.T) {
	c := newSessionTitleCurator(nil)
	var writes []string
	c.setEmitter(func(title string) { writes = append(writes, title) })

	sess := session.New()
	ctx := context.Background()
	c.OnRunStart(ctx, sess)
	c.OnEvent(ctx, sess, respEvent("turf_init", initOut("/x/actions")))
	c.OnEvent(ctx, sess, respEvent("turf_up", awaiting("up", "p-1", 1, 1, 0, 0)))
	if sess.Title != "actions · plan +1 ~0 -0" {
		t.Fatalf("precondition: curator title = %q", sess.Title)
	}

	// User renames the session.
	sess.Title = "My deploy"
	nWrites := len(writes)

	// A later milestone must leave the manual title alone.
	c.OnEvent(ctx, sess, respEvent("turf_apply", ended("up", "p-1", "converged")))
	if sess.Title != "My deploy" {
		t.Errorf("curator overwrote a user-set title: %q", sess.Title)
	}
	if len(writes) != nWrites {
		t.Errorf("curator wrote %d more titles after the user's set: %v", len(writes)-nWrites, writes[nWrites:])
	}
}

// TestCuratorResumeAutoTitleUpdates: a session resumed with a prior *auto* title
// is still re-titled by a new milestone (and init must not downgrade it to the
// bare label in the meantime).
func TestCuratorResumeAutoTitleUpdates(t *testing.T) {
	c := newSessionTitleCurator(nil)
	var last string
	c.setEmitter(func(title string) { last = title })

	sess := session.New()
	sess.Title = "actions · applied +2 ~0 -0" // as if loaded on resume
	ctx := context.Background()
	c.OnRunStart(ctx, sess)

	c.OnEvent(ctx, sess, respEvent("turf_init", initOut("/x/actions")))
	if sess.Title != "actions · applied +2 ~0 -0" {
		t.Errorf("init downgraded a resumed title to %q", sess.Title)
	}
	c.OnEvent(ctx, sess, respEvent("turf_up", awaiting("up", "p-9", 1, 1, 0, 0)))
	if last != "actions · plan +1 ~0 -0" || sess.Title != "actions · plan +1 ~0 -0" {
		t.Errorf("resumed session not re-titled by new plan: last=%q title=%q", last, sess.Title)
	}
}

// TestCuratorResumeHumanTitlePreserved: a session resumed with a *human* title is
// left untouched even by new milestones — the cross-resume improvement over the
// old flag/baseline approach.
func TestCuratorResumeHumanTitlePreserved(t *testing.T) {
	c := newSessionTitleCurator(nil)
	fired := false
	c.setEmitter(func(string) { fired = true })

	sess := session.New()
	sess.Title = "My deploy"
	ctx := context.Background()
	c.OnRunStart(ctx, sess)
	c.OnEvent(ctx, sess, respEvent("turf_init", initOut("/x/actions")))
	c.OnEvent(ctx, sess, respEvent("turf_up", awaiting("up", "p-1", 1, 1, 0, 0)))
	c.OnEvent(ctx, sess, respEvent("turf_apply", ended("up", "p-1", "converged")))
	if sess.Title != "My deploy" || fired {
		t.Errorf("human title not preserved on resume: title=%q fired=%v", sess.Title, fired)
	}
}

// TestCuratorIgnoresSubSessions checks that a delegated sub-session (ParentID
// set) neither re-pins the curator via OnRunStart nor retitles via its tool
// activity.
func TestCuratorIgnoresSubSessions(t *testing.T) {
	c := newSessionTitleCurator(nil)
	fired := false
	c.setEmitter(func(string) { fired = true })

	root := session.New()
	c.OnRunStart(context.Background(), root)

	// A sub-session's OnRunStart must not re-pin the root (IsSubSession guard),
	// and its tool activity must not retitle.
	sub := session.New(session.WithParentID(root.ID))
	c.OnRunStart(context.Background(), sub)
	c.OnEvent(context.Background(), sub, respEvent("turf_init", initOut("/x/other")))

	if fired {
		t.Error("a sub-session tool result retitled the root session")
	}
	if root.Title != "" {
		t.Errorf("root title changed to %q from sub-session activity", root.Title)
	}
	if c.rootID != root.ID {
		t.Errorf("sub-session re-pinned curator: rootID=%q, want %q", c.rootID, root.ID)
	}
}

// TestCuratorRetitlesAfterClear: a fresh top-level session (as /clear or /new
// produce) re-pins the curator and gets its own title; the prior session's
// digest does not leak.
func TestCuratorRetitlesAfterClear(t *testing.T) {
	c := newSessionTitleCurator(nil)
	var last string
	c.setEmitter(func(title string) { last = title })
	ctx := context.Background()

	first := session.New()
	c.OnRunStart(ctx, first)
	c.OnEvent(ctx, first, respEvent("turf_init", initOut("/x/actions")))
	c.OnEvent(ctx, first, respEvent("turf_up", awaiting("up", "p-1", 1, 1, 0, 0)))
	if last != "actions · plan +1 ~0 -0" {
		t.Fatalf("precondition: first title = %q", last)
	}

	// /clear: a brand-new top-level session on the same curator.
	second := session.New()
	c.OnRunStart(ctx, second)
	c.OnEvent(ctx, second, respEvent("turf_init", initOut("/x/webapp")))
	c.OnEvent(ctx, second, respEvent("turf_up", awaiting("up", "p-2", 1, 2, 0, 0)))
	if second.Title != "webapp · plan +2 ~0 -0" || last != "webapp · plan +2 ~0 -0" {
		t.Errorf("post-clear session not titled: title=%q last=%q", second.Title, last)
	}
	if first.Title != "actions · plan +1 ~0 -0" {
		t.Errorf("prior session title changed after clear: %q", first.Title)
	}
}

// TestTitleCuratorToolsExistOnServer is a drift guard: every tool the curator
// keys on must be a real server tool (no stale names). Skips when the server
// binary is absent (see serverTools in permissions_test.go).
func TestTitleCuratorToolsExistOnServer(t *testing.T) {
	byName := serverTools(t)
	for _, n := range titleCuratorTools {
		if !strings.HasPrefix(n, "turf_") {
			t.Errorf("curator tool %q is missing the turf_ prefix", n)
		}
		if _, ok := byName[n]; !ok {
			t.Errorf("curator tool %q is not a server tool (stale — update ingest and titleCuratorTools)", n)
		}
	}
}
