package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"sort"
	"strconv"
	"strings"

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
// with the friendly, English-readable tool title (e.g. "Approve Plan") — resolved via
// toolTitle from the shared turfToolInfo source and styled with the same neutral
// styles.ToolName cagent's built-in renderers use — followed by the target + status.
// Color and the status glyph come from cagent's theme via the styles package and
// toolcommon.Icon, so the whole thing re-themes for free when the user runs /theme.
//
// Two detail levels, switched by cagent's HideToolResults() toggle (Ctrl+O), which
// every renderer receives via SessionStateReader:
//   - hidden  → the compact one-liner.
//   - shown   → the one-liner PLUS a rich, indented "unfold": full attribute
//     diffs (before → after, colored +/~/-), applied state, ready sets, evaluated
//     outputs, provider tables — plenty of detail for inspecting a step. This is
//     turf's default (see newSession).
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
//     string" line; turf's views (attribute diffs, colored tallies, ready-sets, an
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
		"turf_workspace_open":     builder(renderWorkspaceOpen),
		"turf_workspace_close":    builder(renderWorkspaceClose),
		"turf_workspace_list":     builder(renderWorkspaceList),
		"turf_workspace_show":     builder(renderWorkspaceShow),
		"turf_workspace_delete":   builder(renderWorkspaceDelete),
		"turf_plan_new":           builder(renderPlanNew),
		"turf_plan_cancel":        builder(renderPlanCancel),
		"turf_plan_export":        builder(renderPlanExport),
		"turf_plan_approve":       builder(renderPlanApprove),
		"turf_config_init":        builder(renderConfigInit),
		"turf_config_show":        builder(renderConfigShow),
		"turf_config_promote":     builder(renderConfigPromote),
		"turf_declare_backend":    builder(renderDeclareBackend),
		"turf_declare_provider":   builder(renderDeclareProvider),
		"turf_declare_var":        builder(renderDeclareVar),
		"turf_replan":             builder(renderReplan),
		"turf_module_init":        builder(renderModuleInit),
		"turf_declare_module":     builder(renderDeclareModule),
		"turf_module_outputs":     builder(renderModuleOutputs),
		"turf_declare_resource":   builder(renderDeclareResource),
		"turf_resource_import":    builder(renderResourceImport),
		"turf_resource_refresh":   builder(renderResourceRefresh),
		"turf_declare_action":     builder(renderDeclareAction),
		"turf_action_invoke":      builder(renderActionInvoke),
		"turf_declare_outputs":    builder(renderDeclareOutputs),
		"turf_outputs":            builder(renderOutputs),
		"turf_effect_apply":       builder(renderEffectApply),
		"turf_effect_cancel":      builder(renderEffectCancel),
		"turf_state_list":         builder(renderStateList),
		"turf_datasource_read":    builder(renderDatasourceRead),
		"turf_declare_datasource": builder(renderDeclareDatasource),
		"turf_declare_ephemeral":  builder(renderDeclareEphemeral),
		"turf_provider_search":    builder(renderProviderSearch),
		"turf_provider_load":      builder(renderProviderLoad),
		"turf_provider_describe":  builder(renderProviderDescribe),
		"turf_skill_core":         builder(renderSkill),
		"turf_skill_adhoc":        builder(renderSkill),
		"turf_skill_codified":     builder(renderSkill),
		"turf_skill_demo":         builder(renderSkill),
		"turf_read_skill_file":    builder(renderReadSkillFile),
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

