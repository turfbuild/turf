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
	return fmt.Sprintf(`{"config_alias":"default","path":%q,"workspace":"default"}`, path)
}

// awaiting is an up/destroy/apply result paused at a round's approval point, with
// the round's plan counts.
func awaiting(kind, id string, round, add, change, destroy int) string {
	return fmt.Sprintf(`{"convergence_id":%q,"workspace_alias":"default","kind":%q,"outcome":"awaiting_approval","round":%d,
		"plan":{"uri":"turf://workspaces/default/convergences/%s/rounds/%d/plan","digest":"sha256:ab","add":%d,"change":%d,"destroy":%d},
		"rounds":[{"round":%d,"status":"awaiting_approval","approval":"asked","planned":%d}],"next":"…"}`,
		id, kind, round, id, round, add, change, destroy, round, add+change+destroy)
}

// ended is a convergence result that has ended with outcome.
func ended(kind, id, outcome string) string {
	return fmt.Sprintf(`{"convergence_id":%q,"workspace_alias":"default","kind":%q,"outcome":%q,"rounds":[],"next":"…"}`,
		id, kind, outcome)
}

// plannedRun is a plan-only convergence's ending: the plan vended as at an
// approval point, the planfile beside it, nothing applied.
func plannedRun(kind, id string, add, change, destroy int) string {
	return fmt.Sprintf(`{"convergence_id":%q,"workspace_alias":"default","kind":%q,"outcome":"planned",
		"plan":{"uri":"turf://workspaces/default/convergences/%s/rounds/1/plan","digest":"sha256:ab","add":%d,"change":%d,"destroy":%d},
		"planfile":{"ref":%q,"digest":"sha256:ab","expires_at":"2026-11-02T01:00:00Z"},
		"rounds":[{"round":1,"status":"planned","planned":%d}],"ended":true,
		"next":{"tool":"apply","args":{"workspace_alias":"default","planfile":%q}}}`,
		id, kind, id, add, change, destroy, id, add+change+destroy, id)
}

