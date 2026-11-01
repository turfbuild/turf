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
// turf actually did: the session label, then the most recent plan/apply/destroy
// outcome with accurate `+A ~C -D` counts — e.g. "actions · plan +2 ~0 -2",
// "actions · applied +2 ~0 -2", "actions · destroyed -4". The counts are the ones
// the server's own plan summary reports on an up/destroy/apply result, parsed by the
// same renderer view (convergenceView) the timeline line reads, so a title and the
// line beside it always agree. A chat-only session (no turf tool) has no deterministic signal
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
	"turf_init",
	"turf_open",
	"turf_up",
	"turf_destroy",
	"turf_plan",
	"turf_apply",
	"turf_cancel",
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
		// init on a resumed session that already has one).
		if stage == stageNone {
			return
		}
	}
	c.setTitle(ctx, sess, title)
}

// autoTitleRe matches the shapes titleDigest.title() produces beyond the bare
// label. TestAutoTitlePattern locks it to the format so the two can't drift.
// "promoted" is a legacy shape: the curator no longer produces it (config_promote
// is gone from the server), but a session titled by an older turf may still carry
// it, and recognizing it keeps that title curator-owned on resume rather than
// mistaking it for a human one and freezing it.
// The title's building blocks, for autoTitleRe: a plan's tally (planCounts.String)
// and what a convergence applied (appliedWord).
const (
	titleTally   = `(\+\d+ ~\d+ -\d+|no changes)`
	titleApplied = `(applied ` + titleTally + `|applied \d+ change\(s\)|destroyed -\d+)`
)

var autoTitleRe = regexp.MustCompile(`^.+ · (init|plan ` + titleTally + `|planned teardown -\d+|` + titleApplied +
	`|(` + titleApplied + ` · )?(declined|cancelled|stale|planned)( ` + titleTally + `)?|promoted)$`)

// isAutoTitle reports whether s looks like a curator-generated milestone title
// (so the curator may replace it). The bare-label case is handled separately.
func isAutoTitle(s string) bool {
	return autoTitleRe.MatchString(s)
}

// ingest folds one completed tool result into the digest and reports whether the
// tool is one the curator tracks. The caller holds c.mu. Pure parsing/tallying,
// unit-tested directly.
func (c *sessionTitleCurator) ingest(tool, output string) (handled bool) {
	switch tool {
	case "turf_init":
		var v initView
		if json.Unmarshal([]byte(output), &v) == nil {
			// No workspace is open yet, so there is no alias to prefer.
			c.digest.label = sessionLabel("", v.ConfigAlias, v.Workspace, v.Path)
		}
		return true

	case "turf_open":
		var v workspaceOpenView
		// Only fills a hole: init runs first in every canonical workflow and labels
		// the session there. This fires on the reopen/resume path — where the
		// workspace alias, the handle the user is actually working with, is the most
		// specific thing available. The result carries the configuration binding too,
		// so every rung of the ladder is reachable here.
		if json.Unmarshal([]byte(output), &v) == nil && c.digest.label == "" {
			c.digest.label = sessionLabel(v.WorkspaceAlias, v.ConfigAlias, v.WorkspaceName, v.ConfigPath)
		}
		return true

	case "turf_up", "turf_destroy", "turf_plan", "turf_apply", "turf_cancel":
		var v convergenceView
		if json.Unmarshal([]byte(output), &v) != nil {
			return false
		}
		return c.digest.ingestConvergence(v)
	}
	return false
}