func argString(msg *types.Message, key string) string {
	var m map[string]any
	if json.Unmarshal([]byte(msg.ToolCall.Function.Arguments), &m) != nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// argBool reads a boolean request argument. Separate from argString because the
// wire carries a real JSON bool, which argString's string type-assertion drops.
func argBool(msg *types.Message, key string) bool {
	var m map[string]any
	if json.Unmarshal([]byte(msg.ToolCall.Function.Arguments), &m) != nil {
		return false
	}
	v, _ := m[key].(bool)
	return v
}

// turfToolTargetArgs maps a bare turf tool name to its request's target argument(s), in
// priority order — the identifier that names what the call operates on (a resource,
// provider, module, workspace, …). It mirrors the target each renderer already picks in
// its running(msg) branch; errorTarget uses it to give a failed line the same lead
// context a successful one has. Tools absent here (plan_*, state_list, outputs, skills)
// fall back to the workspace alias (or nothing, for zero-arg skills). Arg names track the
// server tool structs in turf-mcp-server's internal/tools package.
var turfToolTargetArgs = map[string][]string{
	"declare_resource":   {"resource_addr"},
	"resource_import":    {"resource_addr"},
	"resource_refresh":   {"resource_addr"},
	"datasource_read":    {"resource_addr"},
	"declare_datasource": {"resource_addr"},
	"declare_ephemeral":  {"resource_addr"},
	"config_init":        {"path"},
	"config_show":        {"address"},
	"declare_backend":    {"type"},
	"declare_provider":   {"name"},
	"declare_var":        {"name"},
	"module_init":        {"source"},
	"declare_module":     {"address"},
	"module_outputs":     {"address"},
	"provider_search":    {"query"},
	"provider_load":      {"source", "name"},
	"provider_describe":  {"resource_type", "datasource_type", "action_type"},
	"declare_action":     {"action_type"},
	"action_invoke":      {"action_type"},
	"effect_apply":       {"effect_id"},
	"effect_cancel":      {"effect_id"},
	"workspace_open":     {"workspace_name"},
	"workspace_close":    {"workspace_alias"},
	"workspace_delete":   {"workspace_name"},
	"read_skill_file":    {"path"},
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
// `tofu plan` reads. Used by declare_resource and by each resource in a walk-summary
// plan. before==nil (a create) renders every attr as an addition; after==nil (a
// destroy) renders every attr as a removal.

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

// kvLines renders a map as aligned "key = value" detail lines (applied state, evaluated
// outputs, resolved config, imported state, …), expanding nested maps/lists via
// renderValue. Null leaves are skipped; max caps the number of rendered lines. This is
// the unmarked (no +/-) value view; diffs pass a marker through renderValue instead.
func kvLines(m map[string]any, indent string, max int) []string {
	return kvLinesMasked(m, nil, indent, max)
}

// kvLinesMasked is kvLines for a value that came with a sensitive_values mask alongside
// it (effect_apply's new_state, datasource_read's state). A nil mask is the plain view.
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
// sensitive_values shape alongside the values: `true` at a sensitive node (covering
// its whole subtree), an object with the non-sensitive keys omitted, a positional
// array for lists/sets/tuples with null at the non-sensitive elements, and the field
// absent entirely when nothing is sensitive. See objchange.SensitiveValuesFromMarked.
//
// The mask matters because show_sensitive puts the real secret on the wire. The
// server hands it to the model deliberately; the timeline is a different surface — it
// is also terminal scrollback and the persisted session — so turf redacts at display
// time from the mask, whatever arrived. In the ordinary (unrevealed) case the values
// are already the __cty_sensitive__ sentinel and the mask agrees with them, so the
// rendered bytes are the same either way.

// sensitiveLeaf stands in for a value we are not allowed to print. It is exactly what
// fmtVal renders the __cty_sensitive__ sentinel as, so a mask-redacted value and a
// pre-redacted one read identically — one form on every surface, including both halves
// of a diff between two unprintable values.
const sensitiveLeaf = "(sensitive)"

// maskSensitive reports whether a mask node marks its whole subtree sensitive.
func maskSensitive(mask any) bool {
	b, ok := mask.(bool)
	return ok && b
}

// maskKey descends an object/map mask by attribute name; nil when the mask says
// nothing about that key (or is not an object at all).
func maskKey(mask any, k string) any {
	m, ok := mask.(map[string]any)
	if !ok {
		return nil
	}
	return m[k]
}

// maskIndex descends a list/set/tuple mask by position; nil when out of range (or the
// mask is not positional).
func maskIndex(mask any, i int) any {
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

// fmtVal renders a JSON-decoded value compactly. turf's sentinels are masked:
// "__cty_unknown__" (a value known only after apply) and "__cty_sensitive__" (a
// sensitive value whose real content stayed server-side and never reached us — the
// masks above cover the case where it did).
func fmtVal(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		switch x {
		case "__cty_unknown__":
			return "(known after apply)"
		case "__cty_sensitive__":
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

func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// --- turf_declare_resource ----------------------------------------------------

type resourcePlanView struct {
	ResourceAddr        string         `json:"resource_addr"`
	ResourceType        string         `json:"resource_type"`
	Provider            string         `json:"provider"`
	Action              string         `json:"action"`                          // create, update, delete, replace, …
	CreateBeforeDestroy bool           `json:"create_before_destroy,omitempty"` // for replace: ± (true) vs ∓ (absent)
	ActionReason        string         `json:"action_reason"`
	RequiresReplace     []string       `json:"requires_replace"`
	Before              map[string]any `json:"before"`
	After               map[string]any `json:"after"`
	// Which paths of Before/After are sensitive, in OpenTofu's before_sensitive/
	// after_sensitive shape. Reported whether or not the values arrived redacted, so a
	// show_sensitive call keeps the classification — and the timeline keeps redacting.
	BeforeSensitive any `json:"before_sensitive,omitempty"`
	AfterSensitive  any `json:"after_sensitive,omitempty"`
	// Importing is set when this change ADOPTS an existing object named by an
	// `import {}` block, so the Before half is the real remote object rather than a
	// null prior — which is why a "noop" action here means "already matches", not
	// "nothing to do".
	Importing *importingInfoView `json:"importing,omitempty"`
	Deferred  *struct {
		Reason string `json:"reason"`
		// LastError is the provider's own words when this deferral was DEMOTED
		// from a provider failure — the configure or RPC error that would have
		// surfaced as an error had the provider's configuration been wholly
		// known. Without it a demoted failure reads as an ordinary wait.
		LastError string `json:"last_error,omitempty"`
	} `json:"deferred,omitempty"`
	// Replan names the pending changes in this Draft that referenced this address
	// and were planned against the previous declaration — they are now stale.
	Replan []string `json:"replan,omitempty"`
}

// importingInfoView is the server's `importing` object: the locator the import
// block carried. Exactly one of ID / Identity is set — a provider-specific id
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

func renderDeclareResource(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, addr(argString(msg, "resource_addr")), width)
	}
	var p resourcePlanView
	if !parseContent(msg, &p) {
		return fallbackLine(msg, s, ss, width)
	}

	label, c, glyph := planAction(p.Action, p.Deferred != nil, p.CreateBeforeDestroy)
	var summary string
	switch {
	case p.Deferred != nil:
		summary = addr(p.ResourceAddr) + dot() + bold("deferred", c)
	case label == "replace":
		// Show the ± (create-before-destroy) / ∓ (destroy-before-create) symbol.
		summary = addr(p.ResourceAddr) + dot() + bold("replace "+glyph, c)
	default:
		summary = addr(p.ResourceAddr) + dot() + bold(label, c)
	}
	// An adoption's before half is the real remote object, so a "no-op" here reads
	// as "already matches" rather than "nothing to do" — say which it is.
	if note := p.Importing.adoptNote(); note != "" {
		summary += dot() + colored(note, styles.Accent)
	}
	if changed := changedKeys(p.Before, p.After); changed != "" {
		summary += dot() + muted(changed)
	}
	if n := len(p.Replan); n > 0 {
		summary += dot() + styles.WarningStyle.Render(fmt.Sprintf("%d replan", n))
	}

	var detail []string
	if p.Provider != "" {
		detail = append(detail, muted("provider ")+providerName(p.Provider))
	}
	if p.ActionReason != "" {
		detail = append(detail, muted("reason: ")+p.ActionReason)
	}
	if p.Importing != nil && len(p.Importing.Identity) > 0 {
		detail = append(detail, section("adopt identity"))
		detail = append(detail, kvLines(p.Importing.Identity, "  ", 10)...)
	}
	if len(p.RequiresReplace) > 0 {
		detail = append(detail, styles.WarningStyle.Render("forces replacement: "+strings.Join(p.RequiresReplace, ", ")))
	}
	if p.Deferred != nil {
		reason := p.Deferred.Reason
		if reason == "" {
			reason = "blocked on upstream changes"
		}
		detail = append(detail, styles.WarningStyle.Render("deferred: "+reason))
		if p.Deferred.LastError != "" {
			detail = append(detail, "  "+styles.ErrorStyle.Render(p.Deferred.LastError))
		}
	}
	if len(p.Replan) > 0 {
		detail = append(detail, section("replan"))
		detail = append(detail, listLines(p.Replan, "  ", 20)...)
	}
	if diff := attrDiff(p.Before, p.After, p.BeforeSensitive, p.AfterSensitive, ""); len(diff) > 0 {
		detail = append(detail, diff...)
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// --- turf_effect_apply ------------------------------------------------------

type effectApplyView struct {
	// Kind is the effect the phase dispatched: create/update/destroy run provider
	// RPCs; forget, import (commit a plan-time adoption) and move (commit a state
	// relocation) are state writes with no provider call.
	Kind         string         `json:"kind"`
	State        string         `json:"state"`
	ResourceAddr string         `json:"resource_addr"`
	Ready        []string       `json:"ready"`
	NewState     map[string]any `json:"new_state,omitempty"`
	// DeposedState is the prior object a create-before-destroy replace moved into
	// the deposed slot before creating its replacement — so state stays consistent
	// if the create fails. Set on the create half of a CBD replace only.
	DeposedState map[string]any `json:"deposed_state,omitempty"`
	// SensitiveValues masks NewState, in OpenTofu's sensitive_values shape. The
	// server sends no separate mask for DeposedState, so this one is reused for it:
	// the two are the same resource type, so their schema-derived sensitivity is
	// identical and only config-flow marks can differ. It matters solely under
	// show_sensitive — unrevealed, DeposedState already arrives as sentinels that
	// fmtVal masks on its own.
	SensitiveValues any `json:"sensitive_values,omitempty"`
	// Outputs are the root outputs this effect newly resolved — an output that
	// referenced the just-applied resource. Same {value, sensitive} shape as the
	// outputs tool, so the same mask carries across.
	Outputs map[string]outputValueView `json:"outputs,omitempty"`
	// Message is the effect's own account of what it did. Load-bearing for a move,
	// whose from→to relocation reaches the wire only here.
	Message string `json:"message,omitempty"`
}

func renderEffectApply(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, addr(argString(msg, "effect_id")), width)
	}
	var e effectApplyView
	if !parseContent(msg, &e) {
		return fallbackLine(msg, s, ss, width)
	}

	state := e.State
	if state == "" {
		state = "applied"
	}
	head := e.Kind
	if head == "" {
		head = "apply"
	}
	c := applyStateColor(e.State)
	summary := addr(e.ResourceAddr) + dot() + bold(head, c) + dot() + bold(state, c)
	if n := len(e.Outputs); n > 0 {
		summary += dot() + muted(fmt.Sprintf("%d output(s)", n))
	}

	var detail []string
	// What the effect wrote first — new state, then any deposed object it moved,
	// then the outputs it resolved — and last what becomes ready: the temporal
	// order of apply. The message trails as the effect's own account of it.
	if len(e.NewState) > 0 {
		detail = append(detail, section("new state"))
		detail = append(detail, kvLinesMasked(e.NewState, e.SensitiveValues, "  ", 40)...)
	}
	if len(e.DeposedState) > 0 {
		detail = append(detail, section("deposed state"))
		detail = append(detail, kvLinesMasked(e.DeposedState, e.SensitiveValues, "  ", 40)...)
	}
	if len(e.Outputs) > 0 {
		detail = append(detail, section("outputs"))
		flat, mask := outputsMask(e.Outputs)
		detail = append(detail, kvLinesMasked(flat, mask, "  ", 20)...)
	}
	if n := len(e.Ready); n > 0 {
		detail = append(detail, section(fmt.Sprintf("%d ready to effect", n)))
		detail = append(detail, effectLines(e.Ready, "  ", 12)...)
	}
	if e.Message != "" {
		detail = append(detail, muted(e.Message))
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// --- turf_declare_module / turf_replan / turf_plan_new ------------------------

type resourcePlanEntry struct {
	Address string `json:"address"`
	// DeposedKey marks a row that is not about the object living at Address: the
	// destroy of an object deposed under it by an interrupted create-before-destroy
	// replace. Two rows can therefore share one Address, told apart only by this
	// key — which is why the rendered header carries it.
	DeposedKey          string         `json:"deposed_key,omitempty"`
	Type                string         `json:"type"`
	Provider            string         `json:"provider"`
	Action              string         `json:"action"`
	CreateBeforeDestroy bool           `json:"create_before_destroy,omitempty"`
	Before              map[string]any `json:"before,omitempty"`
	After               map[string]any `json:"after,omitempty"`
	BeforeSensitive     any            `json:"before_sensitive,omitempty"`
	AfterSensitive      any            `json:"after_sensitive,omitempty"`
	RequiresReplace     []string       `json:"requires_replace,omitempty"`
	// Importing marks an adoption from an `import {}` block. See resourcePlanView.
	Importing *importingInfoView `json:"importing,omitempty"`
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

// movedRecordView is one state relocation a phase's `moved {}` blocks produced.
type movedRecordView struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type planSummaryView struct {
	Address          string              `json:"address"` // declare_module
	Path             string              `json:"path"`    // replan / plan_new (the bound configuration dir)
	PhaseID          string              `json:"phase_id"`
	Resources        []resourcePlanEntry `json:"resources"`
	TopologicalOrder []string            `json:"topological_order"`
	Deferred         []json.RawMessage   `json:"deferred,omitempty"`
	Outputs          json.RawMessage     `json:"outputs,omitempty"`
	// Reads classifies every declared data source the walk touched, the same
	// data_source_reads[] shape declare_datasource reports for the one declaration
	// it writes. A walk reads them all, so this is the whole configuration's worth.
	Reads []dataSourceReadEntry `json:"data_source_reads,omitempty"`
	// Moved are the state relocations this phase's `moved {}` blocks produced, as
	// concrete from→to pairs. plan_new / replan only — declare_module never sets it.
	// Present whenever the phase moved anything, including on a re-plan that finds
	// nothing left to move: the relocation is still part of what applying commits.
	Moved []movedRecordView `json:"moved,omitempty"`
	// Opens classifies every declared ephemeral resource the walk opened, in the
	// same vocabulary Reads uses. Reported per WALK, not per phase, because the
	// object's life is one walk: two consecutive plans of an unchanged
	// configuration list the same addresses as "opened", which is the lifecycle
	// rather than churn — hence the summary line reports only what did NOT open
	// (see ephemeralOpenIssueTally) while the detail block lists them all.
	Opens []ephemeralOpenEntry `json:"ephemeral_opens,omitempty"`
	// Providers reports each provider instance the walk configured in-band. This
	// is where the provider story lives now: workspace_open no longer echoes
	// resolved providers, because the walk — not the open — is what configures
	// them, in dependency order, from the configuration's own provider {} blocks.
	// A non-configured instance is usually the reason a plan deferred.
	Providers []providerStatusView `json:"providers,omitempty"`
	Warnings  []string             `json:"warnings,omitempty"`
}

// providerStatusView is one provider instance's fate during the walk. Status is
// "configured", "configured_with_unknowns" (the block reads values an upstream
// apply has not produced yet) or "deferred". UnknownPaths names those values and
// FeedingAddrs names the resources to apply to make them known — together they are
// the actionable half of a deferral. TeardownConfig, when set ("prior_state"), says
// the phase pinned a SEPARATE configuration for this instance's destroy-side work:
// the deletes run through the world the objects actually live in, not through the
// unknowns above.
type providerStatusView struct {
	Ref            string   `json:"ref"`
	Status         string   `json:"status"`
	UnknownPaths   []string `json:"unknown_paths,omitempty"`
	FeedingAddrs   []string `json:"feeding_addresses,omitempty"`
	LastError      string   `json:"last_error,omitempty"`
	TeardownConfig string   `json:"teardown_config,omitempty"`
}

// providerIssueTally counts the provider instances the walk could not fully
// configure. Like the read and open tallies it stays silent on the ordinary case —
// every instance configured — so the segment appears only when it explains
// something.
func providerIssueTally(providers []providerStatusView) string {
	var n int
	for _, pr := range providers {
		if pr.Status != "" && pr.Status != "configured" {
			n++
		}
	}
	if n == 0 {
		return ""
	}
	return bold(fmt.Sprintf("%d provider(s) unresolved", n), styles.Warning)
}

// providerStatusLines renders the expanded provider section: each instance's ref
// and status, then the two lists that say what to do about a non-configured one.
func providerStatusLines(providers []providerStatusView, indent string, max int) []string {
	var out []string
	for i, pr := range providers {
		if max > 0 && i == max {
			out = append(out, indent+muted(fmt.Sprintf("…(+%d more)", len(providers)-max)))
			break
		}
		c := styles.Success
		if pr.Status != "configured" {
			c = styles.Warning
		}
		row := indent + addr(pr.Ref)
		if pr.Status != "" {
			row += dot() + bold(pr.Status, c)
		}
		if pr.TeardownConfig != "" {
			row += dot() + muted("teardown "+pr.TeardownConfig)
		}
		out = append(out, row)
		if len(pr.UnknownPaths) > 0 {
			out = append(out, indent+"    "+muted("unknown ")+addr(strings.Join(pr.UnknownPaths, ", ")))
		}
		if len(pr.FeedingAddrs) > 0 {
			out = append(out, indent+"    "+muted("feeds from ")+addr(strings.Join(pr.FeedingAddrs, ", ")))
		}
		if pr.LastError != "" {
			out = append(out, indent+"    "+styles.ErrorStyle.Render(pr.LastError))
		}
	}
	return out
}

func renderDeclareModule(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	return renderPlanSummary(msg, s, ss, width, argString(msg, "address"))
}

// renderReplan renders the zero-arg re-projection of the bound configuration;
// the walk mode (destroy/refresh/vars) comes from the phase, so there is no
// request arg to lead with — the summary itself is the story.
func renderReplan(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	return renderPlanSummary(msg, s, ss, width, "")
}

func renderPlanSummary(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width int, target string) string {
	if running(msg) {
		return line(msg, s, targetBody(target), width)
	}
	var p planSummaryView
	if !parseContent(msg, &p) {
		return fallbackLine(msg, s, ss, width)
	}
	head := ""
	if target != "" {
		head = addr(target)
	}
	return planSummaryLine(msg, s, ss, width, head, p)
}

// planSummaryLine renders a parsed walk summary (shared by declare_module,
// replan, and plan_new): the action tally headline plus the per-resource diff
// expansion, the data sources the walk read, evaluated outputs, and warnings.
func planSummaryLine(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width int, head string, p planSummaryView) string {
	summary := planTally(p.Resources)
	if head != "" {
		summary = head + dot() + summary
	}
	if n := len(p.Deferred); n > 0 {
		summary += dot() + styles.WarningStyle.Render(fmt.Sprintf("%d deferred", n))
	}
	if issues := dataReadIssueTally(p.Reads); issues != "" {
		summary += dot() + issues
	}
	if issues := ephemeralOpenIssueTally(p.Opens); issues != "" {
		summary += dot() + issues
	}
	if issues := providerIssueTally(p.Providers); issues != "" {
		summary += dot() + issues
	}
	// An adopted row usually plans as a no-op (the remote object already matches),
	// and planTally omits the no-op bucket — so without its own segment a pure
	// adoption plan would headline as "no changes".
	if n := adoptCount(p.Resources); n > 0 {
		summary += dot() + bold(fmt.Sprintf("↧%d adopted", n), styles.Accent)
	}
	// Relocations are not resource changes, so they get their own segment rather
	// than a tally bucket.
	if n := len(p.Moved); n > 0 {
		summary += dot() + bold(fmt.Sprintf("⇄%d moved", n), styles.Accent)
	}
	if n := outputsCount(p.Outputs); n > 0 {
		summary += dot() + muted(fmt.Sprintf("%d output(s)", n))
	}

	// Expanded: each resource as a header + its full attribute diff, then the state
	// relocations, the data source reads, the evaluated outputs, and any warnings.
	// The resource diffs stay first — they are what the expansion is opened to read —
	// so the reads trail them even though the walk performs reads earlier.
	var detail []string
	const maxRows = 25
	for i, r := range p.Resources {
		if i == maxRows {
			detail = append(detail, muted(fmt.Sprintf("…(+%d more resources)", len(p.Resources)-maxRows)))
			break
		}
		_, c, glyph := planAction(r.Action, false, r.CreateBeforeDestroy)
		header := bold(glyph, c) + " " + addr(planRowAddr(r.Address, r.DeposedKey))
		if note := r.Importing.adoptNote(); note != "" {
			header += dot() + colored(note, styles.Accent)
		}
		if changed := changedKeys(r.Before, r.After); changed != "" {
			header += dot() + muted(changed)
		}
		detail = append(detail, header)
		detail = append(detail, attrDiff(r.Before, r.After, r.BeforeSensitive, r.AfterSensitive, "  ")...)
	}
	if len(p.Moved) > 0 {
		detail = append(detail, section("moved"))
		detail = append(detail, movedLines(p.Moved, "  ", 20)...)
	}
	if len(p.Providers) > 0 {
		detail = append(detail, section("providers"))
		detail = append(detail, providerStatusLines(p.Providers, "  ", 20)...)
	}
	if len(p.Reads) > 0 {
		detail = append(detail, section("data sources"))
		detail = append(detail, dataReadLines(p.Reads, "  ", 20)...)
	}
	if len(p.Opens) > 0 {
		detail = append(detail, section("ephemeral"))
		detail = append(detail, dataReadLines(p.Opens, "  ", 20)...)
	}
	if outs := outputsLines(p.Outputs); len(outs) > 0 {
		detail = append(detail, section("outputs"))
		detail = append(detail, outs...)
	}
	detail = append(detail, warningLines(p.Warnings)...)
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// --- turf_module_outputs ----------------------------------------------------
//
// module_outputs reads a module call's evaluated outputs from the open phase.
// outputs is polymorphic (object, for_each-keyed object, or count array), so we
// keep it raw and count/enumerate via the shared outputs helpers. missing_resources
// flags outputs left "__cty_unknown__" because a dependency isn't applied yet.

type moduleOutputsView struct {
	Address          string          `json:"address"`
	Outputs          json.RawMessage `json:"outputs"`
	MissingResources []string        `json:"missing_resources"`
}

func renderModuleOutputs(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, targetBody(argString(msg, "address")), width)
	}
	var r moduleOutputsView
	if !parseContent(msg, &r) {
		return fallbackLine(msg, s, ss, width)
	}
	target := r.Address
	if target == "" {
		target = argString(msg, "address")
	}
	summary := addr(target)
	if n := outputsCount(r.Outputs); n > 0 {
		summary += dot() + muted(fmt.Sprintf("%d output(s)", n))
	}
	if n := len(r.MissingResources); n > 0 {
		summary += dot() + styles.WarningStyle.Render(fmt.Sprintf("%d not yet applied", n))
	}

	detail := outputsLines(r.Outputs)
	if len(r.MissingResources) > 0 {
		detail = append(detail, section("missing resources"))
		detail = append(detail, listLines(r.MissingResources, "  ", 20)...)
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// --- turf_declare_outputs -----------------------------------------------------

type outputsPlanView struct {
	PhaseID string         `json:"phase_id"`
	Outputs map[string]any `json:"outputs"`
	Unknown []string       `json:"unknown"`
	Removed []string       `json:"removed"`
	// Errors are per-output evaluation failures, keyed by output name. An output
	// that failed to evaluate is absent from Outputs, so without this the line
	// would report it as simply not declared.
	Errors map[string]string `json:"errors,omitempty"`
}

func renderDeclareOutputs(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, "", width)
	}
	var p outputsPlanView
	if !parseContent(msg, &p) {
		return fallbackLine(msg, s, ss, width)
	}

	// Summary: N declared, plus how many resolve only after apply / are masked.
	summary := muted(fmt.Sprintf("%d declared", len(p.Outputs)))
	if len(p.Outputs) == 0 {
		summary = muted("none declared")
	}
	if n := len(p.Removed); n > 0 {
		summary += dot() + muted(fmt.Sprintf("%d removed", n))
	}
	if n := len(p.Unknown); n > 0 {
		summary += dot() + muted(fmt.Sprintf("%d known after apply", n))
	}
	if n := sensitiveCount(p.Outputs); n > 0 {
		summary += dot() + muted(fmt.Sprintf("%d sensitive", n))
	}
	if n := len(p.Errors); n > 0 {
		summary += dot() + bold(fmt.Sprintf("%d error(s)", n), styles.Error)
	}

	// Expanded: each output as name = value, with sentinels masked by fmtVal
	// ("__cty_unknown__" → "(known after apply)", "__cty_sensitive__" → "(sensitive)").
	detail := kvLines(p.Outputs, "  ", 30)
	if len(p.Errors) > 0 {
		detail = append(detail, section("errors"))
		names := make([]string, 0, len(p.Errors))
		for n := range p.Errors {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			detail = append(detail, "  "+addr(n)+dot()+styles.ErrorStyle.Render(p.Errors[n]))
		}
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// sensitiveCount counts outputs whose evaluated value is the masked-sensitive
// sentinel — the display-authoritative signal (the raw value never reaches us).
func sensitiveCount(outputs map[string]any) int {
	n := 0
	for _, v := range outputs {
		if s, ok := v.(string); ok && s == "__cty_sensitive__" {
			n++
		}
	}
	return n
}

// --- turf_outputs -----------------------------------------------------------
//
// outputs reads a workspace's root outputs (name → {value, sensitive}). The server
// masks sensitive values with the "__cty_sensitive__" sentinel unless the caller opted
// in to reveal them (show_sensitive), so the per-output `sensitive` flag is this tool's
// form of a sensitivity mask — the renderer redacts from it either way, and never
// prints a revealed output value.

type outputsView struct {
	WorkspaceAlias string                     `json:"workspace_alias"`
	Outputs        map[string]outputValueView `json:"outputs"`
}

// outputValueView is one evaluated root output, the {value, sensitive} shape the
// server returns from both the outputs tool and effect_apply's newly-resolved
// outputs. The sensitive flag IS the mask here — it never rides along as a
// parallel sensitive_values structure — so outputsMask carries it across rather
// than flattening it away, which would leak a revealed secret into the timeline.
type outputValueView struct {
	Value     any  `json:"value"`
	Sensitive bool `json:"sensitive"`
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

func renderOutputs(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, "", width)
	}
	var r outputsView
	if !parseContent(msg, &r) {
		return fallbackLine(msg, s, ss, width)
	}

	summary := muted("none declared")
	if len(r.Outputs) > 0 {
		summary = muted(fmt.Sprintf("%d declared", len(r.Outputs)))
	}
	if sens := sensitiveOutputs(r.Outputs); sens > 0 {
		summary += dot() + muted(fmt.Sprintf("%d sensitive", sens))
	}

	flat, mask := outputsMask(r.Outputs)
	detail := kvLinesMasked(flat, mask, "  ", 30)
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// --- turf_workspace_open / turf_workspace_close -----------------------------

// A workspace is now opened *against a configuration*, and that binding is the
// call's most consequential result: it fixes the backend, the providers, and what
// every later plan projects. The server no longer echoes resolved providers here
// (it configures them in-band during the walk — see planSummaryView.Providers), so
// what this line reports is where state landed and what the binding is.
type workspaceOpenView struct {
	WorkspaceAlias string `json:"workspace_alias"`
	BackendType    string `json:"backend_type"`
	WorkspaceName  string `json:"workspace_name"`
	ConfigAlias    string `json:"config_alias"`
	ConfigPath     string `json:"config_path"`
	// BackendConfig is the resolved backend body — the declared block with the
	// call's backend_config merged over it.
	BackendConfig map[string]any `json:"backend_config,omitempty"`
	// StatePath is where this workspace's state actually lives, resolved for the
	// workspace name (a local backend's `path` addresses `default` only). Empty
	// for backends that key their state some other way.
	StatePath string   `json:"state_path,omitempty"`
	Resources []string `json:"resources"`
	// Warnings carries informational notes — a backend type diverging from the
	// declared one, or state recording a provider the configuration no longer
	// declares (which a later destroy surfaces as a restore-or-declare error).
	Warnings []string `json:"warnings,omitempty"`
}

func renderWorkspaceOpen(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, "", width)
	}
	var w workspaceOpenView
	if !parseContent(msg, &w) {
		return fallbackLine(msg, s, ss, width)
	}

	name := w.WorkspaceName
	if name == "" {
		name = w.WorkspaceAlias
	}
	summary := addr(name)
	if w.BackendType != "" {
		summary += dot() + keyword(w.BackendType)
	}
	// The configuration this workspace is bound to — the binding fixes the
	// backend and the providers, so it belongs on the compact line.
	if w.ConfigPath != "" {
		summary = appendDot(summary, muted(w.ConfigPath))
	}

	var detail []string
	if w.StatePath != "" {
		detail = append(detail, section("state"), "  "+muted(w.StatePath))
	}
	if len(w.BackendConfig) > 0 {
		detail = append(detail, section("backend config"))
		detail = append(detail, kvLines(w.BackendConfig, "  ", 20)...)
	}
	if n := len(w.Resources); n > 0 {
		detail = append(detail, section(fmt.Sprintf("%d resource(s) in state", n)))
		detail = append(detail, listLines(w.Resources, "  ", 20)...)
	}
	detail = append(detail, warningLines(w.Warnings)...)
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

type workspaceCloseView struct {
	WorkspaceAlias     string `json:"workspace_alias"`
	Completed          bool   `json:"completed"`
	ResourcesCommitted int    `json:"resources_committed"`
	ProvidersClosed    int    `json:"providers_closed"`
	PhasesSuperseded   int    `json:"phases_superseded"`
}

func renderWorkspaceClose(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, "", width)
	}
	var w workspaceCloseView
	if !parseContent(msg, &w) {
		return fallbackLine(msg, s, ss, width)
	}

	name := w.WorkspaceAlias
	if name == "" {
		name = "default"
	}
	summary := addr(name) + dot() + bold("closed", styles.Success)
	if w.ResourcesCommitted > 0 {
		summary += dot() + muted(fmt.Sprintf("%d committed", w.ResourcesCommitted))
	}

	detail := []string{
		muted(fmt.Sprintf("resources committed: %d", w.ResourcesCommitted)),
		muted(fmt.Sprintf("providers closed:    %d", w.ProvidersClosed)),
	}
	if w.PhasesSuperseded > 0 {
		detail = append(detail, muted(fmt.Sprintf("phases superseded:   %d", w.PhasesSuperseded)))
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
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

type workspaceShowView struct {
	Workspaces []struct {
		WorkspaceAlias     string `json:"workspace_alias"`
		BackendType        string `json:"backend_type"`
		WorkspaceName      string `json:"workspace_name"`
		ResourceCount      int    `json:"resource_count"`
		UncommittedChanges int    `json:"uncommitted_changes"`
		ActivePhaseID      string `json:"active_phase_id,omitempty"`
		ActivePhaseStatus  string `json:"active_phase_status,omitempty"`
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
		summary = addr(workspaceName(w.WorkspaceAlias, w.WorkspaceName)) +
			dot() + muted(fmt.Sprintf("%d resource(s)", w.ResourceCount))
		if w.UncommittedChanges > 0 {
			summary += dot() + styles.WarningStyle.Render(fmt.Sprintf("%d uncommitted", w.UncommittedChanges))
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
		row += dot() + muted(fmt.Sprintf("%d resource(s)", w.ResourceCount))
		if w.UncommittedChanges > 0 {
			row += dot() + styles.WarningStyle.Render(fmt.Sprintf("%d uncommitted", w.UncommittedChanges))
		}
		if w.ActivePhaseID != "" {
			ph := "phase " + w.ActivePhaseID
			if w.ActivePhaseStatus != "" {
				ph += " (" + w.ActivePhaseStatus + ")"
			}
			row += dot() + muted(ph)
		}
		detail = append(detail, row)
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// workspaceName prefers the workspace alias, falling back to the backend name.
func workspaceName(alias, name string) string {
	if alias != "" {
		return alias
	}
	return name
}

// --- turf_plan_new / turf_plan_approve --------------------------------------

// renderPlanNew shows the opened Draft plus the initial full-configuration
// walk plan_new now performs (the result carries the same walk summary as
// replan: resources, deferred, outputs). On a fresh empty configuration the
// tally is "no changes"; on a reopen it shows NoOps/drift/orphans immediately.
func renderPlanNew(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, "", width)
	}
	var p planSummaryView
	if !parseContent(msg, &p) {
		return fallbackLine(msg, s, ss, width)
	}
	opened := "opened"
	if p.Path != "" {
		// The bound configuration dir, as the caller passed it (relative under
		// the CLI). Shows which configuration this phase is a projection of.
		opened = "opened " + p.Path
	}
	head := addr(p.PhaseID) + dot() + muted(opened)
	return planSummaryLine(msg, s, ss, width, head, p)
}

type planApproveView struct {
	PhaseID     string   `json:"phase_id"`
	EffectCount int      `json:"effect_count"`
	Ready       []string `json:"ready"`
}

func renderPlanApprove(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, "", width)
	}
	var p planApproveView
	if !parseContent(msg, &p) {
		return fallbackLine(msg, s, ss, width)
	}
	summary := bold(p.PhaseID, styles.Success)
	if p.EffectCount > 0 {
		summary += dot() + muted(fmt.Sprintf("%d effect(s)", p.EffectCount))
	} else {
		summary += dot() + muted("no changes")
	}
	var detail []string
	if n := len(p.Ready); n > 0 {
		detail = append(detail, section(fmt.Sprintf("%d ready to effect", n)))
		detail = append(detail, effectLines(p.Ready, "  ", 20)...)
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// --- turf_state_list --------------------------------------------------------

type stateListView struct {
	WorkspaceAlias string `json:"workspace_alias"`
	Resources      []struct {
		Address string `json:"address"`
		Type    string `json:"type"`
		Mode    string `json:"mode"`
		// Tainted marks an object a previous apply left half-created; the next
		// plan replaces it. Invisible in a plain address listing otherwise.
		Tainted bool `json:"tainted,omitempty"`
		// Deposed are objects stranded under this address by an interrupted
		// create-before-destroy replace. They are still real infrastructure and
		// still cost money, so a state listing has to admit they exist.
		Deposed []struct {
			Key string `json:"key"`
		} `json:"deposed,omitempty"`
	} `json:"resources"`
}

func renderStateList(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, "", width)
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

	var detail []string
	const maxRows = 40
	for i, r := range st.Resources {
		if i == maxRows {
			detail = append(detail, muted(fmt.Sprintf("…(+%d more)", len(st.Resources)-maxRows)))
			break
		}
		row := addr(r.Address)
		meta := strings.TrimSpace(strings.Join([]string{r.Type, r.Mode}, " · "))
		if meta != "" {
			row += dot() + muted(meta)
		}
		if r.Tainted {
			row += dot() + styles.WarningStyle.Render("tainted")
		}
		if n := len(r.Deposed); n > 0 {
			row += dot() + styles.WarningStyle.Render(fmt.Sprintf("%d deposed", n))
		}
		detail = append(detail, row)
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// --- turf_datasource_read ---------------------------------------------------
//
// datasource_read is the one-off lookup: it evaluates a data source and returns its
// read state (attrs), writing nothing to state and nothing to the configuration. It
// carries no replan hints — those moved to declare_datasource, the verb that puts a
// declaration in the configuration for other changes to reference.

type datasourceReadView struct {
	ResourceAddr string         `json:"resource_addr"`
	State        map[string]any `json:"state"`
	// SensitiveValues masks State, in OpenTofu's sensitive_values shape.
	SensitiveValues any `json:"sensitive_values,omitempty"`
}

func renderDatasourceRead(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, targetBody(argString(msg, "resource_addr")), width)
	}
	var r datasourceReadView
	if !parseContent(msg, &r) {
		return fallbackLine(msg, s, ss, width)
	}
	target := r.ResourceAddr
	if target == "" {
		target = argString(msg, "resource_addr")
	}
	summary := addr(target)
	if n := len(r.State); n > 0 {
		summary += dot() + muted(fmt.Sprintf("%d attr(s)", n))
	}
	return lineWithDetail(msg, s, ss, summary, kvLinesMasked(r.State, r.SensitiveValues, "  ", 30), width)
}

// --- turf_declare_datasource --------------------------------------------------
//
// declare_datasource is the declarative counterpart of datasource_read: it writes a
// `data` block into the bound configuration *and* reads it in the same call, so
// ${data.<type>.<name>.<attr>} resolves in the very next declare_resource with no
// replan in between.
//
// The result deliberately carries no value — the read is reported as a
// *classification* in data_source_reads[], and datasource_read is the verb that
// hands you a value. So this renderer shows the declaration outcome (declared /
// removed) plus the per-instance read verdict, not attributes. count/for_each expand
// the declaration into keyed instances, one data_source_reads[] entry each.
//
// warning is the third arm: the declaration stands, but the configuration as a whole
// would not walk, so nothing was read. That is not a failure — the tool succeeds —
// which is exactly why the summary has to say "not read" rather than a bare
// "declared" that reads as fully done.

type declareDatasourceView struct {
	ResourceAddr string                `json:"resource_addr"`
	Declared     bool                  `json:"declared"`
	Removed      bool                  `json:"removed"`
	Replan       []string              `json:"replan"`
	Reads        []dataSourceReadEntry `json:"data_source_reads,omitempty"`
	Warning      string                `json:"warning,omitempty"`
}

// dataSourceReadEntry is one expanded instance's read verdict. Action is "read",
// "deferred" or "error"; Reason narrows a deferral ("config_unknown" — the query
// still carries unknowns; "dependency_pending" — a depends_on target has a change
// this phase has not applied), and DependsOn names the addresses to apply to clear
// it. The server keeps Value off the wire, so there is no attribute to render.
type dataSourceReadEntry struct {
	Address   string   `json:"address"`
	Action    string   `json:"action"`
	Reason    string   `json:"reason,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// ephemeralOpenEntry is one expanded ephemeral instance's open verdict. The server
// emits it field-for-field identically to a data-source read — address, action,
// reason, depends_on, error — because it is the same question asked of a different
// block, so it is the same type under a name that reads correctly at its own call
// sites. Action is "opened", "deferred" or "error"; the VALUE is never on the wire,
// and no tool will hand it over: an ephemeral value exists precisely so that it is
// never written anywhere it can be read back, and the timeline is scrollback.
type ephemeralOpenEntry = dataSourceReadEntry

func renderDeclareDatasource(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, addr(argString(msg, "resource_addr")), width)
	}
	var d declareDatasourceView
	if !parseContent(msg, &d) {
		return fallbackLine(msg, s, ss, width)
	}
	target := d.ResourceAddr
	if target == "" {
		target = argString(msg, "resource_addr")
	}
	summary := addr(target)
	switch {
	case d.Removed:
		summary += dot() + bold("removed", styles.Success)
	case d.Declared:
		summary += dot() + bold("declared", styles.Success)
	}
	if d.Warning != "" {
		summary += dot() + bold("not read", styles.Warning)
	} else if tally := dataReadTally(d.Reads); tally != "" {
		summary += dot() + tally
	}
	if n := len(d.Replan); n > 0 {
		summary += dot() + styles.WarningStyle.Render(fmt.Sprintf("%d replan", n))
	}

	var detail []string
	if d.Warning != "" {
		detail = append(detail, styles.WarningStyle.Render("⚠ "+d.Warning))
	}
	if len(d.Reads) > 0 {
		detail = append(detail, section("reads"))
		detail = append(detail, dataReadLines(d.Reads, "  ", 20)...)
	}
	if len(d.Replan) > 0 {
		detail = append(detail, section("replan"))
		detail = append(detail, listLines(d.Replan, "  ", 20)...)
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// dataReadTally summarizes the read verdicts for the one-line view, and
// ephemeralOpenTally does the same for a declaration's ephemeral opens. A single
// instance renders as the bare colored action word ("read"/"opened", "deferred",
// "error"); several render as per-action counts ("2 read · 1 deferred") in a fixed
// order so the eye lands on the same bucket every time. Empty for no entries — a
// remove, or a targeted walk that never reached the address, has nothing to report
// and should not claim otherwise.
func dataReadTally(reads []dataSourceReadEntry) string {
	return outcomeTally(reads, "read")
}

func ephemeralOpenTally(opens []ephemeralOpenEntry) string {
	return outcomeTally(opens, "opened")
}

func outcomeTally(entries []dataSourceReadEntry, success string) string {
	if len(entries) == 0 {
		return ""
	}
	if len(entries) == 1 {
		label, c, _ := planAction(entries[0].Action, false, false)
		return bold(label, c)
	}
	return strings.Join(actionTally(actionCounts(entries, ""), []string{success, "deferred", "error"}, "%d %s"), dot())
}

// dataReadIssueTally is the plan-summary counterpart of dataReadTally, and
// ephemeralOpenIssueTally the counterpart of ephemeralOpenTally: each counts only
// the verdicts worth interrupting the action tally for.
//
// The difference from the full tallies is the caller's scope. declare_datasource
// reports one declaration's instances, where "read" is the outcome being asked
// about; a walk reads *every* declared data source, so a "read" count there would
// almost always just say "all of them" — the signal is what did not resolve. The
// same holds, more sharply, for ephemeral opens: every walk re-opens every declared
// ephemeral resource, so two consecutive plans of an unchanged configuration list
// the same addresses as "opened". That is the lifecycle, not churn, and tallying it
// on the summary line would read as a change that did not happen.
//
// The "data"/"ephemeral" noun keeps each apart from the resource `N deferred` count
// beside it, which counts a different thing entirely. Empty when everything
// resolved, which is the ordinary case and should cost the line nothing.
func dataReadIssueTally(reads []dataSourceReadEntry) string {
	return issueTally(reads, "read", "data")
}

func ephemeralOpenIssueTally(opens []ephemeralOpenEntry) string {
	return issueTally(opens, "opened", "ephemeral")
}

func issueTally(entries []dataSourceReadEntry, success, noun string) string {
	return strings.Join(actionTally(actionCounts(entries, success), []string{"deferred", "error"}, "%d "+noun+" %s"), dot())
}

// actionCounts buckets entries by their planAction label, dropping the caller's
// success label when it passes one (that is what makes an issue tally an issue
// tally). Pass "" to count every bucket.
func actionCounts(entries []dataSourceReadEntry, skip string) map[string]int {
	counts := map[string]int{}
	for _, e := range entries {
		if label, _, _ := planAction(e.Action, false, false); label != skip {
			counts[label]++
		}
	}
	return counts
}

// actionTally renders the buckets named by order, in that order, skipping empties —
// then hands whatever order did not claim to leftoverActionTally rather than
// dropping it. Consumes counts.
func actionTally(counts map[string]int, order []string, format string) []string {
	var parts []string
	for _, a := range order {
		if n := counts[a]; n > 0 {
			_, c, _ := planAction(a, false, false)
			parts = append(parts, bold(fmt.Sprintf(format, n, a), c))
			delete(counts, a)
		}
	}
	return append(parts, leftoverActionTally(counts, format)...)
}

// leftoverActionTally renders the buckets a caller's fixed action order did not
// claim, in sorted order so the line does not shuffle between renders of the same
// result. Normally empty: the server emits only read/deferred/error, and both
// callers enumerate those explicitly — this is what keeps a fourth action word
// visible instead of silently absent.
func leftoverActionTally(counts map[string]int, format string) []string {
	rest := make([]string, 0, len(counts))
	for a := range counts {
		rest = append(rest, a)
	}
	sort.Strings(rest)
	out := make([]string, 0, len(rest))
	for _, a := range rest {
		_, c, _ := planAction(a, false, false)
		out = append(out, bold(fmt.Sprintf(format, counts[a], a), c))
	}
	return out
}

// dataReadLines renders each instance's verdict as "<glyph> <address> · <action>",
// following formatEffectID's shape, with the deferral reason / provider error text
// indented beneath the entry it belongs to.
func dataReadLines(reads []dataSourceReadEntry, indent string, max int) []string {
	var out []string
	for i, r := range reads {
		if max > 0 && i == max {
			out = append(out, indent+muted(fmt.Sprintf("…(+%d more)", len(reads)-max)))
			break
		}
		label, c, glyph := planAction(r.Action, false, false)
		body := indent + bold(glyph, c) + " " + addr(r.Address) + dot() + bold(label, c)
		if r.Reason != "" {
			body += dot() + muted(r.Reason)
		}
		out = append(out, body)
		if len(r.DependsOn) > 0 {
			out = append(out, indent+"    "+muted("waiting on ")+addr(strings.Join(r.DependsOn, ", ")))
		}
		if r.Error != "" {
			out = append(out, indent+"    "+styles.ErrorStyle.Render(r.Error))
		}
	}
	return out
}

// --- turf_provider_describe -------------------------------------------------
//
// provider_describe returns a TypeDescription (turf/provider/schema_converter.go):
// the resource/data-source schema as {type, description, properties{name:{usage,
// type, sensitive, deprecated, description,…}}, required}. The default renderer
// dumps the whole schema; we summarize the type and, on expand, list its
// attributes with type + usage flags.

type providerDescribeView struct {
	Type        string         `json:"type"`
	Description string         `json:"description"`
	Properties  map[string]any `json:"properties"`
	Required    []string       `json:"required"`
}

func renderProviderDescribe(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	target := argString(msg, "resource_type")
	if target == "" {
		target = argString(msg, "datasource_type")
	}
	if running(msg) {
		return line(msg, s, targetBody(target), width)
	}
	var d providerDescribeView
	if !parseContent(msg, &d) {
		return fallbackLine(msg, s, ss, width)
	}

	typ := d.Type
	if typ == "" {
		typ = target
	}
	summary := addr(typ)
	if n := len(d.Properties); n > 0 {
		summary += dot() + muted(fmt.Sprintf("%d attr(s)", n))
	}
	if prov := argString(msg, "provider"); prov != "" {
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

// --- turf_provider_load -----------------------------------------------------
//
// provider_load downloads a provider plugin and resolves its version constraint,
// returning {name, source, resolved_version}. The default renderer dumps that
// JSON; we collapse it to a one-liner (name · version · source) and, on expand,
// surface the requested version constraint (an arg, not in the result) when it
// differs from what resolved.

type providerLoadView struct {
	Name            string   `json:"name"`
	Source          string   `json:"source"`
	ResolvedVersion string   `json:"resolved_version"`
	Warnings        []string `json:"warnings,omitempty"`
}

func renderProviderLoad(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		target := argString(msg, "source")
		if target == "" {
			target = argString(msg, "name")
		}
		return line(msg, s, targetBody(target), width)
	}
	var p providerLoadView
	if !parseContent(msg, &p) {
		return fallbackLine(msg, s, ss, width)
	}

	summary := addr(p.Name)
	if p.ResolvedVersion != "" {
		summary += dot() + muted("v"+p.ResolvedVersion)
	}
	if p.Source != "" {
		summary += dot() + muted(p.Source)
	}

	// Detail's only value-add over the summary is the requested constraint, which
	// isn't in the result. When absent (or equal), detail is empty and
	// lineWithDetail degrades to the pure one-liner.
	var detail []string
	if c := argString(msg, "version"); c != "" && c != p.ResolvedVersion {
		detail = append(detail, muted("requested  ")+c)
	}
	detail = append(detail, warningLines(p.Warnings)...)
	return lineWithDetail(msg, s, ss, summary, detail, width)
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

// --- turf_plan_cancel -------------------------------------------------------

type planCancelView struct {
	PhaseID string `json:"phase_id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func renderPlanCancel(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, "", width)
	}
	var p planCancelView
	if !parseContent(msg, &p) {
		return fallbackLine(msg, s, ss, width)
	}
	summary := addr(p.PhaseID)
	if p.Status != "" {
		summary += dot() + muted(p.Status)
	}
	var detail []string
	if p.Message != "" {
		detail = append(detail, muted(p.Message))
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// --- turf_plan_export -------------------------------------------------------
//
// The result is the raw `tofu show -json` plan document (fed to an external policy
// engine as input.tfplan). We don't dump it; we summarize with the resource-change
// count, falling back to a byte tally when the shape is unexpected.

type planExportView struct {
	ResourceChanges []struct {
		Address string `json:"address"`
	} `json:"resource_changes"`
}

func renderPlanExport(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, "", width)
	}
	var p planExportView
	if !parseContent(msg, &p) {
		// A successful export is always valid JSON, so an unparseable body is an
		// error result — let fallbackLine surface the framework error text.
		return fallbackLine(msg, s, ss, width)
	}
	summary := muted("exported plan JSON") + dot() + muted(fmt.Sprintf("%d resource changes", len(p.ResourceChanges)))
	return lineWithDetail(msg, s, ss, summary, nil, width)
}

// --- turf_config_init / turf_module_init ------------------------------------
//
// Both introspect a config directory / module source: they resolve required
// providers, variables, and outputs (config_init also reports the backend; the
// server's full result carries a README we don't surface). We show the target +
// a "N providers · N variables · N outputs" tally, and on expand list each.

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

type configInitView struct {
	Path    string `json:"path"` // the configuration dir, as the caller passed it (often relative)
	Backend *struct {
		Type string `json:"type"`
		// Defaulted marks the SYNTHESIZED local default — the entry describing
		// something the directory does not actually contain. The server states it
		// positively and omits the false, so ABSENCE means "declared": reading a
		// missing flag as "not defaulted" is the correct polarity here.
		Defaulted bool `json:"defaulted,omitempty"`
	} `json:"backend"`
	Workspace struct {
		Name string `json:"name"`
	} `json:"workspace"`
	RequiredProviders map[string]initProviderView `json:"required_providers"`
	Variables         []initVariableView          `json:"variables"`
	Outputs           []initOutputView            `json:"outputs"`
	// Dialect is the directory's dialect — "plot" (turf-authored; the declare
	// tools operate here) or "tofu" (a plain root module, read-only to declares).
	// It decides which half of the workflow the user is in, so it leads the line.
	Dialect string `json:"dialect"`
	// Scratch marks a turf-allocated temporary directory (no path was given), so
	// nothing here outlives the session.
	Scratch bool `json:"scratch,omitempty"`
	// Drift is a cheap configuration↔state set comparison per bound workspace,
	// present only on a re-init while one is open. It is not attribute-level drift
	// (that needs a plan walk) — it is what the next plan will walk as orphan
	// destroys and as creates.
	Drift []configDriftView `json:"drift,omitempty"`
}

type configDriftView struct {
	WorkspaceAlias     string   `json:"workspace_alias"`
	InStateNotDeclared []string `json:"in_state_not_declared,omitempty"`
	DeclaredNotInState []string `json:"declared_not_in_state,omitempty"`
}

// driftCounts totals both directions across every bound workspace.
func driftCounts(drift []configDriftView) (orphans, pending int) {
	for _, d := range drift {
		orphans += len(d.InStateNotDeclared)
		pending += len(d.DeclaredNotInState)
	}
	return orphans, pending
}

// driftLines renders the drift section in the plan's own glyph idiom — a state
// resource the configuration no longer declares is a `-` the next plan walks, one
// declared but absent from state is a `+`.
func driftLines(drift []configDriftView, indent string) []string {
	var out []string
	for _, d := range drift {
		if len(d.InStateNotDeclared) == 0 && len(d.DeclaredNotInState) == 0 {
			continue
		}
		if d.WorkspaceAlias != "" {
			out = append(out, indent+addr(d.WorkspaceAlias))
		}
		for _, a := range d.DeclaredNotInState {
			out = append(out, indent+"  "+bold("+", styles.Success)+" "+addr(a))
		}
		for _, a := range d.InStateNotDeclared {
			out = append(out, indent+"  "+bold("-", styles.Error)+" "+addr(a))
		}
	}
	return out
}

func renderConfigInit(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, targetBody(argString(msg, "path")), width)
	}
	var c configInitView
	if !parseContent(msg, &c) {
		return fallbackLine(msg, s, ss, width)
	}
	target := c.Workspace.Name
	if target == "" {
		target = argString(msg, "path")
	}
	summary := targetBody(target)
	// Surface the configuration directory — the workflow's durable identity —
	// as the caller passed it (relative under the CLI, where the cwd is the
	// config dir). Skip when it's already the target (an empty workspace name).
	if c.Path != "" && c.Path != target {
		summary = appendDot(summary, muted(c.Path))
	}
	// The dialect frames everything after it — a plot is turf-authored and the
	// declare tools operate on it; a tofu configuration is read-only to them — so
	// it reads ahead of the backend rather than trailing it.
	if c.Dialect != "" {
		summary = appendDot(summary, keyword(c.Dialect))
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
	// Drift is what the NEXT plan will do about this directory, so it earns a
	// segment of its own rather than sitting only in the expansion.
	if orphans, pending := driftCounts(c.Drift); orphans > 0 || pending > 0 {
		var parts []string
		if pending > 0 {
			parts = append(parts, bold(fmt.Sprintf("+%d", pending), styles.Success))
		}
		if orphans > 0 {
			parts = append(parts, bold(fmt.Sprintf("-%d", orphans), styles.Error))
		}
		summary = appendDot(summary, strings.Join(parts, " ")+muted(" drift"))
	}
	detail := initDetail(c.RequiredProviders, c.Variables, c.Outputs)
	if lines := driftLines(c.Drift, "  "); len(lines) > 0 {
		detail = append(detail, section("drift"))
		detail = append(detail, lines...)
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

type moduleInitView struct {
	Source            string                      `json:"source"`
	Version           string                      `json:"version,omitempty"`
	RequiredProviders map[string]initProviderView `json:"required_providers"`
	Variables         []initVariableView          `json:"variables"`
	Outputs           []initOutputView            `json:"outputs"`
}

func renderModuleInit(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, targetBody(argString(msg, "source")), width)
	}
	var m moduleInitView
	if !parseContent(msg, &m) {
		return fallbackLine(msg, s, ss, width)
	}
	target := m.Source
	if target == "" {
		target = argString(msg, "source")
	}
	summary := targetBody(target)
	if m.Version != "" {
		summary = appendDot(summary, muted("v"+m.Version))
	}
	if parts := initCountParts(len(m.RequiredProviders), len(m.Variables), len(m.Outputs)); parts != "" {
		summary = appendDot(summary, muted(parts))
	}
	detail := initDetail(m.RequiredProviders, m.Variables, m.Outputs)
	return lineWithDetail(msg, s, ss, summary, detail, width)
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

// initDetail renders the expanded provider/variable/output sections shared by
// config_init and module_init.
func initDetail(providers map[string]initProviderView, vars []initVariableView, outs []initOutputView) []string {
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

// --- turf_declare_action ------------------------------------------------------
//
// declare_action both declares (writes the action block into the bound
// configuration) and, with remove=true, un-declares; the result reports which
// via the declared/removed booleans.

type declareActionView struct {
	ActionAddr string `json:"action_addr"`
	ActionType string `json:"action_type"`
	Name       string `json:"name"`
	Declared   bool   `json:"declared"`
	Removed    bool   `json:"removed"`
}

func renderDeclareAction(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, targetBody(argString(msg, "action_type")), width)
	}
	var a declareActionView
	if !parseContent(msg, &a) {
		return fallbackLine(msg, s, ss, width)
	}
	target := a.ActionAddr
	if target == "" {
		target = a.ActionType
	}
	summary := addr(target)
	switch {
	case a.Removed:
		summary += dot() + bold("removed", styles.Success)
	case a.Declared:
		summary += dot() + bold("declared", styles.Success)
	}
	return line(msg, s, summary, width)
}

// --- turf_declare_ephemeral ---------------------------------------------------
//
// An ephemeral resource is a value a provider produces for the duration of one
// operation and that is NEVER written to state, a plan, or the configuration — a
// Vault lease, a decrypted file, a short-lived token — routed into a write-only
// attribute or a provider block. The result reports the open's CLASSIFICATION and
// nothing else: there is no imperative read counterpart, and the value never
// reaches this renderer, so there is nothing here to mask.

type declareEphemeralView struct {
	ResourceAddr string               `json:"resource_addr"`
	ResourceType string               `json:"resource_type"`
	Declared     bool                 `json:"declared"`
	Removed      bool                 `json:"removed"`
	Replan       []string             `json:"replan,omitempty"`
	Opens        []ephemeralOpenEntry `json:"ephemeral_opens,omitempty"`
	// Warnings are close-time diagnostics — the walk opened the object, evaluated
	// against it, and closed it again, and a failed close means something is still
	// held at the provider (a lease not released, a token still valid). Never
	// fatal, and never a reason to read the declaration as not having taken.
	Warnings []string `json:"warnings,omitempty"`
}

func renderDeclareEphemeral(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, addr(argString(msg, "resource_addr")), width)
	}
	var e declareEphemeralView
	if !parseContent(msg, &e) {
		return fallbackLine(msg, s, ss, width)
	}
	target := e.ResourceAddr
	if target == "" {
		target = argString(msg, "resource_addr")
	}
	summary := addr(target)
	switch {
	case e.Removed:
		summary += dot() + bold("removed", styles.Success)
	case e.Declared:
		summary += dot() + bold("declared", styles.Success)
	}
	if tally := ephemeralOpenTally(e.Opens); tally != "" {
		summary += dot() + tally
	}
	if n := len(e.Replan); n > 0 {
		summary += dot() + styles.WarningStyle.Render(fmt.Sprintf("%d replan", n))
	}

	var detail []string
	if len(e.Opens) > 0 {
		detail = append(detail, section("opens"))
		detail = append(detail, dataReadLines(e.Opens, "  ", 20)...)
	}
	if len(e.Replan) > 0 {
		detail = append(detail, section("replan"))
		detail = append(detail, listLines(e.Replan, "  ", 20)...)
	}
	detail = append(detail, warningLines(e.Warnings)...)
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// --- turf_declare_var ---------------------------------------------------------

type declareVarView struct {
	Address  string `json:"address"`
	Name     string `json:"name"`
	Declared bool   `json:"declared"`
	Removed  bool   `json:"removed"`
	Message  string `json:"message"`
}

func renderDeclareVar(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, targetBody(argString(msg, "name")), width)
	}
	var v declareVarView
	if !parseContent(msg, &v) {
		return fallbackLine(msg, s, ss, width)
	}
	target := v.Address
	if target == "" {
		target = v.Name
	}
	summary := addr(target)
	switch {
	case v.Removed:
		summary += dot() + bold("removed", styles.Success)
	case v.Declared:
		summary += dot() + bold("declared", styles.Success)
	}
	// The result echoes no declaration detail, so the one fact worth carrying —
	// that this variable may never be written down — comes from the request.
	if argBool(msg, "ephemeral") {
		summary += dot() + styles.WarningStyle.Render("ephemeral")
	}
	var detail []string
	if v.Message != "" {
		detail = append(detail, muted(v.Message))
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// --- turf_declare_backend -----------------------------------------------------

type declareBackendView struct {
	Type   string         `json:"type"`
	File   string         `json:"file"`
	Config map[string]any `json:"config"`
}

func renderDeclareBackend(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, targetBody(argString(msg, "type")), width)
	}
	var b declareBackendView
	if !parseContent(msg, &b) {
		return fallbackLine(msg, s, ss, width)
	}
	summary := addr(b.Type) + dot() + bold("declared", styles.Success)
	if b.File != "" {
		summary += dot() + muted(b.File)
	}
	detail := kvLines(b.Config, "  ", 20)
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// --- turf_declare_provider ------------------------------------------------------

type declareProviderView struct {
	Address         string   `json:"address"`
	Name            string   `json:"name"`
	RequirementFile string   `json:"requirement_file"`
	BlockFile       string   `json:"block_file"`
	Declared        bool     `json:"declared"`
	Removed         []string `json:"removed"`
	Message         string   `json:"message"`
}

func renderDeclareProvider(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, targetBody(argString(msg, "name")), width)
	}
	var p declareProviderView
	if !parseContent(msg, &p) {
		return fallbackLine(msg, s, ss, width)
	}
	target := p.Address
	if target == "" {
		target = p.Name
	}
	summary := addr(target)
	switch {
	case len(p.Removed) > 0:
		summary += dot() + bold("removed", styles.Success)
	case p.Declared:
		summary += dot() + bold("declared", styles.Success)
	}
	var detail []string
	if p.RequirementFile != "" {
		detail = append(detail, muted("requirement: ")+p.RequirementFile)
	}
	if p.BlockFile != "" {
		detail = append(detail, muted("block: ")+p.BlockFile)
	}
	for _, r := range p.Removed {
		detail = append(detail, muted("removed: ")+r)
	}
	if p.Message != "" {
		detail = append(detail, muted(p.Message))
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// --- turf_config_show ------------------------------------------------------------

type configShowView struct {
	// Dialect is the configuration directory's dialect: "plot" (turf-authored)
	// or "tofu" (a plain root module).
	Dialect string `json:"dialect"`
	Path    string `json:"path"`
	Entries []struct {
		Address string `json:"address"`
		// Kind is the entry's block type (resource/data/module/variable/output/
		// provider/backend/action).
		Kind   string `json:"kind"`
		File   string `json:"file"`
		Intent string `json:"intent,omitempty"`
		Note   string `json:"note,omitempty"`
	} `json:"entries"`
}

func renderConfigShow(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, targetBody(argString(msg, "address")), width)
	}
	var c configShowView
	if !parseContent(msg, &c) {
		return fallbackLine(msg, s, ss, width)
	}
	summary := muted(fmt.Sprintf("%s · %d declared address(es)", c.Dialect, len(c.Entries)))
	if len(c.Entries) == 1 {
		summary = addr(c.Entries[0].Address) + dot() + muted(c.Entries[0].Kind)
	}
	var detail []string
	const maxRows = 30
	for i, e := range c.Entries {
		if i == maxRows {
			detail = append(detail, muted(fmt.Sprintf("…(+%d more)", len(c.Entries)-maxRows)))
			break
		}
		detail = append(detail, addr(e.Address)+dot()+muted(e.File)+dot()+muted(e.Kind))
		if e.Intent != "" {
			detail = append(detail, muted("  "+e.Intent))
		}
		if e.Note != "" {
			detail = append(detail, muted("  "+e.Note))
		}
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// --- turf_config_promote ---------------------------------------------------
//
// config_promote graduates a plot into a plain tofu configuration (a
// strip-fold-rename), reporting the resulting dialect, the .tf files written,
// and the units removed.

type configPromoteView struct {
	Dialect string   `json:"dialect"`
	Path    string   `json:"path"`
	Files   []string `json:"files"`
	Removed []string `json:"removed"`
	Message string   `json:"message"`
}

func renderConfigPromote(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, "promoting plot", width)
	}
	var c configPromoteView
	if !parseContent(msg, &c) {
		return fallbackLine(msg, s, ss, width)
	}
	summary := muted(fmt.Sprintf("promoted to %s · %d file(s), %d removed", c.Dialect, len(c.Files), len(c.Removed)))
	var detail []string
	const maxRows = 30
	for i, f := range c.Files {
		if i == maxRows {
			detail = append(detail, muted(fmt.Sprintf("…(+%d more)", len(c.Files)-maxRows)))
			break
		}
		detail = append(detail, muted(f))
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// --- turf_action_invoke -----------------------------------------------------
//
// action_invoke fires (or, with dry_run, just resolves) a provider action. The
// result reports the terminal status plus a progress log; on dry_run it returns
// the resolved config instead of running the action.

type actionInvokeView struct {
	ActionType string         `json:"action_type"`
	Provider   string         `json:"provider"`
	Status     string         `json:"status"`
	Progress   []string       `json:"progress,omitempty"`
	Config     map[string]any `json:"config,omitempty"`
}

func renderActionInvoke(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, targetBody(argString(msg, "action_type")), width)
	}
	var a actionInvokeView
	if !parseContent(msg, &a) {
		return fallbackLine(msg, s, ss, width)
	}
	typ := a.ActionType
	if typ == "" {
		typ = argString(msg, "action_type")
	}
	status := a.Status
	if status == "" {
		status = "completed"
	}
	summary := addr(typ) + dot() + bold(status, applyStateColor(a.Status))
	if a.Provider != "" {
		summary += dot() + providerName(a.Provider)
	}
	var detail []string
	if len(a.Progress) > 0 {
		detail = append(detail, section("progress"))
		detail = append(detail, listLines(a.Progress, "  ", 20)...)
	}
	if len(a.Config) > 0 {
		detail = append(detail, section("resolved config"))
		detail = append(detail, kvLines(a.Config, "  ", 30)...)
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// --- turf_effect_cancel -----------------------------------------------------
//
// effect_cancel withdraws a scheduled effect; downstream effects that depended on
// it cascade (are re-derived), and a fresh ready set is returned. Both are effect
// IDs, formatted by effectLines.

type effectCancelView struct {
	EffectID string   `json:"effect_id"`
	State    string   `json:"state"`
	Cascaded []string `json:"cascaded,omitempty"`
	Ready    []string `json:"ready"`
	Message  string   `json:"message"`
}

func renderEffectCancel(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, targetBody(argString(msg, "effect_id")), width)
	}
	var e effectCancelView
	if !parseContent(msg, &e) {
		return fallbackLine(msg, s, ss, width)
	}
	target := e.EffectID
	if target == "" {
		target = argString(msg, "effect_id")
	}
	state := e.State
	if state == "" {
		state = "cancelled"
	}
	summary := addr(target) + dot() + bold(state, applyStateColor(e.State))
	if n := len(e.Cascaded); n > 0 {
		summary += dot() + styles.WarningStyle.Render(fmt.Sprintf("%d cascaded", n))
	}
	var detail []string
	if len(e.Cascaded) > 0 {
		detail = append(detail, section("cascaded"))
		detail = append(detail, effectLines(e.Cascaded, "  ", 12)...)
	}
	if n := len(e.Ready); n > 0 {
		detail = append(detail, section(fmt.Sprintf("%d ready to effect", n)))
		detail = append(detail, effectLines(e.Ready, "  ", 12)...)
	}
	if e.Message != "" {
		detail = append(detail, muted(e.Message))
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// --- turf_resource_import / turf_resource_refresh ---------------------------

type resourceImportView struct {
	ResourceAddr  string         `json:"resource_addr"`
	ImportID      string         `json:"import_id"`
	ImportedState map[string]any `json:"imported_state"`
	// Identity is the provider-declared resource identity recorded for the object,
	// present when the type declares one — and the only locator when the import was
	// made by identity rather than by id.
	Identity map[string]any `json:"identity,omitempty"`
	// Warning is set when the import was recorded but could not be flushed to the
	// state backend. The import stands in this session either way, so this is not an
	// error — but it is the difference between a durable import and a session-local
	// one, and must not read as a clean success.
	Warning string `json:"warning,omitempty"`
}

func renderResourceImport(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, targetBody(argString(msg, "resource_addr")), width)
	}
	var r resourceImportView
	if !parseContent(msg, &r) {
		return fallbackLine(msg, s, ss, width)
	}
	target := r.ResourceAddr
	if target == "" {
		target = argString(msg, "resource_addr")
	}
	summary := addr(target) + dot() + bold("imported", styles.Success)
	// The locator: an id, or — when the object was located by a provider-declared
	// resource identity — the identity itself, which is an object, so name it.
	id := r.ImportID
	if id == "" {
		id = argString(msg, "import_id")
	}
	switch {
	case id != "":
		summary += dot() + muted("id "+id)
	case len(r.Identity) > 0:
		summary += dot() + muted("identity")
	}
	if n := len(r.ImportedState); n > 0 {
		summary += dot() + muted(fmt.Sprintf("%d attr(s)", n))
	}
	if r.Warning != "" {
		summary += dot() + styles.WarningStyle.Render("⚠ not flushed")
	}

	var detail []string
	if r.Warning != "" {
		detail = append(detail, styles.WarningStyle.Render("⚠ "+r.Warning))
	}
	if len(r.Identity) > 0 {
		detail = append(detail, section("identity"))
		detail = append(detail, kvLines(r.Identity, "  ", 10)...)
	}
	detail = append(detail, kvLines(r.ImportedState, "  ", 30)...)
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

type resourceRefreshView struct {
	ResourceAddr   string         `json:"resource_addr"`
	Exists         bool           `json:"exists"`
	RefreshedState map[string]any `json:"refreshed_state,omitempty"`
}

func renderResourceRefresh(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, targetBody(argString(msg, "resource_addr")), width)
	}
	var r resourceRefreshView
	if !parseContent(msg, &r) {
		return fallbackLine(msg, s, ss, width)
	}
	target := r.ResourceAddr
	if target == "" {
		target = argString(msg, "resource_addr")
	}
	status := bold("refreshed", styles.Success)
	if !r.Exists {
		status = styles.WarningStyle.Render("no longer exists")
	}
	summary := addr(target) + dot() + status
	if n := len(r.RefreshedState); n > 0 {
		summary += dot() + muted(fmt.Sprintf("%d attr(s)", n))
	}
	detail := kvLines(r.RefreshedState, "  ", 30)
	return lineWithDetail(msg, s, ss, summary, detail, width)
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

