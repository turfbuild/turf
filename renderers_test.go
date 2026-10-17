package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/docker/docker-agent/pkg/tools"
	"github.com/docker/docker-agent/pkg/tui/animation"
	"github.com/docker/docker-agent/pkg/tui/service"
	"github.com/docker/docker-agent/pkg/tui/types"
)

var (
	ansiRE  = regexp.MustCompile("\x1b\\[[0-9;]*m")
	spaceRE = regexp.MustCompile(`[ \t]+`)
)

// plainNorm strips ANSI styling and collapses runs of spaces/tabs so tests can assert on
// rendered structure without depending on exact =-alignment padding or the ANSI resets
// that sit between adjacent styled runs.
func plainNorm(s string) string {
	return spaceRE.ReplaceAllString(ansiRE.ReplaceAllString(s, ""), " ")
}

// shownState / hiddenState drive the two detail levels. StaticSessionState pins
// HideToolResults() to false (detailed); we embed+override for the compact case.
type hiddenState struct{ service.StaticSessionState }

func (hiddenState) HideToolResults() bool { return true }

func renderFor(name, content string, ss service.SessionStateReader) string {
	return renderWithArgs(name, content, "", ss, 1)
}

func renderWithArgs(name, content, argsJSON string, ss service.SessionStateReader, height int) string {
	msg := &types.Message{
		Content:        content,
		ToolStatus:     types.ToolStatusCompleted,
		ToolCall:       tools.ToolCall{Function: tools.FunctionCall{Name: name, Arguments: argsJSON}},
		ToolDefinition: tools.Tool{Name: name},
	}
	b := turfToolRenderers()[name](animation.NewRuntime(), msg, ss)
	b.SetSize(120, height)
	return b.View()
}

// renderErr renders a failed tool call: ToolStatusError with the framework's error
// text in Content, exactly as cagent delivers a tool error to the renderer.
func renderErr(name, content string, ss service.SessionStateReader) string {
	return renderErrArgs(name, content, "", ss)
}

// renderErrArgs is renderErr with request arguments (the JSON cagent puts in
// ToolCall.Function.Arguments), so tests can exercise the request-target context the
// error line leads with.
func renderErrArgs(name, content, argsJSON string, ss service.SessionStateReader) string {
	msg := &types.Message{
		Content:        content,
		ToolStatus:     types.ToolStatusError,
		ToolCall:       tools.ToolCall{Function: tools.FunctionCall{Name: name, Arguments: argsJSON}},
		ToolDefinition: tools.Tool{Name: name},
	}
	b := turfToolRenderers()[name](animation.NewRuntime(), msg, ss)
	b.SetSize(120, 10)
	return b.View()
}

func TestSkill_LoadedLineNotBody(t *testing.T) {
	const body = "# Deploying with Turf\n\nWorkspace lifecycle, provider load...\nmore guidance\n"
	out := renderFor("turf_skill_deployment", body, hiddenState{})
	if !strings.Contains(out, "Deployment Skill") || !strings.Contains(out, "loaded") {
		t.Fatalf("skill summary wrong: %q", out)
	}
	// The line count rides inline on the compact summary (not just the unfold).
	if !strings.Contains(out, "(4 lines)") {
		t.Fatalf("compact skill should show inline line count: %q", out)
	}
	if strings.Contains(out, "Workspace lifecycle") {
		t.Fatalf("compact skill should not dump the guide body: %q", out)
	}

	// Expanded stays a single line: same title + load status + size, and never the
	// guide title or body — the skill renderer emits no Ctrl+O detail block.
	det := renderFor("turf_skill_deployment", body, service.StaticSessionState{})
	if !strings.Contains(det, "Deployment Skill") || !strings.Contains(det, "loaded") || !strings.Contains(det, "(4 lines)") {
		t.Fatalf("expanded skill summary wrong: %q", det)
	}
	if strings.Contains(det, "Deploying with Turf") {
		t.Fatalf("expanded skill should not add the guide title: %q", det)
	}
	if strings.Contains(det, "more guidance") {
		t.Fatalf("expanded skill should not dump the guide body: %q", det)
	}
	if strings.Contains(strings.TrimRight(det, "\n"), "\n") {
		t.Fatalf("expanded skill should be a single line: %q", det)
	}
}

func TestRenderers_BadJSONFallsBack(t *testing.T) {
	for name := range turfToolRenderers() {
		out := renderFor(name, "not json", service.StaticSessionState{})
		if strings.TrimSpace(out) == "" {
			t.Fatalf("%s produced empty output on bad JSON", name)
		}
	}
}

// TestEveryRendererLeadsWithTitle locks in the "English-readable tool name is
// consistently displayed" invariant: every renderer's output leads with the
// friendly turfToolInfo title, resolved the same way the /tools dialog resolves it.
func TestEveryRendererLeadsWithTitle(t *testing.T) {
	for name := range turfToolRenderers() {
		want := turfToolTitle(strings.TrimPrefix(name, appName+"_"))
		out := renderFor(name, "{}", service.StaticSessionState{})
		if !strings.Contains(out, want) {
			t.Errorf("%s should lead with title %q: %q", name, want, out)
		}
	}
}

// TestEveryTurfToolHasRenderer guards coverage: every turf tool declared in
// turfToolInfo (the /tools-dialog source of truth) must have a custom renderer, so
// no tool silently falls back to a raw JSON dump. A new server tool added to
// turfToolInfo without a renderer fails here.
func TestEveryTurfToolHasRenderer(t *testing.T) {
	renderers := turfToolRenderers()
	for bare := range turfToolInfo {
		name := appName + "_" + bare
		if _, ok := renderers[name]; !ok {
			t.Errorf("turf tool %q has a title but no renderer", name)
		}
	}
}

// TestErrorLine_CompactTruncatedExpandedMultiline locks in the fix and its detail
// toggle: a failed tool call surfaces the framework's error text (msg.Content) instead
// of a bare title, wrapping across lines when expanded and truncating to one line
// (with an ellipsis) when compact.
func TestErrorLine_CompactTruncatedExpandedMultiline(t *testing.T) {
	const tail = "ZZZ_TAIL_TOKEN"
	errText := "Cannot invoke action: configuration has unresolved references. " +
		strings.Repeat("filler word ", 15) + tail
	title := turfToolTitle("invoke")

	// Expanded (turf default): leads with the title, wraps across lines, full text.
	shown := renderErr("turf_invoke", errText, service.StaticSessionState{})
	if !strings.Contains(shown, title) {
		t.Fatalf("errored line should lead with title %q: %q", title, shown)
	}
	if !strings.Contains(strings.TrimRight(shown, " \n"), "\n") {
		t.Fatalf("expanded error should wrap across multiple lines: %q", shown)
	}
	if !strings.Contains(plainNorm(shown), tail) {
		t.Fatalf("expanded error should show the full text incl. tail: %q", shown)
	}

	// Compact (Ctrl+O): single line, truncated before the tail, ellipsis shown.
	compact := renderErr("turf_invoke", errText, hiddenState{})
	if !strings.Contains(compact, title) {
		t.Fatalf("compact errored line should lead with title %q: %q", title, compact)
	}
	if strings.Contains(strings.TrimRight(compact, " \n"), "\n") {
		t.Fatalf("compact error should be a single line: %q", compact)
	}
	if strings.Contains(compact, tail) {
		t.Fatalf("compact error should be truncated before the tail: %q", compact)
	}
	if !strings.Contains(compact, "…") {
		t.Fatalf("compact truncation should show an ellipsis: %q", compact)
	}
}

// TestErrorLine_EmptyContentFallsBackToFailed preserves prior behavior when the
// framework reports a failure with no text: the line still reads "failed".
func TestErrorLine_EmptyContentFallsBackToFailed(t *testing.T) {
	out := renderErr("turf_skill_deployment", "", service.StaticSessionState{})
	if !strings.Contains(out, "failed") {
		t.Fatalf("empty-content error should render \"failed\": %q", out)
	}
}

// TestErrorLine_ShowsRequestTarget locks in the request-context fix: a failed call leads
// with the target it acted on (resolved from the tool-call args), in both the expanded
// and the compact one-line form, so the error is as scannable as a success line.
func TestErrorLine_ShowsRequestTarget(t *testing.T) {
	const addr = "kubernetes_deployment_v1.web"
	errText := "Cannot taint: the workspace is held by convergence p-1"
	args := `{"addrs":["` + addr + `"],"workspace_alias":"k8s"}`

	shown := renderErrArgs("turf_taint", errText, args, service.StaticSessionState{})
	if !strings.Contains(plainNorm(shown), addr) {
		t.Fatalf("expanded error should lead with the request target %q: %q", addr, shown)
	}

	compact := renderErrArgs("turf_taint", errText, args, hiddenState{})
	if !strings.Contains(plainNorm(compact), addr) {
		t.Fatalf("compact error should keep the request target %q visible: %q", addr, compact)
	}
	if strings.Contains(strings.TrimRight(compact, " \n"), "\n") {
		t.Fatalf("compact error should stay a single line: %q", compact)
	}
}

