package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/docker/docker-agent/pkg/agent"
	"github.com/docker/docker-agent/pkg/config/latest"
	"github.com/docker/docker-agent/pkg/environment"
	"github.com/docker/docker-agent/pkg/memory/database/sqlite"
	"github.com/docker/docker-agent/pkg/model/provider"
	"github.com/docker/docker-agent/pkg/model/provider/providers"
	"github.com/docker/docker-agent/pkg/permissions"
	"github.com/docker/docker-agent/pkg/runtime"
	"github.com/docker/docker-agent/pkg/session"
	"github.com/docker/docker-agent/pkg/team"
	"github.com/docker/docker-agent/pkg/tools"
	"github.com/docker/docker-agent/pkg/tools/builtin/filesystem"
	"github.com/docker/docker-agent/pkg/tools/builtin/memory"
	skillstool "github.com/docker/docker-agent/pkg/tools/builtin/skills"
	"github.com/docker/docker-agent/pkg/tools/builtin/think"
	"github.com/docker/docker-agent/pkg/tools/builtin/todo"
	"github.com/docker/docker-agent/pkg/tools/builtin/userprompt"
	"github.com/docker/docker-agent/pkg/tools/mcp"
)

const mcpServerBinaryName = "turf-mcp-server"

// workflowInstructions is the agent persona for this CLI. The turf MCP server
// vends its own instructions (a short orientation) plus lazy-loaded skill tools
// (skill_core, skill_adhoc, skill_codified) containing the detailed workflow
// guidance, so we do not duplicate that material here.
//
// The turf MCP toolset is registered under the "turf" name (see
// createAgentRuntime), so cagent exposes every one of its tools to the model
// with a "turf_" prefix. The server's own instructions still name its tools
// bare (e.g. skill_core), so the persona spells out the prefix mapping to keep
// the model from calling the unprefixed name.
const workflowInstructions = `You are turf, an infrastructure management agent powered by OpenTofu providers.

The turf MCP server's tools are exposed to you with a turf_ prefix. When the server's instructions name a tool without it (e.g. skill_core), call the prefixed form (turf_skill_core).

Follow the MCP server's instructions and call the turf_skill_* tools when the user's request matches one. Load turf_skill_core at the start of any session; load turf_skill_adhoc for natural-language infrastructure requests — where you author a plot (a lightweight, turf-authored Terraform configuration) via the declare_* tools — and turf_skill_codified for HCL-driven (declarative) flows over user-authored .tf files.

Tool calls you make in a single turn execute one at a time, in the order you emit them. Turf's planning tools are stateful and order-dependent — every call needs an open workspace, each planning call builds on the shared Draft, and ${...} references resolve eagerly against state plus the Draft — so order dependent calls correctly within a batch: workspace_open before provider or planning calls; a resource before any resource or output that references it. If a call's arguments need a previous call's result (not just its effects), do not batch them — wait for that result first.

If the user types /demo or asks to run the demo, call turf_skill_demo and follow its beat-by-beat guided walkthrough, loading a journey with turf_read_skill_file as its hub directs. When the demo content names read_skill_file (bare), always call turf_read_skill_file — the bare read_skill_file is the user-skills loader (different tool, different arguments).

Confirmation is your responsibility, not the tooling's: turf's tools run without a separate permission prompt, so before anything consequential you must obtain an explicit yes/no from the user with the user_prompt tool (named user_prompt, no turf_ prefix), passing an enum schema {"type": "string", "enum": ["yes", "no"], "title": "..."} so they pick rather than free-type. Proceed only on "yes"; treat "no" — or a decline/cancel action in the response — as a refusal and stop, asking how they would like to adjust. There are three shapes:

- Coded-configuration edits — confirm before you change the user's .tf files. When you work a codified (tofu-dialect) configuration — the user's own hand-authored .tf/.tfvars files — obtain one confirmation before you edit them with write_file/edit_file, with message set to a summary of the edits you intend (what you will add, change, or remove, and in which files). Proceed only on "yes", then make that whole set of edits without prompting again; start a fresh confirmation only for a later, materially different change. This precedes and is separate from the plan confirmation below — editing configuration is not applying it, so you still confirm the plan before turf_plan_approve. Ad-hoc plot authoring with the turf_declare_* tools needs no separate edit confirmation; the plan confirmation covers it.

- Phase convergence — confirm the plan once. For the declare → plan_new → plan_approve → effect_apply loop, seek a single confirmation before you call turf_plan_approve, with message set to a one-line summary of everything the plan will change. Once approved, apply and manage the effects (turf_effect_apply, turf_effect_cancel) without prompting again — those execute the plan the user already approved. Never prompt per effect.

- Standalone consequential operations — confirm each call. Any consequential action outside a phase gets its own targeted user_prompt naming the specific target, immediately before the call: turf_workspace_delete (say which workspace, and warn it destroys the state irreversibly — it cannot be undone; require an explicit "yes" and never infer approval from an earlier plan confirmation), turf_resource_import (name the resource address it adopts into state), turf_config_promote (a one-way plot → tofu configuration transformation), and a directly-invoked turf_action_invoke (one line on what the action does). Reconciling state with turf_resource_refresh is benign and needs no confirmation.

Separately, the user may provide their own SKILL.md files (shown in <available_skills>). Those are loaded with read_skill (no turf_ prefix), not the turf_skill_* tools — use them when a request matches a user skill's description. The turf_skill_* tools are turf's built-in infrastructure workflows; read_skill loads the user's personal skills.`