// planAction maps turf's wire action — a word like "create"/"replace"/"delete"
// (see wireActionFromRune in turf/tools/plan_record.go) — to a human label, a
// theme color, and a one-char glyph for compact tallies and per-resource lines.
// Raw symbols (+, ~, ±, …) are accepted as a fallback for any path that emits them.
//
// A "replace" carries a second bit, create_before_destroy, in the result. The
// server sends it as a bare bool with omitempty, so ABSENT is the meaningful
// default: absent/false is DTC (∓, destroy the old instance first — OpenTofu's
// default ordering) and true is CBD (±, create the new one first). cbd is false
// for non-replace actions, and is ignored when the action is already a ±/∓
// symbol, which is self-describing.
func planAction(action string, deferred bool, cbd bool) (label string, c color.Color, glyph string) {
	if deferred || action == "deferred" {
		return "deferred", styles.Warning, "?"
	}
	switch action {
	case "create", "+":
		return "create", styles.Success, "+"
	case "update", "~":
		return "update", styles.Accent, "~"
	case "delete", "destroy", "-":
		return "destroy", styles.Error, "-"
	case "replace":
		if cbd {
			return "replace", styles.Warning, "±" // create-before-destroy
		}
		return "replace", styles.Warning, "∓" // destroy-before-create (default)
	case "±":
		return "replace", styles.Warning, "±"
	case "∓":
		return "replace", styles.Warning, "∓"
	case "forget", ".":
		return "forget", styles.Highlight, "."
	case "read", "←":
		return "read", styles.Accent, "←"
	case "opened", "○":
		return "opened", styles.Success, "○"
	case "move", "→":
		return "move", styles.Accent, "→"
	case "noop", "no-op", "=":
		return "no-op", styles.Highlight, "="
	case "error":
		return "error", styles.Error, "✗"
	default:
		return "change", styles.Accent, "·"
	}
}