// TestErrorTarget_FallsBackToWorkspaceAlias covers the universal fallback: a tool with no
// specific target arg (up) still surfaces the workspace alias on failure.
func TestErrorTarget_FallsBackToWorkspaceAlias(t *testing.T) {
	// The error text deliberately omits the alias, so a passing test proves the target
	// prefix (not the message) carries it.
	out := renderErrArgs("turf_up", "the workspace is held by another convergence",
		`{"workspace_alias":"prod-db"}`, service.StaticSessionState{})
	if !strings.Contains(plainNorm(out), "prod-db") {
		t.Fatalf("up error should surface the workspace alias: %q", out)
	}
}

// TestEveryRendererReportsErrors guards that the fallback swap reached every renderer
// (including the two skill renderers that don't parse JSON): a failed call always
// surfaces the framework's error text, never a silent bare title.
func TestEveryRendererReportsErrors(t *testing.T) {
	const errText = "boom: something went wrong"
	for name := range turfToolRenderers() {
		out := renderErr(name, errText, service.StaticSessionState{})
		if !strings.Contains(out, errText) {
			t.Errorf("%s should surface the framework error text: %q", name, out)
		}
	}
}

// TestThink_SuppressesThoughtBody locks in the think override: cagent's built-in
// think view dumps the entire running thought log ("Thoughts:\n…"); turf's renderer
// collapses every call to a quiet "Think" line and never echoes the log, in both the
// compact and expanded (Ctrl+O) detail levels. A framework error is still surfaced.
func TestThink_SuppressesThoughtBody(t *testing.T) {
	const content = "Thoughts:\nfirst secret thought\nsecond verbose thought"
	renderThinkFor := func(ss service.SessionStateReader) string {
		msg := &types.Message{
			Content:        content,
			ToolStatus:     types.ToolStatusCompleted,
			ToolCall:       tools.ToolCall{Function: tools.FunctionCall{Name: "think"}},
			ToolDefinition: tools.Tool{Name: "think"},
		}
		b := builtinToolRenderers()["think"](animation.NewRuntime(), msg, ss)
		b.SetSize(120, 10)
		return b.View()
	}

	for _, ss := range []service.SessionStateReader{service.StaticSessionState{}, hiddenState{}} {
		out := renderThinkFor(ss)
		if !strings.Contains(out, "Think") {
			t.Fatalf("think line should show the Think label: %q", out)
		}
		for _, leaked := range []string{"Thoughts", "secret thought", "verbose thought"} {
			if strings.Contains(out, leaked) {
				t.Fatalf("think renderer leaked the thought body %q: %q", leaked, out)
			}
		}
		if strings.Contains(strings.TrimRight(out, " \n"), "\n") {
			t.Fatalf("suppressed think line should be a single line: %q", out)
		}
	}

	// A framework error is terse and diagnostic, so it is still surfaced.
	errMsg := &types.Message{
		Content:        "boom: think failed",
		ToolStatus:     types.ToolStatusError,
		ToolCall:       tools.ToolCall{Function: tools.FunctionCall{Name: "think"}},
		ToolDefinition: tools.Tool{Name: "think"},
	}
	b := builtinToolRenderers()["think"](animation.NewRuntime(), errMsg, service.StaticSessionState{})
	b.SetSize(120, 10)
	if out := b.View(); !strings.Contains(out, "boom: think failed") {
		t.Fatalf("think renderer should surface a framework error: %q", out)
	}
}

// TestRenderValueNesting exercises the shared value renderer directly: nested maps and
// lists expand across =-aligned lines, short scalar lists stay inline, sentinels are
// masked, and no raw JSON leaks — the behavior every kv-based block now inherits.
func TestRenderValueNesting(t *testing.T) {
	m := map[string]any{
		"bucket": "mint-hyena",
		"tags":   map[string]any{"env": "prod", "team": "infra"},
		"ports":  []any{float64(80), float64(443)},
		"arn":    "__cty_unknown__",
		"pw":     "__cty_sensitive__",
		"empty":  map[string]any{},
		"rules":  []any{map[string]any{"id": "r1"}},
	}
	out := plainNorm(strings.Join(kvLines(m, "", 0), "\n"))
	for _, want := range []string{
		`bucket = "mint-hyena"`,
		"tags = {",
		`env = "prod"`,
		`team = "infra"`,
		"ports = [80, 443]", // short scalar list stays inline
		"empty = {}",
		"rules = [", // list holding a map expands
		`id = "r1"`,
		"(known after apply)",
		"(sensitive value)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("nesting missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `{"`) || strings.Contains(out, "[{") {
		t.Fatalf("nesting leaked raw JSON:\n%s", out)
	}
	if strings.Contains(out, "__cty_") {
		t.Fatalf("nesting leaked sentinel:\n%s", out)
	}
}

// TestAttrDiff_MarkersPropagate confirms a create/destroy marks every line (+/-) — not
// just the top-level attr — and that top-level keys are =-aligned, matching tofu plan.
func TestAttrDiff_MarkersPropagate(t *testing.T) {
	after := map[string]any{"id": "x", "tags": map[string]any{"env": "prod"}}
	create := plainNorm(strings.Join(attrDiff(nil, after, nil, nil, ""), "\n"))
	for _, want := range []string{"+ id", "+ tags = {", `+ env = "prod"`} { // nested line carries +
		if !strings.Contains(create, want) {
			t.Fatalf("create diff missing marker %q:\n%s", want, create)
		}
	}

	before := map[string]any{"tags": map[string]any{"env": "prod"}}
	destroy := plainNorm(strings.Join(attrDiff(before, nil, nil, nil, ""), "\n"))
	for _, want := range []string{"- tags = {", `- env = "prod"`} {
		if !strings.Contains(destroy, want) {
			t.Fatalf("destroy diff missing marker %q:\n%s", want, destroy)
		}
	}

	// A scalar change stays inline as old → new (no expansion).
	chg := plainNorm(strings.Join(attrDiff(
		map[string]any{"n": float64(1)}, map[string]any{"n": float64(2)}, nil, nil, ""), "\n"))
	if !strings.Contains(chg, "~ n = 1 → 2") {
		t.Fatalf("scalar change should be inline old → new: %q", chg)
	}
}

// --- sensitivity masks -------------------------------------------------------
//
// The turf server returns OpenTofu's before_sensitive/after_sensitive/sensitive_values
// masks alongside the values, and can be asked (show_sensitive) to put the real secret
// on the wire. These tests pin the property that matters: whatever arrives, no turf
// renderer paints a secret into the timeline.

// secretAbsent mirrors the server's own e2e helper — a redacted attribute is worthless
// if the same value rides along somewhere else in the same line.
func secretAbsent(t *testing.T, out, secret, what string) {
	t.Helper()
	if strings.Contains(out, secret) {
		t.Fatalf("%s leaked the secret %q:\n%s", what, secret, out)
	}
}

// TestAttrDiff_MaskRedactsRevealedValues: with show_sensitive the values arrive in the
// clear, so the mask is the only thing standing between the secret and the screen. It
// has to hold at every shape a mask can take — a scalar attr, a key inside a nested
// object, a list element — and on every diff branch.
func TestAttrDiff_MaskRedactsRevealedValues(t *testing.T) {
	const secret = "hunter2"
	vals := map[string]any{
		"id":      "x",
		"pw":      secret,
		"keepers": map[string]any{"env": "prod", "token": secret},
		"certs":   []any{"public", secret},
	}
	// The OTF shape: true at a sensitive node, non-sensitive entries omitted, lists
	// positional with null where nothing is sensitive.
	mask := map[string]any{
		"pw":      true,
		"keepers": map[string]any{"token": true},
		"certs":   []any{nil, true},
	}

	create := plainNorm(strings.Join(attrDiff(nil, vals, nil, mask, ""), "\n"))
	secretAbsent(t, create, secret, "create diff")
	for _, want := range []string{"+ pw = (sensitive value)", "+ token = (sensitive value)", `+ env = "prod"`} {
		if !strings.Contains(create, want) {
			t.Fatalf("create diff missing %q:\n%s", want, create)
		}
	}

	destroy := plainNorm(strings.Join(attrDiff(vals, nil, mask, nil, ""), "\n"))
	secretAbsent(t, destroy, secret, "destroy diff")
	if !strings.Contains(destroy, "- pw = (sensitive value)") {
		t.Fatalf("destroy diff missing masked scalar:\n%s", destroy)
	}

	// Sensitivity newly applied to an attribute that was in the clear: at most one side
	// is masked, so this stays the ordinary inline old → new.
	added := plainNorm(strings.Join(attrDiff(
		map[string]any{"pw": "old"}, map[string]any{"pw": secret},
		nil, map[string]any{"pw": true}, ""), "\n"))
	secretAbsent(t, added, secret, "newly-sensitive diff")
	if !strings.Contains(added, `~ pw = "old" → (sensitive value)`) {
		t.Fatalf("newly-sensitive attr should read old → (sensitive value): %q", added)
	}

	// A changed collection expands the new value, and the mask still bites inside it.
	coll := plainNorm(strings.Join(attrDiff(
		map[string]any{"keepers": map[string]any{"token": "before"}},
		map[string]any{"keepers": map[string]any{"token": secret}},
		map[string]any{"keepers": map[string]any{"token": true}},
		map[string]any{"keepers": map[string]any{"token": true}}, ""), "\n"))
	secretAbsent(t, coll, secret, "collection diff")
}