const welcomeMessage = `Welcome to Turf — your infrastructure management agent.

I can help you create, update, and delete cloud infrastructure resources
using OpenTofu providers. I support AWS, GCP, Azure, Kubernetes, and
hundreds of other providers from the OpenTofu registry.

New to Turf? Type /demo for a guided, hands-on walkthrough of workspaces,
plans, Terraform Actions, and more.

What infrastructure would you like to manage?
`

type agentOpts struct {
	model          string
	baseURL        string
	tmpDir         string
	pluginCacheDir string
	welcomeMessage string
	memoryPath     string
	noMemory       bool
	// sessionDBPath is the SQLite session-history database path. Empty resolves
	// to .turf/sessions.db in the session directory (see sessionDBDir). noSession
	// disables persistence entirely, leaving the runtime on its ephemeral
	// in-memory store.
	sessionDBPath string
	noSession     bool
	// sessionDBDir anchors the default session db to a directory other than cwd.
	// It is set to the launch/--chdir dir when a --worktree redirected cwd, so
	// session history lives with the real project rather than inside the
	// throwaway worktree (memory and the recorded WorkingDir still follow cwd
	// into the worktree). Empty means "use cwd" — the no-worktree default, and
	// also ignored when sessionDBPath is set explicitly.
	sessionDBDir string
	logFile      string
	logLevel     string
	logFormat    string
	// allowPaths are extra directories (from --allow-path) the filesystem toolset
	// may access beyond cwd and tmpDir. Because the filesystem tools are
	// pre-approved by name (see permissions.go), widening this sandbox also widens
	// the auto-approved area. Relative entries resolve against the effective cwd.
	allowPaths []string
	// interactive reports whether a human is at the terminal (TTY). It gates
	// tools that elicit input from the user — chiefly user_prompt, which can
	// only function when the TUI is up to render the dialog.
	interactive bool
	// autoApprove reports whether the run was launched with --auto-approve/--yes.
	// When true, the persona's user_prompt confirmations are auto-accepted even in
	// an interactive TTY — the whole point of the flag is to proceed without the
	// plan-confirmation dialog (see the user_prompt wiring below).
	autoApprove bool
}

// resolveMCPServer locates the turf-mcp-server binary. Resolution order:
//  1. explicit --mcp-server flag / TURF_MCP_SERVER env var
//  2. lookup on PATH
//
// Returns a clear, actionable error if the binary cannot be found.
func resolveMCPServer() (string, error) {
	if flagMCPServer != "" {
		if _, err := os.Stat(flagMCPServer); err != nil {
			return "", fmt.Errorf("turf-mcp-server not found at %q: %w", flagMCPServer, err)
		}
		return flagMCPServer, nil
	}
	path, err := exec.LookPath(mcpServerBinaryName)
	if err != nil {
		return "", fmt.Errorf("%s not found on PATH. Install it and ensure it is on PATH, or set TURF_MCP_SERVER / --mcp-server", mcpServerBinaryName)
	}
	return path, nil
}

