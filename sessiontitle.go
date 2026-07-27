package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/docker/docker-agent/pkg/runtime"
	"github.com/docker/docker-agent/pkg/session"
)

// sessionTitleCurator owns a turf session's title end-to-end. turf does not wire
// cagent's built-in titler (its LLM one-shot 400s on turf's default thinking
// model and, worse, invents resource counts), so this is the only titling path.
//
// Titles are composed deterministically in the `terraform plan` idiom from what
// turf actually did: the config alias, then the most recent plan/apply/destroy
// outcome with accurate `+A ~C -D` counts — e.g. "actions · plan +2 ~0 -2",
// "actions · applied +2 ~0 -2", "actions · destroyed -4". The counts come from
// the same renderer view structs the TUI plan view parses, so they always match
// the real plan. A chat-only session (no turf tool) has no deterministic signal
// and stays untitled by design.
//
// It is a runtime.EventObserver (registered via runtime.WithEventObserver in
// createAgentRuntime). OnEvent runs synchronously on the runtime's event-forward
// goroutine; titling is a cheap parse + a single indexed store write, so no
// goroutine/queue is needed.
//
// Titles persist through the session store (durable — correct in the /sessions
// browser, on resume, and for the headless up/destroy path). In an interactive
// TUI, tui.go also installs an emit callback so the sidebar/window title refresh
// live (the store write alone does not emit the SessionTitleEvent the TUI reads).
type sessionTitleCurator struct {
	store session.Store

	mu sync.Mutex
	// rootID pins the curator to the current top-level session. It is re-pinned
	// whenever a fresh top-level session appears on the same runtime (/clear,
	// /new, /fork); sub-session (delegated task) events are ignored so their tool
	// activity never retitles the user's session.
	rootID string
	digest titleDigest
	// emit, when set by the interactive TUI, live-refreshes the title. nil in
	// headless runs, where the store write is the only sink.
	emit func(string)
}

// titleCuratorTools are the turf tool results ingest inspects. Kept as an
// explicit list so a drift guard (sessiontitle_test.go) can tie it to the live
// server tool set — a server-side rename then fails CI instead of silently
// stopping the curator from titling. Keep in step with the switch in ingest.
var titleCuratorTools = []string{
	"turf_config_init",
	"turf_workspace_open",
	"turf_plan_new",
	"turf_replan",
	"turf_plan_approve",
	"turf_effect_apply",
	"turf_config_promote",
}

func newSessionTitleCurator(store session.Store) *sessionTitleCurator {
	return &sessionTitleCurator{store: store}
}

// setEmitter installs the live-refresh callback. Called by the interactive TUI
// after the app is built; safe to call concurrently with OnEvent.
func (c *sessionTitleCurator) setEmitter(emit func(string)) {
	c.mu.Lock()
	c.emit = emit
	c.mu.Unlock()
}

// OnRunStart pins the curator to the current top-level session. It re-pins (and
// resets the accumulated digest) when the top-level session ID changes — e.g.
// /clear, /new, or /fork swaps in a fresh session on the same runtime — but never
// on a delegated sub-session, whose tool activity must not retitle the user's
// session. OnRunStart fires on every top-level turn, so the reset is gated on an
// actual ID change to keep the digest stable across a single session's turns.
func (c *sessionTitleCurator) OnRunStart(_ context.Context, sess *session.Session) {
	if sess.IsSubSession() {
		return
	}
	c.mu.Lock()
	if c.rootID != sess.ID {
		c.rootID = sess.ID
		c.digest = titleDigest{}
	}
	c.mu.Unlock()
}

// OnEvent digests a turf tool result and, when it changes the composed title,
// persists the new one.
func (c *sessionTitleCurator) OnEvent(ctx context.Context, sess *session.Session, event runtime.Event) {
	resp, ok := event.(*runtime.ToolCallResponseEvent)
	if !ok {
		return
	}
	c.mu.Lock()
	root := c.rootID
	c.mu.Unlock()
	if root != "" && sess.ID != root {
		return // ignore sub-session tool activity
	}
	if resp.Result != nil && resp.Result.IsError {
		return // a failed tool call carries no outcome to title
	}
	output := resp.Response
	if output == "" && resp.Result != nil {
		output = resp.Result.Output
	}

	c.mu.Lock()
	handled := c.ingest(resp.ToolDefinition.Name, output)
	title := ""
	stage := c.digest.stage
	if handled {
		title = c.digest.title()
	}
	c.mu.Unlock()

	if title == "" || title == sess.Title {
		return
	}
	if cur := sess.Title; cur != "" {
		// Only take over a title the curator could have produced itself (isAutoTitle
		// recognizes every shape, including the "· init" pre-plan title). Anything
		// else the user set via /title or the sidebar — leave it. Stateless, so it
		// also preserves a human title on resume.
		if !isAutoTitle(cur) {
			return
		}
		// Don't downgrade an existing milestone title back to "· init" (e.g.
		// config_init on a resumed session that already has one).
		if stage == stageNone {
			return
		}
	}
	c.setTitle(ctx, sess, title)
}

// autoTitleRe matches the shapes titleDigest.title() produces beyond the bare
// alias. TestAutoTitlePattern locks it to the format so the two can't drift.
var autoTitleRe = regexp.MustCompile(`^.+ · (init|plan (\+\d+ ~\d+ -\d+|no changes)|planned teardown -\d+|applied (\+\d+ ~\d+ -\d+|no changes)|destroyed -\d+|promoted)$`)

// isAutoTitle reports whether s looks like a curator-generated milestone title
// (so the curator may replace it). The bare-alias case is handled separately.
func isAutoTitle(s string) bool {
	return autoTitleRe.MatchString(s)
}

