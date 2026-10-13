package main

import (
	"context"
	"strings"

	"github.com/docker/docker-agent/pkg/tools"
)

// turfMCPToolset decorates the turf MCP toolset so the /tools dialog presents it
// under the turf brand. The inner toolset is registered under the "turf" name
// (see createAgentRuntime in agent.go), so cagent already prefixes every tool
// with "turf_" and reports Name() == "turf". This wrapper layers on the
// remaining display niceties cagent's MCP toolset can't do on its own:
//
//   - Tools() files each tool under a functional sub-category ("turf · run",
//     "turf · apply", …; see turfToolInfo) instead of the flat "turf" heading —
//     the dialog's Tools section groups by Category, and cagent's own guidance is
//     that a Category is a functional bucket, not the toolset name. This breaks the
//     26-tool wall into scannable groups (replacing the generic "mcp" bucket the
//     MCP toolset hardcodes onto every tool).
//   - Tools() also strips the redundant "turf_" prefix for display by setting each
//     tool's Annotations.Title to a friendly name ("Apply Plan" rather than
//     "turf_apply"); the category header already says "turf". This is
//     display-only — providers send only Name/Description/Parameters, and
//     permissions/dispatch key on Name, so the model still calls "turf_apply".
//   - Kind() masks the inner "MCP" classification (see below).
//
// Name() is restated as "turf" for clarity even though the named inner toolset
// already returns it; tools.As reads Name through this wrapper.
//
// Everything else (lifecycle/supervisor state, prompts, metadata) is delegated
// to the inner toolset via Unwrap, so tools.As keeps reaching those
// capabilities through the wrapper.
type turfMCPToolset struct {
	inner tools.ToolSet
}

func newTurfMCPToolset(inner tools.ToolSet) *turfMCPToolset {
	return &turfMCPToolset{inner: inner}
}

func (d *turfMCPToolset) Tools(ctx context.Context) ([]tools.Tool, error) {
	innerTools, err := d.inner.Tools(ctx)
	if err != nil {
		return nil, err
	}
	// Copy before mutating: the inner toolset may cache and return the same
	// slice across calls, and we must not stamp our Category/Title onto its cache.
	out := make([]tools.Tool, len(innerTools))
	copy(out, innerTools)
	for i := range out {
		decorateTurfTool(&out[i])
	}
	return out, nil
}

// decorateTurfTool rewrites one tool's Category and display Title for the /tools
// dialog: it files the tool under its functional sub-category and gives it a
// friendly, verb-first display name ("Apply Plan" rather than "turf_apply").
// Display-only — Name is left untouched, so the model still calls the tool by its
// prefixed name, and permissions/dispatch (which key on Name) are unaffected. Names
// arrive already prefixed ("turf_apply"); TrimPrefix yields the bare name and
// is a no-op if a name is ever unprefixed.
func decorateTurfTool(t *tools.Tool) {
	bare := strings.TrimPrefix(t.Name, appName+"_")
	t.Category = turfCategory(bare)
	t.Annotations.Title = turfToolTitle(bare)
}

// turfToolInfo is the single source of truth for how each turf MCP tool presents
// in the /tools dialog: its functional sub-group (the dialog groups by Category,
// which turfCategory renders as "turf · <group>", sorted alphabetically) and a
// friendly, verb-first display title. The tool set here is kept in lock-step with
// preApprovedTurfTools in permissions.go (which lists every turf tool) — enforced
// by TestTurfToolInfoMatchesPermissionLists in mcptoolset_test.go.
var turfToolInfo = map[string]struct {
	group string
	title string
}{
	// provider — discovery
	"provider_search":   {"provider", "Search Providers"},
	"provider_describe": {"provider", "Describe Provider"},
	// config — `tofu init` registers the configuration directory, installs its
	// modules and providers, and reports what it declares (the agent edits the .tf
	// files themselves with its own file tools)
	"init": {"config", "Init Configuration"},
	// workspace — lifecycle
	"workspace_open":   {"workspace", "Open Workspace"},
	"workspace_show":   {"workspace", "Show Workspace"},
	"workspace_list":   {"workspace", "List Workspaces"},
	"workspace_close":  {"workspace", "Close Workspace"},
	"workspace_delete": {"workspace", "Delete Workspace"},
	// run — the calls that start a convergence and stop at its first approval
	// point. up is titled by the engine console's word for its run kind, "deploy".
	"up":      {"run", "Deploy"},
	"destroy": {"run", "Destroy"},
	"refresh": {"run", "Refresh State"},
	"import":  {"run", "Import Resources"},
	"invoke":  {"run", "Invoke Action"},
	// apply — approving a vended plan (or answering turf actions), and ending a
	// convergence
	"apply":  {"apply", "Apply Plan"},
	"cancel": {"apply", "Cancel Run"},
	// state — reads, and the state edits that apply nothing by themselves
	"show":       {"state", "Show"},
	"output":     {"state", "Read Outputs"},
	"state_list": {"state", "List State"},
	"taint":      {"state", "Taint Resource"},
	"untaint":    {"state", "Untaint Resource"},
	// skills — workflow guides
	"skill_deployment": {"skills", "Deployment Skill"},
	"skill_authoring":  {"skills", "Authoring Skill"},
	"skill_guide":      {"skills", "Guide Skill"},
	"read_skill_file":  {"skills", "Read Skill File"},
}

// turfCategory returns the /tools dialog category for a bare turf tool name.
// Unknown or newly-added server tools fall back to the plain "turf" bucket
// (fail-safe: they still appear, just ungrouped, until added to turfToolInfo).
func turfCategory(bare string) string {
	if info, ok := turfToolInfo[bare]; ok {
		return appName + " · " + info.group
	}
	return appName
}

// turfToolTitle returns the friendly display name for a bare turf tool name,
// falling back to the bare name itself for unknown/newly-added tools.
func turfToolTitle(bare string) string {
	if info, ok := turfToolInfo[bare]; ok {
		return info.title
	}
	return bare
}

// Name drives the Toolsets-list label; Unwrap exposes the inner toolset's
// remaining capabilities (Startable, Statable, …) to tools.As.
func (d *turfMCPToolset) Name() string          { return appName }
func (d *turfMCPToolset) Unwrap() tools.ToolSet { return d.inner }

// Kind masks the inner toolset's "MCP" classification: tools.As stops at this
// wrapper (it satisfies Kinder) and never reaches the inner Kind(), and the
// /tools renderer maps an empty Kind to "Built-in". turf's MCP server is an
// implementation detail, so it's presented as a built-in capability rather
// than an external MCP integration.
func (d *turfMCPToolset) Kind() string { return "" }

var (
	_ tools.ToolSet   = (*turfMCPToolset)(nil)
	_ tools.Named     = (*turfMCPToolset)(nil)
	_ tools.Unwrapper = (*turfMCPToolset)(nil)
	_ tools.Kinder    = (*turfMCPToolset)(nil)
)