// createAgentRuntime constructs a cagent agent, team, and runtime that talks to
// the turf MCP server over stdio. It also opens the session-history store (unless
// disabled via opts.noSession) and returns it so callers can resolve a resume
// reference before building the session. The returned cleanup function must be
// called to stop the MCP subprocess and close the session store.
func createAgentRuntime(ctx context.Context, opts agentOpts) (runtime.Runtime, session.Store, *sessionTitleCurator, func(), error) {
	mcpServerPath, err := resolveMCPServer()
	if err != nil {
		return nil, nil, nil, nil, err
	}

	llm, err := newModelProvider(ctx, opts.model, opts.baseURL)
	if err != nil {
		return nil, nil, nil, nil, modelProviderError(opts.model, err)
	}

	serveArgs := []string{"--transport", "stdio"}
	if opts.tmpDir != "" {
		serveArgs = append(serveArgs, "--tmp-dir", opts.tmpDir)
	}
	// Forward the shared provider plugin cache when the user overrides it;
	// otherwise the server applies its own default (<user cache>/turf/plugins),
	// so repeated CLI runs reuse downloaded providers without configuration.
	if opts.pluginCacheDir != "" {
		serveArgs = append(serveArgs, "--plugin-cache-dir", opts.pluginCacheDir)
	}
	if opts.logFile != "" {
		serveArgs = append(serveArgs, "--log-file", opts.logFile)
	}
	if opts.logLevel != "" {
		serveArgs = append(serveArgs, "--log-level", opts.logLevel)
	}
	if opts.logFormat != "" {
		serveArgs = append(serveArgs, "--log-format", opts.logFormat)
	}
	// Register the MCP toolset under the "turf" name so cagent prefixes every
	// exposed tool with "turf_" (skill_core → turf_skill_core, read_skill_file
	// → turf_read_skill_file, …). This namespaces the server's tools and is what
	// prevents a fatal collision: cagent's user-skills toolset (wired below when
	// the user has SKILL.md files) also exposes a read_skill_file, and two tools
	// sharing a name make the provider reject the whole request — Gemini returns
	// 400 "Duplicate function declaration found". The prefix is model-facing
	// only: cagent strips it before dispatching, so the MCP server still sees the
	// bare tool name. The turf-facing label and tool grouping are refined by the
	// turfMCPToolset wrapper below (see mcptoolset.go). The concrete reference is
	// kept for lifecycle calls (Start/Stop); the wrapper goes into the toolsets
	// slice.
	mcpToolset := mcp.NewToolsetCommand("turf", mcpServerPath, serveArgs, os.Environ(), "")

	if err := mcpToolset.Start(ctx); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("starting %s: %w", mcpServerBinaryName, err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		_ = mcpToolset.Stop(ctx)
		return nil, nil, nil, nil, fmt.Errorf("getting working directory: %w", err)
	}
	// Sandbox the agent's own file access to the directories turf legitimately
	// works in: the working directory (HCL configs, plan/state, memory db) and
	// the provider cache dir when one is set. The allow-list is symlink-hardened
	// (I/O is routed through *os.Root), so a symlink inside an allowed root
	// cannot escape it. This governs only the agent's filesystem tool — the
	// turf-mcp-server subprocess does its own I/O outside this boundary.
	allow := []string{cwd}
	if opts.tmpDir != "" {
		allow = append(allow, opts.tmpDir)
	}
	// --allow-path adds extra roots the file tools may reach (and, since those
	// tools are pre-approved by name, that turf auto-approves). Resolve each to an
	// absolute path — relative entries resolve against the effective cwd (post
	// --chdir / --worktree, which both run before this) — and skip empties/dupes.
	seenAllow := map[string]struct{}{}
	for _, a := range allow {
		seenAllow[a] = struct{}{}
	}
	for _, p := range opts.allowPaths {
		if p == "" {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			_ = mcpToolset.Stop(ctx)
			return nil, nil, nil, nil, fmt.Errorf("resolving --allow-path %q: %w", p, err)
		}
		if _, dup := seenAllow[abs]; dup {
			continue
		}
		seenAllow[abs] = struct{}{}
		allow = append(allow, abs)
	}
	fsToolset := filesystem.New(cwd, filesystem.WithAllowList(allow))

	// Wrap each directly-constructed builtin with tools.WithName so status
	// surfaces (the /tools dialog, error messages) show a stable label
	// instead of the Go type fallback (fmt.Sprintf("%T", ts) → "*think.ToolSet").
	// cagent only applies this naming inside its teamloader registry, which
	// turf bypasses by constructing toolsets in code; the names below match the
	// registry's canonical toolset `type:` keys. (mcp and memory advertise
	// Name() natively, so WithName is a no-op for them.)
	toolsets := []tools.ToolSet{
		newTurfMCPToolset(mcpToolset),
		tools.WithName(fsToolset, "filesystem"),
		tools.WithName(think.New(), "think"),
		tools.WithName(todo.New(), "todo"),
	}

	// user_prompt lets the agent pause and ask the user a structured question
	// (free-text, enum, or object schema) — useful for agent-driven remediation
	// decisions where the model needs a choice the MCP workflow can't anticipate.
	// An attended interactive run gets cagent's real tool: the runtime auto-wires
	// the elicitation handler and the TUI renders a dialog.
	//
	// Everything else gets a synthetic user_prompt that auto-confirms directly
	// (see autoconfirm.go). Two cases need it: (1) the non-interactive exec path
	// (up/destroy over a pipe) — cagent's real tool would route through the
	// runtime's elicitation handler, which auto-declines when there is no client
	// to answer, so the /up prompt's "seek approval before plan_approve" step
	// would fail and stall the run before a later (e.g. deferred) phase; (2) an
	// interactive run launched with --auto-approve/--yes — the persona still calls
	// user_prompt before plan_approve, but --yes means "proceed without the
	// confirmation dialog," so we auto-accept it rather than block on a prompt the
	// user has already opted out of. (ToolsApproved/yolo only bypasses the tool
	// PERMISSION gate; it does nothing to a model-invoked user_prompt elicitation.)
	if opts.interactive && !opts.autoApprove {
		toolsets = append(toolsets, tools.WithName(userprompt.New(), "user_prompt"))
	} else {
		toolsets = append(toolsets, tools.WithName(autoConfirmUserPrompt{}, "user_prompt"))
	}

	// User-authored SKILL.md files, discovered from turf-owned locations only
	// (<TURF_HOME>/skills and <cwd>/.turf/skills) — see loadTurfSkills. This
	// exposes the read_skill / read_skill_file tools (plus run_skill for
	// context:fork skills) — a separate namespace from the turf MCP server's
	// skill_* tools, so the two never collide. Wired only when the user
	// actually has skills; with none, the toolset injects no instructions, so
	// there is zero cost.
	if userSkills := loadTurfSkills(cwd); len(userSkills) > 0 {
		toolsets = append(toolsets, tools.WithName(skillstool.New(userSkills, cwd), "skills"))
	}

	if !opts.noMemory {
		memPath := opts.memoryPath
		if memPath == "" {
			// Default under the turf-owned .turf/ dir (alongside skills and the
			// session db), not the project root. Follows cwd — so it lands inside
			// a --worktree, unlike the anchored session store.
			memPath = filepath.Join(cwd, ".turf", "memory.db")
		}
		// The memory opener (ensureDB → sqliteutil.OpenDB) does not create parent
		// dirs and SQLite won't make the .turf/ subdir, so ensure it exists —
		// matters for the default path and any --memory-path outside cwd.
		if err := os.MkdirAll(filepath.Dir(memPath), 0o750); err != nil {
			_ = mcpToolset.Stop(ctx)
			return nil, nil, nil, nil, fmt.Errorf("creating memory database directory: %w", err)
		}
		memDB, err := sqlite.NewMemoryDatabase(memPath)
		if err != nil {
			_ = mcpToolset.Stop(ctx)
			return nil, nil, nil, nil, fmt.Errorf("opening memory database: %w", err)
		}
		toolsets = append(toolsets, memory.New(memDB))
	}

	agentOptsList := []agent.Opt{
		agent.WithModel(llm),
		agent.WithToolSets(toolsets...),
		agent.WithDescription("Infrastructure management agent"),
		agent.WithAddDate(true),
		// Explicitly opt OUT of cagent's pattern-based redact_secrets. cagent
		// only defaults this ON through the YAML/teamloader path, which turf
		// bypasses by constructing the agent in code — so the field is already
		// false by default. We set it explicitly to record the decision: secret
		// handling belongs in the turf MCP server via OpenTofu-native
		// sensitivity ("sensitive" marks / after_sensitive metadata), which is
		// authoritative and shape-aware, not a regex backstop whose effects on
		// infra workflows are an unknown we don't want to take on here. See the
		// "Secret handling" note in CLAUDE.md.
		agent.WithRedactSecrets(false),
	}
	if opts.welcomeMessage != "" {
		agentOptsList = append(agentOptsList, agent.WithWelcomeMessage(opts.welcomeMessage))
	}
	a := agent.New("turf", workflowInstructions, agentOptsList...)

	// Open the SQLite session-history store (unless disabled) and wire it into
	// the runtime. cagent auto-registers a PersistenceObserver for a configured
	// store, so the conversation is persisted (lazily, on first content) with no
	// further plumbing, and the full TUI's /sessions browser lights up for free.
	// The store must exist before runtime.New so WithSessionStore can take it;
	// callers then resolve any resume reference against it (see resolveTurfSession).
	// The default file is .turf/sessions.db in the session directory: cwd, unless
	// opts.sessionDBDir anchors it elsewhere (the launch/--chdir dir under
	// --worktree, so history lives with the real project — unlike the memory db,
	// which follows cwd into the worktree). An explicit --session-db wins over both.
	var sessStore session.Store
	if !opts.noSession {
		sessPath := opts.sessionDBPath
		if sessPath == "" {
			sessDir := opts.sessionDBDir
			if sessDir == "" {
				sessDir = cwd
			}
			sessPath = filepath.Join(sessDir, ".turf", "sessions.db")
		}
		// Ensure the parent dir exists — creates the default .turf/ dir, and
		// matters when --session-db points outside cwd; NewSQLiteSessionStore
		// can't create a missing directory.
		if err := os.MkdirAll(filepath.Dir(sessPath), 0o750); err != nil {
			_ = mcpToolset.Stop(ctx)
			return nil, nil, nil, nil, fmt.Errorf("creating session database directory: %w", err)
		}
		sessStore, err = session.NewSQLiteSessionStore(ctx, sessPath)
		if err != nil {
			_ = mcpToolset.Stop(ctx)
			return nil, nil, nil, nil, fmt.Errorf("opening session database: %w", err)
		}
	}

	// Pre-approve turf's tools (and the builtin memory tools) at the TEAM level.
	// The dispatcher consults session-level then team-level permission checkers
	// (see runtime.tool_dispatch), and the team checker is a property of the
	// runtime, not of any single session — so this pre-approval survives session
	// replacement (/clear, /new, /fork) and resume without per-session re-stamping.
	// Ask/Deny are left empty: any tool absent from Allow (an unknown or
	// third-party MCP tool) still falls to a prompt (fail-safe). --auto-approve
	// (session ToolsApproved) is evaluated first and still overrides everything.
	t := team.New(
		team.WithAgents(a),
		team.WithPermissions(permissions.NewChecker(&latest.PermissionsConfig{
			Allow: preApprovedTools(),
		})),
	)
	// Serialize each model batch's tool calls in emission order (a fork patch;
	// upstream cagent fans batches out in parallel, unconditionally). Turf's
	// planning tools are stateful and order-dependent — every call needs an
	// open workspace, planning calls build on the shared Draft, and ${...}
	// references resolve eagerly — so concurrent execution of a batch races
	// dependent calls into ordering errors. The model still batches calls in
	// one turn; only execution is serialized, so this costs no extra LLM
	// round-trips (unlike parallel_tool_calls=false, which only the
	// OpenAI/DMR providers honor — Gemini, turf's default, ignores it).
	rtOpts := []runtime.Opt{runtime.WithSequentialToolCalls(true)}
	if sessStore != nil {
		rtOpts = append(rtOpts, runtime.WithSessionStore(sessStore))
	}
	// Keep the session title in step with what the agent does: a turf-authored
	// observer that watches turf's tool results and retitles at each infra
	// milestone (plan, apply/destroy complete, promote) via a one-shot LLM call.
	// It layers on top of cagent's built-in first-message titler (still wired in
	// tui.go). It persists titles through the session store (durable — browser,
	// resume, headless up/destroy); the interactive TUI additionally installs a
	// live-refresh emitter (see runTUI/runLeanTUI). Returned to callers so the
	// TUI can wire that emitter after the app is built.
	titleCurator := newSessionTitleCurator(sessStore)
	rtOpts = append(rtOpts, runtime.WithEventObserver(titleCurator))
	rt, err := runtime.New(ctx, t, rtOpts...)
	if err != nil {
		if sessStore != nil {
			_ = sessStore.Close()
		}
		_ = mcpToolset.Stop(ctx)
		return nil, nil, nil, nil, fmt.Errorf("creating runtime: %w", err)
	}

	// The runtime never closes an embedder-owned session store, so cleanup does.
	cleanup := func() {
		_ = mcpToolset.Stop(context.WithoutCancel(ctx))
		if sessStore != nil {
			_ = sessStore.Close()
		}
	}

	return rt, sessStore, titleCurator, cleanup, nil
}