// TestMasks_WholeValueSensitive: OpenTofu writes a bare `true` when a whole value is
// marked. Every attribute beneath it must collapse, in a diff and in a plain view.
func TestMasks_WholeValueSensitive(t *testing.T) {
	const secret = "hunter2"
	vals := map[string]any{"user": "root", "pass": secret, "nested": map[string]any{"k": secret}}

	create := plainNorm(strings.Join(attrDiff(nil, vals, nil, true, ""), "\n"))
	secretAbsent(t, create, secret, "whole-value create")
	secretAbsent(t, create, "root", "whole-value create")
	if !strings.Contains(create, "+ pass = (sensitive value)") {
		t.Fatalf("whole-value mask should mask each attribute:\n%s", create)
	}

	update := plainNorm(strings.Join(attrDiff(
		map[string]any{"pass": "old-" + secret}, map[string]any{"pass": secret}, true, true, ""), "\n"))
	secretAbsent(t, update, secret, "whole-value update")
	if !strings.Contains(update, "~ pass = (sensitive value) → (sensitive value)") {
		t.Fatalf("whole-value update should read (sensitive value) → (sensitive value):\n%s", update)
	}

	plain := plainNorm(strings.Join(kvLinesMasked(vals, true, "", 0), "\n"))
	secretAbsent(t, plain, secret, "whole-value kvLinesMasked")
	secretAbsent(t, plain, "root", "whole-value kvLinesMasked")
}

// TestAttrDiff_MaskedChange covers the two halves of an unprintable change: redacted,
// the sentinel is identical on both sides and there is nothing to report; revealed, the
// renderer can prove it moved and says so without printing either half.
func TestAttrDiff_MaskedChange(t *testing.T) {
	mask := map[string]any{"pw": true}

	// Redacted (the default): both sides are the same sentinel, so the row drops out —
	// unchanged from before masks existed. The renderer genuinely cannot tell.
	hidden := plainNorm(strings.Join(attrDiff(
		map[string]any{"n": float64(1), "pw": "__cty_sensitive__"},
		map[string]any{"n": float64(2), "pw": "__cty_sensitive__"},
		mask, mask, ""), "\n"))
	if strings.Contains(hidden, "pw") {
		t.Fatalf("an indistinguishable sensitive attr should not render a row: %q", hidden)
	}
	if !strings.Contains(hidden, "~ n = 1 → 2") {
		t.Fatalf("the ordinary attr should still diff: %q", hidden)
	}

	// Revealed: two different plaintexts, so the change is real. Same shape as any
	// other scalar change, both halves masked.
	shown := plainNorm(strings.Join(attrDiff(
		map[string]any{"pw": "old-secret"},
		map[string]any{"pw": "new-secret"},
		mask, mask, ""), "\n"))
	secretAbsent(t, shown, "old-secret", "masked change")
	secretAbsent(t, shown, "new-secret", "masked change")
	if !strings.Contains(shown, "~ pw = (sensitive value) → (sensitive value)") {
		t.Fatalf("a proven change to an unprintable value should read (sensitive value) → (sensitive value): %q", shown)
	}

	// A revealed *collection* takes the same shape rather than expanding and losing
	// the arrow.
	coll := plainNorm(strings.Join(attrDiff(
		map[string]any{"pw": map[string]any{"k": "old-secret"}},
		map[string]any{"pw": map[string]any{"k": "new-secret"}},
		mask, mask, ""), "\n"))
	secretAbsent(t, coll, "old-secret", "masked collection change")
	if !strings.Contains(coll, "~ pw = (sensitive value) → (sensitive value)") {
		t.Fatalf("a masked collection change should take the same shape: %q", coll)
	}
}

