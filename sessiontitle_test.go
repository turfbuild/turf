package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker-agent/pkg/runtime"
	"github.com/docker/docker-agent/pkg/session"
	"github.com/docker/docker-agent/pkg/tools"
	"github.com/docker/docker-agent/pkg/tools/mcp"
)

// respEvent builds a completed tool-call response event carrying output JSON,
// as the runtime hands it to an EventObserver.
func respEvent(tool, output string) *runtime.ToolCallResponseEvent {
	return &runtime.ToolCallResponseEvent{
		ToolDefinition: tools.Tool{Name: tool},
		Response:       output,
	}
}

func TestTitleDigestTitle(t *testing.T) {
	cases := []struct {
		name  string
		setup func(d *titleDigest)
		want  string
	}{
		{
			name:  "pre-plan init",
			setup: func(d *titleDigest) { d.alias = "myapp" },
			want:  "myapp · init",
		},
		{
			name:  "plan add and change",
			setup: func(d *titleDigest) { d.alias = "myapp"; d.stage = stagePlan; d.added, d.changed = 3, 1 },
			want:  "myapp · plan +3 ~1 -0",
		},
		{
			name:  "plan no changes",
			setup: func(d *titleDigest) { d.alias = "myapp"; d.stage = stagePlan },
			want:  "myapp · plan no changes",
		},
		{
			name:  "planned teardown",
			setup: func(d *titleDigest) { d.alias = "myapp"; d.stage = stagePlan; d.destroyed = 3 },
			want:  "myapp · planned teardown -3",
		},
		{
			name:  "applied",
			setup: func(d *titleDigest) { d.alias = "myapp"; d.stage = stageApplied; d.added = 2; d.destroyed = 2 },
			want:  "myapp · applied +2 ~0 -2",
		},
		{
			name:  "destroyed",
			setup: func(d *titleDigest) { d.alias = "myapp"; d.stage = stageApplied; d.destroyed = 4 },
			want:  "myapp · destroyed -4",
		},
		{
			name:  "promoted",
			setup: func(d *titleDigest) { d.alias = "myapp"; d.stage = stagePromoted },
			want:  "myapp · promoted",
		},
		{
			name:  "no alias yet",
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

func TestTallyPlanActions(t *testing.T) {
	var d titleDigest
	// A replace counts as one add + one destroy (terraform convention). Fixture:
	// 2 create, 1 update, 1 delete, 1 replace (+1/-1), read/no-op ignored.
	d.tallyPlan([]resourcePlanEntry{
		{Action: "create"}, {Action: "create"}, {Action: "update"},
		{Action: "delete"}, {Action: "replace"}, {Action: "read"}, {Action: "no-op"},
	})
	if d.added != 3 || d.changed != 1 || d.destroyed != 2 {
		t.Errorf("tally = +%d ~%d -%d, want +3 ~1 -2", d.added, d.changed, d.destroyed)
	}
}

func TestAutoTitlePattern(t *testing.T) {
	// Every shape title() can produce must be recognized (or be the bare alias /
	// empty) so the auto-detect regex can't drift from the format.
	digests := []titleDigest{
		{alias: "a"},
		{alias: "a", stage: stagePlan, added: 2, changed: 1, destroyed: 3},
		{alias: "a", stage: stagePlan}, // no changes
		{alias: "a", stage: stagePlan, destroyed: 4},
		{alias: "a", stage: stageApplied, added: 2, destroyed: 3},
		{alias: "a", stage: stageApplied, destroyed: 4},
		{alias: "a", stage: stagePromoted},
	}
	for _, d := range digests {
		got := d.title()
		if got == "" || got == d.alias || isAutoTitle(got) {
			continue
		}
		t.Errorf("title() = %q is neither empty, the alias, nor matched by isAutoTitle", got)
	}

	// Human titles must NOT be mistaken for auto titles.
	for _, human := range []string{"My deploy", "prod rollout", "actions", "fix the bug · later"} {
		if isAutoTitle(human) {
			t.Errorf("isAutoTitle(%q) = true, want false", human)
		}
	}
}

func TestConfigAlias(t *testing.T) {
	cases := []struct {
		name, workspace, path, want string
	}{
		{"workspace name wins", "prod", "/some/dir", "prod"},
		{"path base", "", "/Users/x/envs/actions", "actions"},
		{"relative path base", "", "envs/prod/myapp", "myapp"},
		{"dot path falls back to cwd base", "", ".", expectedCwdAlias(t)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := configAlias(tc.workspace, tc.path); got != tc.want {
				t.Errorf("configAlias(%q,%q) = %q, want %q", tc.workspace, tc.path, got, tc.want)
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
		{"turf_config_init", `{"path":"envs/prod/myapp"}`, true},
		{"turf_workspace_open", `{"workspace_alias":"prod"}`, true},
		{"turf_plan_new", `{"resources":[{"action":"create"},{"action":"create"},{"action":"create"},{"action":"update"}]}`, true},
		{"turf_plan_approve", `{"effect_count":4}`, true},
		{"turf_effect_apply", `{}`, true},
		{"turf_effect_apply", `{}`, true},
		{"turf_effect_apply", `{}`, true},
		{"turf_effect_apply", `{}`, true}, // 4th of 4 → apply complete
		{"turf_state_list", `{}`, false},  // untracked tool
	}
	for i, s := range steps {
		if got := c.ingest(s.tool, s.output); got != s.handled {
			t.Errorf("step %d (%s): handled = %v, want %v", i, s.tool, got, s.handled)
		}
	}
	if c.digest.alias != "myapp" {
		t.Errorf("alias = %q, want %q", c.digest.alias, "myapp")
	}
	if got, want := c.digest.title(), "myapp · applied +3 ~1 -0"; got != want {
		t.Errorf("title() = %q, want %q", got, want)
	}
}

// TestCuratorRetitlesEndToEnd drives OnEvent and asserts the exact deterministic
// title lands via the emitter at each step, including the pre-plan alias and the
// no-write-until-last-effect behavior.
func TestCuratorRetitlesEndToEnd(t *testing.T) {
	c := newSessionTitleCurator(nil) // nil store: emitter is the observable sink
	var titles []string
	c.setEmitter(func(title string) { titles = append(titles, title) })

	sess := session.New()
	ctx := context.Background()
	c.OnRunStart(ctx, sess)

	feed := func(tool, out string) { c.OnEvent(ctx, sess, respEvent(tool, out)) }
	feed("turf_config_init", `{"path":"/x/actions"}`)
	feed("turf_plan_new", `{"resources":[{"action":"create"},{"action":"create"},{"action":"delete"}]}`)
	feed("turf_plan_approve", `{"effect_count":3}`)
	feed("turf_effect_apply", `{}`) // partial — title must not change
	feed("turf_effect_apply", `{}`)
	feed("turf_effect_apply", `{}`) // last effect → applied

	want := []string{
		"actions · init",          // config_init
		"actions · plan +2 ~0 -1", // plan_new
		"actions · applied +2 ~0 -1",
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
	c.OnEvent(ctx, sess, respEvent("turf_config_init", `{"path":"/x/myapp"}`))
	c.OnEvent(ctx, sess, respEvent("turf_replan", `{"resources":[{"action":"delete"},{"action":"delete"}]}`))
	if last != "myapp · planned teardown -2" {
		t.Errorf("after destroy plan: %q", last)
	}
	c.OnEvent(ctx, sess, respEvent("turf_plan_approve", `{"effect_count":2}`))
	c.OnEvent(ctx, sess, respEvent("turf_effect_apply", `{}`))
	c.OnEvent(ctx, sess, respEvent("turf_effect_apply", `{}`))
	if last != "myapp · destroyed -2" {
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
	c.OnEvent(ctx, sess, respEvent("turf_config_init", `{"path":"/x/actions"}`))
	c.OnEvent(ctx, sess, respEvent("turf_plan_new", `{"resources":[{"action":"create"}]}`))
	if sess.Title != "actions · plan +1 ~0 -0" {
		t.Fatalf("precondition: curator title = %q", sess.Title)
	}

	// User renames the session.
	sess.Title = "My deploy"
	nWrites := len(writes)

	// A later milestone must leave the manual title alone.
	c.OnEvent(ctx, sess, respEvent("turf_plan_approve", `{"effect_count":1}`))
	c.OnEvent(ctx, sess, respEvent("turf_effect_apply", `{}`))
	if sess.Title != "My deploy" {
		t.Errorf("curator overwrote a user-set title: %q", sess.Title)
	}
	if len(writes) != nWrites {
		t.Errorf("curator wrote %d more titles after the user's set: %v", len(writes)-nWrites, writes[nWrites:])
	}
}

// TestCuratorResumeAutoTitleUpdates: a session resumed with a prior *auto* title
// is still re-titled by a new milestone (and config_init must not downgrade it
// to the bare alias in the meantime).
func TestCuratorResumeAutoTitleUpdates(t *testing.T) {
	c := newSessionTitleCurator(nil)
	var last string
	c.setEmitter(func(title string) { last = title })

	sess := session.New()
	sess.Title = "actions · applied +2 ~0 -0" // as if loaded on resume
	ctx := context.Background()
	c.OnRunStart(ctx, sess)

	c.OnEvent(ctx, sess, respEvent("turf_config_init", `{"path":"/x/actions"}`))
	if sess.Title != "actions · applied +2 ~0 -0" {
		t.Errorf("config_init downgraded a resumed title to %q", sess.Title)
	}
	c.OnEvent(ctx, sess, respEvent("turf_replan", `{"resources":[{"action":"create"}]}`))
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
	c.OnEvent(ctx, sess, respEvent("turf_config_init", `{"path":"/x/actions"}`))
	c.OnEvent(ctx, sess, respEvent("turf_plan_new", `{"resources":[{"action":"create"}]}`))
	c.OnEvent(ctx, sess, respEvent("turf_plan_approve", `{"effect_count":1}`))
	c.OnEvent(ctx, sess, respEvent("turf_effect_apply", `{}`))
	if sess.Title != "My deploy" || fired {
		t.Errorf("human title not preserved on resume: title=%q fired=%v", sess.Title, fired)
	}
}

// TestCuratorIgnoresSubSessions checks that tool activity on a session other
// than the pinned root does not retitle.
func TestCuratorIgnoresSubSessions(t *testing.T) {
	c := newSessionTitleCurator(nil)
	fired := false
	c.setEmitter(func(string) { fired = true })

	root := session.New()
	c.OnRunStart(context.Background(), root)

	sub := session.New()
	c.OnEvent(context.Background(), sub, respEvent("turf_config_init", `{"path":"/x/other"}`))

	if fired {
		t.Error("a sub-session tool result retitled the root session")
	}
	if root.Title != "" {
		t.Errorf("root title changed to %q from sub-session activity", root.Title)
	}
}

// TestTitleCuratorToolsExistOnServer is a drift guard: every tool the curator
// keys on must be a real server tool (no stale names). Skips when the server
// binary is absent, mirroring examples_test.go / permissions_test.go.
func TestTitleCuratorToolsExistOnServer(t *testing.T) {
	path, err := resolveMCPServer()
	if err != nil {
		t.Skipf("turf-mcp-server not resolvable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ts := mcp.NewToolsetCommand("turf", path, []string{"--transport", "stdio"}, os.Environ(), "")
	if err := ts.Start(ctx); err != nil {
		t.Fatalf("start turf-mcp-server: %v", err)
	}
	defer func() { _ = ts.Stop(context.Background()) }()

	toolList, err := ts.Tools(ctx)
	if err != nil {
		t.Fatalf("list turf-mcp-server tools: %v", err)
	}
	byName := make(map[string]struct{}, len(toolList))
	for _, tl := range toolList {
		byName[tl.Name] = struct{}{}
	}
	for _, n := range titleCuratorTools {
		if !strings.HasPrefix(n, "turf_") {
			t.Errorf("curator tool %q is missing the turf_ prefix", n)
		}
		if _, ok := byName[n]; !ok {
			t.Errorf("curator tool %q is not a server tool (stale — update ingest and titleCuratorTools)", n)
		}
	}
}