// planTally counts resources by action and renders a colored "+a ~b ±c ∓d -e"
// summary, omitting zero buckets — CBD (±) and DTC (∓) replacements counted apart.
func planTally(rs []resourcePlanEntry) string {
	counts := map[string]int{}
	for _, r := range rs {
		_, _, g := planAction(r.Action, false, r.CreateBeforeDestroy)
		counts[g]++
	}
	buckets := []struct {
		glyph string
		c     color.Color
	}{
		{"+", styles.Success}, {"~", styles.Accent},
		{"±", styles.Warning}, {"∓", styles.Warning}, {"-", styles.Error},
	}
	parts := []string{}
	for _, b := range buckets {
		if counts[b.glyph] > 0 {
			parts = append(parts, bold(fmt.Sprintf("%s%d", b.glyph, counts[b.glyph]), b.c))
		}
	}
	if len(parts) == 0 {
		return muted("no changes")
	}
	return strings.Join(parts, " ")
}

// adoptCount counts the rows that adopt an existing object named by an
// `import {}` block. They usually plan as no-ops, which planTally does not show.
func adoptCount(rs []resourcePlanEntry) int {
	n := 0
	for _, r := range rs {
		if r.Importing != nil {
			n++
		}
	}
	return n
}

// movedLines renders the phase's state relocations as "from → to" detail rows,
// = -aligned on the source address the same way kvLines aligns keys.
func movedLines(moved []movedRecordView, indent string, max int) []string {
	froms := make([]string, 0, len(moved))
	for _, m := range moved {
		froms = append(froms, m.From)
	}
	w := maxKeyWidth(froms)
	out := make([]string, 0, len(moved))
	for _, m := range moved {
		out = append(out, indent+addr(padRight(m.From, w))+muted(" → ")+addr(m.To))
	}
	return capLines(out, indent, max)
}