// TestKvLinesMasked_CollapsesSensitiveSubtrees is the plain-view half: a mask covering a
// whole map or list must stop the expansion entirely, not just redact the leaves.
func TestKvLinesMasked_CollapsesSensitiveSubtrees(t *testing.T) {
	const secret = "hunter2"
	m := map[string]any{
		"id":      "x",
		"config":  map[string]any{"user": "root", "pass": secret},
		"whole":   map[string]any{"a": secret, "b": secret},
		"ports":   []any{float64(80), secret},
		"nothing": "plain",
	}
	mask := map[string]any{
		"config": map[string]any{"pass": true},
		"whole":  true,
		"ports":  []any{nil, true},
	}
	out := plainNorm(strings.Join(kvLinesMasked(m, mask, "", 0), "\n"))
	secretAbsent(t, out, secret, "kvLinesMasked")
	for _, want := range []string{
		`id = "x"`,
		"config = {",
		`user = "root"`,
		"pass = (sensitive value)",
		"whole = (sensitive value)", // the whole subtree collapses; no brace, no keys
		"ports = [80, (sensitive value)]",
		`nothing = "plain"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("kvLinesMasked missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "whole = {") {
		t.Fatalf("a wholly-sensitive map must not expand:\n%s", out)
	}
}

// TestTurfToolTargetArgsMatchServerSchemas is the drift guard turfToolTargetArgs
// went without — and it is exactly the map that rotted when the server renamed
// turf_open/turf_delete's `name` argument to `workspace_name`. A stale
// entry here is silent: errorTarget simply finds nothing, and a failed tool call
// loses the lead context that tells the reader WHAT failed.
//
// It asserts every arg name is a real property of that tool's live input schema.
// The reverse direction is deliberately not asserted: a tool may take many args
// and the map names only the one that identifies the target.
//
// Note this guards REQUEST arguments only. The server publishes no result schema
// over MCP, so a renamed RESULT field stays unguarded — parseContent decodes it as
// a zero value and the line quietly degrades. The per-tool renderer tests, whose
// JSON literals are hand-written against the server's structs, are the only cover
// there.
func TestTurfToolTargetArgsMatchServerSchemas(t *testing.T) {
	byName := serverTools(t)

	for bare, args := range turfToolTargetArgs {
		name := "turf_" + bare
		tl, ok := byName[name]
		if !ok {
			t.Errorf("turfToolTargetArgs names %q, which is not a server tool (stale entry)", name)
			continue
		}
		props := schemaProperties(t, tl)
		if props == nil {
			t.Errorf("%s: could not read input schema properties", name)
			continue
		}
		for _, arg := range args {
			if _, ok := props[arg]; !ok {
				t.Errorf("%s: target arg %q is not in the server's input schema (renamed or removed) — known args: %s",
					name, arg, strings.Join(sortedKeys(props), ", "))
			}
		}
	}
}

// schemaProperties pulls the `properties` object out of a tool's JSON input schema.
// Tool.Parameters is an `any` holding whatever the MCP layer decoded, so this goes
// through JSON rather than guessing at a concrete Go type.
func schemaProperties(t *testing.T, tl tools.Tool) map[string]any {
	t.Helper()

	raw, err := json.Marshal(tl.Parameters)
	if err != nil {
		return nil
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if json.Unmarshal(raw, &schema) != nil {
		return nil
	}
	return schema.Properties
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// --- convergence (up / destroy / refresh / import / invoke / apply / cancel) --
//
// The fixtures below are the v1 server's own results, recorded on the wire during the
// manual suite's smoke run (doof docs/development/testing/manual-mcp-e2e-suite.md,
// Run 1) against fixture `core`, paths shortened. Each tool's line is asserted in both
// detail levels.

// upRound1 is `up` stopping at round 1's approval point: four creates, one resource
// deferred to round 2 (it reads an id round 1 creates).
const upRound1 = `{"convergence_id":"p-5f3d7adf","workspace_alias":"default","kind":"up",
	"outcome":"awaiting_approval","round":1,
	"plan":{"uri":"turf://workspaces/default/convergences/p-5f3d7adf/rounds/1/plan",
		"digest":"sha256:247f7cef0852f871bf01d5f31401523a80452ff87e3cbfb4130d64264db75c1b",
		"add":4,"change":0,"destroy":0,"deferred":1,"outputs":3},
	"expires_at":"2026-10-10T23:44:36Z",
	"rounds":[{"round":1,"status":"awaiting_approval","approval":"asked","planned":5,"deferred":1,"applied":0}],
	"next":"Review the plan with show (plan.uri: OpenTofu's json-format plan, as ` + "`" + `tofu show -json` + "`" + ` prints it, redacted); hand it to a policy tool if you have one. To approve and apply it, call apply with convergence_id and plan.digest. To change course, edit the configuration and call up again, which supersedes this convergence; cancel ends it now."}`

// applyRound2 is the apply of round 1: it applied four, planned round 2, and stopped.
const applyRound2 = `{"convergence_id":"p-5f3d7adf","workspace_alias":"default","kind":"up",
	"outcome":"awaiting_approval","round":2,
	"plan":{"uri":"turf://workspaces/default/convergences/p-5f3d7adf/rounds/2/plan",
		"digest":"sha256:0badac7182bcbfe386f11b873c546139305dd621e8df846bda54fab498a487fc",
		"add":2,"change":0,"destroy":0,"outputs":1},
	"expires_at":"2026-10-10T23:45:20Z",
	"rounds":[{"round":1,"status":"applied","approval":"asked","planned":5,"deferred":1,"applied":4},
		{"round":2,"status":"awaiting_approval","approval":"asked","planned":6,"deferred":0,"applied":0}],
	"next":"Review the plan with show (plan.uri: OpenTofu's json-format plan, as ` + "`" + `tofu show -json` + "`" + ` prints it, redacted); hand it to a policy tool if you have one. To approve and apply it, call apply with convergence_id and plan.digest. To change course, edit the configuration and call up again, which supersedes this convergence; cancel ends it now."}`

// applyConverged is the apply of round 2: nothing left deferred.
const applyConverged = `{"convergence_id":"p-5f3d7adf","workspace_alias":"default","kind":"up",
	"outcome":"converged",
	"rounds":[{"round":1,"status":"applied","approval":"asked","planned":5,"deferred":1,"applied":4},
		{"round":2,"status":"applied","approval":"asked","planned":6,"deferred":0,"applied":2}],
	"outputs":{"finalized":["fan","password","pet"]},
	"next":"Done: nothing is left deferred."}`

// upNoChange is an up whose single round planned nothing to apply: it converged
// without pausing.
const upNoChange = `{"convergence_id":"p-14f188d8","workspace_alias":"default","kind":"up",
	"outcome":"converged",
	"rounds":[{"round":1,"status":"applied","approval":"unneeded","planned":6,"deferred":0,"applied":0}],
	"outputs":{"finalized":["fan","password","pet"]},"next":"Done."}`

const destroyRound1 = `{"convergence_id":"d-b75b58b2","workspace_alias":"default","kind":"destroy",
	"outcome":"awaiting_approval","round":1,
	"plan":{"uri":"turf://workspaces/default/convergences/d-b75b58b2/rounds/1/plan",
		"digest":"sha256:6a6bd549bc3088b216e54764b10d2166004b4645eb1d6e07ae47cbf9fc1964b1",
		"add":0,"change":0,"destroy":6,"outputs":3},
	"expires_at":"2026-10-10T23:46:30Z",
	"rounds":[{"round":1,"status":"awaiting_approval","approval":"asked","planned":6,"deferred":0,"applied":0}],
	"next":"Review the plan with show (plan.uri: OpenTofu's json-format plan, as ` + "`" + `tofu show -json` + "`" + ` prints it, redacted); hand it to a policy tool if you have one. To approve and apply it, call apply with convergence_id and plan.digest. To change course, edit the configuration and call up again, which supersedes this convergence; cancel ends it now."}`

const destroyConverged = `{"convergence_id":"d-b75b58b2","workspace_alias":"default","kind":"destroy",
	"outcome":"converged",
	"rounds":[{"round":1,"status":"applied","approval":"asked","planned":6,"deferred":0,"applied":6}],
	"next":"The destroy is complete."}`

func TestConvergence_UpAwaitingApproval(t *testing.T) {
	compact := plainNorm(renderFor("turf_up", upRound1, hiddenState{}))
	for _, want := range []string{"Deploy", "p-5f3d7adf", "round 1", "+4", "1 deferred", "3 output change(s)", "awaiting approval"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("up compact missing %q: %q", want, compact)
		}
	}
	// The default workspace is noise on a convergence line; the next-step guidance is
	// the agent's, not the reader's; and an up line is titled by its kind already.
	for _, absent := range []string{"default", "Review the plan", "deploy"} {
		if strings.Contains(compact, absent) {
			t.Fatalf("up compact should not show %q: %q", absent, compact)
		}
	}

	shown := plainNorm(renderFor("turf_up", upRound1, service.StaticSessionState{}))
	for _, want := range []string{
		"plan:", "turf://workspaces/default/convergences/p-5f3d7adf/rounds/1/plan", "sha256:247f7cef0852", "expires 2026-10-10T23:44:36Z",
		"rounds:", "round 1 · awaiting approval · 5 planned · 1 deferred",
	} {
		if !strings.Contains(shown, want) {
			t.Fatalf("up expanded missing %q:\n%s", want, shown)
		}
	}
	if strings.Contains(shown, "247f7cef0852f871") {
		t.Fatalf("the digest should be shortened: %s", shown)
	}
}

func TestConvergence_ApplyPausesAtTheNextRound(t *testing.T) {
	out := plainNorm(renderFor("turf_apply", applyRound2, service.StaticSessionState{}))
	for _, want := range []string{
		"Apply Plan", "p-5f3d7adf · deploy · round 2", "+2", "1 output change(s)", "awaiting approval",
		"round 1 · applied · 4 applied · 1 deferred · approved",
		"round 2 · awaiting approval · 6 planned",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("apply (round 2) missing %q:\n%s", want, out)
		}
	}
}

func TestConvergence_Converged(t *testing.T) {
	compact := plainNorm(renderFor("turf_apply", applyConverged, hiddenState{}))
	for _, want := range []string{"Apply Plan", "p-5f3d7adf", "deploy", "converged", "2 rounds", "6 applied"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("converged compact missing %q: %q", want, compact)
		}
	}
	shown := plainNorm(renderFor("turf_apply", applyConverged, service.StaticSessionState{}))
	if !strings.Contains(shown, "finalized fan, password, pet") {
		t.Fatalf("converged expansion should name the finalized outputs:\n%s", shown)
	}

	// A deploy with nothing to apply converges in its first call, and says so.
	none := plainNorm(renderFor("turf_up", upNoChange, service.StaticSessionState{}))
	for _, want := range []string{"converged", "no changes", "nothing to approve"} {
		if !strings.Contains(none, want) {
			t.Fatalf("no-change up missing %q:\n%s", want, none)
		}
	}
}

// A destroy is "done", not "converged": converging is a deploy's word.
func TestConvergence_Destroy(t *testing.T) {
	plan := plainNorm(renderFor("turf_destroy", destroyRound1, hiddenState{}))
	for _, want := range []string{"Destroy", "d-b75b58b2", "round 1", "-6", "awaiting approval"} {
		if !strings.Contains(plan, want) {
			t.Fatalf("destroy plan missing %q: %q", want, plan)
		}
	}
	done := plainNorm(renderFor("turf_apply", destroyConverged, hiddenState{}))
	for _, want := range []string{"d-b75b58b2", "destroy", "done", "6 applied"} {
		if !strings.Contains(done, want) {
			t.Fatalf("destroy apply missing %q: %q", want, done)
		}
	}
	if strings.Contains(done, "converged") {
		t.Fatalf("a destroy should end \"done\": %q", done)
	}
}

// A convergence that fails arrives as an MCP error that still carries the whole
// result. The line must read it as a convergence, not dump the JSON as error text.
func TestConvergence_FailedArrivesAsAnErrorWithItsBody(t *testing.T) {
	const content = `{"convergence_id":"p-9","workspace_alias":"default","kind":"up","outcome":"failed",
		"rounds":[{"round":1,"status":"failed","approval":"auto","planned":3,"deferred":0,"applied":2,"failed":1,
			"failures":[{"address":"aws_s3_bucket.logs","error":"BucketAlreadyExists: the name is taken"}],
			"warnings":["provider returned a deprecation warning"]}],
		"error":"round 1 failed: 1 effect failed","next":"Fix the configuration and run up again."}`
	compact := plainNorm(renderErr("turf_apply", content, hiddenState{}))
	for _, want := range []string{"p-9", "failed", "2 applied", "1 failed"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("failed compact missing %q: %q", want, compact)
		}
	}
	shown := plainNorm(renderErr("turf_apply", content, service.StaticSessionState{}))
	for _, want := range []string{
		"round 1 · failed · 2 applied · 1 failed · auto-approved",
		"✗ aws_s3_bucket.logs: BucketAlreadyExists: the name is taken",
		"⚠ provider returned a deprecation warning",
		"round 1 failed: 1 effect failed",
	} {
		if !strings.Contains(shown, want) {
			t.Fatalf("failed expansion missing %q:\n%s", want, shown)
		}
	}
	if strings.Contains(shown, `{"`) || strings.Contains(shown, "convergence_id") {
		t.Fatalf("a failed convergence leaked its raw JSON:\n%s", shown)
	}

	// A refusal is plain text and stays an ordinary error line, led by the convergence
	// the call named.
	const refusal = `stale plan digest: plan_digest "sha256:0000" is not the digest of the plan round 1 waits on.`
	ref := plainNorm(renderErrArgs("turf_apply", refusal, `{"convergence_id":"p-5f3d7adf","plan_digest":"sha256:0000"}`, service.StaticSessionState{}))
	for _, want := range []string{"Apply Plan", "p-5f3d7adf", "stale plan digest"} {
		if !strings.Contains(ref, want) {
			t.Fatalf("refusal missing %q: %q", want, ref)
		}
	}
}

// Stalled, exhausted, declined, stale and cancelled are neutral endings that still
// owe an account: a stall names how much was left deferred.
func TestConvergence_StalledNamesWhatIsLeft(t *testing.T) {
	const content = `{"convergence_id":"p-7","workspace_alias":"default","kind":"up","outcome":"stalled",
		"rounds":[{"round":1,"status":"applied","approval":"asked","planned":4,"deferred":2,"applied":2},
			{"round":2,"status":"applied","approval":"unneeded","planned":2,"deferred":2,"applied":0}],
		"outputs":{"finalized":["a"],"carried":["b"]},"next":"…"}`
	out := plainNorm(renderErr("turf_apply", content, service.StaticSessionState{}))
	for _, want := range []string{"stalled · 2 deferred", "2 applied", "carried b (still deferred)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stalled missing %q:\n%s", want, out)
		}
	}
}

