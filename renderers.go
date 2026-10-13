package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/docker/docker-agent/pkg/tui/animation"
	"github.com/docker/docker-agent/pkg/tui/components/spinner"
	"github.com/docker/docker-agent/pkg/tui/components/tool"
	"github.com/docker/docker-agent/pkg/tui/components/toolcommon"
	"github.com/docker/docker-agent/pkg/tui/core/layout"
	"github.com/docker/docker-agent/pkg/tui/service"
	"github.com/docker/docker-agent/pkg/tui/styles"
	"github.com/docker/docker-agent/pkg/tui/types"
)

// Custom TUI renderers for turf's tools. The turf MCP toolset is registered under
// the "turf" name (agent.go), so cagent exposes its tools with a "turf_" prefix —
// that prefixed name is the renderer key (see turfToolRenderers). The tool result
// arrives as JSON in msg.Content (MCP TextContent from turf-mcp-server); we parse
// only the fields we draw into local mirror structs, since the authoritative result
// types in the turf module are unexported.
//
// Design goals (per the experiment): blend into the conversation. Every renderer
// emits a borderless icon + one colored line, matching the default tool line
// (✓ Title …) so turf tools read as first-class conversation entries. The line leads
// with the friendly, English-readable tool title (e.g. "Apply Plan") — resolved via
// toolTitle from the shared turfToolInfo source and styled with the same neutral
// styles.ToolName cagent's built-in renderers use — followed by the target + status.
// Color and the status glyph come from cagent's theme via the styles package and
// toolcommon.Icon, so the whole thing re-themes for free when the user runs /theme.
//
// Two detail levels, switched by cagent's HideToolResults() toggle (Ctrl+O), which
// every renderer receives via SessionStateReader:
//   - hidden  → the compact one-liner.
//   - shown   → the one-liner PLUS a rich, indented "unfold": a convergence's
//     rounds and invocations, a plan document's attribute diffs (before → after,
//     colored +/~/-), state values, outputs — plenty of detail for inspecting a
//     step. This is turf's default (see newSession).
//
// The vocabulary is Terraform's where Terraform has a word for it (the plan glyphs
// in planAction, "(known after apply)", "(sensitive value)"), and the Turf engine
// console's where it is a Turf concept Terraform lacks: a convergence's outcome
// (converged, stalled, exhausted, declined), a round's status, an action
// invocation's verdict. Color stays the CLI theme's own.
//
// Reusing toolcommon.Icon gives us the live spinner, elapsed-time, and exact
// left-margin alignment of the built-in tool lines at no cost.
//
// Entry-point parity with cagent. We plug into cagent's renderer registry through the
// exact hook its own built-ins use — nothing bespoke at the integration layer:
//   - Registration: tui.WithToolRenderers(map[string]tool.Builder) → tool.Register(key,
//     builder), which cagent's tool.New() consults (by exact tool name) before its
//     built-in table — the same path readfile/shell/todo/etc. take.
//   - Shape: tool.Builder + toolcommon.NewBase(msg, ss, render) + toolcommon.Renderer,
//     identical to every built-in component (see cagent pkg/tui/components/tool).
//
// The divergence is one level lower, inside the Renderer bodies, and is deliberate:
//   - We hand-write each Renderer rather than composing cagent's SimpleRenderer /
//     SimpleRendererWithResult + RenderTool helpers. Those model a "one arg + one result
//     string" line; turf's views (attribute diffs, colored tallies, round lists, an
//     expand/collapse detail block) don't fit that shape. This is also why the leading
//     title lives in line() (resolved from turfToolInfo) instead of delegating to
//     RenderTool, which derives its title from msg.ToolDefinition.DisplayName() — not
//     reliably populated on our messages.
//   - We parse msg.Content as JSON rather than reading a typed msg.ToolResult.Meta,
//     because turf's result types live in the separate turf-mcp-server process and cross
//     the MCP boundary as JSON text (the authoritative Go types are unexported).

// registerTurfToolRenderers installs turf's per-tool renderers on cagent's global
// tool-renderer registry. The full TUI does this via tui.WithToolRenderers (in
// runTUI); the lean TUI never calls tui.New, so runLeanTUI registers them directly
// — both paths funnel through the same tool.Register and the same renderer sources
// (turfToolRenderers plus builtinToolRenderers).
func registerTurfToolRenderers() {
	for name, b := range turfToolRenderers() {
		tool.Register(name, b)
	}
	for name, b := range builtinToolRenderers() {
		tool.Register(name, b)
	}
}

// builtinToolRenderers maps cagent *built-in* tool names (bare, not "turf_"-prefixed)
// to turf's overriding renderers. Unlike turfToolRenderers — turf's own MCP tools —
// these replace a view cagent already ships. Registered renderers win over the
// built-in ones (see tool.Register), so this quiets a built-in's default output.
// Currently just the "think" scratchpad, whose result echoes the whole running
// thought log; the default renderer dumps it verbatim, so we suppress it.
func builtinToolRenderers() map[string]tool.Builder {
	return map[string]tool.Builder{
		"think": builder(renderThink),
	}
}

// turfToolRenderers maps turf's "turf_"-prefixed tool names to renderers. Wired
// into the full TUI via tui.WithToolRenderers in tui.go, and into the lean TUI via
// registerTurfToolRenderers.
func turfToolRenderers() map[string]tool.Builder {
	return map[string]tool.Builder{
		"turf_init":              builder(renderInit),
		"turf_workspace_open":    builder(renderWorkspaceOpen),
		"turf_workspace_close":   builder(renderWorkspaceClose),
		"turf_workspace_list":    builder(renderWorkspaceList),
		"turf_workspace_show":    builder(renderWorkspaceShow),
		"turf_workspace_delete":  builder(renderWorkspaceDelete),
		"turf_up":                builder(renderConvergence),
		"turf_destroy":           builder(renderConvergence),
		"turf_refresh":           builder(renderConvergence),
		"turf_import":            builder(renderConvergence),
		"turf_invoke":            builder(renderConvergence),
		"turf_apply":             builder(renderConvergence),
		"turf_cancel":            builder(renderConvergence),
		"turf_show":              builder(renderShow),
		"turf_output":            builder(renderOutput),
		"turf_state_list":        builder(renderStateList),
		"turf_taint":             builder(renderTaint),
		"turf_untaint":           builder(renderTaint),
		"turf_provider_search":   builder(renderProviderSearch),
		"turf_provider_describe": builder(renderProviderDescribe),
		"turf_skill_core":        builder(renderSkill),
		"turf_skill_authoring":   builder(renderSkill),
		"turf_skill_codified":    builder(renderSkill),
		"turf_skill_demo":        builder(renderSkill),
		"turf_skill_guide":       builder(renderSkill),
		"turf_read_skill_file":   builder(renderReadSkillFile),
	}
}

// builder adapts a toolcommon.Renderer into a tool.Builder (the map value type
// tui.WithToolRenderers expects). NewBase supplies the spinner/sizing boilerplate.
func builder(render toolcommon.Renderer) tool.Builder {
	return func(ar *animation.Runtime, msg *types.Message, ss service.SessionStateReader) layout.Model {
		return toolcommon.NewBase(ar, msg, ss, render)
	}
}

// --- think (cagent built-in) ------------------------------------------------
//
// The built-in think tool is a pure reasoning scratchpad: each call appends a
// thought and returns the *entire* running log ("Thoughts:\n…"), which cagent's
// default renderer dumps verbatim — increasingly noisy as the log grows. This
// renderer suppresses that body, collapsing every think call to a single quiet
// "Think" line. A framework error is still surfaced (errorLine), since that is
// terse and diagnostic rather than the verbose success output we're hiding.
// Registered under the bare "think" name (builtinToolRenderers), so it overrides
// cagent's built-in view rather than living alongside turf's "turf_" tools.
func renderThink(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if out, ok := errorLine(msg, s, ss, width); ok {
		return out
	}
	content := toolcommon.Icon(msg, s) + styles.ToolName.Render("Think")
	return styles.RenderComposite(styles.ToolMessageStyle.Width(width), content)
}

// --- shared line helpers ----------------------------------------------------

// toolTitle returns the friendly English tool name for a message's tool, resolved
// from turfToolInfo (mcptoolset.go) — the same source the /tools dialog uses. We look
// it up from the tool name here rather than trusting msg.ToolDefinition.DisplayName()
// so the title is present regardless of whether the message carries the decorated
// Annotations.Title.
func toolTitle(msg *types.Message) string {
	return turfToolTitle(strings.TrimPrefix(msg.ToolCall.Function.Name, appName+"_"))
}

// line composes the standard tool line: status glyph + the friendly tool title +
// a one-line body, wrapped to width with the muted tool-message base style (so it
// sits like every other tool). The title leads every turf line — styled with the
// same neutral styles.ToolName cagent's built-in renderers use — so turf tools read
// as first-class conversation entries with a consistent, English-readable name.
func line(msg *types.Message, s spinner.Spinner, body string, width int) string {
	content := toolcommon.Icon(msg, s) + styles.ToolName.Render(toolTitle(msg))
	if body != "" {
		content += " " + body
	}
	return styles.RenderComposite(styles.ToolMessageStyle.Width(width), content)
}

// lineWithDetail renders the one-liner, plus an indented detail block when the user
// has expanded tool output (Ctrl+O). detail lines are already styled strings; a
// line may carry leading spaces to nest beneath a section header.
func lineWithDetail(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, summary string, detail []string, width int) string {
	if ss.HideToolResults() || len(detail) == 0 {
		return line(msg, s, summary, width)
	}
	var b strings.Builder
	b.WriteString(summary)
	for _, d := range detail {
		b.WriteString("\n    " + d)
	}
	return line(msg, s, b.String(), width)
}

// errorLine renders a failed tool call, surfacing the framework's error text
// (msg.Content, populated from cagent's ToolCallResult.Output on ToolStatusError). It
// leads with the request's target (errorTarget) — the resource/provider/workspace the
// call acted on — styled like a successful line's target (addr + " · "), so a failure is
// as scannable as a success and doesn't drop the context the error text often omits.
// It follows the same detail toggle (Ctrl+O) as every other turf line: expanded, the
// full error wraps across as many lines as it needs; compact, it collapses to a single
// line truncated (with an ellipsis) to fit beside the title and target. ok is false when
// the message isn't an error, so callers fall through to their normal render.
func errorLine(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width int) (string, bool) {
	if msg.ToolStatus != types.ToolStatusError {
		return "", false
	}
	var targetSeg string
	if t := errorTarget(msg); t != "" {
		targetSeg = addr(t) + dot()
	}
	text := strings.TrimSpace(msg.Content)
	if text == "" {
		return line(msg, s, targetSeg+styles.ErrorStyle.Render("failed"), width), true
	}
	if ss.HideToolResults() {
		// Compact: one line. Flatten any newlines and truncate the error to the width
		// left beside the title, target, and the space line() inserts before the body —
		// so the target stays visible and only the error text is clipped.
		flat := strings.Join(strings.Fields(text), " ")
		prefix := ansi.StringWidth(toolcommon.Icon(msg, s) + styles.ToolName.Render(toolTitle(msg)) + " " + targetSeg)
		if budget := width - prefix; budget > 0 {
			flat = ansi.Truncate(flat, budget, "…")
		}
		return line(msg, s, targetSeg+styles.ToolErrorMessageStyle.Render(flat), width), true
	}
	// Expanded: the full error, wrapped across lines by line()'s width styling.
	return line(msg, s, targetSeg+styles.ToolErrorMessageStyle.Render(text), width), true
}

// fallbackLine is the standard post-parseContent fallback: the framework's error
// detail on a failed call, else the bare title — unchanged behavior for a completed
// result whose JSON didn't match (e.g. TestRenderers_BadJSONFallsBack).
func fallbackLine(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width int) string {
	if out, ok := errorLine(msg, s, ss, width); ok {
		return out
	}
	return line(msg, s, "", width)
}

// targetBody renders a tool's primary target (a resource/provider/workspace address)
// as a one-line body, or "" when there is none — so the line degrades to the title
// alone rather than rendering an empty highlighted string. Handy for running/fallback
// branches where the target is the only thing worth showing beside the title.
func targetBody(target string) string {
	if target == "" {
		return ""
	}
	return addr(target)
}

// running reports whether the tool hasn't produced its result yet (in-flight).
func running(msg *types.Message) bool {
	return msg.ToolStatus != types.ToolStatusCompleted && msg.ToolStatus != types.ToolStatusError
}

// parseContent unmarshals msg.Content into v; ok is false while in-flight or when
// the content isn't the expected JSON (callers fall back to a raw/args view).
func parseContent[T any](msg *types.Message, v *T) bool {
	if running(msg) || msg.Content == "" {
		return false
	}
	return json.Unmarshal([]byte(msg.Content), v) == nil
}

