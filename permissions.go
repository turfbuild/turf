package main

// Tool pre-approval policy.
//
// The turf-mcp-server is the structural source of truth for tool risk: it
// faithfully annotates every tool with MCP hints (readOnlyHint, destructiveHint).
// The turf CLI is a showcase for a first-class, reliable UX, and that raw "MCP"
// layer is an implementation detail the user should not feel. So the CLI
// pre-approves ALL of turf's own tools — the permission layer never fires a
// prompt for a turf tool — and moves confirmation of consequential actions into
// the agent, which seeks a user_prompt yes/no per the persona (see
// workflowInstructions in agent.go): once before editing a coded (tofu-dialect)
// configuration — the user's own .tf/.tfvars, edited via the filesystem
// write_file/edit_file tools (ad-hoc plot authoring via declare_* is NOT gated —
// the plan gate covers it); once before plan_approve for a whole phase; and
// per-call for standalone destructive ops (workspace_delete, resource_import,
// config_promote, a directly-invoked action_invoke). This trades the deterministic
// per-tool permission gate for agent-driven confirmation, on purpose — the old
// gate double-prompted right after the user had already approved a plan, which is
// friction on the tools that are the heart of the UX.
//
// Why this stays safe:
//   - Names are the LLM-facing, turf_-prefixed tool names (the MCP toolset is
//     registered under the "turf" name, so cagent prefixes every server tool with
//     "turf_"; see agent.go / mcptoolset.go). preApprovedTurfTools is an explicit,
//     static, turf_-only enumeration — so any unknown or third-party MCP tool the
//     CLI may load in future is absent from it and still prompts. Pre-approval is
//     scoped to turf's own tools; it is NOT a blanket "approve anything turf_*".
//   - Because the list is an explicit enumeration and not a prefix match, a NEW
//     turf server tool is absent from it and therefore prompts at runtime
//     (fail-safe), and the drift test fails CI until it is consciously classified.
//     Worst case is over-prompting, never under-prompting.
//   - The server's destructiveHint annotations remain the strong core; the drift
//     test ties them to agentConfirmTurfTools so a newly-destructive server tool
//     forces a persona-confirmation decision rather than sliding through silently.
//
// Precedence in cagent's approval pipeline (see runtime/toolexec): yolo
// (--auto-approve) > session Allow/Ask > ReadOnlyHint > default Ask. turf leaves
// Ask empty (newSession passes Allow: preApprovedTurfTools, Ask: nil), so no turf
// tool falls to a permission prompt; --auto-approve still approves everything,
// unchanged.

// preApprovedTurfTools run without a permission prompt: every turf tool. This is
// the single source of truth for the set of tools the CLI knows about
// (turfToolInfo in mcptoolset.go is kept in lock-step with it, enforced by
// mcptoolset_test.go). Confirmation of consequential actions is the agent's job
// via user_prompt (see agentConfirmTurfTools and the persona in agent.go), not the
// permission layer's.
var preApprovedTurfTools = []string{
	// Providers: discovery + (in-memory) configuration.
	"turf_provider_search",
	"turf_provider_describe",
	"turf_provider_load",
	"turf_provider_configure",
	// Workspace lifecycle. workspace_delete is server-annotated destructive and
	// irreversible; it is pre-approved like the rest but the persona must obtain an
	// explicit, irreversibility-warning user_prompt before calling it (see
	// agentConfirmTurfTools). workspace_close is destructive too but authorizes
	// nothing new (it flushes already-approved state), so it is neither confirmed
	// nor persona-gated.
	"turf_workspace_open",
	"turf_workspace_list",
	"turf_workspace_show",
	"turf_workspace_close",
	"turf_workspace_delete",
	// State / outputs. state_list, outputs, and the *_outputs reads are read-only;
	// resource_refresh only reconciles state to live reality (like `tofu refresh`)
	// so it is benign and runs silently; resource_import adopts existing infra into
	// state and the persona confirms it (see agentConfirmTurfTools).
	"turf_state_list",
	"turf_outputs",
	"turf_declare_outputs",
	"turf_module_outputs",
	"turf_resource_import",
	"turf_resource_refresh",
	// Data source: read-only.
	"turf_datasource_read",
	// Draft / phase lifecycle. plan_approve seals the Draft into an Execution and
	// effect_apply/effect_cancel run the approved effects — their confirmation is
	// the single phase-level user_prompt the persona seeks before plan_approve, not
	// a per-effect prompt.
	"turf_plan_new",
	"turf_plan_cancel",
	"turf_plan_approve",
	"turf_replan",
	"turf_effect_apply",
	"turf_effect_cancel",
	// Config / module authoring. The declare family authors a turf-owned plot
	// (git-recoverable checkout mutations, not live infra); it is deliberately NOT
	// edit-gated by the persona — ad-hoc stays streamlined, and the plan gate covers
	// it before apply. (The persona's coded-config edit gate is over write_file/
	// edit_file on the user's own .tf/.tfvars, not these plot tools.) config_promote
	// graduates a plot into a plain tofu configuration — a one-way directory
	// transformation the persona confirms (see agentConfirmTurfTools).
	"turf_config_init",
	"turf_config_show",
	"turf_config_promote",
	"turf_declare_backend",
	"turf_declare_provider",
	"turf_module_init",
	"turf_declare_module",
	// Resource / action planning — accumulates into the Draft. action_invoke fires
	// an imperative side effect; the persona confirms a directly-invoked one.
	"turf_declare_resource",
	"turf_declare_var",
	"turf_declare_action",
	"turf_action_invoke",
	// Skills / docs.
	"turf_skill_core",
	"turf_skill_adhoc",
	"turf_skill_codified",
	"turf_skill_demo",
	"turf_read_skill_file",
}