// A stale plan was approved and never applied, because the configuration moved
// after it: a neutral ending, its round cancelled at its gate, approved, saying how
// the configuration moved.
func TestConvergence_StaleSaysPlanAgain(t *testing.T) {
	const content = `{"convergence_id":"p-8","workspace_alias":"default","kind":"up","outcome":"stale",
		"rounds":[{"round":1,"status":"cancelled","approval":"asked","planned":1,"deferred":0,"applied":0,
			"stale_reason":"the configuration changed after it was planned"}],
		"next":"The configuration changed, or no longer loads, after this plan was made (rounds[].stale_reason), so nothing was applied. If it no longer loads, fix what stale_reason names; then run up to plan it again and review the new plan. Don't edit between up and apply."}`
	compact := plainNorm(renderErr("turf_apply", content, hiddenState{}))
	for _, want := range []string{"p-8", "stale"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("stale compact missing %q: %q", want, compact)
		}
	}
	shown := plainNorm(renderErr("turf_apply", content, service.StaticSessionState{}))
	for _, want := range []string{"round 1 · cancelled · 1 planned · approved", "stale: the configuration changed after it was planned"} {
		if !strings.Contains(shown, want) {
			t.Fatalf("stale expansion missing %q:\n%s", want, shown)
		}
	}
}

// awaiting_actions lists every open turf action: what it asks, what a failed answer
// does, the values it was handed — and which answers the last apply refused.
func TestConvergence_AwaitingActions(t *testing.T) {
	const content = `{"convergence_id":"p-3","workspace_alias":"default","kind":"up","outcome":"awaiting_actions","round":1,
		"action_requests":[
			{"key":"k1","round":1,"action":"action.turf_action.drain","invocation":"action.turf_action.drain@before_update@aws_instance.web",
			 "caller":"aws_instance.web","event":"before_update","on_failure":"halt","if_failed":"halts aws_instance.web and what waits on it",
			 "prompt":"Drain the node before it is updated.","inputs":{"node":"ip-10-0-0-1","timeout":300}},
			{"key":"k2","round":1,"action":"action.turf_action.ask","invocation":"action.turf_action.ask@invoke",
			 "event":"invoke","on_failure":"continue","if_failed":"is recorded as a warning; the run continues",
			 "prompt":"Ask the user to confirm the maintenance window."}],
		"refused":{"k2":"a result whose verdict and diagnostics disagree"},
		"rounds":[{"round":1,"status":"applying","approval":"asked","planned":2,"deferred":0,"applied":1}],
		"next":"…"}`
	compact := plainNorm(renderFor("turf_apply", content, hiddenState{}))
	for _, want := range []string{"p-3", "round 1", "awaiting 2 action(s)", "1 refused"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("awaiting_actions compact missing %q: %q", want, compact)
		}
	}
	shown := plainNorm(renderFor("turf_apply", content, service.StaticSessionState{}))
	for _, want := range []string{
		"2 action(s) waiting:",
		"k1 · action.turf_action.drain · before_update of aws_instance.web",
		"Drain the node before it is updated.",
		"if it fails: halts aws_instance.web and what waits on it",
		`node = "ip-10-0-0-1"`, "timeout = 300",
		"k2 · action.turf_action.ask · invoked directly",
		"refused: a result whose verdict and diagnostics disagree",
		"round 1 · applying · 1 applied",
	} {
		if !strings.Contains(shown, want) {
			t.Fatalf("awaiting_actions expansion missing %q:\n%s", want, shown)
		}
	}
}

// An answer sent to a convergence that already ended is refused with no open request
// to sit under; the line must still show it, or the misdirected answer vanishes.
func TestConvergence_RefusedAfterTheEnd(t *testing.T) {
	const content = `{"convergence_id":"p-1","workspace_alias":"default","kind":"up","outcome":"converged",
		"refused":{"k9":"convergence p-1 is converged: nothing is waiting on a result"},
		"rounds":[{"round":1,"status":"applied","approval":"asked","planned":1,"deferred":0,"applied":1}],"next":"…"}`
	out := plainNorm(renderFor("turf_apply", content, service.StaticSessionState{}))
	for _, want := range []string{"converged", "1 refused", "refused:", "k9: convergence p-1 is converged: nothing is waiting on a result"} {
		if !strings.Contains(out, want) {
			t.Fatalf("refusal after the end missing %q:\n%s", want, out)
		}
	}
}

// A round lists the invocations that did not simply succeed, in the console's words.
func TestConvergence_ActionOutcomes(t *testing.T) {
	const content = `{"convergence_id":"p-4","workspace_alias":"default","kind":"up","outcome":"failed",
		"rounds":[{"round":1,"status":"failed","approval":"asked","planned":3,"deferred":0,"applied":1,"failed":1,"invoked":3,
			"actions":[
				{"invocation":"i1","action":"action.aws_lambda_invoke.migrate","type":"aws_lambda_invoke","caller":"aws_db_instance.main","event":"after_create","status":"failed","on_failure":"taint","detail":"timeout","caller_tainted":true},
				{"invocation":"i2","action":"action.turf_action.notify","type":"turf_action","caller":"aws_db_instance.main","event":"after_create","status":"failed","on_failure":"continue","detail":"slack is down"},
				{"invocation":"i3","action":"action.turf_action.gate","type":"turf_action","caller":"aws_db_instance.main","event":"before_create","status":"skipped"},
				{"invocation":"i4","action":"action.turf_action.log","type":"turf_action","status":"invoked","warnings":["log retention is short"]}],
			"actions_omitted":2}],
		"next":"…"}`
	out := plainNorm(renderErr("turf_apply", content, service.StaticSessionState{}))
	for _, want := range []string{
		"action.aws_lambda_invoke.migrate · after_create of aws_db_instance.main · failed · tainted its caller: timeout",
		"action.turf_action.notify · after_create of aws_db_instance.main · failed · continued: slack is down",
		"action.turf_action.gate · before_create of aws_db_instance.main · skipped — its condition was false",
		"action.turf_action.log · invoked directly · invoked",
		"⚠ log retention is short",
		"…(+2 more actions)",
		"3 invoked",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("action outcomes missing %q:\n%s", want, out)
		}
	}
}

// The plan tally follows Terraform's summary: a replace counts in both + and -, with
// import, forget and the actions to invoke as words beside it.
func TestConvergence_PlanTallySegments(t *testing.T) {
	const content = `{"convergence_id":"p-5","workspace_alias":"west","kind":"up","outcome":"awaiting_approval","round":1,
		"plan":{"uri":"turf://workspaces/west/convergences/p-5/rounds/1/plan","digest":"sha256:ab","add":2,"change":1,"destroy":1,"replace":1,"import":1,"forget":1,"read":2,
			"drift":1,"invocations":[{"address":"action.turf_action.gate","type":"turf_action","caller":"aws_instance.web","event":"before_create","on_failure":"halt"},
				{"address":"action.aws_lambda_invoke.ping","type":"aws_lambda_invoke","event":"after_update","caller":"aws_instance.web","on_failure":"continue"}]},
		"rounds":[{"round":1,"status":"awaiting_approval","approval":"asked","planned":6,"deferred":0,"applied":0}],"next":"…"}`
	// Long enough to wrap at the test width, so newlines are folded before asserting.
	compact := plainNorm(strings.ReplaceAll(renderFor("turf_up", content, hiddenState{}), "\n", " "))
	for _, want := range []string{"west", "+2 ~1 -1", "1 replaced", "1 to import", "1 to forget", "2 to read", "2 to invoke", "1 drifted"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("tally missing %q: %q", want, compact)
		}
	}
	shown := plainNorm(renderFor("turf_up", content, service.StaticSessionState{}))
	for _, want := range []string{
		"2 to invoke:",
		"action.turf_action.gate · before_create of aws_instance.web",
		"action.aws_lambda_invoke.ping · after_update of aws_instance.web · on failure: continue",
	} {
		if !strings.Contains(shown, want) {
			t.Fatalf("invocations missing %q:\n%s", want, shown)
		}
	}
	// halt is the default, so it is not spelled out.
	if strings.Contains(shown, "on failure: halt") {
		t.Fatalf("the default on_failure should not be shown:\n%s", shown)
	}
}