// argString reads one request argument as display text. A list of strings (taint's
// addrs) reads as its comma-joined elements; any other non-string value reads as "".
func argString(msg *types.Message, key string) string {
	var m map[string]any
	if json.Unmarshal([]byte(msg.ToolCall.Function.Arguments), &m) != nil {
		return ""
	}
	switch v := m[key].(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, e := range v {
			str, ok := e.(string)
			if !ok {
				return ""
			}
			parts = append(parts, str)
		}
		return strings.Join(parts, ", ")
	}
	return ""
}

// argInt reads one numeric request argument (show's round); 0 when absent.
func argInt(msg *types.Message, key string) int {
	var m map[string]any
	if json.Unmarshal([]byte(msg.ToolCall.Function.Arguments), &m) != nil {
		return 0
	}
	if f, ok := m[key].(float64); ok {
		return int(f)
	}
	return 0
}

// turfToolTargetArgs maps a bare turf tool name to its request's target argument(s), in
// priority order — the identifier that names what the call operates on (a resource,
// provider, convergence, workspace, …). It mirrors the target each renderer already
// picks in its running(msg) branch; errorTarget uses it to give a failed line the same
// lead context a successful one has. Tools absent here (up, destroy, refresh, import,
// state_list, skills) fall back to the workspace alias (or nothing, for zero-arg skills).
// Arg names track the server tool structs in turf-mcp-server's internal/tools package,
// and TestTurfToolTargetArgsMatchServerSchemas holds them to the live input schemas.
var turfToolTargetArgs = map[string][]string{
	"init":              {"path", "config_alias"},
	"workspace_open":    {"workspace_name", "config_alias"},
	"workspace_close":   {"workspace_alias"},
	"workspace_delete":  {"workspace_name"},
	"invoke":            {"action_addr"},
	"apply":             {"convergence_id"},
	"show":              {"convergence_id"},
	"output":            {"name"},
	"taint":             {"addrs"},
	"untaint":           {"addrs"},
	"provider_search":   {"query"},
	"provider_describe": {"resource_type", "datasource_type", "action_type", "provider"},
	"read_skill_file":   {"path"},
}

// errorTarget resolves the request's primary target from the tool-call args, so a failed
// line names WHAT was operated on rather than only the error. Falls back to
// workspace_alias — present on nearly every turf tool — so even target-less tools carry
// workspace context; returns "" for zero-arg tools (skills) and unknown tools that took
// no workspace_alias.
func errorTarget(msg *types.Message) string {
	bare := strings.TrimPrefix(msg.ToolCall.Function.Name, appName+"_")
	for _, key := range turfToolTargetArgs[bare] {
		if v := argString(msg, key); v != "" {
			return v
		}
	}
	return argString(msg, "workspace_alias")
}

func bold(s string, c color.Color) string { return styles.BoldStyle.Foreground(c).Render(s) }
func colored(s string, c color.Color) string {
	return styles.BaseStyle.Foreground(c).Render(s)
}
func muted(s string) string   { return styles.MutedStyle.Render(s) }
func addr(s string) string    { return bold(s, styles.Highlight) }
func keyword(s string) string { return bold(s, styles.Accent) }
func dot() string             { return muted(" · ") }

// providerName styles a provider name or alias identifier — Highlight-colored but
// non-bold, so it reads as target-related metadata without competing with addr()
// (bold Highlight) on the same line.
func providerName(s string) string { return colored(s, styles.Highlight) }

// section returns a muted "<label>:" header line for the detail unfold.
func section(label string) string { return muted(label + ":") }

// --- attribute diff ---------------------------------------------------------
//
// The heart of the expanded view: a colored before→after diff, mirroring how
// `tofu plan` reads. Used by each resource change in a plan document (show).
// before==nil (a create) renders every attr as an addition; after==nil (a destroy)
// renders every attr as a removal.

const maxDiffLines = 60

func attrDiff(before, after map[string]any, beforeMask, afterMask any, indent string) []string {
	keys := unionKeys(before, after)
	sort.Strings(keys)
	// First pass: keep only keys that actually change, so the = alignment is computed
	// over exactly the rendered rows (not the skipped, unchanged attrs). A redacted
	// sensitive attribute is identical on both sides (the same sentinel string), so it
	// drops out here — we genuinely cannot tell whether it changed.
	changed := make([]string, 0, len(keys))
	for _, k := range keys {
		bv, bok := before[k]
		av, aok := after[k]
		bNil := !bok || bv == nil
		aNil := !aok || av == nil
		if (bNil && aNil) || (!bNil && !aNil && jsonEqual(bv, av)) {
			continue
		}
		changed = append(changed, k)
	}
	w := maxKeyWidth(changed)
	var out []string
	for i, k := range changed {
		if len(out) >= maxDiffLines {
			out = append(out, indent+muted(fmt.Sprintf("…(+%d more attrs)", len(changed)-i)))
			break
		}
		bv, bok := before[k]
		av, aok := after[k]
		bm, am := maskKey(beforeMask, k), maskKey(afterMask, k)
		bNil := !bok || bv == nil
		aNil := !aok || av == nil
		head := muted(padRight(k, w) + " = ")
		switch {
		case bNil:
			// A create: the whole subtree is new, so every line carries a + marker.
			out = append(out, renderValue(bold("+", styles.Success), head, av, am, indent, styles.Success)...)
		case aNil:
			// A destroy: the whole subtree is gone, so every line carries a - marker.
			out = append(out, renderValue(bold("-", styles.Error), head, bv, bm, indent, styles.Error)...)
		case maskSensitive(bm) && maskSensitive(am):
			// Both halves are sensitive, so neither can be printed — but the equality pass
			// above kept the key, so we are looking at two different values and the change
			// is real. Only reachable under show_sensitive: redacted, the two sides are the
			// same sentinel and drop out above. Rendered as an ordinary scalar change with
			// both halves masked, so it reads like every other row. The branch sits ahead of
			// the scalar check so a revealed *collection* takes this shape too, rather than
			// falling through and losing the arrow.
			out = append(out, indent+bold("~", styles.Accent)+" "+head+muted(sensitiveLeaf)+muted(" → ")+colored(sensitiveLeaf, styles.Accent))
		case isScalar(bv) && isScalar(av):
			// A scalar change reads best inline as old → new. At most one side is sensitive
			// here (both is the case above), so this also covers sensitivity being newly
			// applied or dropped: "old" → (sensitive).
			out = append(out, indent+bold("~", styles.Accent)+" "+head+muted(maskedVal(bv, bm))+muted(" → ")+colored(maskedVal(av, am), styles.Accent))
		default:
			// A collection changed: mark the header and expand the new value. A full
			// per-line recursive sub-diff (mixed +/-/~ inside the block) is a deliberate
			// non-goal; showing the new value already avoids the raw-JSON dump.
			out = append(out, renderValue("", bold("~", styles.Accent)+" "+head, av, am, indent, styles.Accent)...)
		}
	}
	return out
}

// kvLines renders a map as aligned "key = value" detail lines (state values, outputs,
// an action request's inputs, …), expanding nested maps/lists via
// renderValue. Null leaves are skipped; max caps the number of rendered lines. This is
// the unmarked (no +/-) value view; diffs pass a marker through renderValue instead.
func kvLines(m map[string]any, indent string, max int) []string {
	return kvLinesMasked(m, nil, indent, max)
}

// kvLinesMasked is kvLines for a value that came with a sensitive_values mask alongside
// it (a state resource's values, a state document's). A nil mask is the plain view.
func kvLinesMasked(m map[string]any, mask any, indent string, max int) []string {
	keys := nonNilKeys(m)
	w := maxKeyWidth(keys)
	var out []string
	for _, k := range keys {
		out = append(out, renderValue("", muted(padRight(k, w)+" = "), m[k], maskKey(mask, k), indent, styles.Highlight)...)
	}
	return capLines(out, indent, max)
}

// --- shared value renderer (tofu-plan idiom) --------------------------------
//
// renderValue is the single place turf turns a JSON-decoded OpenTofu value into
// timeline lines. Scalars (and turf's masked sentinels) format via fmtVal on one line;
// non-empty maps and lists expand across indented, =-aligned lines the way `tofu plan`
// reads. Every value site funnels through here (kvLines for plain state/config/outputs,
// attrDiff for before→after diffs), so nested values never fall through to fmtVal's raw
// JSON.
//
// head is the already-styled text between the marker and the value (e.g. muted("tags =
// ")); c colors the scalar leaves and inline elements (Highlight for plain views;
// Success/Error/Accent for a diff side). Structural punctuation ({}, [], commas) stays
// muted so the colored leaves carry the signal.
//
// marker is the styled +/- glyph a create/destroy diff prepends to *every* line of the
// value, or "" for the plain unmarked view (kvLines, and the header-only ~ case). When a
// marker is present each line reserves two columns for it, so children indent by 4 and a
// closing brace sits two in from the marker — matching how `tofu plan` lays out a
// wholly-added or wholly-removed block.
const inlineListWidth = 60

// markerPrefix is the per-line "<marker> " a diff prepends, or "" for the unmarked view.
func markerPrefix(marker string) string {
	if marker == "" {
		return ""
	}
	return marker + " "
}

// childIndent / closeIndent place a block's child rows and its closing brace. A marked
// block reserves two columns for the marker (children indent by 4, close sits two in); an
// unmarked block uses the plain 2-space step and closes flush with its opener.
func childIndent(indent, marker string) string {
	if marker == "" {
		return indent + "  "
	}
	return indent + "    "
}

func closeIndent(indent, marker string) string {
	if marker == "" {
		return indent
	}
	return indent + "  "
}

func renderValue(marker, head string, v any, mask any, indent string, c color.Color) []string {
	// The mask wins before the type switch: a sensitive node collapses its whole
	// subtree, so we never descend into (and never print) what it covers. This is the
	// one place that has to hold — every value site funnels through here — and it is
	// what makes show_sensitive mean "into the agent's context", not "onto the screen".
	if maskSensitive(mask) {
		return []string{indent + markerPrefix(marker) + head + colored(sensitiveLeaf, c)}
	}
	switch x := v.(type) {
	case map[string]any:
		return renderMap(marker, head, x, mask, indent, c)
	case []any:
		return renderList(marker, head, x, mask, indent, c)
	default:
		return []string{indent + markerPrefix(marker) + head + colored(fmtVal(v), c)}
	}
}

// renderMap expands a map to "<head>{ … }" with each non-nil entry on its own aligned,
// deeper-indented line (the marker propagating to each). An empty (or all-null) map
// renders inline as "<head>{}".
func renderMap(marker, head string, m map[string]any, mask any, indent string, c color.Color) []string {
	keys := nonNilKeys(m)
	if len(keys) == 0 {
		return []string{indent + markerPrefix(marker) + head + muted("{}")}
	}
	w := maxKeyWidth(keys)
	child := childIndent(indent, marker)
	out := []string{indent + markerPrefix(marker) + head + muted("{")}
	for _, k := range keys {
		out = append(out, renderValue(marker, muted(padRight(k, w)+" = "), m[k], maskKey(mask, k), child, c)...)
	}
	return append(out, closeIndent(indent, marker)+muted("}"))
}

// renderList renders a list inline ("<head>[a, b, c]") when it holds only scalars and its
// unstyled form is short; otherwise it expands one element per line with a trailing comma
// (recursing for nested collections, the marker propagating to each). An empty list
// renders as "<head>[]".
func renderList(marker, head string, s []any, mask any, indent string, c color.Color) []string {
	if len(s) == 0 {
		return []string{indent + markerPrefix(marker) + head + muted("[]")}
	}
	if scalarList(s) {
		raw := make([]string, len(s))
		styled := make([]string, len(s))
		for i, e := range s {
			raw[i] = maskedVal(e, maskIndex(mask, i))
			styled[i] = colored(raw[i], c)
		}
		if len([]rune("["+strings.Join(raw, ", ")+"]")) <= inlineListWidth {
			return []string{indent + markerPrefix(marker) + head + muted("[") + strings.Join(styled, muted(", ")) + muted("]")}
		}
	}
	child := childIndent(indent, marker)
	out := []string{indent + markerPrefix(marker) + head + muted("[")}
	for i, e := range s {
		lines := renderValue(marker, "", e, maskIndex(mask, i), child, c)
		lines[len(lines)-1] += muted(",")
		out = append(out, lines...)
	}
	return append(out, closeIndent(indent, marker)+muted("]"))
}

// capLines truncates rendered detail lines to max, appending a muted "…(+N more)" so a
// large or deeply nested value can't flood the timeline. max<=0 means no cap.
func capLines(lines []string, indent string, max int) []string {
	if max <= 0 || len(lines) <= max {
		return lines
	}
	out := append([]string{}, lines[:max]...)
	return append(out, indent+muted(fmt.Sprintf("…(+%d more)", len(lines)-max)))
}