// applyStateColor colors the apply status by outcome. turf's effect states are
// free-form; we key off the common success/error words and fall back to accent.
func applyStateColor(state string) color.Color {
	switch s := strings.ToLower(state); {
	case strings.Contains(s, "error"), strings.Contains(s, "fail"):
		return styles.Error
	case strings.Contains(s, "done"), strings.Contains(s, "complete"), strings.Contains(s, "applied"), strings.Contains(s, "ok"):
		return styles.Success
	default:
		return styles.Accent
	}
}

// --- misc value helpers -----------------------------------------------------

// effectLines renders a set of effect IDs as formatted detail lines. An effect ID
// has the shape "<symbol>/<resource_addr>/<operation>" (e.g. "±/random_string.x/
// create"); we color the action symbol, highlight the address, and mute the op.
func effectLines(ids []string, indent string, max int) []string {
	var out []string
	for i, id := range ids {
		if max > 0 && i == max {
			out = append(out, indent+muted(fmt.Sprintf("…(+%d more)", len(ids)-max)))
			break
		}
		out = append(out, indent+formatEffectID(id))
	}
	return out
}

func formatEffectID(id string) string {
	parts := strings.Split(id, "/")
	if len(parts) < 3 {
		return muted(id)
	}
	sym := parts[0]
	op := parts[len(parts)-1]
	address := strings.Join(parts[1:len(parts)-1], "/")
	_, c, _ := planAction(sym, false, false)
	return bold(sym, c) + " " + addr(address) + dot() + muted(op)
}