// While a convergence call runs there is no result yet, and cagent does not relay the
// server's progress notifications, so the line says what the call is doing.
func TestConvergence_RunningWords(t *testing.T) {
	run := func(name, args string) string {
		msg := &types.Message{
			ToolStatus:     types.ToolStatusRunning,
			ToolCall:       tools.ToolCall{Function: tools.FunctionCall{Name: name, Arguments: args}},
			ToolDefinition: tools.Tool{Name: name},
		}
		b := turfToolRenderers()[name](animation.NewRuntime(), msg, service.StaticSessionState{})
		b.SetSize(120, 1)
		return plainNorm(b.View())
	}
	cases := []struct{ name, args, want string }{
		{"turf_up", `{}`, "planning…"},
		{"turf_destroy", `{"workspace_alias":"west"}`, "west · planning…"},
		{"turf_apply", `{"convergence_id":"p-1","plan_digest":"sha256:ab"}`, "p-1 · applying…"},
		{"turf_apply", `{"convergence_id":"p-1","action_results":{"k1":{"succeeded":true}}}`, "answering actions…"},
		{"turf_cancel", `{}`, "cancelling…"},
	}
	for _, c := range cases {
		if out := run(c.name, c.args); !strings.Contains(out, c.want) {
			t.Errorf("%s running line missing %q: %q", c.name, c.want, out)
		}
	}
}

// --- show ---------------------------------------------------------------------

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The recorded round-2 plan of fixture core: two creates whose ids are unknown until
// apply, four no-ops, and one output change.
func TestShow_PlanDocument(t *testing.T) {
	doc := readTestdata(t, "show-plan-core.json")
	compact := plainNorm(renderWithArgs("turf_show", doc, `{"convergence_id":"p-5f3d7adf","round":2}`, hiddenState{}, 1))
	for _, want := range []string{"Show", "plan · p-5f3d7adf · round 2", "+2", "1 output change(s)"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("show compact missing %q: %q", want, compact)
		}
	}
	shown := plainNorm(renderWithArgs("turf_show", doc, `{}`, service.StaticSessionState{}, 1))
	for _, want := range []string{
		"+ terraform_data.fan[0]",
		`+ input = "v1-happy-buffalo-0"`,
		"+ id = (known after apply)",
		"+ output = (known after apply)",
		"outputs:",
		"+ fan = [(known after apply), (known after apply)]",
	} {
		if !strings.Contains(shown, want) {
			t.Fatalf("show expansion missing %q:\n%s", want, shown)
		}
	}
	// Terraform lists no no-ops, and neither does a sentinel reach the screen.
	for _, absent := range []string{"local_file.marker", "random_pet.name", "__cty_", "triggers_replace"} {
		if strings.Contains(shown, absent) {
			t.Fatalf("show expansion should not contain %q:\n%s", absent, shown)
		}
	}
}

// One of every change a plan document can carry, in Terraform's glyphs — and the
// document is NOT redacted (show_sensitive), so its masks are all that stands between
// the secrets and the screen.
func TestShow_EveryActionAndItsMasks(t *testing.T) {
	doc := readTestdata(t, "show-plan-every-action.json")
	compact := plainNorm(renderFor("turf_show", doc, hiddenState{}))
	for _, want := range []string{"+2 ~1 -2", "1 replaced", "1 to import", "1 to forget", "1 to read", "1 to invoke", "1 deferred", "1 drifted", "1 output change(s)"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("every-action compact missing %q: %q", want, compact)
		}
	}
	shown := plainNorm(renderFor("turf_show", doc, service.StaticSessionState{}))
	for _, want := range []string{
		"+ aws_db_instance.main",
		"+ password = (sensitive value)",
		"+ port = 5432",
		"~ aws_instance.web",
		`~ tags = {`,
		"- aws_instance.old",
		"-/+ aws_instance.r",
		"aws_s3_bucket.b · adopt id=b",
		"<= data.aws_ami.a",
		". aws_instance.gone",
		"changed outside Turf:",
		"aws_instance.web · drifted",
		"~ secret = (sensitive value) → (sensitive value)",
		"deferred:",
		"+ k8s.x · resource_config_unknown",
		"+ conn = (sensitive value)",
		"1 to invoke:",
		"action.turf_confirm.gate · before_create of aws_db_instance.main",
	} {
		if !strings.Contains(shown, want) {
			t.Fatalf("every-action expansion missing %q:\n%s", want, shown)
		}
	}
	// t0k is the deferred change's sensitive value; deferred rows render header-only,
	// so its absence is a tripwire for a future body rather than proof of its mask.
	for _, secret := range []string{"hunter2", "t0k"} {
		secretAbsent(t, shown, secret, "show")
	}
}

// A change whose after is unknown as a whole has no attributes to diff: it reads
// "(known after apply)", and an update must not turn into a list of removals.
func TestShow_WhollyUnknownAfter(t *testing.T) {
	const doc = `{"planned_values":{},"resource_changes":[
		{"address":"k8s_manifest.a","change":{"actions":["create"],"before":null,"after":null,"after_unknown":true}},
		{"address":"k8s_manifest.b","change":{"actions":["update"],"before":{"spec":"old"},"after":null,"after_unknown":true}}]}`
	out := plainNorm(renderFor("turf_show", doc, service.StaticSessionState{}))
	if strings.Count(out, "(known after apply)") != 2 {
		t.Fatalf("each wholly-unknown change should read (known after apply) once:\n%s", out)
	}
	if strings.Contains(out, "- spec") {
		t.Fatalf("a wholly-unknown update rendered as a removal:\n%s", out)
	}
}

// An empty state document is a workspace with nothing in it, not an unparseable one.
func TestShow_EmptyStateDocument(t *testing.T) {
	out := plainNorm(renderWithArgs("turf_show", `{"format_version":"1.0"}`, `{"what":"state"}`, hiddenState{}, 1))
	if !strings.Contains(out, "state · 0 resource(s)") {
		t.Fatalf("empty state document: %q", out)
	}
}

// The engine's document spells an invocation's event and on_failure as Terraform's
// binary prints them; the line spells them as the configuration does.
func TestShow_InvocationSpelling(t *testing.T) {
	const doc = `{"format_version":"1.2","planned_values":{},"resource_changes":[],
		"action_invocations":[{"address":"action.turf_action.notify","type":"turf_action",
			"lifecycle_action_trigger":{"triggering_resource_address":"aws_instance.web","action_trigger_event":"AfterUpdate","on_failure":"ActionOnFailureContinue"}}]}`
	out := plainNorm(renderFor("turf_show", doc, service.StaticSessionState{}))
	if !strings.Contains(out, "action.turf_action.notify · after_update of aws_instance.web · on failure: continue") {
		t.Fatalf("invocation not in the configuration's spelling:\n%s", out)
	}
	// Like Terraform's "Actions: 1 to invoke", the invocation is the plan's whole
	// story: no resource bucket, and no "no changes" contradicting it.
	if !strings.Contains(out, "plan · 1 to invoke") || strings.Contains(out, "no changes") {
		t.Fatalf("an actions-only plan should tally its invocations alone: %s", out)
	}
}

func TestShow_StateDocument(t *testing.T) {
	const doc = `{"format_version":"1.0","values":{
		"outputs":{"pw":{"sensitive":true,"value":"hunter2"},"name":{"sensitive":false,"value":"v1-happy-buffalo"}},
		"root_module":{"resources":[
			{"address":"random_pet.name","values":{"id":"v1-happy-buffalo","length":2},"sensitive_values":{}},
			{"address":"random_password.db","values":{"result":"hunter2","length":20},"sensitive_values":{"result":true},"tainted":true}],
		"child_modules":[{"resources":[{"address":"module.net.aws_vpc.this","values":{"cidr_block":"10.0.0.0/16"}}]}]}}}`
	compact := plainNorm(renderWithArgs("turf_show", doc, `{"what":"state"}`, hiddenState{}, 1))
	for _, want := range []string{"Show", "state", "3 resource(s)", "2 output(s)"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("state compact missing %q: %q", want, compact)
		}
	}
	shown := plainNorm(renderWithArgs("turf_show", doc, `{"what":"state","show_sensitive":true}`, service.StaticSessionState{}, 1))
	for _, want := range []string{
		"random_password.db · tainted", "result = (sensitive value)", "length = 20",
		"module.net.aws_vpc.this", `cidr_block = "10.0.0.0/16"`,
		"pw = (sensitive value)", `name = "v1-happy-buffalo"`,
	} {
		if !strings.Contains(shown, want) {
			t.Fatalf("state expansion missing %q:\n%s", want, shown)
		}
	}
	secretAbsent(t, shown, "hunter2", "show state")
}

func TestPlanAction_TerraformGlyphs(t *testing.T) {
	cases := []struct {
		actions []string
		glyph   string
	}{
		{[]string{"create"}, "+"},
		{[]string{"update"}, "~"},
		{[]string{"delete"}, "-"},
		{[]string{"delete", "create"}, "-/+"},
		{[]string{"create", "delete"}, "+/-"},
		{[]string{"forget", "create"}, "./+"},
		{[]string{"read"}, "<="},
		{[]string{"forget"}, "."},
		{[]string{"no-op"}, ""},
		{[]string{"bogus"}, "?"},
	}
	for _, c := range cases {
		if _, _, g := planAction(c.actions); g != c.glyph {
			t.Errorf("planAction(%v) glyph = %q, want %q", c.actions, g, c.glyph)
		}
	}
}