// newModelProvider creates a cagent LLM provider from a "provider/model" string.
//
// It routes through cagent's full provider registry (the same factory cagent's
// own teamloader uses) rather than dispatching to a fixed set of clients. That
// registry resolves every provider cagent supports — cloud (openai, anthropic,
// google, amazon-bedrock), local (dmr / Docker Model Runner), and the many
// OpenAI-compatible aliases (ollama, mistral, groq, deepseek, xai, azure, …) —
// filling in each provider's default base URL, token env var, and API type, and
// expanding ${env.*} references. An unknown provider errors here (rather than
// silently falling through to OpenAI), which surfaces typos instead of hiding
// them behind a confusing "OPENAI_API_KEY required".
//
// baseURL, when set (via --base-url / TURF_MODEL_BASE_URL), overrides the
// endpoint — the escape hatch for arbitrary OpenAI-compatible servers (vLLM,
// LM Studio, gateways). DMR and Ollama resolve their own local endpoints, so
// they need no base URL.
func newModelProvider(ctx context.Context, modelRef, baseURL string) (provider.Provider, error) {
	parts := strings.SplitN(modelRef, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid model format %q, expected provider/model (e.g. google/gemini-pro-latest, anthropic/claude-sonnet-4-6, dmr/ai/qwen3)", modelRef)
	}

	cfg := &latest.ModelConfig{
		Provider: parts[0],
		Model:    parts[1],
		BaseURL:  baseURL,
	}
	return providers.NewDefaultRegistry().New(ctx, cfg, environment.NewDefaultProvider())
}