// listLines renders string slice elements as indented detail lines, capped.
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

func listLines(items []string, indent string, max int) []string {
	var out []string
	for i, it := range items {
		if max > 0 && i == max {
			out = append(out, indent+muted(fmt.Sprintf("…(+%d more)", len(items)-max)))
			break
		}
		out = append(out, indent+muted(it))
	}
	return out
}

// outputsCount counts top-level keys for an object, or elements for an array.
func outputsCount(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil {
		return len(obj)
	}
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) == nil {
		return len(arr)
	}
	return 0
}

// outputsLines renders evaluated outputs (object of name→value) as kv detail lines.
func outputsLines(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) == nil && len(obj) > 0 {
		return kvLines(obj, "  ", 30)
	}
	return nil
}

// changedKeys lists attributes whose value differs between before and after, for a
// compact one-line summary. Returns "" when there's nothing meaningful to show.
func changedKeys(before, after map[string]any) string {
	if before == nil && after == nil {
		return ""
	}
	keys := unionKeys(before, after)
	var changed []string
	for _, k := range keys {
		if !jsonEqual(before[k], after[k]) {
			changed = append(changed, k)
		}
	}
	if len(changed) == 0 {
		return ""
	}
	sort.Strings(changed)
	const maxKeys = 6
	if len(changed) > maxKeys {
		extra := len(changed) - maxKeys
		changed = append(changed[:maxKeys], fmt.Sprintf("…(+%d more)", extra))
	}
	return strings.Join(changed, ", ")
}

func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}