func TestWithUnknowns(t *testing.T) {
	after := map[string]any{"input": "x", "tags": map[string]any{"a": "1"}, "list": []any{"k", nil}}
	unknown := map[string]any{"id": true, "tags": map[string]any{"b": true}, "list": []any{false, true}, "input": false}
	got := withUnknowns(after, unknown).(map[string]any)
	if got["id"] != unknownSentinel || got["input"] != "x" {
		t.Fatalf("scalar overlay wrong: %#v", got)
	}
	if tags := got["tags"].(map[string]any); tags["a"] != "1" || tags["b"] != unknownSentinel {
		t.Fatalf("nested overlay wrong: %#v", tags)
	}
	if list := got["list"].([]any); list[0] != "k" || list[1] != unknownSentinel {
		t.Fatalf("list overlay wrong: %#v", list)
	}
	// A wholly unknown value, and no mask at all.
	if withUnknowns(nil, true) != unknownSentinel {
		t.Fatal("a wholly unknown value should be the sentinel")
	}
	if v := withUnknowns("x", nil); v != "x" {
		t.Fatalf("no mask should leave the value alone: %#v", v)
	}
	// An empty mask must not turn a destroy's null `after` into an empty object.
	if v := withUnknowns(nil, map[string]any{}); v != nil {
		t.Fatalf("an empty mask should leave null alone: %#v", v)
	}
	if after["id"] != nil {
		t.Fatal("withUnknowns must not mutate its input")
	}
}

// --- init / workspaces / state / outputs ------------------------------------

// initCore is init's result for fixture core, recorded, with the hashes and package
// directories dropped.
const initCore = `{"config_alias":"default","path":"/work/v1-run/core","backend":{"type":"local","defaulted":true},"workspace":"default",
	"providers":[{"provider":"registry.opentofu.org/hashicorp/local","version":"2.9.1","constraints":"~> 2.5"},
		{"provider":"registry.opentofu.org/hashicorp/random","version":"3.9.1","constraints":"~> 3.6"}],
	"lock_file":{"path":"/work/v1-run/core/.terraform.lock.hcl","status":"created"},
	"variables":[{"name":"prefix","type":"string","default":"v1","required":false}],
	"outputs":[{"name":"fan"},{"name":"password","sensitive":true},{"name":"pet"}]}`

// The unnamed configuration ("default") leads with its path, not the sentinel.
func TestInit_Core(t *testing.T) {
	compact := plainNorm(renderFor("turf_init", initCore, hiddenState{}))
	for _, want := range []string{"Init Configuration /work/v1-run/core", "backend local (default)", "2 provider(s) · 1 variable(s) · 3 output(s)"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("init compact missing %q: %q", want, compact)
		}
	}
	if strings.Contains(compact, "default ·") {
		t.Fatalf("the unnamed configuration's alias must not lead the line: %q", compact)
	}
	shown := plainNorm(renderFor("turf_init", initCore, service.StaticSessionState{}))
	for _, want := range []string{
		"hashicorp/local ~> 2.5 → v2.9.1",
		"hashicorp/random ~> 3.6 → v3.9.1",
		"prefix string",
		"password sensitive",
		"lock file created",
	} {
		if !strings.Contains(shown, want) {
			t.Fatalf("init expansion missing %q:\n%s", want, shown)
		}
	}
}

// A named configuration leads with its alias; a declared backend says nothing about
// defaults; an import block, a failed install and an unpinned provider each show.
func TestInit_AliasImportsAndInstallErrors(t *testing.T) {
	const content = `{"config_alias":"web","path":"infra/web","backend":{"type":"s3"},"workspace":"default",
		"providers":[{"provider":"registry.opentofu.org/hashicorp/aws","constraints":"~> 6.0","error":"no version matches ~> 6.0"},
			{"provider":"example.com/acme/dns","version":"1.2.0"}],
		"imports":[{"to":"aws_s3_bucket.logs","id":"my-logs"}],"modules_installed":true}`
	out := plainNorm(renderFor("turf_init", content, service.StaticSessionState{}))
	for _, want := range []string{
		"web · infra/web · backend s3", "1 to import", "1 provider(s) failed to install",
		"hashicorp/aws ~> 6.0 no version matches ~> 6.0",
		"example.com/acme/dns unpinned → v1.2.0",
		"aws_s3_bucket.logs · id=my-logs", "modules installed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("init missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "(default)") {
		t.Fatalf("a declared backend must not read as defaulted: %s", out)
	}
}

func TestWorkspaceOpen_Binding(t *testing.T) {
	const content = `{"workspace_alias":"default","config_alias":"default","config_path":"/work/v1-run/core",
		"workspace_name":"default","backend_type":"local","state_path":"/work/v1-run/core/terraform.tfstate",
		"lineage":"fa47e9cf","serial":5,"resource_count":6}`
	compact := plainNorm(renderFor("turf_open", content, hiddenState{}))
	for _, want := range []string{"Open Workspace", "default · local · /work/v1-run/core · 6 resource(s)"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("turf_open compact missing %q: %q", want, compact)
		}
	}
	shown := plainNorm(renderFor("turf_open", content, service.StaticSessionState{}))
	if !strings.Contains(shown, "/work/v1-run/core/terraform.tfstate · serial 5 · lineage fa47e9cf") {
		t.Fatalf("turf_open expansion missing the statefile:\n%s", shown)
	}
}

// Reopening a workspace a convergence still holds is how a paused round survives a
// restart; the open line has to say so.
func TestWorkspaceOpen_ReportsTheHoldingConvergence(t *testing.T) {
	const content = `{"workspace_alias":"prod","config_alias":"web","config_path":"infra/web","workspace_name":"default",
		"backend_type":"s3","resource_count":4,
		"convergence":{"id":"p-5f3d7adf","kind":"up","stage":"awaiting_approval","round":2,"followed":true,"next":"…"}}`
	out := plainNorm(renderFor("turf_open", content, hiddenState{}))
	for _, want := range []string{"prod", "holds p-5f3d7adf · deploy · awaiting approval · round 2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("turf_open missing %q: %q", want, out)
		}
	}
	// The alias is the session's handle for the workspace, so it wins over the state
	// slot's name ("default").
	if strings.Contains(out, "default") {
		t.Fatalf("the alias should win over the workspace name: %q", out)
	}
}

func TestWorkspaceShow_Entries(t *testing.T) {
	const content = `{"workspaces":[
		{"workspace_alias":"prod","config_alias":"web","config_path":"infra/web","workspace_name":"default","backend_type":"s3",
		 "opened_at":"2026-10-13T10:00:00Z","convergence":{"id":"p-3","kind":"up","stage":"awaiting_actions","round":1,"open_questions":2,"followed":true}},
		{"workspace_alias":"dev","config_alias":"web","config_path":"infra/web","workspace_name":"dev","opened_at":"2026-10-13T10:05:00Z",
		 "warning":"the engine is unreachable"}]}`
	compact := plainNorm(renderFor("turf_status", content, hiddenState{}))
	if !strings.Contains(compact, "2 open") {
		t.Fatalf("turf_status compact: %q", compact)
	}
	shown := plainNorm(renderFor("turf_status", content, service.StaticSessionState{}))
	for _, want := range []string{
		"prod · s3 · infra/web · opened 2026-10-13T10:00:00Z",
		"holds p-3 · deploy · awaiting 2 action(s) · round 1",
		"dev · infra/web",
		"⚠ the engine is unreachable",
	} {
		if !strings.Contains(shown, want) {
			t.Fatalf("turf_status missing %q:\n%s", want, shown)
		}
	}
	single := plainNorm(renderFor("turf_status", `{"workspaces":[{"workspace_alias":"default","workspace_name":"default","config_path":"/c","opened_at":"t"}]}`, hiddenState{}))
	if !strings.Contains(single, "default · /c") {
		t.Fatalf("a single workspace leads with its name: %q", single)
	}
}

func TestWorkspaceClose_Detached(t *testing.T) {
	plain := plainNorm(renderFor("turf_close", `{"workspace_alias":"default"}`, hiddenState{}))
	if !strings.Contains(plain, "default · closed") || strings.Contains(plain, "engine") {
		t.Fatalf("turf_close: %q", plain)
	}
	det := plainNorm(renderFor("turf_close", `{"workspace_alias":"prod","detached":"p-5f3d7adf"}`, hiddenState{}))
	if !strings.Contains(det, "prod · closed · left p-5f3d7adf running in the engine") {
		t.Fatalf("turf_close should name the convergence it detached from: %q", det)
	}
}

func TestWorkspaceList_Summary(t *testing.T) {
	const content = `{"workspaces": ["default", "staging", "prod"]}`
	out := renderFor("turf_list", content, service.StaticSessionState{})
	for _, want := range []string{"List Workspaces", "3 found", "staging"} {
		if !strings.Contains(out, want) {
			t.Fatalf("turf_list missing %q: %q", want, out)
		}
	}
	empty := renderFor("turf_list", `{"workspaces": []}`, hiddenState{})
	if !strings.Contains(empty, "none") {
		t.Fatalf("empty turf_list should say none: %q", empty)
	}
}