// nonNilKeys returns m's keys with null-valued leaves dropped, sorted for stable output.
func nonNilKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if v == nil {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// maxKeyWidth is the longest key length in keys, used to =-align a block.
func maxKeyWidth(keys []string) int {
	w := 0
	for _, k := range keys {
		if len(k) > w {
			w = len(k)
		}
	}
	return w
}

// padRight pads s with trailing spaces to width (for =-alignment within a block).
func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

// scalarList reports whether s holds no nested maps or lists.
func scalarList(s []any) bool {
	for _, e := range s {
		if !isScalar(e) {
			return false
		}
	}
	return true
}

// isScalar reports whether v is a leaf (not a map or list).
func isScalar(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return false
	}
	return true
}

func unionKeys(a, b map[string]any) []string {
	seen := map[string]struct{}{}
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	return keys
}

// --- sensitivity masks ------------------------------------------------------
//
// Every value-bearing turf tool that can reveal a secret returns OpenTofu's
// sensitive_values shape alongside the values (a plan document's change carries the
// same shape as before_sensitive/after_sensitive): `true` at a sensitive node (covering
// its whole subtree), an object with the non-sensitive keys omitted, a positional
// array for lists/sets/tuples with null at the non-sensitive elements, and the field
// absent entirely when nothing is sensitive. See objchange.SensitiveValuesFromMarked.
//
// The mask matters because show_sensitive (show, output, state_list) puts the real
// secret on the wire. The
// server hands it to the model deliberately; the timeline is a different surface — it
// is also terminal scrollback and the persisted session — so turf redacts at display
// time from the mask, whatever arrived. In the ordinary (unrevealed) case the values
// are already the __cty_sensitive__ sentinel and the mask agrees with them, so the
// rendered bytes are the same either way.

// sensitiveLeaf stands in for a value we are not allowed to print. It is exactly what
// fmtVal renders the __cty_sensitive__ sentinel as, so a mask-redacted value and a
// pre-redacted one read identically — one form on every surface, including both halves
// of a diff between two unprintable values. The words are Terraform's own.
const sensitiveLeaf = "(sensitive value)"

// maskSensitive reports whether a mask node marks its whole subtree sensitive.
func maskSensitive(mask any) bool {
	b, ok := mask.(bool)
	return ok && b
}

// maskKey descends an object/map mask by attribute name; nil when the mask says
// nothing about that key (or is not an object at all). A sensitive node covers its
// whole subtree, so descending from one stays sensitive — which is what keeps a mask
// that marks a whole value (a bare `true` at the root) from leaking its attributes.
func maskKey(mask any, k string) any {
	if maskSensitive(mask) {
		return true
	}
	m, ok := mask.(map[string]any)
	if !ok {
		return nil
	}
	return m[k]
}

// maskIndex descends a list/set/tuple mask by position; nil when out of range (or the
// mask is not positional). Like maskKey, a sensitive node stays sensitive below.
func maskIndex(mask any, i int) any {
	if maskSensitive(mask) {
		return true
	}
	s, ok := mask.([]any)
	if !ok || i < 0 || i >= len(s) {
		return nil
	}
	return s[i]
}

// maskedVal is fmtVal for a scalar leaf that may be covered by a mask — the inline
// forms (a scalar diff, a short all-scalar list) that format a value without going
// through renderValue's own mask guard.
func maskedVal(v any, mask any) string {
	if maskSensitive(mask) {
		return sensitiveLeaf
	}
	return fmtVal(v)
}

// The two placeholders fmtVal prints in Terraform's words. The server redacts a
// sensitive value to sensitiveSentinel; unknownSentinel is turf's own, overlaid from a
// plan document's after_unknown mask by withUnknowns.
const (
	sensitiveSentinel = "__cty_sensitive__"
	unknownSentinel   = "__cty_unknown__"
)

// fmtVal renders a JSON-decoded value compactly. The placeholders are masked:
// unknownSentinel (a value known only after apply) and sensitiveSentinel (a sensitive
// value whose real content stayed server-side and never reached us — the masks above
// cover the case where it did).
func fmtVal(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		switch x {
		case unknownSentinel:
			return "(known after apply)"
		case sensitiveSentinel:
			return sensitiveLeaf
		}
		return strconv.Quote(x)
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		b, _ := json.Marshal(x)
		return truncateStr(string(b), 160)
	}
}

// withUnknowns overlays a plan document's after_unknown mask onto after, putting
// unknownSentinel at every node the mask marks unknown. The mask has the shape of the
// sensitive masks — `true` covering a whole subtree, an object of the keys it says
// anything about, a positional array — and OpenTofu leaves an unknown value out of (or
// null in) `after`, so without the overlay a pending attribute would not render at all.
// With it, attrDiff and fmtVal print "(known after apply)" with no unknown-aware code of
// their own.
func withUnknowns(v any, unknown any) any {
	switch u := unknown.(type) {
	case bool:
		if u {
			return unknownSentinel
		}
	case map[string]any:
		if len(u) == 0 {
			return v
		}
		m, _ := v.(map[string]any)
		out := make(map[string]any, len(m)+len(u))
		for k, x := range m {
			out[k] = x
		}
		for k, uk := range u {
			out[k] = withUnknowns(m[k], uk)
		}
		return out
	case []any:
		if len(u) == 0 {
			return v
		}
		s, _ := v.([]any)
		out := make([]any, max(len(s), len(u)))
		for i := range out {
			var x, ui any
			if i < len(s) {
				x = s[i]
			}
			if i < len(u) {
				ui = u[i]
			}
			out[i] = withUnknowns(x, ui)
		}
		return out
	}
	return v
}

func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// importingInfoView is a plan document change's `importing` object: the locator the
// import carried. Exactly one of ID / Identity is set — a provider-specific id
// string, or a provider-declared resource identity object (Terraform 1.12+).
type importingInfoView struct {
	ID       string         `json:"id,omitempty"`
	Identity map[string]any `json:"identity,omitempty"`
}

// adoptNote renders the locator as a one-line note for a plan row header. An
// identity is a whole object, so it is named rather than spelled out.
func (i *importingInfoView) adoptNote() string {
	switch {
	case i == nil:
		return ""
	case i.ID != "":
		return "adopt id=" + i.ID
	case len(i.Identity) > 0:
		return "adopt identity"
	default:
		return "adopt"
	}
}

// planRowAddr renders a plan row's address the way Terraform names it, so a
// deposed destroy is distinguishable from the ordinary destroy of the object
// living at the same address.
func planRowAddr(address, deposedKey string) string {
	if deposedKey == "" {
		return address
	}
	return address + " (deposed " + deposedKey + ")"
}

// --- turf_up / destroy / refresh / import / invoke / apply / cancel ---------
//
// Every convergence tool returns the same result (turf-mcp-server's
// convergence.Result): up and its siblings start a convergence and stop at round
// 1's approval point; apply approves the vended plan (or answers turf actions) and
// stops at the next one, or at the end; cancel ends one. So one renderer serves all
// seven, and the line reads the same whichever call produced it.
//
// The result carries counts, not resource changes: the per-resource plan is a
// document on disk (plan.path) the server deletes once the round applies, and the
// timeline does not read it. show renders a plan document's rows.
//
// A convergence that ended any way but converged (failed, stalled, exhausted,
// declined, cancelled, error, detached) arrives as an MCP error that still carries
// the whole result, so the renderer parses before it defers to errorLine. A refusal —
// a stale digest, a busy workspace — is plain text and falls through to errorLine.

type convergenceView struct {
	ConvergenceID  string `json:"convergence_id"`
	WorkspaceAlias string `json:"workspace_alias"`
	// Kind is up, destroy, refresh, import or invoke: what started the convergence.
	// apply and cancel report the kind of the convergence they act on.
	Kind string `json:"kind"`
	// Outcome is awaiting_approval or awaiting_actions while paused; otherwise how it
	// ended (converged, stalled, exhausted, failed, declined, cancelled, error,
	// detached). A cancel of a convergence nobody follows reports the driver's word.
	Outcome string `json:"outcome"`
	// Round, Plan and ExpiresAt are set at an approval point.
	Round     int             `json:"round,omitempty"`
	Plan      *vendedPlanView `json:"plan,omitempty"`
	ExpiresAt string          `json:"expires_at,omitempty"`
	// ActionRequests are the turf actions waiting on the agent at awaiting_actions,
	// which its next apply answers by key; Refused names the answers an apply sent
	// that were not taken, each request still open.
	ActionRequests []actionRequestView     `json:"action_requests,omitempty"`
	Refused        map[string]string       `json:"refused,omitempty"`
	Rounds         []roundView             `json:"rounds"`
	Outputs        *convergenceOutputsView `json:"outputs,omitempty"`
	Error          string                  `json:"error,omitempty"`
	Note           string                  `json:"note,omitempty"`
}

// vendedPlanView is a round's plan as the result reports it: where the document
// is, its digest (the approval), and the counts OpenTofu's plan summary prints. A
// replace is counted inside both Add and Destroy, as Terraform counts it.
type vendedPlanView struct {
	Path        string           `json:"path"`
	Digest      string           `json:"digest"`
	Add         int              `json:"add"`
	Change      int              `json:"change"`
	Destroy     int              `json:"destroy"`
	Replace     int              `json:"replace,omitempty"`
	Import      int              `json:"import,omitempty"`
	Forget      int              `json:"forget,omitempty"`
	Read        int              `json:"read,omitempty"`
	Deferred    int              `json:"deferred,omitempty"`
	Drift       int              `json:"drift,omitempty"`
	Outputs     int              `json:"outputs,omitempty"`
	Invocations []invocationView `json:"invocations,omitempty"`
	Errored     bool             `json:"errored,omitempty"`
}

// invocationView is one action invocation a plan schedules. Caller and Event are
// empty for an action run directly (invoke); Event is spelled as the configuration
// writes it (before_create).
type invocationView struct {
	Address   string `json:"address"`
	Type      string `json:"type"`
	Caller    string `json:"caller,omitempty"`
	Event     string `json:"event,omitempty"`
	OnFailure string `json:"on_failure,omitempty"`
}

// actionRequestView is one turf action waiting on the agent. Prompt says what to do
// and what counts as failure; IfFailed says what a failed answer will do; Inputs are
// the configuration's values for the step.
type actionRequestView struct {
	Key        string          `json:"key"`
	Round      int             `json:"round"`
	Action     string          `json:"action"`
	Invocation string          `json:"invocation"`
	Caller     string          `json:"caller,omitempty"`
	Event      string          `json:"event"`
	OnFailure  string          `json:"on_failure"`
	IfFailed   string          `json:"if_failed"`
	Prompt     string          `json:"prompt"`
	Inputs     json.RawMessage `json:"inputs,omitempty"`
}

// roundView is one round of a convergence: its status (planning, awaiting_approval
// and applying while it runs; then applied, failed, partial, cancelled or error),
// how its plan was approved (asked, auto, unneeded), and what its apply did.
type roundView struct {
	Round     int    `json:"round"`
	Status    string `json:"status"`
	Approval  string `json:"approval,omitempty"`
	Planned   int    `json:"planned"`
	Deferred  int    `json:"deferred"`
	Applied   int    `json:"applied"`
	Drift     int    `json:"drift,omitempty"`
	Failed    int    `json:"failed,omitempty"`
	Skipped   int    `json:"skipped,omitempty"`
	Cancelled int    `json:"cancelled,omitempty"`
	Invoked   int    `json:"invoked,omitempty"`
	Failures  []struct {
		Address string `json:"address"`
		Error   string `json:"error"`
	} `json:"failures,omitempty"`
	// Actions are the invocations that did not simply succeed (a plain success is
	// only counted, in Invoked); ActionsOmitted counts the ones past the engine's
	// bound.
	Actions        []actionOutcomeView `json:"actions,omitempty"`
	ActionsOmitted int                 `json:"actions_omitted,omitempty"`
	Diagnostics    []string            `json:"diagnostics,omitempty"`
	Warnings       []string            `json:"warnings,omitempty"`
	CheckFailures  []string            `json:"check_failures,omitempty"`
	CancelCause    string              `json:"cancel_cause,omitempty"`
	Error          string              `json:"error,omitempty"`
}

