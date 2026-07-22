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

When you need the user to confirm a plan before applying it — or to make any yes/no decision — call the user_prompt tool (named user_prompt, no turf_ prefix) with an enum schema so the user picks an option instead of typing a free-text answer. For a plan confirmation, set message to a one-line summary of what will change and pass schema {"type": "string", "enum": ["yes", "no"], "title": "Approve plan?"}. Proceed to turf_plan_approve / turf_effect_apply only when the user picks "yes"; treat "no" — or a decline/cancel action in the response — as a refusal and stop, asking how they would like to adjust.

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
	logFile        string
	logLevel       string
	logFormat      string
	// interactive reports whether a human is at the terminal (TTY). It gates
	// tools that elicit input from the user — chiefly user_prompt, which can
	// only function when the TUI is up to render the dialog.
	interactive bool
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
// the turf MCP server over stdio. The returned cleanup function must be called
// to stop the MCP subprocess.
func createAgentRuntime(ctx context.Context, opts agentOpts) (runtime.Runtime, func(), error) {
	mcpServerPath, err := resolveMCPServer()
	if err != nil {
		return nil, nil, err
	}

	llm, err := newModelProvider(ctx, opts.model, opts.baseURL)
	if err != nil {
		return nil, nil, modelProviderError(opts.model, err)
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
		return nil, nil, fmt.Errorf("starting %s: %w", mcpServerBinaryName, err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		_ = mcpToolset.Stop(ctx)
		return nil, nil, fmt.Errorf("getting working directory: %w", err)
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
	// Interactive runs get cagent's real tool: the runtime auto-wires the
	// elicitation handler and the TUI renders a dialog. In the non-interactive
	// exec path (up/destroy over a pipe, --auto-approve) that same tool would
	// route through the runtime's elicitation handler, which auto-declines when
	// there is no client to answer — so the /up prompt's "seek approval before
	// plan_approve" step fails and the run stalls before applying a later (e.g.
	// deferred) phase. Swap in a synthetic user_prompt that auto-confirms
	// directly, so headless runs proceed (see autoconfirm.go).
	if opts.interactive {
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
			memPath = filepath.Join(cwd, ".turf-memory.db")
		}
		memDB, err := sqlite.NewMemoryDatabase(memPath)
		if err != nil {
			_ = mcpToolset.Stop(ctx)
			return nil, nil, fmt.Errorf("opening memory database: %w", err)
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

	t := team.New(team.WithAgents(a))
	// Serialize each model batch's tool calls in emission order (a fork patch;
	// upstream cagent fans batches out in parallel, unconditionally). Turf's
	// planning tools are stateful and order-dependent — every call needs an
	// open workspace, planning calls build on the shared Draft, and ${...}
	// references resolve eagerly — so concurrent execution of a batch races
	// dependent calls into ordering errors. The model still batches calls in
	// one turn; only execution is serialized, so this costs no extra LLM
	// round-trips (unlike parallel_tool_calls=false, which only the
	// OpenAI/DMR providers honor — Gemini, turf's default, ignores it).
	rt, err := runtime.New(ctx, t, runtime.WithSequentialToolCalls(true))
	if err != nil {
		_ = mcpToolset.Stop(ctx)
		return nil, nil, fmt.Errorf("creating runtime: %w", err)
	}

	cleanup := func() {
		_ = mcpToolset.Stop(context.WithoutCancel(ctx))
	}

	return rt, cleanup, nil
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

// newSession builds an empty session with turf's permission policy. The active
// user turn is not seeded here; it is delivered by the caller — the TUI via its
// first-message mechanism, the headless path via cli.Run's user messages (see
// runExecWith) — so the prompt is sent exactly once.
func newSession(autoApprove bool) *session.Session {
	opts := []session.Opt{}
	opts = append(opts, session.WithToolsApproved(autoApprove))
	// Pre-approve turf's safe tools (reads + Draft-only planning) so they run
	// without a confirmation prompt, while the mutation gate (effect_apply,
	// action_invoke, …) still asks. See permissions.go for the policy and lists.
	// --auto-approve (WithToolsApproved above) still overrides this and approves
	// everything.
	opts = append(opts, session.WithPermissions(&session.PermissionsConfig{
		Allow: preApprovedTurfTools,
		Ask:   alwaysConfirmTurfTools,
	}))
	// Default to the detailed tool views: turf's custom renderers read
	// HideToolResults() and show the full panels (and raw results for un-painted
	// tools) when it's false. Ctrl+O flips it to collapse each tool to a single
	// colored line. Starting detailed makes tool activity visible without an
	// extra keystroke.
	opts = append(opts, session.WithHideToolResults(false))
	return session.New(opts...)
}