func TestWorkspaceDelete_Summary(t *testing.T) {
	out := plainNorm(renderFor("turf_delete", `{"workspace_name":"staging","resource_count":2,"deleted":true}`, hiddenState{}))
	for _, want := range []string{"Delete Workspace", "staging · deleted · 2 orphaned resource(s)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("turf_delete missing %q: %q", want, out)
		}
	}
}

// outputCore is output's result for fixture core, recorded.
const outputCore = `{"outputs":{"fan":{"sensitive":false,"type":["tuple",["string","string"]],"value":["v1-happy-buffalo-0","v1-happy-buffalo-1"]},
	"password":{"sensitive":true,"type":"string","value":"__cty_sensitive__"},
	"pet":{"sensitive":false,"type":"string","value":"v1-happy-buffalo"}},"workspace_alias":"default"}`

func TestOutput_Core(t *testing.T) {
	compact := plainNorm(renderFor("turf_output", outputCore, hiddenState{}))
	for _, want := range []string{"Read Outputs", "3 output(s) · 1 sensitive"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("output compact missing %q: %q", want, compact)
		}
	}
	shown := plainNorm(renderFor("turf_output", outputCore, service.StaticSessionState{}))
	for _, want := range []string{`fan = ["v1-happy-buffalo-0", "v1-happy-buffalo-1"]`, "password = (sensitive value)", `pet = "v1-happy-buffalo"`} {
		if !strings.Contains(shown, want) {
			t.Fatalf("output expansion missing %q:\n%s", want, shown)
		}
	}
	if strings.Contains(shown, "__cty_") || strings.Contains(shown, "tuple") {
		t.Fatalf("output leaked a sentinel or a type:\n%s", shown)
	}
	// Asked for one output by name, the line leads with it.
	one := plainNorm(renderWithArgs("turf_output", `{"outputs":{"pet":{"sensitive":false,"value":"x"}},"workspace_alias":"default"}`, `{"name":"pet"}`, hiddenState{}, 1))
	if !strings.Contains(one, "Read Outputs pet") {
		t.Fatalf("a named output should lead the line: %q", one)
	}
}

func TestStateList_ValuesTaintedDeposed(t *testing.T) {
	const content = `{"tofu_version":"1.12.6","workspace_alias":"default","lineage":"fa47e9cf","serial":5,"resources":[
		{"address":"random_password.db","mode":"managed","type":"random_password","name":"db","provider_name":"registry.opentofu.org/hashicorp/random","schema_version":3,
		 "values":{"length":20,"result":"__cty_sensitive__"},"sensitive_values":{"result":true}},
		{"address":"aws_instance.web","mode":"managed","type":"aws_instance","name":"web","provider_name":"registry.opentofu.org/hashicorp/aws","schema_version":1,
		 "tainted":true,"deposed":["00000001"],"identity":{"id":"i-123"}},
		{"address":"data.aws_ami.a","mode":"data","type":"aws_ami","name":"a","provider_name":"registry.opentofu.org/hashicorp/aws","schema_version":0}],
		"outputs":{"pet":{"sensitive":false,"value":"x"}},"warnings":["state records provider null, which the configuration no longer declares"]}`
	compact := plainNorm(renderFor("turf_state_list", content, hiddenState{}))
	for _, want := range []string{"List State", "3 resource(s) · 1 tainted · 1 deposed · 1 output(s) · serial 5"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("state_list compact missing %q: %q", want, compact)
		}
	}
	shown := plainNorm(renderFor("turf_state_list", content, service.StaticSessionState{}))
	for _, want := range []string{
		"random_password.db", "length = 20", "result = (sensitive value)",
		"aws_instance.web · tainted · deposed 00000001 · identity",
		"data.aws_ami.a · data",
		"outputs:", `pet = "x"`,
		"⚠ state records provider null",
	} {
		if !strings.Contains(shown, want) {
			t.Fatalf("state_list expansion missing %q:\n%s", want, shown)
		}
	}
}

func TestTaint_Line(t *testing.T) {
	out := plainNorm(renderWithArgs("turf_taint", `{"workspace_alias":"default","addrs":["aws_instance.web","aws_instance.db"],"status":"tainted","serial":8}`,
		`{"addrs":["aws_instance.web","aws_instance.db"]}`, hiddenState{}, 1))
	if !strings.Contains(out, "Taint Resource aws_instance.web, aws_instance.db · tainted · serial 8") {
		t.Fatalf("taint line: %q", out)
	}
	un := plainNorm(renderFor("turf_untaint", `{"workspace_alias":"default","addrs":["aws_instance.web"],"status":"untainted","serial":9}`, hiddenState{}))
	if !strings.Contains(un, "Untaint Resource aws_instance.web · untainted · serial 9") {
		t.Fatalf("untaint line: %q", un)
	}
}

func TestProviderDescribe_Summary(t *testing.T) {
	const content = `{"schema_version":0,"provider":"registry.opentofu.org/hashicorp/random","type":"random_pet",
		"description": "Generates random pet names.",
		"properties": {"length": {"type": "number", "usage": "optional"},
		"id": {"type": "string", "usage": "computed", "sensitive": false}},
		"required": [], "identity_schema": null}`
	out := renderWithArgs("turf_provider_describe", content, `{"provider":"hashicorp/random","provider_version":"3.6.0","resource_type":"random_pet"}`, service.StaticSessionState{}, 1)
	for _, want := range []string{"Describe Provider", "random_pet", "2 attr(s)", "hashicorp/random", "length", "optional", "Generates random pet names"} {
		if !strings.Contains(out, want) {
			t.Fatalf("provider_describe missing %q: %q", want, out)
		}
	}
	if strings.Contains(plainNorm(out), "identity") {
		t.Fatalf("a type with no identity schema should not claim one: %q", out)
	}
}

// Asked for the builtin turf provider with no type, provider_describe answers with the
// provider's whole schema, which lists Turf's own actions.
func TestProviderDescribe_WholeProvider(t *testing.T) {
	const content = `{"provider":{"type":"","properties":{}},"resources":{},"data_sources":{},
		"actions":{"turf_action":{"type":"turf_action","properties":{"prompt":{"type":"string"}}}},"version":"0.0.0"}`
	out := plainNorm(renderWithArgs("turf_provider_describe", content, `{"provider":"terraform.io/builtin/turf"}`, service.StaticSessionState{}, 1))
	for _, want := range []string{"terraform.io/builtin/turf · 1 action(s)", "actions:", "turf_action"} {
		if !strings.Contains(out, want) {
			t.Fatalf("whole-provider describe missing %q: %q", want, out)
		}
	}
}

func TestProviderSearch_Summary(t *testing.T) {
	const content = `{"providers": [
		{"name": "hashicorp/aws", "version": "6.1.0", "description": "Amazon Web Services", "popularity": 0.98},
		{"name": "hashicorp/awscc", "version": "1.0.0", "description": "AWS Cloud Control", "popularity": 0.5}]}`
	out := renderWithArgs("turf_provider_search", content, `{"query":"aws"}`, service.StaticSessionState{}, 1)
	for _, want := range []string{"Search Providers", "aws", "2 result(s)", "hashicorp/aws", "6.1.0", "Amazon Web Services"} {
		if !strings.Contains(out, want) {
			t.Fatalf("provider_search missing %q: %q", want, out)
		}
	}
}

// TestRenderers_MasksSurviveToTheTimeline drives the real renderers with the payload a
// show_sensitive call produces — plaintext values plus their mask — and asserts nothing
// reaches the rendered line. This is the end the mask plumbing exists for.
func TestRenderers_MasksSurviveToTheTimeline(t *testing.T) {
	const secret = "hunter2"
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"turf_show", `{"planned_values":{},"resource_changes":[{
			"address": "random_password.pw", "change": {"actions": ["create"],
			"before": null, "after": {"result": "` + secret + `", "length": 16},
			"after_unknown": {}, "after_sensitive": {"result": true}}}]}`, "+ result = (sensitive value)"},
		{"turf_show", `{"planned_values":{},"resource_changes":[{
			"address": "random_pet.p", "change": {"actions": ["update"],
			"before": {"keepers": {"pw": "` + secret + `"}},
			"after": {"keepers": {"pw": "` + secret + `-next"}},
			"before_sensitive": {"keepers": {"pw": true}},
			"after_sensitive": {"keepers": {"pw": true}}}}]}`, "pw = (sensitive value)"},
		{"turf_output", `{
			"workspace_alias": "default",
			"outputs": {"pw": {"value": "` + secret + `", "sensitive": true},
			            "name": {"value": "plain", "sensitive": false}}
		}`, "pw = (sensitive value)"},
		{"turf_state_list", `{"workspace_alias":"default","resources":[{"address":"random_password.pw","mode":"managed","type":"random_password",
			"values":{"result":"` + secret + `"},"sensitive_values":{"result":true}}],
			"outputs":{"pw":{"value":"` + secret + `","sensitive":true}}}`, "result = (sensitive value)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := renderFor(tc.name, tc.content, service.StaticSessionState{})
			secretAbsent(t, out, secret, tc.name)
			if !strings.Contains(plainNorm(out), tc.want) {
				t.Fatalf("%s missing %q:\n%s", tc.name, tc.want, out)
			}
		})
	}
}