// ingestConvergence folds one up/destroy/plan/apply/cancel result into the digest.
// Only a deploy or a destroy titles the session — a refresh, an import or an invoke
// is upkeep on infrastructure the title already names. Each round of a convergence
// stops at its own approval point (plan's result), so the title shows the round
// awaiting approval — a start verb's awaiting_plan has no counts yet; once the
// convergence converges (or ends stalled or exhausted, having applied what it
// could) it shows everything its rounds applied, summed. A convergence that ends
// with its last plan unapplied — declined, cancelled, stale — titles that ending
// with the plan's counts, so a session that stopped at "no" does not go on reading
// as a plan still waiting, and names first what its earlier rounds applied
// ("applied +4 ~0 -0 · declined +2 ~0 -0"), since that reached the world; one
// that ends before any plan was seen with nothing applied (a cancel at round 1's
// plan gate) titles nothing. A new convergence starts the sum over; a failure
// leaves the title on the last plan, which is what was still pending.
func (d *titleDigest) ingestConvergence(v convergenceView) bool {
	if v.Kind != "up" && v.Kind != "destroy" {
		return false
	}
	if v.ConvergenceID != d.convergenceID || d.rounds == nil {
		d.convergenceID = v.ConvergenceID
		d.rounds = map[int]planCounts{}
	}
	switch v.Outcome {
	case "awaiting_approval":
		if v.Plan == nil {
			return false
		}
		round := planCounts{added: v.Plan.Add, changed: v.Plan.Change, destroyed: v.Plan.Destroy}
		d.rounds[v.Round] = round
		d.setCounts(round)
		d.appliedOnly = 0
		d.stage = stagePlan
		return true
	case "declined", "cancelled", "stale":
		// The round that ended it, and what the rounds before it applied: a
		// decline at round 2's approval point, or a cancel at its plan gate,
		// comes after round 1 reached the world.
		prior, priorOnly, anyPrior := d.appliedBefore(v.Rounds)
		var ending planCounts
		planned := false
		if n := len(v.Rounds); n > 0 {
			ending, planned = d.rounds[v.Rounds[n-1].Round]
		} else {
			// A result that lists no rounds: the last plan seen.
			ending, planned = d.lastPlanned()
		}
		if !planned && !anyPrior {
			return false
		}
		d.setCounts(ending)
		d.endedPlanned = planned
		d.prior, d.priorOnly, d.anyPrior = prior, priorOnly, anyPrior
		d.appliedOnly = 0
		d.ending = v.Outcome
		d.stage = stageEnded
		return true
	case "planned":
		// A plan-only run: the plan is kept as a planfile and nothing applies.
		// The result carries the plan the way an approval point does, so the
		// ending titles with its counts. The round is not recorded: applying
		// the planfile is a convergence of its own, which starts the sum over.
		if v.Plan == nil {
			return false
		}
		d.setCounts(planCounts{added: v.Plan.Add, changed: v.Plan.Change, destroyed: v.Plan.Destroy})
		d.endedPlanned, d.anyPrior = true, false
		d.appliedOnly = 0
		d.ending = v.Outcome
		d.stage = stageEnded
		return true
	case "converged", "stalled", "exhausted":
		// Every round these endings record was applied (a stalled or exhausted
		// convergence stops after applying its last round, with work still
		// deferred), so the sum is what reached the world.
		d.stage = stageApplied
		d.appliedOnly = 0
		applied, unseen := 0, false
		for _, r := range v.Rounds {
			applied += r.Applied
			if _, ok := d.rounds[r.Round]; !ok && r.Applied > 0 {
				unseen = true
			}
		}
		if !unseen {
			var total planCounts
			for _, r := range d.rounds {
				total.added += r.added
				total.changed += r.changed
				total.destroyed += r.destroyed
			}
			d.setCounts(total)
			return true
		}
		// A round applied that this curator never saw planned — an auto-approved run
		// (turf up --yes converges in one call), or a convergence followed again after
		// a restart — so its add/change/destroy split is unknown. A destroy applies
		// only deletes; a deploy can say how many changes it applied, not which.
		if v.Kind == "destroy" {
			d.setCounts(planCounts{destroyed: applied})
		} else {
			d.setCounts(planCounts{})
			d.appliedOnly = applied
		}
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
	// label names what this session is about — see sessionLabel for which of the
	// four candidate names wins. Not called "alias": two of those candidates are
	// literally named alias on the wire, and they are not the same thing.
	label string
	stage titleStage
	// The title's counts: the round awaiting approval at stagePlan, the
	// convergence's applied total at stageApplied.
	added, changed, destroyed int
	// appliedOnly, at stageApplied, is how many changes a deploy applied when no
	// round's plan was seen to split them into add/change/destroy (see
	// ingestConvergence); zero when the counts above are the title.
	appliedOnly int
	// ending, at stageEnded, is the outcome word of a convergence that ended with
	// its last plan unapplied: declined, cancelled or stale.
	ending string
	// convergenceID and rounds track the convergence being titled: each round's plan
	// counts, keyed by round, so a round re-vended after a restart is not counted
	// twice when the convergence converges.
	convergenceID string
	rounds        map[int]planCounts
	// endedPlanned, at stageEnded, says the round that ended the convergence had
	// been planned, so its counts are the title's; false for a cancel at a plan
	// gate, which planned nothing.
	endedPlanned bool
	// prior, at stageEnded, is what the rounds before the ending one applied,
	// summed as at stageApplied — priorOnly their count of changes when one of
	// them was never seen planned, so the split is unknown; anyPrior says some
	// round applied before the end.
	prior     planCounts
	priorOnly int
	anyPrior  bool
}

// planCounts are a plan's add/change/destroy counts as Terraform's plan summary
// counts them: a replace is one add and one destroy.
type planCounts struct {
	added, changed, destroyed int
}

type titleStage int

const (
	stageNone titleStage = iota
	stagePlan
	stageApplied
	// stageEnded: the convergence ended without applying its last plan; the
	// digest's ending says how, its counts are that plan's.
	stageEnded
)

// lastPlanned is the plan counts of the latest round seen planned in the
// convergence being titled, if any round was.
func (d *titleDigest) lastPlanned() (planCounts, bool) {
	best, found := 0, false
	for n := range d.rounds {
		if !found || n > best {
			best, found = n, true
		}
	}
	if !found {
		return planCounts{}, false
	}
	return d.rounds[best], true
}

// appliedBefore sums what the applied rounds among rounds planned, as this
// curator saw them planned (a replace one add and one destroy); only is the
// changes they applied when one of them was never seen planned, so the split is
// unknown; any says some round applied a change at all.
func (d *titleDigest) appliedBefore(rounds []roundView) (total planCounts, only int, any bool) {
	applied, unseen := 0, false
	for _, r := range rounds {
		if r.Status != "applied" {
			continue
		}
		applied += r.Applied
		if c, ok := d.rounds[r.Round]; ok {
			total.added += c.added
			total.changed += c.changed
			total.destroyed += c.destroyed
		} else if r.Applied > 0 {
			unseen = true
		}
	}
	if unseen {
		return planCounts{}, applied, true
	}
	return total, 0, applied > 0
}

// defaultWorkspaceName is the OpenTofu workspace every configuration has when
// nobody selected another one. The server always sends it (workspace.name has no
// omitempty and falls back to this when .terraform/environment is absent), so it
// is a sentinel rather than a label — see sessionLabel.
const defaultWorkspaceName = "default"

// defaultAlias is the name the server reports for a configuration or workspace
// registered without an alias. Like the default workspace name it names every
// unnamed binding alike, so it is a sentinel, not a label.
const defaultAlias = "default"

// sessionLabel derives the short, human label that leads every auto-title. It is
// the answer to "which piece of infrastructure is this session about?", and the
// rungs are ordered by how specifically each names that:
//
//  1. The WORKSPACE ALIAS — this session's own handle for the workspace, and the
//     name the user types in every later tool call. It is set exactly when more
//     than one workspace is open, which is exactly when a session most needs
//     telling apart. It is also the safest token to interpolate: the server
//     constrains it to ^[a-zA-Z0-9][a-zA-Z0-9_-]*$.
//  2. The CONFIG ALIAS — the configuration's own handle, minted by init.
//  3. The WORKSPACE NAME, but only when it is not "default". A real state slot a
//     `tofu workspace select` user created ("production") names something; the
//     default sentinel names every configuration ever written, so it would label
//     every session identically and is skipped.
//  4. The base of the config path, else of the cwd — because the /up prompt may
//     pass "." and a relative path.
//
// Rungs 1 and 2 skip the unnamed binding's "default" (defaultAlias) for the
// same reason rung 3 skips the default workspace. Both exclusions are
// deliberately confined to the title. The timeline is a different surface: a
// turf_open line reports one specific call, so it should say "default" when
// that is the workspace it opened (see workspaceName).
func sessionLabel(workspaceAlias, configAlias, workspaceName, path string) string {
	if workspaceAlias != "" && workspaceAlias != defaultAlias {
		return workspaceAlias
	}
	if configAlias != "" && configAlias != defaultAlias {
		return configAlias
	}
	if workspaceName != "" && workspaceName != defaultWorkspaceName {
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

func (d *titleDigest) setCounts(c planCounts) {
	d.added, d.changed, d.destroyed = c.added, c.changed, c.destroyed
}

// isDestroy reports whether the plan is a pure teardown (only deletes). A mixed
// plan with adds/changes is a normal apply, not a teardown.
func (d *titleDigest) isDestroy() bool { return d.planCounts().isDestroy() }

// planCounts are the title's counts as one value.
func (d *titleDigest) planCounts() planCounts {
	return planCounts{added: d.added, changed: d.changed, destroyed: d.destroyed}
}

// isDestroy says the counts are deletes alone.
func (c planCounts) isDestroy() bool {
	return c.destroyed > 0 && c.added == 0 && c.changed == 0
}

// appliedWord is what a convergence applied, in the title's idiom: "destroyed
// -N" for deletes alone, "applied N change(s)" when the split is unknown,
// "applied +A ~C -D" otherwise.
func appliedWord(c planCounts, only int) string {
	switch {
	case only > 0:
		return fmt.Sprintf("applied %d change(s)", only)
	case c.isDestroy():
		return "destroyed " + destroyCount(c.destroyed)
	}
	return "applied " + c.String()
}

// title renders the finished session title in the terraform-plan glyph idiom,
// e.g. "actions", "actions · plan +2 ~0 -2", "actions · applied +2 ~0 -2",
// "actions · destroyed -4", "actions · declined +2 ~3 -2" — or "actions · applied
// 6 change(s)" for a deploy whose plans were never seen. Returns "" before the
// session label is known.
func (d *titleDigest) title() string {
	if d.label == "" {
		return ""
	}
	switch d.stage {
	case stagePlan:
		if d.isDestroy() {
			return d.label + " · planned teardown " + destroyCount(d.destroyed)
		}
		return d.label + " · plan " + d.counts()
	case stageApplied:
		return d.label + " · " + appliedWord(d.planCounts(), d.appliedOnly)
	case stageEnded:
		if d.ending == "planned" && d.isDestroy() {
			// A plan-only destroy reads as a planned teardown, as a destroy
			// plan at its approval point does.
			return d.label + " · planned teardown " + destroyCount(d.destroyed)
		}
		// What the earlier rounds applied comes first — it reached the world —
		// then the ending, with the counts of the plan it left when one was.
		end := d.ending
		if d.endedPlanned {
			end += " " + d.counts()
		}
		if d.anyPrior {
			return d.label + " · " + appliedWord(d.prior, d.priorOnly) + " · " + end
		}
		return d.label + " · " + end
	default:
		// Pre-plan: config known, nothing done yet. The " · init" suffix makes it
		// a regular auto-title shape (matched by isAutoTitle) rather than a bare
		// word needing a special case.
		return d.label + " · init"
	}
}

func (d *titleDigest) counts() string { return d.planCounts().String() }

// String formats the full add/change/destroy tally as "+A ~C -D" (terraform-plan
// always shows all three); "no changes" when every bucket is zero.
func (c planCounts) String() string {
	if c.added == 0 && c.changed == 0 && c.destroyed == 0 {
		return "no changes"
	}
	return fmt.Sprintf("+%d ~%d -%d", c.added, c.changed, c.destroyed)
}

func destroyCount(n int) string {
	return fmt.Sprintf("-%d", n)
}
