# turf

[![CI](https://github.com/turfbuild/turf/actions/workflows/ci.yml/badge.svg)](https://github.com/turfbuild/turf/actions/workflows/ci.yml)
[![Security](https://github.com/turfbuild/turf/actions/workflows/security.yml/badge.svg)](https://github.com/turfbuild/turf/actions/workflows/security.yml)

A reference AI-powered infrastructure management CLI built on the **turf MCP
server** — a drop-in replacement for
Terraform with agentic superpowers: full support for Terraform HCL and the
module registry, driven by an AI agent. `turf` drives the server through
[cagent](https://github.com/docker/docker-agent) to plan and apply changes against
any OpenTofu provider.

It is intended as a showcase: a small, readable example of how to wrap the
turf MCP server in a polished UX. The server is the product; this CLI is one
way to consume it.

## Install

### Homebrew (recommended) — macOS and Linux

Installs both the `turf` CLI and the `turf-mcp-server` binary from the alpha
release:

```sh
brew install turfbuild/tap/turf
turf --version
which turf-mcp-server
```

> **Alpha / pre-release.** This is early evaluation software. Expect rough
> edges, and please don't redistribute the binaries.

### From source

Build just the CLI from this repository (the source is MPL-2.0):

```sh
go install github.com/turfbuild/turf@latest   # → a `turf` binary
# or, from a clone:
make build                                     # → bin/turf
```

The CLI launches `turf-mcp-server` as a subprocess and must be able to find
it. Install the server separately and ensure it is on `PATH`:

```sh
# Verify the server is reachable
which turf-mcp-server

# Or point the CLI at a specific binary
export TURF_MCP_SERVER=/path/to/turf-mcp-server
```

## Usage

```sh
# Interactive infrastructure management (TUI)
turf chat

# Start the guided product demo (type inside the session)
turf chat  # then: /demo

# Deploy from HCL configuration in the current directory
turf up

# ...or target another directory (like `tofu -chdir=DIR`)
turf -C ./examples/azure-storage-container up

# Destroy everything in a configuration
turf -C ./examples/azure-storage-container destroy

# Use a different model
turf --model anthropic/claude-sonnet-4-6 chat

# Run a local model — no API key, no cost (see Model providers)
turf --model dmr/ai/qwen3 chat

# Run a one-shot request without the TUI (see Scripting with exec)
turf exec "what workspaces exist?"
```

Type `/demo` inside `turf chat` to launch the guided walkthrough. It covers workspaces, remote backends, registry modules, Terraform Actions, and a live Kubernetes stack converged via deferrals. **Showcase** is the recommended starting point — a fast, cross-cutting grand tour — but you can jump straight to any deep-dive topic (`/demo actions`, `/demo deferrals`, etc.).

### Flags

| Flag                  | Env                | Default                       | Description                                      |
|-----------------------|--------------------|-------------------------------|--------------------------------------------------|
| `--model`             | `TURF_MODEL`       | `google/gemini-pro-latest`    | LLM as `provider/model` (see [Model providers](#model-providers)) |
| `--base-url`          | `TURF_MODEL_BASE_URL` | _(provider default)_       | Model endpoint for OpenAI-compatible servers (vLLM, LM Studio, gateways) |
| `--mcp-server`        | `TURF_MCP_SERVER`  | _(PATH lookup)_               | Path to the `turf-mcp-server` binary             |
| `--tmp-dir`           | `TURF_TMP_DIR`     | system temp                   | Cache directory for downloaded provider binaries |
| `--memory-path`       |                    | `./.turf/memory.db`           | SQLite memory database                           |
| `--no-memory`         |                    | `false`                       | Disable persistent agent memory                  |
| `--session-db`        | `TURF_SESSION_DB`  | `./.turf/sessions.db`         | SQLite session-history database (`chat`/`exec` resume via `--session`/`--continue`) |
| `--no-session`        |                    | `false`                       | Disable session persistence and resume           |
| `--chdir`, `-C`       |                    |                               | Switch to this directory before running          |
| `--log-file`          | `TF_LOG_PATH`      | _(stderr)_                    | Write server logs to this file                   |
| `--log-level`         | `TF_LOG_CORE`, `TF_LOG` | `info`                   | `trace`/`debug`/`info`/`warn`/`error`/`off`      |
| `--log-format`        | `TF_LOG` `-JSON` suffix | `text`                   | `text` or `json`                                 |
| `--theme`             | `TURF_THEME`       | `calm-roots`                  | TUI theme name; overrides the saved choice for this run |

The three `--log-*` flags are pass-throughs to the `turf-mcp-server` subprocess. When unset, the `TF_LOG_*` env vars (matching OpenTofu's convention) reach the server through environment inheritance. Provider plugins also inherit these vars, so `TF_LOG_PROVIDER=DEBUG` enables plugin-side debug logging into the same sink as the server.

### Scripting with `exec`

`turf exec` runs a **single** natural-language request against the model (set
with `--model`) without launching the TUI, streaming the run to stdout. It is
the entry point for driving turf from a script, a CI job, or another agent — the
same real model and tools as `chat`, just headless.

```sh
# A message argument — quoting is optional; words are joined into one request
turf exec create a random_pet named demo
turf exec "create a random_pet named demo"

# Use -- to pass a message that begins with a dash
turf exec -- --dry-run first, then apply

# Read the request from stdin
echo "what workspaces exist?" | turf exec
turf exec - < request.txt
```

The process **exits non-zero if the run fails**, so `exec` composes with normal
shell error handling (`set -e`, `&&`, CI steps).

**`--json` — machine-readable event stream.** With `--json`, turf emits one JSON
object per line for every runtime event (agent text, tool calls, tool results,
warnings, errors) instead of formatted text. This is the reliable way to assert
on a run from a script even though model prose is non-deterministic — you match
on structure (which tools ran, whether an error occurred), not wording:

```sh
# Which turf tools did the run invoke?
turf exec --json "create a random_pet" | jq -r 'select(.type=="tool_call") | .tool_call.function.name'

# Fail if any error event was emitted
turf exec --json "reconcile the stack" | jq -e 'select(.type=="error")' && echo "run had errors" >&2
```

**`--yes` / `--auto-approve` — unattended runs.** `exec` is non-interactive, so
plan-approval questions are auto-confirmed, but the mutation gate (apply,
imports, refresh) still asks on stdin by default. Pass `--yes` to auto-approve
those so a run proceeds with no human at the keyboard. Use `--hide-tool-calls`
to print only the agent's prose.

> `exec` still launches `turf-mcp-server`, so it must be on `PATH` (see
> [Install](#install)). For repeatable runs with no cloud credentials or cost,
> point it at HCL using OpenTofu's credential-free providers (`null`, `local`,
> `random`, `tls`).

## Model providers

turf runs on any model provider that Docker's [docker-agent] runtime supports —
select one with `--model provider/model` (env `TURF_MODEL`). The default is
`google/gemini-pro-latest`. Docker's [providers overview] lists the full matrix
and each provider's exact configuration.

> **turf needs a tool-calling model.** Every turf action runs through tools, so
> the model *must* support tool (function) calling. The cloud models below all
> qualify; for local models, choose a tool-capable one (see below) — a model
> without it fails with `does not support tools`.

### Local models — no API key, no cost

Run the model entirely on your own machine. This is the fastest way to try turf,
and it keeps your infrastructure prompts off third-party APIs.

**Docker Model Runner** (bundled with Docker Desktop):

```sh
docker model pull ai/qwen3     # once — fetch the model
turf --model dmr/ai/qwen3      # run turf against it
```

turf discovers the local Model Runner automatically; set `MODEL_RUNNER_HOST` to
point at a remote runner. See Docker's [Model Runner guide][dmr].

**Ollama** (if you already run it):

```sh
turf --model ollama/llama3.2   # defaults to http://localhost:11434
```

Pick a **tool-capable** local model — DMR's `ai/qwen3`, or on Ollama one of
`llama3.1`/`llama3.2`, `qwen2.5`, or the `mistral` family. Older or smaller
builds (plain `llama3`, tiny 1B GGUFs) often lack tool support and fail with
`does not support tools`.

### Cloud providers

Export the matching API key, then select the model. A representative set:

| `--model` prefix | Example `--model`                    | API key env var                      |
|------------------|--------------------------------------|--------------------------------------|
| `anthropic`      | `anthropic/claude-sonnet-4-6`        | `ANTHROPIC_API_KEY`                  |
| `openai`         | `openai/gpt-5`                       | `OPENAI_API_KEY`                     |
| `google`         | `google/gemini-pro-latest` (default) | `GEMINI_API_KEY` or `GOOGLE_API_KEY` |
| `mistral`        | `mistral/mistral-large-latest`       | `MISTRAL_API_KEY`                    |
| `groq`           | `groq/llama-3.3-70b-versatile`       | `GROQ_API_KEY`                       |
| `deepseek`       | `deepseek/deepseek-chat`             | `DEEPSEEK_API_KEY`                   |
| `xai`            | `xai/grok-4`                         | `XAI_API_KEY`                        |
| `amazon-bedrock` | `amazon-bedrock/anthropic.claude-3-5-sonnet-20241022-v2:0` | standard AWS credentials |

turf inherits docker-agent's full provider list — including Azure OpenAI and
GitHub Copilot. See the [providers overview] for every option.

### Custom OpenAI-compatible endpoints

For any OpenAI-compatible server (vLLM, LM Studio, an internal gateway), use the
`openai` provider and override the endpoint with `--base-url` (env
`TURF_MODEL_BASE_URL`):

```sh
turf --model openai/my-model --base-url http://localhost:8000/v1
```

[docker-agent]: https://docs.docker.com/ai/docker-agent/
[providers overview]: https://docs.docker.com/ai/docker-agent/providers/overview/
[dmr]: https://docs.docker.com/ai/docker-agent/dmr/

## Troubleshooting

### turf won't start

**`could not start the model … API key … is required`** — turf could not reach an
LLM. Either set the key for your provider (see [Model providers](#model-providers))
or switch to a keyless local model:

```sh
turf --model dmr/ai/qwen3
```

**`unknown provider …` / `invalid model format …`** — `--model` must be
`provider/model` (e.g. `anthropic/claude-sonnet-4-6`, `dmr/ai/qwen3`). Check the
prefix against the [providers overview].

**`turf-mcp-server not found on PATH`** — the CLI launches the server as a
subprocess and must find it. Install it and put it on `PATH`, or point at it
explicitly (see [Install](#install)):

```sh
export TURF_MCP_SERVER=/path/to/turf-mcp-server
```

### `model failed: … does not support tools`

turf drives everything through tools, so the model must support tool (function)
calling — many local models don't. Switch to a tool-capable model: DMR's
`ai/qwen3`, or on Ollama one of `llama3.1`/`llama3.2`, `qwen2.5`, or `mistral`
rather than plain `llama3`. See [Model providers](#model-providers).

## Theming

The TUI ships with the **calm-roots** theme by default. Themes are loaded
and saved from turf's own config home — `~/.turf` (override with `TURF_HOME`) —
so turf never reads or writes `~/.cagent`.

To use a custom theme, drop a YAML file at `~/.turf/themes/<name>.yaml` and pick
it inside the TUI with the `/theme` command. A partial file is merged onto the
built-in default, so you only specify the colors you want to change. Edits
hot-reload while the `/theme` picker is open, and your selection persists across
restarts.

To preview a theme without changing your saved choice, launch with `--theme`
(or set `TURF_THEME`):

```sh
turf --theme tokyo-night chat
```

```yaml
# ~/.turf/themes/my-brand.yaml
version: 1
name: "My Brand"
colors:
  text_primary: "#C0C0C0"
markdown:
  heading: "#7AA2F7"
  link: "#7AA2F7"
```

## Skills

You can teach turf your own procedures by dropping `SKILL.md` files into
turf-owned locations. Each skill is a directory:

```
~/.turf/skills/<name>/SKILL.md          # global: org best practices, migration playbooks
<project>/.turf/skills/<name>/SKILL.md  # project: versioned alongside your infra config
```

turf scans **only** these two locations (override the global one with
`TURF_HOME`) — it never reads `~/.claude`, `~/.codex`, or `~/.agents`. A project
skill overrides a global skill of the same name. The working-dir location is
exactly `<cwd>/.turf/skills` — there is no walk up the tree, so what loads is
predictable from where you launch turf.

A `SKILL.md` needs YAML frontmatter with at least `name` and `description` (the
description is what turf matches a request against). Keep the body lean and push
detail into supporting files, loaded on demand:

```markdown
---
name: azure-migration
description: Adopt hand-built Azure resources into managed state. Use when the user says "adopt" or "import our existing".
# context: fork   # optional — run this skill as an isolated sub-agent
---

# Azure migration playbook

Adopt-then-reconcile; never create before importing.

1. Import each existing resource (ID formats: see references/import-ids.md).
2. Plan — a `=` no-op means clean adoption; `~`/`±` means drift to reconcile.
```

```
~/.turf/skills/azure-migration/
  SKILL.md
  references/
    import-ids.md      # loaded on demand when the skill needs it
```

These are separate from turf's built-in infrastructure workflows (which the
agent already knows) — your skills add to them, they don't replace them.

## Architecture

```
turf (this binary)
   │
   │  cagent runtime + LLM (local via Docker Model Runner / Ollama,
   │                        or cloud: Anthropic / OpenAI / Google / …)
   │
   ▼
turf-mcp-server (separate binary, on PATH)
   │
   ▼
OpenTofu providers (downloaded on demand)
```

The CLI never imports turf's server code. The only contract is the MCP
protocol spoken over stdio.

## Examples

Runnable examples live in
[turfbuild/turf-examples](https://github.com/turfbuild/turf-examples) — ordinary
HCL configurations you drive with `turf -C <dir>`: a local Kubernetes kind
cluster (CRD + custom resource, or a Helm release; credential-free, Docker
only), Azure and GCP modules, and language/feature tours (Terraform Actions,
Turf-native actions).

## License

The `turf` CLI — the source in this repository — is the **Covered Software**,
licensed under the **Mozilla Public License 2.0 (MPL-2.0)**. See
[LICENSE](./LICENSE). Copyright © The Turf Authors.

**Turf as a whole** — this CLI combined with the proprietary `turf-mcp-server`
and distributed together as the alpha binaries — is the MPL-2.0 **Larger Work**.
Those distributed binaries are offered under a separate **Pre-Release Evaluation
License**, which is bundled inside each release archive (and installed alongside
the Homebrew formula). MPL-2.0 expressly permits combining the Covered Software
into such a Larger Work under other terms.
