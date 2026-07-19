package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
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
// — both paths funnel through the same tool.Register and the same turfToolRenderers
// source.
func registerTurfToolRenderers() {
	for name, b := range turfToolRenderers() {
		tool.Register(name, b)
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
		"turf_plan_approve":       builder(renderPlanApprove),
		"turf_config_init":        builder(renderConfigInit),
		"turf_config_show":        builder(renderConfigShow),
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
		"turf_provider_search":    builder(renderProviderSearch),
		"turf_provider_load":      builder(renderProviderLoad),
		"turf_provider_describe":  builder(renderProviderDescribe),
		"turf_provider_configure": builder(renderProviderConfigure),
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
	return func(msg *types.Message, ss service.SessionStateReader) layout.Model {
		return toolcommon.NewBase(msg, ss, render)
	}
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
	"provider_configure": {"name"},
	"declare_action":     {"action_type"},
	"action_invoke":      {"action_type"},
	"effect_apply":       {"effect_id"},
	"effect_cancel":      {"effect_id"},
	"workspace_open":     {"alias", "name"},
	"workspace_close":    {"workspace_alias"},
	"workspace_delete":   {"name"},
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

func attrDiff(before, after map[string]any, indent string) []string {
	keys := unionKeys(before, after)
	sort.Strings(keys)
	// First pass: keep only keys that actually change, so the = alignment is computed
	// over exactly the rendered rows (not the skipped, unchanged attrs).
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
		bNil := !bok || bv == nil
		aNil := !aok || av == nil
		head := muted(padRight(k, w) + " = ")
		switch {
		case bNil:
			// A create: the whole subtree is new, so every line carries a + marker.
			out = append(out, renderValue(bold("+", styles.Success), head, av, indent, styles.Success)...)
		case aNil:
			// A destroy: the whole subtree is gone, so every line carries a - marker.
			out = append(out, renderValue(bold("-", styles.Error), head, bv, indent, styles.Error)...)
		case isScalar(bv) && isScalar(av):
			// A scalar change reads best inline as old → new.
			out = append(out, indent+bold("~", styles.Accent)+" "+head+muted(fmtVal(bv))+muted(" → ")+colored(fmtVal(av), styles.Accent))
		default:
			// A collection changed: mark the header and expand the new value. A full
			// per-line recursive sub-diff (mixed +/-/~ inside the block) is a deliberate
			// non-goal; showing the new value already avoids the raw-JSON dump.
			out = append(out, renderValue("", bold("~", styles.Accent)+" "+head, av, indent, styles.Accent)...)
		}
	}
	return out
}

// kvLines renders a map as aligned "key = value" detail lines (applied state, evaluated
// outputs, resolved config, imported state, …), expanding nested maps/lists via
// renderValue. Null leaves are skipped; max caps the number of rendered lines. This is
// the unmarked (no +/-) value view; diffs pass a marker through renderValue instead.
func kvLines(m map[string]any, indent string, max int) []string {
	keys := nonNilKeys(m)
	w := maxKeyWidth(keys)
	var out []string
	for _, k := range keys {
		out = append(out, renderValue("", muted(padRight(k, w)+" = "), m[k], indent, styles.Highlight)...)
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

func renderValue(marker, head string, v any, indent string, c color.Color) []string {
	switch x := v.(type) {
	case map[string]any:
		return renderMap(marker, head, x, indent, c)
	case []any:
		return renderList(marker, head, x, indent, c)
	default:
		return []string{indent + markerPrefix(marker) + head + colored(fmtVal(v), c)}
	}
}

// renderMap expands a map to "<head>{ … }" with each non-nil entry on its own aligned,
// deeper-indented line (the marker propagating to each). An empty (or all-null) map
// renders inline as "<head>{}".
func renderMap(marker, head string, m map[string]any, indent string, c color.Color) []string {
	keys := nonNilKeys(m)
	if len(keys) == 0 {
		return []string{indent + markerPrefix(marker) + head + muted("{}")}
	}
	w := maxKeyWidth(keys)
	child := childIndent(indent, marker)
	out := []string{indent + markerPrefix(marker) + head + muted("{")}
	for _, k := range keys {
		out = append(out, renderValue(marker, muted(padRight(k, w)+" = "), m[k], child, c)...)
	}
	return append(out, closeIndent(indent, marker)+muted("}"))
}

// renderList renders a list inline ("<head>[a, b, c]") when it holds only scalars and its
// unstyled form is short; otherwise it expands one element per line with a trailing comma
// (recursing for nested collections, the marker propagating to each). An empty list
// renders as "<head>[]".
func renderList(marker, head string, s []any, indent string, c color.Color) []string {
	if len(s) == 0 {
		return []string{indent + markerPrefix(marker) + head + muted("[]")}
	}
	if scalarList(s) {
		raw := make([]string, len(s))
		styled := make([]string, len(s))
		for i, e := range s {
			raw[i] = fmtVal(e)
			styled[i] = colored(raw[i], c)
		}
		if len([]rune("["+strings.Join(raw, ", ")+"]")) <= inlineListWidth {
			return []string{indent + markerPrefix(marker) + head + muted("[") + strings.Join(styled, muted(", ")) + muted("]")}
		}
	}
	child := childIndent(indent, marker)
	out := []string{indent + markerPrefix(marker) + head + muted("[")}
	for _, e := range s {
		lines := renderValue(marker, "", e, child, c)
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

// fmtVal renders a JSON-decoded value compactly. turf's sentinels are masked:
// "__cty_unknown__" (a value known only after apply) and "__cty_sensitive__" (a
// sensitive value whose real content stays server-side and never reaches us).
func fmtVal(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		switch x {
		case "__cty_unknown__":
			return "(known after apply)"
		case "__cty_sensitive__":
			return "(sensitive)"
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
	CreateBeforeDestroy *bool          `json:"create_before_destroy,omitempty"` // for replace: ± (true) vs ∓ (false)
	ActionReason        string         `json:"action_reason"`
	RequiresReplace     []string       `json:"requires_replace"`
	Before              map[string]any `json:"before"`
	After               map[string]any `json:"after"`
	Deferred            *struct {
		Reason string `json:"reason"`
	} `json:"deferred,omitempty"`
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
	if changed := changedKeys(p.Before, p.After); changed != "" {
		summary += dot() + muted(changed)
	}

	var detail []string
	if p.Provider != "" {
		detail = append(detail, muted("provider ")+providerName(p.Provider))
	}
	if p.ActionReason != "" {
		detail = append(detail, muted("reason: ")+p.ActionReason)
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
	}
	if diff := attrDiff(p.Before, p.After, ""); len(diff) > 0 {
		detail = append(detail, diff...)
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// --- turf_effect_apply ------------------------------------------------------

type effectApplyView struct {
	Kind         string         `json:"kind"`
	State        string         `json:"state"`
	ResourceAddr string         `json:"resource_addr"`
	Ready        []string       `json:"ready"`
	NewState     map[string]any `json:"new_state,omitempty"`
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

	var detail []string
	// new state first, then what becomes ready to effect — the temporal order of apply.
	if len(e.NewState) > 0 {
		detail = append(detail, section("new state"))
		detail = append(detail, kvLines(e.NewState, "  ", 40)...)
	}
	if n := len(e.Ready); n > 0 {
		detail = append(detail, section(fmt.Sprintf("%d ready to effect", n)))
		detail = append(detail, effectLines(e.Ready, "  ", 12)...)
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// --- turf_declare_module / turf_replan / turf_plan_new ------------------------

type resourcePlanEntry struct {
	Address             string         `json:"address"`
	Type                string         `json:"type"`
	Provider            string         `json:"provider"`
	Action              string         `json:"action"`
	CreateBeforeDestroy *bool          `json:"create_before_destroy,omitempty"`
	Before              map[string]any `json:"before,omitempty"`
	After               map[string]any `json:"after,omitempty"`
	RequiresReplace     []string       `json:"requires_replace,omitempty"`
}

type planSummaryView struct {
	Address          string              `json:"address"` // declare_module
	Path             string              `json:"path"`    // replan / plan_new (the bound configuration dir)
	PhaseID          string              `json:"phase_id"`
	Resources        []resourcePlanEntry `json:"resources"`
	TopologicalOrder []string            `json:"topological_order"`
	Deferred         []json.RawMessage   `json:"deferred,omitempty"`
	Outputs          json.RawMessage     `json:"outputs,omitempty"`
	Warnings         []string            `json:"warnings,omitempty"`
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
// expansion, evaluated outputs, and warnings.
func planSummaryLine(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width int, head string, p planSummaryView) string {
	summary := planTally(p.Resources)
	if head != "" {
		summary = head + dot() + summary
	}
	if n := len(p.Deferred); n > 0 {
		summary += dot() + styles.WarningStyle.Render(fmt.Sprintf("%d deferred", n))
	}
	if n := outputsCount(p.Outputs); n > 0 {
		summary += dot() + muted(fmt.Sprintf("%d output(s)", n))
	}

	// Expanded: each resource as a header + its full attribute diff, then the
	// evaluated outputs and any warnings.
	var detail []string
	const maxRows = 25
	for i, r := range p.Resources {
		if i == maxRows {
			detail = append(detail, muted(fmt.Sprintf("…(+%d more resources)", len(p.Resources)-maxRows)))
			break
		}
		_, c, glyph := planAction(r.Action, false, r.CreateBeforeDestroy)
		header := bold(glyph, c) + " " + addr(r.Address)
		if changed := changedKeys(r.Before, r.After); changed != "" {
			header += dot() + muted(changed)
		}
		detail = append(detail, header)
		detail = append(detail, attrDiff(r.Before, r.After, "  ")...)
	}
	if outs := outputsLines(p.Outputs); len(outs) > 0 {
		detail = append(detail, section("outputs"))
		detail = append(detail, outs...)
	}
	for _, w := range p.Warnings {
		detail = append(detail, styles.WarningStyle.Render("⚠ "+w))
	}
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

	// Expanded: each output as name = value, with sentinels masked by fmtVal
	// ("__cty_unknown__" → "(known after apply)", "__cty_sensitive__" → "(sensitive)").
	detail := kvLines(p.Outputs, "  ", 30)
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
// outputs reads a workspace's root outputs (name → {value, sensitive}). The
// server masks sensitive values with the "__cty_sensitive__" sentinel unless the
// caller opted in to reveal them; fmtVal renders that sentinel as "(sensitive)".

type outputsView struct {
	WorkspaceAlias string `json:"workspace_alias"`
	Outputs        map[string]struct {
		Value     any  `json:"value"`
		Sensitive bool `json:"sensitive"`
	} `json:"outputs"`
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
	sens := 0
	for _, o := range r.Outputs {
		if o.Sensitive {
			sens++
		}
	}
	if sens > 0 {
		summary += dot() + muted(fmt.Sprintf("%d sensitive", sens))
	}

	// Flatten to name→value so kvLines can render (and mask) each leaf.
	flat := make(map[string]any, len(r.Outputs))
	for k, o := range r.Outputs {
		flat[k] = o.Value
	}
	detail := kvLines(flat, "  ", 30)
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// --- turf_workspace_open / turf_workspace_close -----------------------------

type workspaceOpenView struct {
	WorkspaceAlias    string                     `json:"workspace_alias"`
	BackendType       string                     `json:"backend_type"`
	Name              string                     `json:"name"`
	Resources         []string                   `json:"resources"`
	ResolvedProviders map[string]json.RawMessage `json:"resolved_providers,omitempty"`
}

func renderWorkspaceOpen(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, "", width)
	}
	var w workspaceOpenView
	if !parseContent(msg, &w) {
		return fallbackLine(msg, s, ss, width)
	}

	name := w.Name
	if name == "" {
		name = w.WorkspaceAlias
	}
	summary := addr(name)
	if w.BackendType != "" {
		summary += dot() + keyword(w.BackendType)
	}
	if providers := providerNames(w.ResolvedProviders); providers != "" {
		summary += dot() + providerName(providers)
	}

	var detail []string
	if len(w.ResolvedProviders) > 0 {
		detail = append(detail, section("providers"))
		names := make([]string, 0, len(w.ResolvedProviders))
		for n := range w.ResolvedProviders {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			src, ver := providerSourceVersion(w.ResolvedProviders[n])
			row := "  " + addr(n)
			if src != "" {
				row += muted("  " + src)
			}
			if ver != "" {
				row += muted("  v" + ver)
			}
			detail = append(detail, row)
		}
	}
	if n := len(w.Resources); n > 0 {
		detail = append(detail, section(fmt.Sprintf("%d resource(s) in state", n)))
		detail = append(detail, listLines(w.Resources, "  ", 20)...)
	}
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
		Name               string `json:"name"`
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
		summary = addr(workspaceName(w.WorkspaceAlias, w.Name)) +
			dot() + muted(fmt.Sprintf("%d resource(s)", w.ResourceCount))
		if w.UncommittedChanges > 0 {
			summary += dot() + styles.WarningStyle.Render(fmt.Sprintf("%d uncommitted", w.UncommittedChanges))
		}
	default:
		summary = muted(fmt.Sprintf("%d open", len(r.Workspaces)))
	}

	var detail []string
	for _, w := range r.Workspaces {
		row := addr(workspaceName(w.WorkspaceAlias, w.Name))
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
	head := addr(p.PhaseID) + dot() + muted("opened")
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
		detail = append(detail, row)
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// --- turf_datasource_read ---------------------------------------------------
//
// datasource_read evaluates a data source and returns its read state (attrs) plus
// an advisory replan list — resource addresses whose (still-unknown) attributes the
// data source depends on, hinting the read may change once they're applied.

type datasourceReadView struct {
	ResourceAddr string         `json:"resource_addr"`
	State        map[string]any `json:"state"`
	Replan       []string       `json:"replan"`
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
	if n := len(r.Replan); n > 0 {
		summary += dot() + styles.WarningStyle.Render(fmt.Sprintf("%d replan", n))
	}

	detail := kvLines(r.State, "  ", 30)
	if len(r.Replan) > 0 {
		detail = append(detail, section("replan"))
		detail = append(detail, listLines(r.Replan, "  ", 20)...)
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
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
	Name            string `json:"name"`
	Source          string `json:"source"`
	ResolvedVersion string `json:"resolved_version"`
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
	return lineWithDetail(msg, s, ss, summary, detail, width)
}

// --- turf_provider_configure ------------------------------------------------
//
// provider_configure wires a provider's credentials/settings into the workspace.
// The result reports whether all config values resolved to known literals
// ("configured") or whether at least one was still unknown ("configured_with_unknowns");
// the latter means dependent resources will defer until the upstream is applied
// and the provider is re-configured. We surface the status prominently in the
// summary and list any unknown keys in the expand so the user knows exactly
// what to resolve.

type providerConfigureView struct {
	Provider    string   `json:"provider"`
	Alias       string   `json:"alias,omitempty"`
	Status      string   `json:"status"`
	UnknownKeys []string `json:"unknown_keys,omitempty"`
}

func renderProviderConfigure(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, targetBody(argString(msg, "name")), width)
	}
	var p providerConfigureView
	if !parseContent(msg, &p) {
		return fallbackLine(msg, s, ss, width)
	}

	name := p.Provider
	if name == "" {
		name = argString(msg, "name")
	}

	statusColor := styles.Success
	statusLabel := "configured"
	if p.Status == "configured_with_unknowns" {
		statusColor = styles.Warning
		statusLabel = "configured with unknowns"
	}

	summary := addr(name) + dot() + bold(statusLabel, statusColor)
	if p.Alias != "" {
		summary += dot() + muted("alias ") + providerName(p.Alias)
	}
	if n := len(p.UnknownKeys); n > 0 {
		summary += dot() + styles.WarningStyle.Render(fmt.Sprintf("%d unknown key(s)", n))
	}

	var detail []string
	if len(p.UnknownKeys) > 0 {
		detail = append(detail, section("unknown keys (resolve then re-configure)"))
		detail = append(detail, listLines(p.UnknownKeys, "  ", 20)...)
	}
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
// render a clean "skill <slug> loaded (N lines)" line — the size inline, not in
// the unfold — and, on expand, just the guide title, never the whole body.

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
	var detail []string
	if title := firstLine(msg.Content); title != "" {
		detail = append(detail, muted("guide: ")+title)
	}
	return lineWithDetail(msg, s, ss, summary, detail, width)
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
	Name          string `json:"name"`
	ResourceCount int    `json:"resource_count"`
	Deleted       bool   `json:"deleted"`
}

func renderWorkspaceDelete(msg *types.Message, s spinner.Spinner, ss service.SessionStateReader, width, _ int) string {
	if running(msg) {
		return line(msg, s, targetBody(argString(msg, "name")), width)
	}
	var w workspaceDeleteView
	if !parseContent(msg, &w) {
		return fallbackLine(msg, s, ss, width)
	}
	name := w.Name
	if name == "" {
		name = argString(msg, "name")
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

// --- turf_config_init / turf_module_init ------------------------------------
//
// Both introspect a config directory / module source: they resolve required
// providers, variables, and outputs (config_init also reports the backend; the
// server's full result carries a README we don't surface). We show the target +
// a "N providers · N variables · N outputs" tally, and on expand list each.

type initProviderView struct {
	Source  string `json:"source"`
	Version string `json:"version,omitempty"`
}

type initVariableView struct {
	Name      string `json:"name"`
	Type      string `json:"type,omitempty"`
	Required  bool   `json:"required"`
	Sensitive bool   `json:"sensitive,omitempty"`
}

type initOutputView struct {
	Name      string `json:"name"`
	Sensitive bool   `json:"sensitive,omitempty"`
}

type configInitView struct {
	Backend *struct {
		Type string `json:"type"`
	} `json:"backend"`
	Workspace struct {
		Name string `json:"name"`
	} `json:"workspace"`
	RequiredProviders map[string]initProviderView `json:"required_providers"`
	Variables         []initVariableView          `json:"variables"`
	Outputs           []initOutputView            `json:"outputs"`
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
	if c.Backend != nil && c.Backend.Type != "" {
		summary = appendDot(summary, muted("backend "+c.Backend.Type))
	}
	if parts := initCountParts(len(c.RequiredProviders), len(c.Variables), len(c.Outputs)); parts != "" {
		summary = appendDot(summary, muted(parts))
	}
	detail := initDetail(c.RequiredProviders, c.Variables, c.Outputs)
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
			if p := providers[n]; p.Source != "" {
				row += muted("  " + p.Source)
				if p.Version != "" {
					row += muted("  " + p.Version)
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
	id := r.ImportID
	if id == "" {
		id = argString(msg, "import_id")
	}
	if id != "" {
		summary += dot() + muted("id "+id)
	}
	if n := len(r.ImportedState); n > 0 {
		summary += dot() + muted(fmt.Sprintf("%d attr(s)", n))
	}
	detail := kvLines(r.ImportedState, "  ", 30)
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
// A "replace" carries a second bit, create_before_destroy, in the result: true is
// CBD (±, create the new instance first) and false is DTC (∓, destroy first) — the
// two distinct replacement strategies. cbd is nil for non-replace actions (and is
// ignored when the action is already a ±/∓ symbol, which is self-describing).
func planAction(action string, deferred bool, cbd *bool) (label string, c color.Color, glyph string) {
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
		if cbd != nil && !*cbd {
			return "replace", styles.Warning, "∓" // destroy-before-create
		}
		return "replace", styles.Warning, "±" // create-before-destroy (default)
	case "±":
		return "replace", styles.Warning, "±"
	case "∓":
		return "replace", styles.Warning, "∓"
	case "forget", ".":
		return "forget", styles.Highlight, "."
	case "read", "←":
		return "read", styles.Accent, "←"
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
	_, c, _ := planAction(sym, false, nil)
	return bold(sym, c) + " " + addr(address) + dot() + muted(op)
}

// listLines renders string slice elements as indented detail lines, capped.
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

func providerNames(m map[string]json.RawMessage) string {
	if len(m) == 0 {
		return ""
	}
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func providerSourceVersion(raw json.RawMessage) (source, version string) {
	var v struct {
		Source  string `json:"source"`
		Version string `json:"version"`
	}
	if json.Unmarshal(raw, &v) == nil {
		return v.Source, v.Version
	}
	return "", ""
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
