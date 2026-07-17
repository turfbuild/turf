package main

// Tool pre-approval policy.
//
// turf's real safety boundary is the plan-then-approve gate: nothing mutates
// live infrastructure until the user approves a plan and an effect is applied.
// Everything upstream of that — reading state, searching providers, and building
// up the in-memory *Draft* via the *_plan / *_init tools — has no effect on real
// infra or persisted state and is safe to run without a confirmation prompt.
//
// These two lists encode that as an explicit, per-tool session permission policy
// (wired in newSession via session.WithPermissions). Names are the LLM-facing,
// turf_-prefixed tool names (the MCP toolset is registered under the "turf" name,
// so cagent prefixes every server tool with "turf_"; see agent.go / mcptoolset.go).
//
// Precedence in cagent's approval pipeline (see runtime/toolexec): yolo
// (--auto-approve) > session Allow/Ask > ReadOnlyHint > default Ask. So:
//   - --auto-approve still approves everything, unchanged.
//   - Otherwise, preApprovedTurfTools run without a prompt.
//   - alwaysConfirmTurfTools fall to Ask (prompt). Listing them in Ask is
//     belt-and-suspenders: Ask (ForceAsk) also overrides ReadOnlyHint, so a gate
//     tool can never be silently auto-approved even if the server later marks it
//     read-only by mistake.
//
// This is the CLI's own policy, deliberately a static curated list rather than
// derived from the server's MCP annotations: a new/unknown server tool is absent
// from Allow and therefore prompts (fail-safe — the worst case is over-prompting,
// never under-prompting).

// preApprovedTurfTools run without a confirmation prompt: pure reads and
// Draft-only planning that authorize no new change to live infrastructure —
// plus workspace_close (see below), the one deliberate exception.
var preApprovedTurfTools = []string{
	// Providers: discovery + (in-memory) configuration.
	"turf_provider_search",
	"turf_provider_describe",
	"turf_provider_load",
	"turf_provider_configure",
	// Workspace: open (acquire lock), read, and close (flush + release lock).
	// workspace_close is server-annotated destructive because it persists state —
	// but that state is the result of already-approved effects, so closing
	// authorizes nothing new. It's the bookend of workspace_open (also pre-approved)
	// and the server tells the agent to "always call it when done", so prompting
	// there is pure friction. This is the sole "destructive but pre-authorized"
	// exception; it's tracked explicitly in permissions_test.go so the guard still
	// blocks every other destructive tool. (workspace_delete — true, irreversible
	// deletion — stays in alwaysConfirmTurfTools.)
	"turf_workspace_open",
	"turf_workspace_list",
	"turf_workspace_show",
	"turf_workspace_close",
	// State / outputs: read-only.
	"turf_state_list",
	"turf_outputs",
	"turf_declare_outputs",
	"turf_module_outputs",
	// Data source: read-only.
	"turf_datasource_read",
	// Draft lifecycle.
	"turf_plan_new",
	"turf_plan_cancel",
	// Plan approval — seals the in-memory Draft into an Execution; it authorizes
	// no live-infra change (the mutation still happens at effect_apply, which
	// stays behind confirmation) and the server annotates it destructiveHint=false.
	// The model already gates approval upstream via the built-in user_prompt tool
	// (a yes/no dialog, per the /up and /destroy persona), so prompting again on
	// plan_approve is a redundant second confirmation for what the user just
	// approved. Pre-approve it to keep user_prompt the single checkpoint.
	// (Future: have plan_approve elicit via MCP directly, then take it back off.)
	"turf_plan_approve",
	// Config / module authoring. The declare family writes through into the
	// user's configuration directory as .tf.json files — a checkout mutation,
	// not a live-infra one: the files are git-recoverable, plan_cancel reports
	// touched_files, and no infrastructure changes until an approved effect
	// applies. Prompting on every declare would gate the whole planning loop.
	"turf_config_init",
	"turf_config_show",
	"turf_declare_backend",
	"turf_declare_provider",
	"turf_replan",
	"turf_module_init",
	"turf_declare_module",
	// Resource / action planning — accumulates into the Draft only.
	"turf_declare_resource",
	"turf_declare_var",
	"turf_declare_action",
	// Skills / docs.
	"turf_skill_core",
	"turf_skill_adhoc",
	"turf_skill_codified",
	"turf_skill_demo",
	"turf_read_skill_file",
}

// alwaysConfirmTurfTools always require confirmation: they mutate live infra,
// write persisted state, or cross the plan-approval review gate.
var alwaysConfirmTurfTools = []string{
	"turf_effect_apply",     // the only tool that applies real infra changes
	"turf_action_invoke",    // imperative side effects
	"turf_effect_cancel",    // can cascade-cancel dependents
	"turf_workspace_delete", // irreversible state deletion
	"turf_resource_import",  // adopts existing infra into state
	"turf_resource_refresh", // writes reconciled state
}