// The cagent builtin toolsets turf pre-approves so the permission layer never
// prompts for them (bare names, NOT turf_-prefixed server tools). Split per toolset
// so each list can be tied to its package's tool-name constants by a drift guard in
// permissions_test.go — a new/renamed builtin tool fails CI until it is consciously
// classified. `think` is not listed: it is a single, pure, read-only tool that
// already auto-approves via ReadOnlyHint and has nothing to author.

// preApprovedMemoryTools: turf's advisory-knowledge slot — the agent reads and
// writes it silently to inform planning (see the builtin-toolset note in
// CLAUDE.md), so prompting on it is pure friction. The read tools
// (get_memories/search_memories) already auto-approve via ReadOnlyHint; listed here
// too so the whole toolset is covered regardless of any upstream hint change.
var preApprovedMemoryTools = []string{
	"add_memory",
	"get_memories",
	"delete_memory",
	"search_memories",
	"update_memory",
}

// preApprovedFilesystemTools: the agent's own file tools. Pre-approved BY NAME (not
// arg-scoped path patterns) because the toolset is already hard-sandboxed to the
// allow-list roots — the working dir, the scratch dir, and any --allow-path — via an
// *os.Root (see createAgentRuntime in agent.go). That sandbox, not the permission
// layer, is the real boundary: a file tool physically cannot touch anything outside
// the allowed roots, so pre-approval only removes the redundant prompt for work that
// is already confined to them, and --allow-path widens the auto-approved area simply
// by widening the sandbox. (The read tools already auto-approve via ReadOnlyHint;
// the writers — write_file/edit_file/create_directory/remove_directory — did not,
// and are the ones this unblocks. All are listed so the toolset is covered whole.)
var preApprovedFilesystemTools = []string{
	"read_file",
	"read_multiple_files",
	"edit_file",
	"write_file",
	"directory_tree",
	"list_directory",
	"search_files_content",
	"create_directory",
	"remove_directory",
}

// preApprovedTodoTools: the in-flight step checklist. These already auto-approve via
// ReadOnlyHint, but are pre-approved explicitly so turf's trust in them is stated
// (not inherited from an upstream annotation the code itself flags as "technically
// not read-only") and survives if that hint ever changes.
var preApprovedTodoTools = []string{
	"create_todo",
	"create_todos",
	"update_todos",
	"list_todos",
}

// preApprovedBuiltinTools is the union of the per-toolset lists above — the full set
// of cagent builtin tools turf pre-approves.
func preApprovedBuiltinTools() []string {
	out := make([]string, 0, len(preApprovedMemoryTools)+len(preApprovedFilesystemTools)+len(preApprovedTodoTools))
	out = append(out, preApprovedMemoryTools...)
	out = append(out, preApprovedFilesystemTools...)
	out = append(out, preApprovedTodoTools...)
	return out
}

// preApprovedTools is the full set turf pre-approves at the team (process) level:
// every turf server tool plus the builtin tools above. This is installed as a
// team-level permission checker (see createAgentRuntime in agent.go), which is a
// property of the runtime rather than of any single session — so the pre-approval
// survives session replacement (/clear, /new, /fork) and resume without needing to
// be re-stamped per session. Copy-then-append so neither source slice is mutated.
func preApprovedTools() []string {
	builtins := preApprovedBuiltinTools()
	out := make([]string, 0, len(preApprovedTurfTools)+len(builtins))
	out = append(out, preApprovedTurfTools...)
	out = append(out, builtins...)
	return out
}

// agentConfirmTurfTools names the high-consequence tools whose confirmation the
// persona guarantees via user_prompt. Every entry is pre-approved at the
// permission layer (a subset of preApprovedTurfTools) — this list is documentation
// plus a drift guard (permissions_test.go), not a permission list. It is turf's
// own confirm-policy, deliberately NOT a mirror of the server's destructiveHint:
// it includes non-destructive-but-consequential tools (effect_cancel can
// cascade-cancel dependents; resource_import adopts infra into state), and it
// excludes workspace_close (destructive but authorizes nothing new). Two
// confirmation shapes, spelled out in the persona (see agent.go):
//   - effect_apply / effect_cancel are covered by the single phase-level
//     confirmation the persona seeks before plan_approve — NOT prompted per-effect;
//   - workspace_delete, resource_import, config_promote, and a directly-invoked
//     action_invoke are standalone ops confirmed with their own targeted user_prompt.
var agentConfirmTurfTools = []string{
	"turf_effect_apply",     // applies real infra changes (confirmed at plan_approve)
	"turf_effect_cancel",    // can cascade-cancel dependents (confirmed at plan_approve)
	"turf_action_invoke",    // imperative side effects
	"turf_workspace_delete", // irreversible state deletion — warn it cannot be undone
	"turf_resource_import",  // adopts existing infra into state
	"turf_config_promote",   // one-way plot → tofu configuration transformation
}