// ingest folds one completed tool result into the digest and reports whether the
// tool is one the curator tracks. The caller holds c.mu. Pure parsing/tallying,
// unit-tested directly.
func (c *sessionTitleCurator) ingest(tool, output string) (handled bool) {
	switch tool {
	case "turf_config_init":
		var v configInitView
		if json.Unmarshal([]byte(output), &v) == nil {
			c.digest.alias = configAlias(v.Workspace.Name, v.Path)
		}
		return true

	case "turf_workspace_open":
		var v workspaceOpenView
		if json.Unmarshal([]byte(output), &v) == nil && c.digest.alias == "" {
			if v.Name != "" {
				c.digest.alias = v.Name
			} else if v.WorkspaceAlias != "" {
				c.digest.alias = v.WorkspaceAlias
			}
		}
		return true

	case "turf_plan_new", "turf_replan":
		var v planSummaryView
		if json.Unmarshal([]byte(output), &v) != nil {
			return false
		}
		c.digest.tallyPlan(v.Resources)
		c.digest.stage = stagePlan
		c.digest.applied = 0
		c.digest.effectCount = 0
		return true

	case "turf_plan_approve":
		var v planApproveView
		if json.Unmarshal([]byte(output), &v) == nil {
			c.digest.effectCount = v.EffectCount
		}
		return true // records the target count; title unchanged until effects land

	case "turf_effect_apply":
		c.digest.applied++
		// The plan title holds until the last effect of the approved plan lands,
		// which flips the stage to applied/destroyed.
		if c.digest.effectCount > 0 && c.digest.applied >= c.digest.effectCount {
			c.digest.stage = stageApplied
		}
		return true

	case "turf_config_promote":
		c.digest.stage = stagePromoted
		return true
	}
	return false
}

// setTitle is the single title sink: it always persists (durable, headless-safe)
// and then, if an emitter is installed, fires it for live TUI refresh.
func (c *sessionTitleCurator) setTitle(ctx context.Context, sess *session.Session, title string) {
	if c.store != nil {
		if err := c.store.UpdateSessionTitle(ctx, sess.ID, title); err != nil {
			slog.WarnContext(ctx, "persisting session title", "session_id", sess.ID, "error", err)
		}
	}
	sess.Title = title
	c.mu.Lock()
	emit := c.emit
	c.mu.Unlock()
	if emit != nil {
		emit(title)
	}
}

// titleDigest accumulates the salient facts of a session's infra work. Its zero
// value is a fresh session with no activity.
type titleDigest struct {
	alias                     string
	stage                     titleStage
	added, changed, destroyed int
	effectCount               int
	applied                   int
}

type titleStage int

const (
	stageNone titleStage = iota
	stagePlan
	stageApplied
	stagePromoted
)

// configAlias derives a short, human label for the configuration. It prefers the
// workspace name, else the base of the config path — falling back to the base of
// the cwd when the path is uninformative (".", "", "/"), since the /up prompt may
// pass a relative path.
func configAlias(workspaceName, path string) string {
	if workspaceName != "" {
		return workspaceName
	}
	if base := cleanBase(path); base != "" {
		return base
	}
	if cwd, err := os.Getwd(); err == nil {
		return cleanBase(cwd)
	}
	return ""
}

func cleanBase(path string) string {
	base := filepath.Base(strings.TrimSpace(path))
	switch base {
	case ".", "/", "":
		return ""
	}
	return base
}

// tallyPlan counts a plan's resource actions into add/change/destroy buckets.
// Action words match turf's wire vocabulary (see planAction in renderers.go).
func (d *titleDigest) tallyPlan(resources []resourcePlanEntry) {
	d.added, d.changed, d.destroyed = 0, 0, 0
	for _, r := range resources {
		switch r.Action {
		case "create", "+":
			d.added++
		case "update", "~":
			d.changed++
		case "delete", "destroy", "-":
			d.destroyed++
		case "replace", "±", "∓":
			// terraform counts a replacement as one add + one destroy.
			d.added++
			d.destroyed++
		}
	}
}

// isDestroy reports whether the plan is a pure teardown (only deletes). A mixed
// plan with adds/changes is a normal apply, not a teardown.
func (d *titleDigest) isDestroy() bool {
	return d.destroyed > 0 && d.added == 0 && d.changed == 0
}

// title renders the finished session title in the terraform-plan glyph idiom,
// e.g. "actions", "actions · plan +2 ~0 -2", "actions · applied +2 ~0 -2",
// "actions · destroyed -4". Returns "" before the config alias is known.
func (d *titleDigest) title() string {
	if d.alias == "" {
		return ""
	}
	switch d.stage {
	case stagePlan:
		if d.isDestroy() {
			return d.alias + " · planned teardown " + destroyCount(d.destroyed)
		}
		return d.alias + " · plan " + d.counts()
	case stageApplied:
		if d.isDestroy() {
			return d.alias + " · destroyed " + destroyCount(d.destroyed)
		}
		return d.alias + " · applied " + d.counts()
	case stagePromoted:
		return d.alias + " · promoted"
	default:
		// Pre-plan: config known, nothing done yet. The " · init" suffix makes it
		// a regular auto-title shape (matched by isAutoTitle) rather than a bare
		// word needing a special case.
		return d.alias + " · init"
	}
}

// counts formats the full add/change/destroy tally as "+A ~C -D" (terraform-plan
// always shows all three); "no changes" when every bucket is zero.
func (d *titleDigest) counts() string {
	if d.added == 0 && d.changed == 0 && d.destroyed == 0 {
		return "no changes"
	}
	return fmt.Sprintf("+%d ~%d -%d", d.added, d.changed, d.destroyed)
}

func destroyCount(n int) string {
	return fmt.Sprintf("-%d", n)
}