// modelProviderError wraps a model-provider construction failure with actionable
// guidance. The most common cause at launch is a missing API key for the
// selected provider, so the hint names the model, then points at two ways
// forward — set a key, or (featured) run a keyless local model — plus the Docker
// docs that enumerate every provider.
func modelProviderError(modelRef string, err error) error {
	return fmt.Errorf(`could not start the model %q: %w

turf needs an LLM to run. Either:
  • set your provider's API key, e.g. export ANTHROPIC_API_KEY=… (or OPENAI_API_KEY, GEMINI_API_KEY, …), or
  • run a local model with Docker Model Runner — no API key, no cost:
        turf --model dmr/ai/qwen3

Choosing a provider: https://docs.docker.com/ai/docker-agent/providers/overview/`, modelRef, err)
}

// applyTurfSessionPolicy stamps turf's session-level display and approval flags
// onto a session. It runs both on a freshly created session (newSession) and on
// one loaded from the store on resume (resolveTurfSession); cagent's own resume
// path re-applies only tools-approved and hide-tool-results, so re-stamping them
// here keeps a resumed session consistent with the current --auto-approve flag.
//
// Tool PRE-APPROVAL no longer lives here: it is installed once at the team
// (process) level in createAgentRuntime (see preApprovedTools / the team's
// permission checker), which — unlike a per-session Allow list — survives session
// replacement (/clear, /new, /fork) and resume by construction.
func applyTurfSessionPolicy(sess *session.Session, autoApprove bool) {
	session.WithToolsApproved(autoApprove)(sess)
	// Default to the detailed tool views: turf's custom renderers read
	// HideToolResults() and show the full panels (and raw results for un-painted
	// tools) when it's false. Ctrl+O flips it to collapse each tool to a single
	// colored line. Starting detailed makes tool activity visible without an
	// extra keystroke.
	session.WithHideToolResults(false)(sess)
}

// newSession builds an empty session with turf's permission policy. The active
// user turn is not seeded here; it is delivered by the caller — the TUI via its
// first-message mechanism, the headless path via cli.Run's user messages (see
// runExecWith) — so the prompt is sent exactly once.
//
// The session is stamped with the current working directory (WithWorkingDir).
// cagent leaves Session.WorkingDir empty unless the embedder sets it, and the
// TUI's /sessions browser groups entries by that field ("This workspace" vs
// elsewhere) — so without it, turf's own sessions never group under the dir they
// were created in. turf runs entirely in cwd (after any --chdir / --worktree
// redirect), so os.Getwd() is the right value.
func newSession(autoApprove bool) *session.Session {
	opts := []session.Opt{}
	if cwd, err := os.Getwd(); err == nil {
		opts = append(opts, session.WithWorkingDir(cwd))
	}
	sess := session.New(opts...)
	applyTurfSessionPolicy(sess, autoApprove)
	return sess
}
