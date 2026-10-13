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
// workflowInstructions in agent.go). A configuration is ordinary Terraform the
// agent edits with the filesystem write_file/edit_file tools, so the policy is:
//   - files the agent CREATES for a new configuration (a natural-language request
//     in a new/empty directory) need no separate confirmation — the plan approval
//     covers them before anything is applied;
//   - edits to PRE-EXISTING .tf/.tfvars files (the user's own) get one
//     confirmation per edit set, summarizing the intended edits;
//   - each round's plan is approved by apply, under the server's own rule: the
//     agent approves a low-risk round itself and consults the user for anything
//     else, always for a destroy plan, and always for an import or invoke plan;
//   - workspace_delete is a standalone, irreversible op confirmed per call.
// This trades the deterministic per-tool permission gate for agent-driven
// confirmation, on purpose — a gate on apply would double-prompt right after the
// user had already approved a plan, which is friction on the tool that is the heart
// of the UX.
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
// (--auto-approve) > session Allow/Ask > team Allow/Ask (where this list lives) >
// ReadOnlyHint > default Ask. turf leaves Ask empty, so no turf tool falls to a
// permission prompt; --auto-approve still approves everything, unchanged.

// preApprovedTurfTools run without a permission prompt: every turf tool. This is
// the single source of truth for the set of tools the CLI knows about
// (turfToolInfo in mcptoolset.go is kept in lock-step with it, enforced by
// mcptoolset_test.go). Confirmation of consequential actions is the agent's job
// via user_prompt (see agentConfirmTurfTools and the persona in agent.go), not the
// permission layer's.
var preApprovedTurfTools = []string{
	// Providers: discovery. A provider added to the configuration is installed by
	// re-running init.
	"turf_provider_search",
	"turf_provider_describe",
	// Configuration: `tofu init` registers the directory and installs its modules
	// and providers. The configuration itself is ordinary Terraform the agent
	// authors with the filesystem write_file/edit_file tools; the persona's edit gate
	// (one confirmation per edit set to the user's pre-existing .tf/.tfvars files)
	// lives there, not here.
	"turf_init",
	// Workspace lifecycle. workspace_delete is server-annotated destructive and
	// irreversible; it is pre-approved like the rest but the persona must obtain an
	// explicit, irreversibility-warning user_prompt before calling it (see
	// agentConfirmTurfTools). workspace_close only stops following the workspace —
	// a convergence holding it goes on in the engine — so it is not destructive.
	"turf_workspace_open",
	"turf_workspace_list",
	"turf_workspace_show",
	"turf_workspace_close",
	"turf_workspace_delete",
	// Runs. up, destroy, refresh, import and invoke each start a convergence and
	// stop at its first approval point: they plan, and apply nothing.
	"turf_up",
	"turf_destroy",
	"turf_refresh",
	"turf_import",
	"turf_invoke",
	// Approval. apply approves the plan a round vended (or answers the turf actions
	// a round asked) — the persona applies the server's approval rule before each
	// call (see agentConfirmTurfTools). cancel ends a convergence: what is in flight
	// finishes and the rest is abandoned, so it authorizes nothing new.
	"turf_apply",
	"turf_cancel",
	// State. show, output and state_list are read-only. taint and untaint edit
	// state but apply nothing: a taint's replace shows up in the next up's plan,
	// which is approved like any other.
	"turf_show",
	"turf_output",
	"turf_state_list",
	"turf_taint",
	"turf_untaint",
	// Skills / docs.
	"turf_skill_deployment",
	"turf_skill_authoring",
	"turf_skill_guide",
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
// cancel, taint and untaint are destructive yet authorize no new change (see
// destructiveNoConfirm in permissions_test.go). Two confirmation shapes, spelled out
// in the persona (see agent.go):
//   - apply is the approval point of every round. The persona follows the server's
//     rule: it approves a low-risk round itself, and consults the user for anything
//     else — always for a destroy plan, and always for an import or invoke plan.
//   - workspace_delete is a standalone op confirmed with its own targeted
//     user_prompt.
var agentConfirmTurfTools = []string{
	"turf_apply",            // applies a round's plan to real infrastructure
	"turf_workspace_delete", // irreversible state deletion — warn it cannot be undone
}