// atGate is a result stopped at a round's plan gate (a start verb's, or an apply's
// when another round is needed): no plan yet.
func atGate(kind, id string, round int) string {
	return fmt.Sprintf(`{"convergence_id":%q,"workspace_alias":"default","kind":%q,"outcome":"awaiting_plan","round":%d,
		"rounds":[{"round":%d,"status":"awaiting_plan"}],"ended":false,"next":{"tool":"plan","args":{"convergence_id":%q}}}`,
		id, kind, round, round, id)
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
			name: "declined",
			setup: func(d *titleDigest) {
				d.label = "myapp"
				d.stage = stageEnded
				d.ending, d.endedPlanned = "declined", true
				d.added, d.changed, d.destroyed = 2, 3, 2
			},
			want: "myapp · declined +2 ~3 -2",
		},
		{
			name: "cancelled with nothing to change",
			setup: func(d *titleDigest) {
				d.label = "myapp"
				d.stage = stageEnded
				d.ending, d.endedPlanned = "cancelled", true
			},
			want: "myapp · cancelled no changes",
		},
		{
			name: "cancelled at a later plan gate, after a round applied",
			setup: func(d *titleDigest) {
				d.label = "myapp"
				d.stage = stageEnded
				d.ending = "cancelled"
				d.anyPrior, d.prior = true, planCounts{added: 4}
			},
			want: "myapp · applied +4 ~0 -0 · cancelled",
		},
		{
			name: "declined at a later approval point, after a round applied",
			setup: func(d *titleDigest) {
				d.label = "myapp"
				d.stage = stageEnded
				d.ending, d.endedPlanned = "declined", true
				d.added = 2
				d.anyPrior, d.prior = true, planCounts{added: 4, changed: 1}
			},
			want: "myapp · applied +4 ~1 -0 · declined +2 ~0 -0",
		},
		{
			name: "cancelled after rounds never seen planned",
			setup: func(d *titleDigest) {
				d.label = "myapp"
				d.stage = stageEnded
				d.ending = "cancelled"
				d.anyPrior, d.priorOnly = true, 6
			},
			want: "myapp · applied 6 change(s) · cancelled",
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

	// The start verb stops at the gate with no plan: nothing to title yet.
	if feed("turf_up", atGate("up", "p-1", 1)) || c.digest.title() != "core · init" {
		t.Fatalf("a round at its plan gate has no counts: %q", c.digest.title())
	}
	feed("turf_plan", awaiting("up", "p-1", 1, 4, 0, 0))
	if got, want := c.digest.title(), "core · plan +4 ~0 -0"; got != want {
		t.Fatalf("round 1: %q, want %q", got, want)
	}
	// The apply stops at round 2's gate; a plan that returned at the deadline is
	// still planning: neither moves the title.
	if feed("turf_apply", atGate("up", "p-1", 2)) {
		t.Error("an apply at the next gate should not retitle")
	}
	planning := `{"convergence_id":"p-1","workspace_alias":"default","kind":"up","outcome":"planning","round":2,"rounds":[],"ended":false}`
	if feed("turf_plan", planning) || c.digest.title() != "core · plan +4 ~0 -0" {
		t.Fatalf("a plan still planning should not retitle: %q", c.digest.title())
	}
	feed("turf_plan", awaiting("up", "p-1", 2, 1, 1, 1))
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

	// An ending that leaves the last plan unapplied names itself with that plan's
	// counts, so the title stops reading as a plan still waiting.
	feed("turf_up", atGate("up", "p-4", 1))
	feed("turf_plan", awaiting("up", "p-4", 1, 2, 3, 2))
	feed("turf_apply", ended("up", "p-4", "declined"))
	if got, want := c.digest.title(), "core · declined +2 ~3 -2"; got != want {
		t.Fatalf("declined: %q, want %q", got, want)
	}
	// A cancel at the plan gate planned nothing, so it titles nothing; one at the
	// approval point names the plan it left.
	feed("turf_up", atGate("up", "p-5", 1))
	if feed("turf_cancel", ended("up", "p-5", "cancelled")) || c.digest.title() != "core · declined +2 ~3 -2" {
		t.Fatalf("a cancel at the plan gate should not retitle: %q", c.digest.title())
	}
	feed("turf_plan", awaiting("up", "p-5", 1, 0, 1, 0))
	feed("turf_cancel", ended("up", "p-5", "cancelled"))
	if got, want := c.digest.title(), "core · cancelled +0 ~1 -0"; got != want {
		t.Fatalf("cancelled: %q, want %q", got, want)
	}
	// An ending after rounds that applied names what they applied first: a
	// cancel at round 2's plan gate, a decline at round 2's approval point.
	feed("turf_up", atGate("up", "p-7", 1))
	feed("turf_plan", awaiting("up", "p-7", 1, 4, 0, 0))
	feed("turf_apply", atGate("up", "p-7", 2))
	feed("turf_cancel", `{"convergence_id":"p-7","workspace_alias":"default","kind":"up","outcome":"cancelled",
		"rounds":[{"round":1,"status":"applied","approval":"user","planned":4,"applied":4},{"round":2,"status":"cancelled"}],"ended":true}`)
	if got, want := c.digest.title(), "core · applied +4 ~0 -0 · cancelled"; got != want {
		t.Fatalf("cancelled at round 2's gate: %q, want %q", got, want)
	}
	feed("turf_up", atGate("up", "p-8", 1))
	feed("turf_plan", awaiting("up", "p-8", 1, 4, 0, 0))
	feed("turf_apply", atGate("up", "p-8", 2))
	feed("turf_plan", awaiting("up", "p-8", 2, 2, 0, 0))
	feed("turf_apply", `{"convergence_id":"p-8","workspace_alias":"default","kind":"up","outcome":"declined",
		"rounds":[{"round":1,"status":"applied","approval":"user","planned":4,"applied":4},{"round":2,"status":"declined","planned":2}],"ended":true}`)
	if got, want := c.digest.title(), "core · applied +4 ~0 -0 · declined +2 ~0 -0"; got != want {
		t.Fatalf("declined at round 2: %q, want %q", got, want)
	}
	// Taken up after a restart, round 1 applied before this session saw it
	// planned: the applied count stands in.
	feed("turf_apply", `{"convergence_id":"p-9","workspace_alias":"default","kind":"up","outcome":"cancelled",
		"rounds":[{"round":1,"status":"applied","approval":"user","planned":3,"applied":3},{"round":2,"status":"cancelled"}],"ended":true}`)
	if got, want := c.digest.title(), "core · applied 3 change(s) · cancelled"; got != want {
		t.Fatalf("cancelled after an unseen round: %q, want %q", got, want)
	}
	// Stalled and exhausted applied what they planned, like converged.
	feed("turf_up", atGate("up", "p-6", 1))
	feed("turf_plan", awaiting("up", "p-6", 1, 3, 0, 0))
	feed("turf_apply", ended("up", "p-6", "exhausted"))
	if got, want := c.digest.title(), "core · applied +3 ~0 -0"; got != want {
		t.Fatalf("exhausted: %q, want %q", got, want)
	}

	// A convergence taken up after a restart converges with rounds this session never
	// saw planned: a destroy still knows it deleted what it applied, and a deploy
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
	feed("turf_plan", planNoChange)
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
		{label: "a", stage: stageEnded, ending: "declined", endedPlanned: true, added: 2, changed: 3, destroyed: 2},
		{label: "a", stage: stageEnded, ending: "cancelled", endedPlanned: true},
		{label: "a", stage: stageEnded, ending: "stale", endedPlanned: true, destroyed: 1},
		{label: "a", stage: stageEnded, ending: "planned", endedPlanned: true, added: 1},
		{label: "a", stage: stageEnded, ending: "planned", endedPlanned: true, destroyed: 6}, // a plan-only destroy: planned teardown
		{label: "a", stage: stageEnded, ending: "cancelled", anyPrior: true, prior: planCounts{added: 4}},
		{label: "a", stage: stageEnded, ending: "declined", endedPlanned: true, added: 2, anyPrior: true, prior: planCounts{added: 4, changed: 1}},
		{label: "a", stage: stageEnded, ending: "cancelled", anyPrior: true, priorOnly: 6},
		{label: "a", stage: stageEnded, ending: "stale", anyPrior: true, prior: planCounts{destroyed: 2}},
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

		// The unnamed binding's alias is "default" too, and for the same reason
		// is not a label: every unnamed session would be titled alike.
		{"default config alias is not a label", "", "default", "default", "envs/prod/myapp", "myapp"},
		{"default workspace alias is not a label", "default", "default", "default", "envs/prod/myapp", "myapp"},
		{"a named config beats the default workspace alias", "default", "app", "default", "envs/prod/myapp", "app"},
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
		{"turf_up", atGate("up", "p-1", 1), false}, // tracked, but a gate carries no plan
		{"turf_plan", awaiting("up", "p-1", 1, 3, 1, 0), true},
		{"turf_apply", ended("up", "p-1", "converged"), true},
		{"turf_up", atGate("up", "p-2", 1), false},
		{"turf_cancel", ended("up", "p-2", "cancelled"), false}, // tracked, but nothing was planned
		{"turf_state_list", `{}`, false},                        // untracked tool
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
		{
			"the unnamed workspace, as the server reports it",
			`{"workspace_alias":"default","workspace_name":"default","config_alias":"default","config_path":"/x/myapp"}`,
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
	feed("turf_up", upAwaitingPlan)
	feed("turf_plan", planRound1)
	feed("turf_apply", applyRound1Gate)
	feed("turf_plan", planRound2)
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
	c.OnEvent(ctx, sess, respEvent("turf_destroy", destroyAwaitingPlan))
	if last != "core · init" {
		t.Errorf("after destroy at its gate, nothing planned: %q", last)
	}
	c.OnEvent(ctx, sess, respEvent("turf_plan", planDestroy))
	if last != "core · planned teardown -6" {
		t.Errorf("after destroy plan: %q", last)
	}
	c.OnEvent(ctx, sess, respEvent("turf_apply", applyDestroyConverged))
	if last != "core · destroyed -6" {
		t.Errorf("after destroy apply: %q", last)
	}
}

// A declined plan titles the session with the ending and the plan's counts (the v2
// server returns a decline as an ordinary result, so it reaches the curator); a
// cancel of a convergence whose plan this session never saw leaves the title alone.
func TestCuratorTitlesADecline(t *testing.T) {
	c := newSessionTitleCurator(nil)
	var last string
	c.setEmitter(func(title string) { last = title })

	sess := session.New()
	ctx := context.Background()
	c.OnRunStart(ctx, sess)
	c.OnEvent(ctx, sess, respEvent("turf_init", initOut("/x/core")))
	c.OnEvent(ctx, sess, respEvent("turf_plan", planReplace))
	if last != "core · plan +2 ~3 -2" {
		t.Fatalf("after the replace plan: %q", last)
	}
	c.OnEvent(ctx, sess, respEvent("turf_apply", applyDeclined))
	if last != "core · declined +2 ~3 -2" {
		t.Errorf("after the decline: %q", last)
	}
	c.OnEvent(ctx, sess, respEvent("turf_cancel", cancelAtApproval))
	if last != "core · declined +2 ~3 -2" {
		t.Errorf("a cancel of a convergence never planned here retitled: %q", last)
	}
}

// TestCuratorTitlesAPlannedRun: a plan-only run ends planned with its plan
// vended, so the ending titles with the plan's counts, as a decline does; a
// plan-only destroy reads as a planned teardown.
func TestCuratorTitlesAPlannedRun(t *testing.T) {
	c := newSessionTitleCurator(nil)
	var last string
	c.setEmitter(func(title string) { last = title })

	sess := session.New()
	ctx := context.Background()
	c.OnRunStart(ctx, sess)
	c.OnEvent(ctx, sess, respEvent("turf_init", initOut("/x/core")))
	c.OnEvent(ctx, sess, respEvent("turf_up", atGate("up", "p-7", 1)))
	if last != "core · init" {
		t.Fatalf("a start verb at its gate titles nothing: %q", last)
	}
	c.OnEvent(ctx, sess, respEvent("turf_plan", plannedRun("up", "p-7", 2, 1, 0)))
	if last != "core · planned +2 ~1 -0" {
		t.Errorf("after the planned run: %q", last)
	}
	c.OnEvent(ctx, sess, respEvent("turf_plan", plannedRun("destroy", "d-8", 0, 0, 6)))
	if last != "core · planned teardown -6" {
		t.Errorf("after the planned destroy: %q", last)
	}
	// Applying the planfile converges a new convergence whose round the
	// curator never saw planned: the applied count stands in.
	c.OnEvent(ctx, sess, respEvent("turf_apply", `{"convergence_id":"p-9","workspace_alias":"default","kind":"destroy","outcome":"converged",
		"rounds":[{"round":1,"status":"applied","approval":"planfile","planned":6,"applied":6}],"ended":true}`))
	if last != "core · destroyed -6" {
		t.Errorf("after the planfile's apply: %q", last)
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