// actionOutcomeView is one invocation a round settled: invoked, failed, or skipped
// (its trigger's condition was false at apply).
type actionOutcomeView struct {
	Invocation    string   `json:"invocation"`
	Action        string   `json:"action,omitempty"`
	Type          string   `json:"type"`
	Caller        string   `json:"caller,omitempty"`
	Event         string   `json:"event,omitempty"`
	Status        string   `json:"status"`
	OnFailure     string   `json:"on_failure,omitempty"`
	Detail        string   `json:"detail,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
	CallerTainted bool     `json:"caller_tainted,omitempty"`
}

// convergenceOutputsView names the root outputs the last round finalized, and the
// ones it carried forward because what they read is still deferred. The values are
// the output tool's to read.
type convergenceOutputsView struct {
	Finalized []string `json:"finalized,omitempty"`
	Carried   []string `json:"carried,omitempty"`
}

func renderConvergence(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, appendDot(targetBody(errorTarget(msg)), muted(convergenceRunningWord(msg))), width)
	}
	var c convergenceView
	if !parseContent(msg, &c) || c.Outcome == "" {
		return fallbackLine(msg, s, ss, width)
	}
	return lineWithDetail(msg, s, ss, convergenceSummary(msg, c), convergenceDetail(c), width)
}

// convergenceRunningWord is what an in-flight convergence call is doing. The server
// relays progress only as MCP progress notifications, which cagent does not
// surface, so the word is inferred from the call itself.
func convergenceRunningWord(msg *types.Message) string {
	switch strings.TrimPrefix(msg.ToolCall.Function.Name, appName+"_") {
	case "apply":
		if strings.Contains(msg.ToolCall.Function.Arguments, `"action_results"`) {
			return "answering actions…"
		}
		return "applying…"
	case "cancel":
		return "cancelling…"
	default:
		return "planning…"
	}
}

// convergenceSummary composes the one-liner: which convergence, then where it
// stands — a round's plan awaiting approval, turf actions awaiting answers, or how
// it ended.
func convergenceSummary(msg *types.Message, c convergenceView) string {
	var summary string
	// The alias is set only when several workspaces are open, so the default is noise
	// here — the same rule the session title applies (see sessionLabel).
	if c.WorkspaceAlias != "" && c.WorkspaceAlias != defaultWorkspaceName {
		summary = addr(c.WorkspaceAlias)
	}
	if c.ConvergenceID != "" {
		summary = appendDot(summary, addr(c.ConvergenceID))
	}
	// up and its siblings are titled by their kind already ("Deploy", "Destroy");
	// apply and cancel act on a convergence some earlier call started, so they name it.
	if bare := strings.TrimPrefix(msg.ToolCall.Function.Name, appName+"_"); bare == "apply" || bare == "cancel" {
		if w := runKindWord(c.Kind); w != "" {
			summary = appendDot(summary, muted(w))
		}
	}
	switch c.Outcome {
	case "awaiting_approval":
		if c.Round > 0 {
			summary = appendDot(summary, muted(fmt.Sprintf("round %d", c.Round)))
		}
		if c.Plan != nil {
			summary = appendDot(summary, planCountsTally(*c.Plan))
		}
		return appendDot(summary, bold("awaiting approval", styles.Warning)) + refusedSegment(c)
	case "awaiting_actions":
		if c.Round > 0 {
			summary = appendDot(summary, muted(fmt.Sprintf("round %d", c.Round)))
		}
		summary = appendDot(summary, bold(fmt.Sprintf("awaiting %d action(s)", len(c.ActionRequests)), styles.Warning))
		return summary + refusedSegment(c)
	}
	word, col := outcomeWord(c.Kind, c.Outcome)
	summary = appendDot(summary, bold(word, col))
	t := roundTotals(c.Rounds)
	switch c.Outcome {
	case "converged":
		if n := len(c.Rounds); n > 1 {
			summary += dot() + muted(fmt.Sprintf("%d rounds", n))
		}
		if t.applied == 0 && t.invoked == 0 {
			summary += dot() + muted("no changes")
		}
	case "stalled", "exhausted":
		if n := lastRound(c.Rounds).Deferred; n > 0 {
			summary += dot() + styles.WarningStyle.Render(fmt.Sprintf("%d deferred", n))
		}
	}
	if t.applied > 0 {
		summary += dot() + muted(fmt.Sprintf("%d applied", t.applied))
	}
	if t.invoked > 0 {
		summary += dot() + muted(fmt.Sprintf("%d invoked", t.invoked))
	}
	if t.failed > 0 {
		summary += dot() + styles.ErrorStyle.Render(fmt.Sprintf("%d failed", t.failed))
	}
	return summary + refusedSegment(c)
}

// refusedSegment counts the action results the last apply sent that were not taken.
// Any outcome can carry them — an answer sent to a convergence that has already
// ended is refused too — so every summary appends it.
func refusedSegment(c convergenceView) string {
	if n := len(c.Refused); n > 0 {
		return dot() + styles.ErrorStyle.Render(fmt.Sprintf("%d refused", n))
	}
	return ""
}

// convergenceDetail is the expansion: the vended plan, its invocations, the turf
// actions waiting, each round, and the outputs — the order a reader asks about them.
func convergenceDetail(c convergenceView) []string {
	var detail []string
	if p := c.Plan; p != nil {
		row := muted(p.Path)
		if p.Digest != "" {
			row = appendDot(row, muted(shortDigest(p.Digest)))
		}
		if c.ExpiresAt != "" {
			row = appendDot(row, muted("expires "+c.ExpiresAt))
		}
		detail = append(detail, section("plan"), "  "+row)
		if n := len(p.Invocations); n > 0 {
			detail = append(detail, section(fmt.Sprintf("%d to invoke", n)))
			for _, inv := range p.Invocations {
				detail = append(detail, "  "+invocationLine(inv.Address, inv.Event, inv.Caller, inv.OnFailure))
			}
		}
	}
	if n := len(c.ActionRequests); n > 0 {
		detail = append(detail, section(fmt.Sprintf("%d action(s) waiting", n)))
		for _, r := range c.ActionRequests {
			detail = append(detail, actionRequestLines(r, c.Refused[r.Key])...)
		}
	}
	// A refused answer with no open request beside it (the convergence ended, or the
	// key named nothing) has no request row to sit under, so it gets its own.
	if orphans := orphanRefusals(c); len(orphans) > 0 {
		detail = append(detail, section("refused"))
		for _, key := range orphans {
			detail = append(detail, "  "+addr(key)+muted(": ")+styles.ErrorStyle.Render(c.Refused[key]))
		}
	}
	if len(c.Rounds) > 0 {
		detail = append(detail, section("rounds"))
		for _, r := range c.Rounds {
			detail = append(detail, roundLines(r)...)
		}
	}
	if o := c.Outputs; o != nil && (len(o.Finalized) > 0 || len(o.Carried) > 0) {
		detail = append(detail, section("outputs"))
		if len(o.Finalized) > 0 {
			detail = append(detail, "  "+muted("finalized ")+addr(strings.Join(o.Finalized, ", ")))
		}
		if len(o.Carried) > 0 {
			detail = append(detail, "  "+muted("carried ")+addr(strings.Join(o.Carried, ", "))+muted(" (still deferred)"))
		}
	}
	if c.Note != "" {
		detail = append(detail, muted(c.Note))
	}
	if c.Error != "" {
		detail = append(detail, styles.ToolErrorMessageStyle.Render(c.Error))
	}
	return detail
}

// orphanRefusals lists, sorted, the refused keys no open action request carries.
func orphanRefusals(c convergenceView) []string {
	open := make(map[string]bool, len(c.ActionRequests))
	for _, r := range c.ActionRequests {
		open[r.Key] = true
	}
	var keys []string
	for k := range c.Refused {
		if !open[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// actionRequestLines renders one turf action waiting on an answer: which request
// (its key is what the answer is filed under), what it asks, what a failed answer
// does, and the values it was handed.
func actionRequestLines(r actionRequestView, refused string) []string {
	head := "  " + addr(r.Key)
	if r.Action != "" && r.Action != r.Key {
		head += dot() + keyword(r.Action)
	}
	if t := triggerPhrase(r.Event, r.Caller); t != "" {
		head += dot() + muted(t)
	}
	out := []string{head}
	if r.Prompt != "" {
		out = append(out, "    "+r.Prompt)
	}
	if r.IfFailed != "" {
		out = append(out, "    "+muted("if it fails: "+r.IfFailed))
	}
	var inputs map[string]any
	if len(r.Inputs) > 0 && json.Unmarshal(r.Inputs, &inputs) == nil && len(inputs) > 0 {
		out = append(out, kvLines(inputs, "    ", 12)...)
	}
	if refused != "" {
		out = append(out, "    "+styles.ErrorStyle.Render("refused: "+refused))
	}
	return out
}

// roundLines renders one round: its status and counts, then what went wrong or
// warned in it.
func roundLines(r roundView) []string {
	row := "  " + muted(fmt.Sprintf("round %d", r.Round)) + dot() + bold(roundStatusWord(r.Status), roundStatusColor(r.Status))
	counts := []string{}
	if r.Applied > 0 {
		counts = append(counts, fmt.Sprintf("%d applied", r.Applied))
	} else if r.Planned > 0 {
		counts = append(counts, fmt.Sprintf("%d planned", r.Planned))
	}
	if r.Deferred > 0 {
		counts = append(counts, fmt.Sprintf("%d deferred", r.Deferred))
	}
	if r.Drift > 0 {
		counts = append(counts, fmt.Sprintf("%d drifted", r.Drift))
	}
	if r.Invoked > 0 {
		counts = append(counts, fmt.Sprintf("%d invoked", r.Invoked))
	}
	if r.Skipped > 0 {
		counts = append(counts, fmt.Sprintf("%d skipped", r.Skipped))
	}
	if r.Cancelled > 0 {
		counts = append(counts, fmt.Sprintf("%d cancelled", r.Cancelled))
	}
	if len(counts) > 0 {
		row += dot() + muted(strings.Join(counts, " · "))
	}
	if r.Failed > 0 {
		row += dot() + styles.ErrorStyle.Render(fmt.Sprintf("%d failed", r.Failed))
	}
	if w := approvalWord(r); w != "" {
		row += dot() + muted(w)
	}
	out := []string{row}
	for _, f := range r.Failures {
		out = append(out, "    "+styles.ErrorStyle.Render("✗ ")+addr(f.Address)+muted(": ")+styles.ToolErrorMessageStyle.Render(f.Error))
	}
	for _, a := range r.Actions {
		word, col := actionVerdict(a)
		name := a.Action
		if name == "" {
			name = a.Invocation
		}
		row := "    " + keyword(name)
		if t := triggerPhrase(a.Event, a.Caller); t != "" {
			row += dot() + muted(t)
		}
		row += dot() + bold(word, col)
		if a.Detail != "" {
			row += muted(": ") + styles.ToolErrorMessageStyle.Render(a.Detail)
		}
		out = append(out, row)
		for _, w := range a.Warnings {
			out = append(out, "      "+styles.WarningStyle.Render("⚠ "+w))
		}
	}
	if r.ActionsOmitted > 0 {
		out = append(out, "    "+muted(fmt.Sprintf("…(+%d more actions)", r.ActionsOmitted)))
	}
	for _, f := range r.CheckFailures {
		out = append(out, "    "+styles.WarningStyle.Render("check failed: "+f))
	}
	for _, w := range r.Warnings {
		out = append(out, "    "+styles.WarningStyle.Render("⚠ "+w))
	}
	for _, d := range r.Diagnostics {
		out = append(out, "    "+muted(d))
	}
	if r.CancelCause != "" {
		out = append(out, "    "+muted("cancelled: "+r.CancelCause))
	}
	if r.Error != "" {
		out = append(out, "    "+styles.ToolErrorMessageStyle.Render(r.Error))
	}
	return out
}

// planCountsTally renders a plan's counts as "+A ~C -D" (zero buckets omitted, a
// replace inside both + and -, as Terraform's plan summary counts it) followed by the
// segments OpenTofu's summary would add — import, forget — and the convergence's own:
// deferred work, drift, output changes, invocations. "no changes" when nothing moves.
func planCountsTally(p vendedPlanView) string {
	var buckets []string
	if p.Add > 0 {
		buckets = append(buckets, bold(fmt.Sprintf("+%d", p.Add), styles.Success))
	}
	if p.Change > 0 {
		buckets = append(buckets, bold(fmt.Sprintf("~%d", p.Change), styles.Accent))
	}
	if p.Destroy > 0 {
		buckets = append(buckets, bold(fmt.Sprintf("-%d", p.Destroy), styles.Error))
	}
	out := strings.Join(buckets, " ")
	if p.Replace > 0 {
		out = appendDot(out, colored(fmt.Sprintf("%d replaced", p.Replace), styles.Warning))
	}
	if p.Import > 0 {
		out = appendDot(out, colored(fmt.Sprintf("%d to import", p.Import), styles.Accent))
	}
	if p.Forget > 0 {
		out = appendDot(out, colored(fmt.Sprintf("%d to forget", p.Forget), styles.Highlight))
	}
	if p.Read > 0 {
		out = appendDot(out, muted(fmt.Sprintf("%d to read", p.Read)))
	}
	if n := len(p.Invocations); n > 0 {
		out = appendDot(out, colored(fmt.Sprintf("%d to invoke", n), styles.Accent))
	}
	if out == "" {
		out = muted("no changes")
	}
	if p.Deferred > 0 {
		out += dot() + styles.WarningStyle.Render(fmt.Sprintf("%d deferred", p.Deferred))
	}
	if p.Drift > 0 {
		out += dot() + styles.WarningStyle.Render(fmt.Sprintf("%d drifted", p.Drift))
	}
	if p.Outputs > 0 {
		out += dot() + muted(fmt.Sprintf("%d output change(s)", p.Outputs))
	}
	if p.Errored {
		out += dot() + styles.ErrorStyle.Render("errored")
	}
	return out
}

// runKindWord names a convergence's kind the way the engine console's run rail
// does: "deploy" for up, "refresh-only" for a refresh.
func runKindWord(kind string) string {
	switch kind {
	case "up":
		return "deploy"
	case "refresh":
		return "refresh-only"
	default:
		return kind
	}
}

// outcomeWord names how a convergence ended, in the engine console's words: a deploy
// converges, every other kind is done; stalled, exhausted, declined and cancelled are
// neutral, because red is for failure and nothing else.
func outcomeWord(kind, outcome string) (string, color.Color) {
	switch outcome {
	case "converged":
		if kind == "up" || kind == "" {
			return "converged", styles.Success
		}
		return "done", styles.Success
	case "failed", "error":
		return outcome, styles.Error
	default:
		return outcome, styles.TextMuted
	}
}

func roundStatusWord(status string) string {
	return strings.ReplaceAll(status, "_", " ")
}

func roundStatusColor(status string) color.Color {
	switch status {
	case "applied":
		return styles.Success
	case "failed", "error":
		return styles.Error
	case "planning", "awaiting_approval", "applying":
		return styles.Warning
	default:
		return styles.TextMuted
	}
}

// approvalWord says how a settled round's plan came to be applied, in the console's
// words; a round still waiting says so in its status instead.
func approvalWord(r roundView) string {
	switch r.Approval {
	case "auto":
		return "auto-approved"
	case "unneeded":
		return "nothing to approve"
	case "asked":
		switch r.Status {
		case "applied", "failed", "partial":
			return "approved"
		}
	}
	return ""
}

// actionVerdict names how a round settled an invocation, in the console's words.
func actionVerdict(a actionOutcomeView) (string, color.Color) {
	switch {
	case a.Status == "skipped":
		return "skipped — its condition was false", styles.TextMuted
	case a.Status == "failed" && a.CallerTainted:
		return "failed · tainted its caller", styles.Error
	case a.Status == "failed" && a.OnFailure == "continue":
		return "failed · continued", styles.Warning
	case a.Status == "failed":
		return "failed", styles.Error
	case a.Status == "invoked":
		return "invoked", styles.Success
	default:
		return a.Status, styles.TextMuted
	}
}

// triggerPhrase says what fired an invocation: "<event> of <caller>" for a trigger,
// "invoked directly" for an action run on its own (invoke).
func triggerPhrase(event, caller string) string {
	switch {
	case event == "" || event == "invoke":
		if caller != "" {
			return "invoked, called from " + caller
		}
		return "invoked directly"
	case caller == "":
		return event
	default:
		return event + " of " + caller
	}
}

// invocationLine renders one scheduled invocation. on_failure is shown only when it
// is not the default halt, as the console shows it.
func invocationLine(address, event, caller, onFailure string) string {
	row := keyword(address) + dot() + muted(triggerPhrase(event, caller))
	if onFailure != "" && onFailure != "halt" {
		row += dot() + muted("on failure: "+onFailure)
	}
	return row
}

type roundTally struct{ applied, invoked, failed int }

func roundTotals(rounds []roundView) roundTally {
	var t roundTally
	for _, r := range rounds {
		t.applied += r.Applied
		t.invoked += r.Invoked
		t.failed += r.Failed
	}
	return t
}

func lastRound(rounds []roundView) roundView {
	if len(rounds) == 0 {
		return roundView{}
	}
	return rounds[len(rounds)-1]
}

// shortDigest trims a "sha256:<hex>" digest to a recognizable prefix.
func shortDigest(d string) string {
	algo, hex, ok := strings.Cut(d, ":")
	if !ok || len(hex) <= 12 {
		return d
	}
	return algo + ":" + hex[:12]
}

// --- turf_show ----------------------------------------------------------------
//
// show returns OpenTofu's own json-format document — a round's plan, exactly as
// `tofu show -json` prints it, or the workspace's state — redacted by the server
// (sensitive values read "__cty_sensitive__", the *_sensitive shadows kept). It is
// the one surface that carries the per-resource plan, so this renderer is where the
// attribute diffs live: each change as a header in Terraform's glyph idiom, then its
// before → after diff, with the document's own after_unknown overlaid so a pending
// attribute reads "(known after apply)".

type showView struct {
	// PlannedValues is present on every plan document, changes or not; a state
	// document has Values instead.
	PlannedValues     json.RawMessage       `json:"planned_values,omitempty"`
	ResourceChanges   []resourceChangeView  `json:"resource_changes,omitempty"`
	ResourceDrift     []resourceChangeView  `json:"resource_drift,omitempty"`
	DeferredChanges   []deferredChangeView  `json:"deferred_changes,omitempty"`
	OutputChanges     map[string]changeView `json:"output_changes,omitempty"`
	ActionInvocations []planInvocationView  `json:"action_invocations,omitempty"`
	Errored           bool                  `json:"errored,omitempty"`
	Values            *stateValuesView      `json:"values,omitempty"`
}

type resourceChangeView struct {
	Address         string `json:"address"`
	PreviousAddress string `json:"previous_address,omitempty"`
	// Deposed is the key of a deposed object this change is about, rather than the
	// object living at Address.
	Deposed      string     `json:"deposed,omitempty"`
	ActionReason string     `json:"action_reason,omitempty"`
	Change       changeView `json:"change"`
}

type changeView struct {
	Actions         []string           `json:"actions"`
	Before          any                `json:"before"`
	After           any                `json:"after"`
	AfterUnknown    any                `json:"after_unknown,omitempty"`
	BeforeSensitive any                `json:"before_sensitive,omitempty"`
	AfterSensitive  any                `json:"after_sensitive,omitempty"`
	ReplacePaths    [][]any            `json:"replace_paths,omitempty"`
	Importing       *importingInfoView `json:"importing,omitempty"`
}

type deferredChangeView struct {
	Reason         string             `json:"reason"`
	ResourceChange resourceChangeView `json:"resource_change"`
}

// planInvocationView is an action invocation as the plan document spells it. The
// engine's document writes the trigger event and on_failure as Terraform's binary
// prints them (BeforeCreate, ActionOnFailureHalt); configSpelling turns both back into
// the configuration's words, as the server's own summary does.
type planInvocationView struct {
	Address   string `json:"address"`
	Type      string `json:"type"`
	Lifecycle *struct {
		TriggeringResourceAddress string `json:"triggering_resource_address"`
		ActionTriggerEvent        string `json:"action_trigger_event"`
		OnFailure                 string `json:"on_failure"`
	} `json:"lifecycle_action_trigger,omitempty"`
}

type stateValuesView struct {
	Outputs    map[string]outputValueView `json:"outputs,omitempty"`
	RootModule stateModuleView            `json:"root_module"`
}

type stateModuleView struct {
	Resources    []stateDocResourceView `json:"resources,omitempty"`
	ChildModules []stateModuleView      `json:"child_modules,omitempty"`
}

type stateDocResourceView struct {
	Address         string         `json:"address"`
	Values          map[string]any `json:"values,omitempty"`
	SensitiveValues any            `json:"sensitive_values,omitempty"`
	Tainted         bool           `json:"tainted,omitempty"`
	DeposedKey      string         `json:"deposed_key,omitempty"`
}

func renderShow(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	head := showHead(msg)
	if running(msg) {
		return line(msg, s, head, width)
	}
	var v showView
	if !parseContent(msg, &v) {
		return fallbackLine(msg, s, ss, width)
	}
	if argString(msg, "what") == "state" || (v.Values != nil && v.PlannedValues == nil) {
		return renderStateDoc(msg, s, ss, width, head, v)
	}
	p := summarizePlanDoc(v)
	summary := appendDot(head, planCountsTally(p))
	return lineWithDetail(msg, s, ss, summary, planDocDetail(v), width)
}

// showHead names which document a show call asked for: the plan of a convergence's
// round (the one awaiting approval, else the latest, when no round is named), or the
// state.
func showHead(msg *types.Message) string {
	if argString(msg, "what") == "state" {
		return keyword("state")
	}
	head := keyword("plan")
	if id := argString(msg, "convergence_id"); id != "" {
		head += dot() + addr(id)
	}
	if n := argInt(msg, "round"); n > 0 {
		head += dot() + muted(fmt.Sprintf("round %d", n))
	}
	return head
}

// summarizePlanDoc counts a plan document the way the server's plan summary does, so
// a show line and the up/apply line that vended the same plan read the same.
func summarizePlanDoc(v showView) vendedPlanView {
	var p vendedPlanView
	for _, rc := range v.ResourceChanges {
		if rc.Change.Importing != nil {
			p.Import++
		}
		switch strings.Join(rc.Change.Actions, ",") {
		case "create":
			p.Add++
		case "update":
			p.Change++
		case "delete":
			p.Destroy++
		case "delete,create", "create,delete":
			p.Replace++
			p.Add++
			p.Destroy++
		case "forget,create":
			p.Forget++
			p.Add++
		case "forget":
			p.Forget++
		case "read":
			p.Read++
		}
	}
	for _, oc := range v.OutputChanges {
		if strings.Join(oc.Actions, ",") != "no-op" {
			p.Outputs++
		}
	}
	p.Deferred = len(v.DeferredChanges)
	p.Drift = len(v.ResourceDrift)
	p.Errored = v.Errored
	for _, ai := range v.ActionInvocations {
		p.Invocations = append(p.Invocations, ai.view())
	}
	return p
}

func (ai planInvocationView) view() invocationView {
	inv := invocationView{Address: ai.Address, Type: ai.Type}
	if t := ai.Lifecycle; t != nil {
		inv.Caller = t.TriggeringResourceAddress
		inv.Event = configSpelling(t.ActionTriggerEvent)
		inv.OnFailure = configSpelling(strings.TrimPrefix(t.OnFailure, "ActionOnFailure"))
	}
	return inv
}

// configSpelling spells an event or an on_failure value the way the configuration
// language writes it ("before_create", "halt"), whatever spelling the document
// carries ("BeforeCreate", "Halt").
func configSpelling(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

// planDocDetail renders a plan document's expansion: the resource changes (each a
// header and its attribute diff), what changed outside Turf, what was deferred, the
// output changes, and the actions to invoke.
func planDocDetail(v showView) []string {
	var detail []string
	var rows []resourceChangeView
	for _, rc := range v.ResourceChanges {
		if worthARow(rc) {
			rows = append(rows, rc)
		}
	}
	const maxRows = 25
	for i, rc := range rows {
		if i == maxRows {
			detail = append(detail, muted(fmt.Sprintf("…(+%d more resource changes)", len(rows)-maxRows)))
			break
		}
		detail = append(detail, resourceChangeHeader(rc, ""))
		detail = append(detail, resourceChangeDiff(rc.Change, "  ")...)
	}
	if len(v.ResourceDrift) > 0 {
		detail = append(detail, section("changed outside Turf"))
		for _, rc := range v.ResourceDrift {
			note := styles.WarningStyle.Render("drifted")
			if rc.Change.After == nil {
				note = styles.ErrorStyle.Render("gone")
			}
			detail = append(detail, "  "+addr(planRowAddr(rc.Address, rc.Deposed))+dot()+note)
			detail = append(detail, resourceChangeDiff(rc.Change, "    ")...)
		}
	}
	if len(v.DeferredChanges) > 0 {
		detail = append(detail, section("deferred"))
		for _, d := range v.DeferredChanges {
			row := "  " + resourceChangeHeader(d.ResourceChange, "")
			if d.Reason != "" {
				row += dot() + styles.WarningStyle.Render(d.Reason)
			}
			detail = append(detail, row)
		}
	}
	if lines := outputChangeLines(v.OutputChanges); len(lines) > 0 {
		detail = append(detail, section("outputs"))
		detail = append(detail, lines...)
	}
	if len(v.ActionInvocations) > 0 {
		detail = append(detail, section(fmt.Sprintf("%d to invoke", len(v.ActionInvocations))))
		for _, ai := range v.ActionInvocations {
			inv := ai.view()
			detail = append(detail, "  "+invocationLine(inv.Address, inv.Event, inv.Caller, inv.OnFailure))
		}
	}
	return detail
}

// worthARow reports whether a resource change belongs in the expansion. Terraform
// lists no no-ops; the exceptions are an adoption (an import that plans as a no-op
// because the remote object already matches) and a move, both of which apply
// something.
func worthARow(rc resourceChangeView) bool {
	if strings.Join(rc.Change.Actions, ",") != "no-op" {
		return true
	}
	return rc.Change.Importing != nil || (rc.PreviousAddress != "" && rc.PreviousAddress != rc.Address)
}

// resourceChangeHeader renders a change's header: Terraform's glyph, the address,
// then what explains it — an adoption, a move, the paths forcing a replace, and
// OpenTofu's action reason.
func resourceChangeHeader(rc resourceChangeView, indent string) string {
	_, c, glyph := planAction(rc.Change.Actions)
	header := indent
	if glyph != "" {
		header += bold(glyph, c) + " "
	}
	header += addr(planRowAddr(rc.Address, rc.Deposed))
	if note := rc.Change.Importing.adoptNote(); note != "" {
		header += dot() + colored(note, styles.Accent)
	}
	if rc.PreviousAddress != "" && rc.PreviousAddress != rc.Address {
		header += dot() + muted("moved from "+rc.PreviousAddress)
	}
	if paths := replacePaths(rc.Change.ReplacePaths); paths != "" {
		header += dot() + styles.WarningStyle.Render("forces replacement: "+paths)
	}
	if rc.ActionReason != "" {
		header += dot() + muted(strings.ReplaceAll(rc.ActionReason, "_", " "))
	}
	return header
}

// resourceChangeDiff renders a change's before → after attribute diff, with the
// unknown parts of after overlaid so they read "(known after apply)". An after that
// is unknown as a whole has no attributes to diff, and must not read as a destroy.
func resourceChangeDiff(ch changeView, indent string) []string {
	before, _ := ch.Before.(map[string]any)
	overlaid := withUnknowns(ch.After, ch.AfterUnknown)
	if overlaid == unknownSentinel {
		return []string{indent + muted("(known after apply)")}
	}
	after, _ := overlaid.(map[string]any)
	return attrDiff(before, after, ch.BeforeSensitive, ch.AfterSensitive, indent)
}

// outputChangeLines renders the plan's output changes as one aligned diff: each
// output is a key of the same before/after pair, so a create, a destroy and an update
// all read as attrDiff rows, masked by each output's own sensitivity.
func outputChangeLines(changes map[string]changeView) []string {
	before, after := map[string]any{}, map[string]any{}
	beforeMask, afterMask := map[string]any{}, map[string]any{}
	for name, oc := range changes {
		if strings.Join(oc.Actions, ",") == "no-op" {
			continue
		}
		before[name] = oc.Before
		after[name] = withUnknowns(oc.After, oc.AfterUnknown)
		beforeMask[name] = oc.BeforeSensitive
		afterMask[name] = oc.AfterSensitive
	}
	if len(after) == 0 {
		return nil
	}
	return attrDiff(before, after, beforeMask, afterMask, "  ")
}

// replacePaths renders a change's replace_paths — each a list of steps, attribute
// names and element keys — as "a.b, c[0]".
func replacePaths(paths [][]any) string {
	var out []string
	for _, p := range paths {
		var b strings.Builder
		for _, step := range p {
			switch x := step.(type) {
			case string:
				if b.Len() > 0 {
					b.WriteByte('.')
				}
				b.WriteString(x)
			case float64:
				fmt.Fprintf(&b, "[%s]", strconv.FormatFloat(x, 'f', -1, 64))
			}
		}
		if b.Len() > 0 {
			out = append(out, b.String())
		}
	}
	return strings.Join(out, ", ")
}

// renderStateDoc renders a state document: a resource count and, expanded, each
// resource with its values (masked by its sensitive_values) and the root outputs.
func renderStateDoc(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width int, head string, v showView) string {
	var resources []stateDocResourceView
	var outputs map[string]outputValueView
	if v.Values != nil {
		resources = flattenStateModule(v.Values.RootModule)
		outputs = v.Values.Outputs
	}
	summary := appendDot(head, muted(fmt.Sprintf("%d resource(s)", len(resources))))
	if n := len(outputs); n > 0 {
		summary += dot() + muted(fmt.Sprintf("%d output(s)", n))
	}
	var detail []string
	const maxRows = 25
	for i, r := range resources {
		if i == maxRows {
			detail = append(detail, muted(fmt.Sprintf("…(+%d more resources)", len(resources)-maxRows)))
			break
		}
		row := addr(planRowAddr(r.Address, r.DeposedKey))
		if r.Tainted {
			row += dot() + styles.WarningStyle.Render("tainted")
		}
		detail = append(detail, row)
		detail = append(detail, kvLinesMasked(r.Values, r.SensitiveValues, "  ", 12)...)
	}
	if len(outputs) > 0 {
		detail = append(detail, section("outputs"))
		flat, mask := outputsMask(outputs)
		detail = append(detail, kvLinesMasked(flat, mask, "  ", 20)...)
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// flattenStateModule lists a state document's resources, root module first, then
// each child module's in document order.
func flattenStateModule(m stateModuleView) []stateDocResourceView {
	out := append([]stateDocResourceView{}, m.Resources...)
	for _, c := range m.ChildModules {
		out = append(out, flattenStateModule(c)...)
	}
	return out
}

// --- turf_output ------------------------------------------------------------
//
// output reads a workspace's root outputs (name → {value, sensitive, type}). The
// server masks sensitive values with the "__cty_sensitive__" sentinel unless the
// caller opted in to reveal them (show_sensitive), so the per-output `sensitive` flag
// is this tool's form of a sensitivity mask — the renderer redacts from it either way,
// and never prints a revealed output value.

type outputsView struct {
	WorkspaceAlias string                     `json:"workspace_alias"`
	Outputs        map[string]outputValueView `json:"outputs"`
}

// outputValueView is one root output, the {value, sensitive, type} shape the server
// returns from output and state_list, and that a state document's outputs carry. The
// sensitive flag IS the mask here — it never rides along as a parallel
// sensitive_values structure — so outputsMask carries it across rather than
// flattening it away, which would leak a revealed secret into the timeline.
type outputValueView struct {
	Value     any             `json:"value"`
	Sensitive bool            `json:"sensitive"`
	Type      json.RawMessage `json:"type,omitempty"`
}

// outputsMask flattens name→output into the name→value map the value renderers
// take, plus the parallel mask kvLinesMasked descends. Shared by every renderer
// that shows outputs in this shape.
func outputsMask(outputs map[string]outputValueView) (flat, mask map[string]any) {
	flat = make(map[string]any, len(outputs))
	mask = map[string]any{}
	for k, o := range outputs {
		flat[k] = o.Value
		if o.Sensitive {
			mask[k] = true
		}
	}
	return flat, mask
}

// sensitiveOutputs counts the outputs carrying the sensitive flag.
func sensitiveOutputs(outputs map[string]outputValueView) int {
	n := 0
	for _, o := range outputs {
		if o.Sensitive {
			n++
		}
	}
	return n
}

func renderOutput(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	name := argString(msg, "name")
	if running(msg) {
		return line(msg, s, targetBody(name), width)
	}
	var r outputsView
	if !parseContent(msg, &r) {
		return fallbackLine(msg, s, ss, width)
	}

	summary := targetBody(name)
	switch {
	case name != "":
	case len(r.Outputs) == 0:
		summary = muted("none")
	default:
		summary = muted(fmt.Sprintf("%d output(s)", len(r.Outputs)))
	}
	if sens := sensitiveOutputs(r.Outputs); sens > 0 {
		summary = appendDot(summary, muted(fmt.Sprintf("%d sensitive", sens)))
	}

	flat, mask := outputsMask(r.Outputs)
	detail := kvLinesMasked(flat, mask, "  ", 30)
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// --- turf_workspace_open / turf_workspace_close -----------------------------

// A workspace is opened *against a configuration*, and that binding is the call's
// most consequential result: it fixes the backend and what every later plan
// projects. The open also reports the convergence already holding the workspace, if
// any — which is what makes a re-attach after a restart legible: the paused round is
// still there, waiting on an apply.
type workspaceOpenView struct {
	WorkspaceAlias string `json:"workspace_alias"`
	ConfigAlias    string `json:"config_alias"`
	ConfigPath     string `json:"config_path"`
	WorkspaceName  string `json:"workspace_name"`
	BackendType    string `json:"backend_type,omitempty"`
	// StatePath is where this workspace's state actually lives, resolved for the
	// workspace name. Empty for backends that key their state some other way.
	StatePath string `json:"state_path,omitempty"`
	// Lineage and Serial identify the statefile; absent until one exists.
	Lineage       string               `json:"lineage,omitempty"`
	Serial        uint64               `json:"serial,omitempty"`
	ResourceCount int                  `json:"resource_count"`
	Convergence   *convergenceFactView `json:"convergence,omitempty"`
	// Warnings carries informational notes — a backend type diverging from the
	// declared one, or state recording a provider the configuration no longer
	// declares.
	Warnings []string `json:"warnings,omitempty"`
}

// convergenceFactView is the convergence holding a workspace, as workspace_open and
// workspace_show report it. Stage is awaiting_approval, awaiting_actions, planning
// or applying while it runs, else how it ended; Followed says whether this server is
// following it (a convergence lives in the engine and outlasts the server).
type convergenceFactView struct {
	ID            string `json:"id"`
	Kind          string `json:"kind,omitempty"`
	Stage         string `json:"stage"`
	Round         int    `json:"round,omitempty"`
	OpenQuestions int    `json:"open_questions,omitempty"`
	Followed      bool   `json:"followed"`
}

// convergenceFactLine renders the holding convergence as a one-line segment: its id,
// its kind, and where it stands.
func convergenceFactLine(f *convergenceFactView) string {
	if f == nil || f.ID == "" {
		return ""
	}
	out := muted("holds ") + addr(f.ID)
	if w := runKindWord(f.Kind); w != "" {
		out += dot() + muted(w)
	}
	switch f.Stage {
	case "awaiting_approval":
		out += dot() + bold("awaiting approval", styles.Warning)
	case "awaiting_actions":
		out += dot() + bold(fmt.Sprintf("awaiting %d action(s)", f.OpenQuestions), styles.Warning)
	case "planning", "applying":
		out += dot() + bold(f.Stage, styles.Warning)
	case "":
	default:
		word, c := outcomeWord(f.Kind, f.Stage)
		out += dot() + bold(word, c)
	}
	if f.Round > 0 {
		out += dot() + muted(fmt.Sprintf("round %d", f.Round))
	}
	return out
}

func renderWorkspaceOpen(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, targetBody(errorTarget(msg)), width)
	}
	var w workspaceOpenView
	if !parseContent(msg, &w) {
		return fallbackLine(msg, s, ss, width)
	}

	summary := addr(workspaceName(w.WorkspaceAlias, w.WorkspaceName))
	if w.BackendType != "" {
		summary += dot() + keyword(w.BackendType)
	}
	// The configuration this workspace is bound to — the binding fixes the
	// backend and what every plan projects, so it belongs on the compact line.
	if w.ConfigPath != "" {
		summary = appendDot(summary, muted(w.ConfigPath))
	}
	summary = appendDot(summary, muted(fmt.Sprintf("%d resource(s)", w.ResourceCount)))
	if fact := convergenceFactLine(w.Convergence); fact != "" {
		summary += dot() + fact
	}

	var detail []string
	if w.StatePath != "" {
		row := "  " + muted(w.StatePath)
		if w.Lineage != "" {
			row += dot() + muted(fmt.Sprintf("serial %d · lineage %s", w.Serial, w.Lineage))
		}
		detail = append(detail, section("state"), row)
	}
	detail = append(detail, warningLines(w.Warnings)...)
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// workspaceCloseView is a close's result. Detached names the convergence the server
// stopped following: closing never cancels one, so it goes on in the engine and a
// later workspace_open re-attaches to it.
type workspaceCloseView struct {
	WorkspaceAlias string `json:"workspace_alias"`
	Detached       string `json:"detached,omitempty"`
}

func renderWorkspaceClose(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, targetBody(argString(msg, "workspace_alias")), width)
	}
	var w workspaceCloseView
	if !parseContent(msg, &w) {
		return fallbackLine(msg, s, ss, width)
	}

	name := w.WorkspaceAlias
	if name == "" {
		name = defaultWorkspaceName
	}
	summary := addr(name) + dot() + bold("closed", styles.Success)
	if w.Detached != "" {
		summary += dot() + muted("left ") + addr(w.Detached) + muted(" running in the engine")
	}
	return line(msg, s, summary, width)
}

// --- turf_workspace_list / turf_workspace_show ------------------------------

type workspaceListView struct {
	Workspaces []string `json:"workspaces"`
}

func renderWorkspaceList(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, "", width)
	}
	var r workspaceListView
	if !parseContent(msg, &r) {
		return fallbackLine(msg, s, ss, width)
	}
	summary := muted("none")
	if len(r.Workspaces) > 0 {
		summary = muted(fmt.Sprintf("%d found", len(r.Workspaces)))
	}
	if bt := argString(msg, "backend_type"); bt != "" {
		summary += dot() + muted(bt)
	}

	var detail []string
	const maxRows = 30
	for i, w := range r.Workspaces {
		if i == maxRows {
			detail = append(detail, muted(fmt.Sprintf("…(+%d more)", len(r.Workspaces)-maxRows)))
			break
		}
		detail = append(detail, addr(w))
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// workspaceShowView lists the workspaces open in this server, each with its
// binding and the convergence holding it, if any.
type workspaceShowView struct {
	Workspaces []struct {
		WorkspaceAlias string               `json:"workspace_alias"`
		ConfigAlias    string               `json:"config_alias"`
		ConfigPath     string               `json:"config_path"`
		WorkspaceName  string               `json:"workspace_name"`
		BackendType    string               `json:"backend_type,omitempty"`
		StatePath      string               `json:"state_path,omitempty"`
		OpenedAt       string               `json:"opened_at"`
		Convergence    *convergenceFactView `json:"convergence,omitempty"`
		Warning        string               `json:"warning,omitempty"`
	} `json:"workspaces"`
}

func renderWorkspaceShow(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, "", width)
	}
	var r workspaceShowView
	if !parseContent(msg, &r) {
		return fallbackLine(msg, s, ss, width)
	}

	var summary string
	switch len(r.Workspaces) {
	case 0:
		summary = muted("none open")
	case 1:
		w := r.Workspaces[0]
		summary = addr(workspaceName(w.WorkspaceAlias, w.WorkspaceName))
		if w.ConfigPath != "" {
			summary += dot() + muted(w.ConfigPath)
		}
		if fact := convergenceFactLine(w.Convergence); fact != "" {
			summary += dot() + fact
		}
	default:
		summary = muted(fmt.Sprintf("%d open", len(r.Workspaces)))
	}

	var detail []string
	for _, w := range r.Workspaces {
		row := addr(workspaceName(w.WorkspaceAlias, w.WorkspaceName))
		if w.BackendType != "" {
			row += dot() + muted(w.BackendType)
		}
		if w.ConfigPath != "" {
			row += dot() + muted(w.ConfigPath)
		}
		if w.OpenedAt != "" {
			row += dot() + muted("opened "+w.OpenedAt)
		}
		detail = append(detail, row)
		if fact := convergenceFactLine(w.Convergence); fact != "" {
			detail = append(detail, "  "+fact)
		}
		if w.Warning != "" {
			detail = append(detail, "  "+styles.WarningStyle.Render("⚠ "+w.Warning))
		}
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// workspaceName is the one rule for naming a workspace on the timeline: prefer the
// SESSION ALIAS, fall back to the OpenTofu workspace name.
//
// The alias is what the user passes to every later tool call, and it is set exactly
// when several workspaces are open — the case where naming them apart matters. The
// workspace name is a state slot inside the backend and is usually the literal
// "default" even then (the server steers callers toward vars over workspace names),
// so preferring it would label every open workspace identically.
//
// Unlike the session title's sessionLabel, "default" is kept here: a workspace line
// reports one specific call, and the default workspace really is what it opened.
func workspaceName(alias, name string) string {
	if alias != "" {
		return alias
	}
	return name
}

// --- turf_state_list --------------------------------------------------------

type stateListView struct {
	WorkspaceAlias string `json:"workspace_alias"`
	// Lineage and Serial identify the statefile the listing read.
	Lineage   string              `json:"lineage,omitempty"`
	Serial    uint64              `json:"serial,omitempty"`
	Resources []stateResourceView `json:"resources"`
	// Outputs are the root outputs, present only when there are any.
	Outputs  map[string]outputValueView `json:"outputs,omitempty"`
	Warnings []string                   `json:"warnings,omitempty"`
}

type stateResourceView struct {
	Address string `json:"address"`
	Mode    string `json:"mode"`
	Type    string `json:"type"`
	// Tainted marks an object the next plan replaces: one a failed apply left
	// half-created, or one taint marked. Invisible in a plain address listing
	// otherwise.
	Tainted bool `json:"tainted,omitempty"`
	// Deposed are the keys of objects stranded under this address by an interrupted
	// create-before-destroy replace. They are still real infrastructure and still cost
	// money, so a state listing has to admit they exist.
	Deposed []string `json:"deposed,omitempty"`
	// Values and SensitiveValues are set only when the call asked for include_values.
	Values          map[string]any `json:"values,omitempty"`
	SensitiveValues any            `json:"sensitive_values,omitempty"`
	Identity        map[string]any `json:"identity,omitempty"`
}

func renderStateList(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, targetBody(argString(msg, "workspace_alias")), width)
	}
	var st stateListView
	if !parseContent(msg, &st) {
		return fallbackLine(msg, s, ss, width)
	}
	summary := muted(fmt.Sprintf("%d resource(s)", len(st.Resources)))
	var tainted, deposed int
	for _, r := range st.Resources {
		if r.Tainted {
			tainted++
		}
		deposed += len(r.Deposed)
	}
	if tainted > 0 {
		summary += dot() + styles.WarningStyle.Render(fmt.Sprintf("%d tainted", tainted))
	}
	if deposed > 0 {
		summary += dot() + styles.WarningStyle.Render(fmt.Sprintf("%d deposed", deposed))
	}
	if n := len(st.Outputs); n > 0 {
		summary += dot() + muted(fmt.Sprintf("%d output(s)", n))
	}
	if st.Serial > 0 {
		summary += dot() + muted(fmt.Sprintf("serial %d", st.Serial))
	}

	var detail []string
	const maxRows = 40
	for i, r := range st.Resources {
		if i == maxRows {
			detail = append(detail, muted(fmt.Sprintf("…(+%d more)", len(st.Resources)-maxRows)))
			break
		}
		row := addr(r.Address)
		if r.Mode == "data" {
			row += dot() + muted("data")
		}
		if r.Tainted {
			row += dot() + styles.WarningStyle.Render("tainted")
		}
		for _, key := range r.Deposed {
			row += dot() + styles.WarningStyle.Render("deposed "+key)
		}
		if len(r.Identity) > 0 {
			row += dot() + muted("identity")
		}
		detail = append(detail, row)
		detail = append(detail, kvLinesMasked(r.Values, r.SensitiveValues, "  ", 12)...)
	}
	if len(st.Outputs) > 0 {
		detail = append(detail, section("outputs"))
		flat, mask := outputsMask(st.Outputs)
		detail = append(detail, kvLinesMasked(flat, mask, "  ", 20)...)
	}
	detail = append(detail, warningLines(st.Warnings)...)
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// --- turf_taint / turf_untaint ----------------------------------------------
//
// taint marks resource instances for replacement by the next plan; untaint lifts the
// mark. Either is a state edit that applies nothing by itself: the replace shows up in
// the next up's plan, which is approved like any other.

type taintView struct {
	WorkspaceAlias string   `json:"workspace_alias"`
	Addrs          []string `json:"addrs"`
	Status         string   `json:"status"`
	Serial         uint64   `json:"serial"`
}

func renderTaint(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	target := argString(msg, "addrs")
	if running(msg) {
		return line(msg, s, targetBody(target), width)
	}
	var t taintView
	if !parseContent(msg, &t) {
		return fallbackLine(msg, s, ss, width)
	}
	if len(t.Addrs) > 0 {
		target = strings.Join(t.Addrs, ", ")
	}
	summary := targetBody(target)
	if t.Status != "" {
		c := styles.Accent
		switch t.Status {
		case "tainted":
			c = styles.Warning
		case "untainted":
			c = styles.Success
		}
		summary = appendDot(summary, bold(t.Status, c))
	}
	if t.Serial > 0 {
		summary = appendDot(summary, muted(fmt.Sprintf("serial %d", t.Serial)))
	}
	return line(msg, s, summary, width)
}

// --- turf_provider_describe -------------------------------------------------
//
// provider_describe returns a TypeDescription (turf-engine tofu/provider/
// schema_converter.go): one resource, data source or action type's schema as {type,
// description, properties{name:{usage, type, sensitive, deprecated, description,…}},
// required, identity_schema}. Asked for the builtin turf provider with no type, it
// returns that provider's whole ProviderSchema instead ({resources, data_sources,
// actions}), which lists Turf's own actions. The default renderer dumps either; we
// summarize the type and, on expand, list its attributes with type + usage flags.

type providerDescribeView struct {
	Type           string         `json:"type"`
	Description    string         `json:"description"`
	Properties     map[string]any `json:"properties"`
	Required       []string       `json:"required"`
	IdentitySchema map[string]any `json:"identity_schema,omitempty"`
	// The ProviderSchema form: type name → description, per kind.
	Resources   map[string]json.RawMessage `json:"resources,omitempty"`
	DataSources map[string]json.RawMessage `json:"data_sources,omitempty"`
	Actions     map[string]json.RawMessage `json:"actions,omitempty"`
}

func renderProviderDescribe(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	target := errorTarget(msg)
	if running(msg) {
		return line(msg, s, targetBody(target), width)
	}
	var d providerDescribeView
	if !parseContent(msg, &d) {
		return fallbackLine(msg, s, ss, width)
	}
	if d.Type == "" && (len(d.Resources) > 0 || len(d.DataSources) > 0 || len(d.Actions) > 0) {
		return renderProviderSchema(msg, s, ss, width, target, d)
	}

	typ := d.Type
	if typ == "" {
		typ = target
	}
	summary := addr(typ)
	if n := len(d.Properties); n > 0 {
		summary += dot() + muted(fmt.Sprintf("%d attr(s)", n))
	}
	if len(d.IdentitySchema) > 0 {
		summary += dot() + muted("identity")
	}
	if prov := argString(msg, "provider"); prov != "" && prov != typ {
		summary += dot() + providerName(prov)
	}

	var detail []string
	if d.Description != "" {
		detail = append(detail, muted(firstLine(d.Description)))
	}
	if len(d.Properties) > 0 {
		detail = append(detail, section("attributes"))
		req := map[string]bool{}
		for _, r := range d.Required {
			req[r] = true
		}
		names := make([]string, 0, len(d.Properties))
		for n := range d.Properties {
			names = append(names, n)
		}
		sort.Strings(names)
		const maxAttrs = 40
		for i, n := range names {
			if i == maxAttrs {
				detail = append(detail, "  "+muted(fmt.Sprintf("…(+%d more)", len(names)-maxAttrs)))
				break
			}
			detail = append(detail, "  "+attrSchemaLine(n, d.Properties[n], req[n]))
		}
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// renderProviderSchema renders the whole-provider form: how many types of each kind,
// and their names on expand.
func renderProviderSchema(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width int, target string, d providerDescribeView) string {
	summary := targetBody(target)
	kinds := []struct {
		label string
		types map[string]json.RawMessage
	}{
		{"resource type(s)", d.Resources},
		{"data source(s)", d.DataSources},
		{"action(s)", d.Actions},
	}
	var detail []string
	for _, k := range kinds {
		if len(k.types) == 0 {
			continue
		}
		summary = appendDot(summary, muted(fmt.Sprintf("%d %s", len(k.types), k.label)))
		names := make([]string, 0, len(k.types))
		for n := range k.types {
			names = append(names, n)
		}
		sort.Strings(names)
		detail = append(detail, section(strings.TrimSuffix(k.label, "(s)")+"s"))
		for _, n := range names {
			detail = append(detail, "  "+addr(n))
		}
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// attrSchemaLine renders one schema attribute: name + type + usage/required flag,
// with sensitive/deprecated markers. prop is the per-attribute info map.
func attrSchemaLine(name string, prop any, required bool) string {
	m, _ := prop.(map[string]any)
	out := addr(name)
	if t, ok := m["type"].(string); ok && t != "" {
		out += muted("  " + t)
	}
	usage, _ := m["usage"].(string)
	if required {
		usage = "required"
	}
	if usage != "" {
		out += muted("  " + usage)
	}
	if b, _ := m["sensitive"].(bool); b {
		out += " " + styles.WarningStyle.Render("sensitive")
	}
	if b, _ := m["deprecated"].(bool); b {
		out += " " + styles.WarningStyle.Render("deprecated")
	}
	return out
}

// --- turf_provider_search ---------------------------------------------------
//
// provider_search queries the registry; the default renderer dumps the whole
// {providers:[…]} array. We show the query + a match count, and on expand list
// each provider as name · version · description.

type providerSearchView struct {
	Providers []struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		Description string `json:"description"`
	} `json:"providers"`
}

func renderProviderSearch(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, targetBody(argString(msg, "query")), width)
	}
	var r providerSearchView
	if !parseContent(msg, &r) {
		return fallbackLine(msg, s, ss, width)
	}
	var summary string
	if q := argString(msg, "query"); q != "" {
		summary = addr(q) + dot()
	}
	summary += muted(fmt.Sprintf("%d result(s)", len(r.Providers)))

	var detail []string
	const maxRows = 20
	for i, p := range r.Providers {
		if i == maxRows {
			detail = append(detail, muted(fmt.Sprintf("…(+%d more)", len(r.Providers)-maxRows)))
			break
		}
		row := addr(p.Name)
		if p.Version != "" {
			row += dot() + muted("v"+p.Version)
		}
		if p.Description != "" {
			row += dot() + muted(truncateStr(p.Description, 60))
		}
		detail = append(detail, row)
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// --- turf_skill_* / turf_read_skill_file ------------------------------------
//
// Skill tools are lazy-loaded how-to guides: a zero-arg skill_<slug> call returns
// the guide's SKILL.md markdown, which is too long to dump in the timeline. We
// render a clean single "skill <slug> loaded (N lines)" line — the size inline —
// with no expand detail; the leading title already names which skill this is.

func renderSkill(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	// The title (e.g. "Core Skill") already names which skill this is, so the body
	// just carries load status — no need to repeat the slug.
	if running(msg) {
		return line(msg, s, "", width)
	}
	if out, ok := errorLine(msg, s, ss, width); ok {
		return out
	}
	summary := muted("loaded")
	if n := lineCount(msg.Content); n > 0 {
		summary += muted(fmt.Sprintf(" (%d lines)", n))
	}
	return line(msg, s, summary, width)
}

func renderReadSkillFile(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	path := argString(msg, "path")
	if running(msg) {
		return line(msg, s, targetBody(path), width)
	}
	if out, ok := errorLine(msg, s, ss, width); ok {
		return out
	}
	summary := addr(path)
	if slug := argString(msg, "slug"); slug != "" {
		summary += dot() + muted(slug)
	}
	if n := lineCount(msg.Content); n > 0 {
		summary += muted(fmt.Sprintf(" (%d lines)", n))
	}
	return line(msg, s, summary, width)
}

// --- turf_workspace_delete --------------------------------------------------

type workspaceDeleteView struct {
	WorkspaceName string `json:"workspace_name"`
	ResourceCount int    `json:"resource_count"`
	Deleted       bool   `json:"deleted"`
}

func renderWorkspaceDelete(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, targetBody(argString(msg, "workspace_name")), width)
	}
	var w workspaceDeleteView
	if !parseContent(msg, &w) {
		return fallbackLine(msg, s, ss, width)
	}
	name := w.WorkspaceName
	if name == "" {
		name = argString(msg, "workspace_name")
	}
	status := bold("deleted", styles.Success)
	if !w.Deleted {
		status = styles.WarningStyle.Render("not deleted")
	}
	summary := addr(name) + dot() + status
	if w.ResourceCount > 0 {
		summary += dot() + styles.WarningStyle.Render(fmt.Sprintf("%d orphaned resource(s)", w.ResourceCount))
	}
	return line(msg, s, summary, width)
}

// --- turf_init --------------------------------------------------------------
//
// init is `tofu init`: it registers a configuration directory, installs its modules
// and providers, writes the dependency lock file, and reports what the directory
// declares. We show the configuration + a "N providers · N variables · N outputs"
// tally, and on expand list each, the providers with the version init installed.

type initProviderView struct {
	Source  string `json:"source"`
	Version string `json:"version,omitempty"`
	// Inferred marks a requirement turf derived from usage rather than read from a
	// required_providers block — Terraform implies one from every resource's local
	// name. Source is then the address OpenTofu itself implies, and Version is
	// empty because the module stated no constraint. Worth showing: it is the
	// difference between a pin the configuration made and one nobody wrote down.
	Inferred bool `json:"inferred,omitempty"`
}

// initInstalledView is one provider init installed (or found built in, or failed
// to install), keyed back to the requirement by its local name.
type initInstalledView struct {
	LocalName string `json:"local_name"`
	Source    string `json:"source"`
	Version   string `json:"version,omitempty"`
	BuiltIn   bool   `json:"builtin,omitempty"`
	Error     string `json:"error,omitempty"`
}

type initVariableView struct {
	Name      string `json:"name"`
	Type      string `json:"type,omitempty"`
	Required  bool   `json:"required"`
	Sensitive bool   `json:"sensitive,omitempty"`
	// Ephemeral is Terraform's `ephemeral = true`: the value may reach provider
	// configuration, an ephemeral module input, or a write-only attribute, and
	// nothing else — never state, never a stored plan.
	Ephemeral bool `json:"ephemeral,omitempty"`
}

type initOutputView struct {
	Name      string `json:"name"`
	Sensitive bool   `json:"sensitive,omitempty"`
}

type initView struct {
	// ConfigAlias is the handle this configuration was registered under — the
	// configuration's own name, and what the session title labels the work with.
	// Empty for the unnamed default configuration.
	ConfigAlias string `json:"config_alias"`
	Path        string `json:"path"`
	// Scratch marks a turf-allocated temporary directory (no path was given), so
	// nothing here outlives the session.
	Scratch bool `json:"scratch,omitempty"`
	Backend *struct {
		Type string `json:"type"`
		// Defaulted marks the SYNTHESIZED local default — the entry describing
		// something the directory does not actually contain. The server states it
		// positively and omits the false, so ABSENCE means "declared".
		Defaulted bool `json:"defaulted,omitempty"`
	} `json:"backend"`
	// Workspace is the OpenTofu workspace selected in the directory, "default"
	// unless someone ran `tofu workspace select`.
	Workspace         string                      `json:"workspace"`
	RequiredProviders map[string]initProviderView `json:"required_providers"`
	Providers         []initInstalledView         `json:"providers,omitempty"`
	LockFile          *struct {
		Path string `json:"path"`
		// Status is created, updated, unchanged or not_updated.
		Status string `json:"status"`
	} `json:"lock_file,omitempty"`
	ProviderConfigs []struct {
		Type  string `json:"type"`
		Alias string `json:"alias,omitempty"`
	} `json:"provider_configs,omitempty"`
	Variables []initVariableView `json:"variables,omitempty"`
	Outputs   []initOutputView   `json:"outputs,omitempty"`
	// Imports are the configuration's import {} blocks, which the next up adopts.
	Imports []struct {
		To       string `json:"to"`
		ID       string `json:"id,omitempty"`
		Identity string `json:"identity,omitempty"`
	} `json:"imports,omitempty"`
	ModulesInstalled bool `json:"modules_installed,omitempty"`
}

func renderInit(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, targetBody(errorTarget(msg)), width)
	}
	var c initView
	if !parseContent(msg, &c) {
		return fallbackLine(msg, s, ss, width)
	}
	target := c.ConfigAlias
	if target == "" {
		target = c.Path
	}
	if target == "" {
		target = argString(msg, "path")
	}
	summary := targetBody(target)
	// Surface the configuration directory — the workflow's durable identity —
	// when the alias led the line.
	if c.Path != "" && c.Path != target {
		summary = appendDot(summary, muted(c.Path))
	}
	if c.Scratch {
		summary = appendDot(summary, muted("scratch"))
	}
	if c.Backend != nil && c.Backend.Type != "" {
		backend := "backend " + c.Backend.Type
		if c.Backend.Defaulted {
			backend += " (default)"
		}
		summary = appendDot(summary, muted(backend))
	}
	if parts := initCountParts(len(c.RequiredProviders), len(c.Variables), len(c.Outputs)); parts != "" {
		summary = appendDot(summary, muted(parts))
	}
	if n := len(c.Imports); n > 0 {
		summary = appendDot(summary, colored(fmt.Sprintf("%d to import", n), styles.Accent))
	}
	if n := initProviderErrors(c.Providers); n > 0 {
		summary = appendDot(summary, styles.ErrorStyle.Render(fmt.Sprintf("%d provider(s) failed to install", n)))
	}

	detail := initDetail(c.RequiredProviders, c.Providers, c.Variables, c.Outputs)
	if len(c.ProviderConfigs) > 0 {
		names := make([]string, 0, len(c.ProviderConfigs))
		for _, pc := range c.ProviderConfigs {
			n := pc.Type
			if pc.Alias != "" {
				n += "." + pc.Alias
			}
			names = append(names, n)
		}
		detail = append(detail, section("provider configurations"), "  "+addr(strings.Join(names, ", ")))
	}
	if len(c.Imports) > 0 {
		detail = append(detail, section("imports"))
		for _, im := range c.Imports {
			note := "id=" + im.ID
			if im.ID == "" {
				note = "identity"
			}
			detail = append(detail, "  "+addr(im.To)+dot()+colored(note, styles.Accent))
		}
	}
	var notes []string
	if c.ModulesInstalled {
		notes = append(notes, "modules installed")
	}
	if lf := c.LockFile; lf != nil && lf.Status != "" {
		notes = append(notes, "lock file "+strings.ReplaceAll(lf.Status, "_", " "))
	}
	if len(notes) > 0 {
		detail = append(detail, muted(strings.Join(notes, " · ")))
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// initProviderErrors counts the providers init could not install.
func initProviderErrors(installed []initInstalledView) int {
	n := 0
	for _, p := range installed {
		if p.Error != "" {
			n++
		}
	}
	return n
}

// initCountParts renders the "N provider(s) · N variable(s) · N output(s)" tally,
// omitting empty buckets; returns "" when everything is zero.
func initCountParts(nProviders, nVars, nOutputs int) string {
	var parts []string
	if nProviders > 0 {
		parts = append(parts, fmt.Sprintf("%d provider(s)", nProviders))
	}
	if nVars > 0 {
		parts = append(parts, fmt.Sprintf("%d variable(s)", nVars))
	}
	if nOutputs > 0 {
		parts = append(parts, fmt.Sprintf("%d output(s)", nOutputs))
	}
	return strings.Join(parts, " · ")
}

// initDetail renders init's expanded provider/variable/output sections. Each
// required provider carries the version init installed for it — or that it is built
// in, or why it failed.
func initDetail(providers map[string]initProviderView, installed []initInstalledView, vars []initVariableView, outs []initOutputView) []string {
	byName := make(map[string]initInstalledView, len(installed))
	for _, p := range installed {
		byName[p.LocalName] = p
	}
	var detail []string
	if len(providers) > 0 {
		detail = append(detail, section("required providers"))
		names := make([]string, 0, len(providers))
		for n := range providers {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			row := "  " + addr(n)
			p := providers[n]
			if p.Source != "" {
				row += muted("  " + p.Source)
				if p.Version != "" {
					row += muted("  " + p.Version)
				}
			}
			if p.Inferred {
				row += " " + muted("inferred")
			}
			if in, ok := byName[n]; ok {
				switch {
				case in.Error != "":
					row += " " + styles.ErrorStyle.Render(in.Error)
				case in.BuiltIn:
					row += " " + muted("built in")
				case in.Version != "":
					row += muted(" → ") + colored("v"+in.Version, styles.Success)
				}
			}
			detail = append(detail, row)
		}
	}
	if len(vars) > 0 {
		detail = append(detail, section("variables"))
		for _, v := range vars {
			row := "  " + addr(v.Name)
			if v.Type != "" {
				row += muted("  " + v.Type)
			}
			if v.Required {
				row += muted("  required")
			}
			if v.Sensitive {
				row += " " + styles.WarningStyle.Render("sensitive")
			}
			if v.Ephemeral {
				row += " " + styles.WarningStyle.Render("ephemeral")
			}
			detail = append(detail, row)
		}
	}
	if len(outs) > 0 {
		detail = append(detail, section("outputs"))
		for _, o := range outs {
			row := "  " + addr(o.Name)
			if o.Sensitive {
				row += " " + styles.WarningStyle.Render("sensitive")
			}
			detail = append(detail, row)
		}
	}
	return detail
}

// appendDot joins a trailing segment onto a summary with the " · " separator,
// or returns the segment alone when the summary is still empty (so a line never
// opens with a stray separator).
func appendDot(summary, add string) string {
	if summary == "" {
		return add
	}
	return summary + dot() + add
}

// firstLine returns the first non-empty line of markdown, stripped of any leading
// '#'/spaces (so a "# Title" heading reads as "Title"), truncated for the timeline.
func firstLine(md string) string {
	for _, ln := range strings.Split(md, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		return truncateStr(strings.TrimSpace(strings.TrimLeft(t, "# ")), 80)
	}
	return ""
}

func lineCount(s string) int {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// --- action symbol / theme color mapping ------------------------------------

// planAction maps a plan document change's actions — OpenTofu's json-format list,
// ["create"], ["delete","create"], … — to a human label, a theme color, and the glyph
// `tofu plan` prints for it (DiffActionSymbol in OpenTofu's jsonformat renderers):
//
//   - create            -/+  delete then create (the default replace)
//     ~  update            +/-  create then delete (create_before_destroy)
//   - delete            ./+  forget then create (a replace under destroy = false)
//     <= read              .    forget
//
// A no-op has no glyph, as in `tofu plan`. The colors are the CLI theme's own, so
// /theme keeps owning the look.
func planAction(actions []string) (label string, c color.Color, glyph string) {
	switch strings.Join(actions, ",") {
	case "create":
		return "create", styles.Success, "+"
	case "update":
		return "update", styles.Accent, "~"
	case "delete":
		return "destroy", styles.Error, "-"
	case "delete,create":
		return "replace", styles.Warning, "-/+"
	case "create,delete":
		return "replace", styles.Warning, "+/-"
	case "forget,create":
		return "replace", styles.Warning, "./+"
	case "read":
		return "read", styles.Accent, "<="
	case "forget":
		return "forget", styles.Highlight, "."
	case "no-op":
		return "no-op", styles.Highlight, ""
	default:
		return "change", styles.Accent, "?"
	}
}

// warningLines renders a result's non-fatal diagnostics as ⚠-prefixed detail rows.
// Every turf tool that reports warnings reports them the same way, so the shape is
// shared rather than repeated per renderer.
func warningLines(warnings []string) []string {
	lines := make([]string, 0, len(warnings))
	for _, w := range warnings {
		lines = append(lines, styles.WarningStyle.Render("⚠ "+w))
	}
	return lines
}

func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}
