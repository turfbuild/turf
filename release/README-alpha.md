# Turf

Turf™ is a self-hosted, autonomous infrastructure-as-code tool, built on
OpenTofu™. It deploys the stack an AI factory runs on and keeps it that way,
with an agent overseeing.

Turf is free for individuals, education and small businesses within the
Community limits. Government and larger organizations need a commercial
license: https://turf.build/licensing/. You accept the license terms
(https://turf.build/license/v1/) once, with `turf license accept`.

Turf runs ordinary Terraform® configurations, with the module registry and every
provider, driven by an AI agent over the Model Context Protocol. This archive
contains five binaries:

- **`turf`** — the reference command-line client (open source, MPL-2.0).
- **`turf-mcp-server`** — the infrastructure MCP server (the core; see `LICENSE`).
- **`turf-engine`** and **`turf-builtin-provider`** — the Turf engine, which
  plans and applies, and its built-in provider (same license as the server).
- **`turf-ui`** — the Turf console, a read-only web view of the engine (same
  license as the server).

The CLI launches the server as a subprocess, and the server starts the engine
on your machine the first time it needs it, so all five need to be on your
`PATH`, side by side. The engine runs on **restate-server**, which is not
in this archive: install it from Restate (`brew install
restatedev/tap/restate-server`). Homebrew installs everything together.

## Install (Homebrew, macOS)

```sh
brew install restatedev/tap/restate-server turfbuild/tap/turf
```

This installs `turf`, `turf-mcp-server`, `turf-engine`, `turf-builtin-provider`,
`turf-ui`, and restate-server from Restate's tap. Homebrew loads a formula from a third-party
tap only when you name it, so the command names both. Verify:

```sh
turf --version
which turf-mcp-server turf-engine restate-server
turf license accept             # accept the license terms, once
```

`turf license accept` accepts the Turf license terms
(https://turf.build/license/v1/; `turf license` prints them) and records it in
`~/.turf/license.json`, which every Turf command and the engine read. Until
then no engine starts (the service, if you run it, waits), and the first `turf`
command in a terminal asks `Type yes to accept: `. In CI or a container, set `TURF_ACCEPT_EULA=yes` in the
environment instead; a variable in your shell does not reach the Homebrew
service. Details: `docs/license-acceptance.md` in the turf repository.

To upgrade later: finish or cancel any rollout waiting for approval, run
`brew upgrade turf`, then restart the engine, which an upgrade leaves running:
`turf-mcp-server engine stop` (the next request starts the new one), or `brew
services restart turf` if you run the service. To remove: `turf-mcp-server
engine stop` (`brew services stop turf` for the service), then `brew uninstall
turf`.

The server alone also installs through npx, with no Homebrew and no `turf` CLI
(Node 18 or later; macOS or Linux). One command registers it with your agents,
and its first start downloads it:
`npx add-mcp@2 @turfbuild/turf-mcp-server --name turf -g`.
Without the CLI, accept the license terms with
`npx -y @turfbuild/turf-mcp-server license accept`.

## The local engine

The first request that needs it starts this user's engine — a `restate-server`,
a `turf-engine` and the Turf console in `~/.turf/engine` — and every server
(each `turf` command, each MCP client) shares it. It keeps running when they
exit, so a rollout waiting for your approval is still there next time.

```sh
turf-mcp-server engine status   # where it runs, how it was started, its console, its logs
turf-mcp-server engine stop     # stop it
turf-mcp-server engine console  # sign this browser in to the console, once
                                # (turf console does the same); a workspace is /w/<lineage>
brew services start turf        # optional: start it at login instead
```

The service takes over an engine a server started, and a terminal can run it in
the foreground: `turf-mcp-server engine run`. `TURF_ENGINE_AUTOSTART=off` in a
server's environment keeps that server from starting it.

The console is read-only, listens on loopback alone (`TURF_CONSOLE_ADDR` moves
it, or turns it `off`), and only you can open it: a browser signs in once with
`turf console`, and the links an agent hands you open directly after.

Providers run in the engine, with a small fixed environment, not your shell's
or your agent's. To hand them a variable, name it for the server: in the
agent's MCP entry for Turf, an `env` block holding `"AWS_PROFILE": "prod"` and
`"TURF_PROVIDER_ENV": "AWS_PROFILE"` is enough for the AWS provider to plan
(`TURF_PROVIDER_ENV` is a comma-separated list of `NAME`, `NAME=VALUE` or
`PREFIX_*`; `PATH` is never sent; restart the agent after a change).
Credentials in files (`~/.aws/config`'s default profile,
`~/.kube/config`, gcloud, `az`) work as anywhere, and so do provider arguments.
With [mise](https://mise.jdx.dev) installed, each configuration's providers
also get its mise environment (`mise.toml`, and `mise.local.toml` for
per-machine settings such as `AWS_PROFILE`): run `mise trust` and `mise
install` in the directory first — trusting a config lets it run its code when
a plan is made. `TURF_MISE=off` turns that off. For anything the engine itself
needs (a proxy, say), run the service and put it in `~/.homebrew/services/turf.env`
(`KEY=value` lines), followed by `brew services restart turf`. To use an engine
you run yourself, set `TURF_RESTATE_URL` to its restate-server ingress.

## Quick start: from your agent

Register the server with the agent you already use — Claude Code, Claude Desktop,
Cursor, Codex, Gemini CLI, VS Code — in one command. It detects the agents on this
machine, lets you pick, and writes each config with the server's absolute path
(Node 18+; add-mcp is Neon's open-source installer). Without Node, the agent's own
command does the same, e.g. `claude mcp add turf --scope user -- turf-mcp-server`.

```sh
npx add-mcp@2 "$(command -v turf-mcp-server)" --name turf -g
```

In Claude Code and Claude Desktop, Turf runs on your Claude subscription — no model
API key. Turf's prompts are slash commands there (arguments are one word each):

```
/turf:guide                      # a use-case walkthrough on a real example
/turf:up path/to/config          # deploy or reconcile a configuration
/turf:destroy path/to/config     # tear it down
```

Every other client takes the same requests in words. Per-client setup:
https://github.com/turfbuild#connect-your-agent.

## Quick start: the Turf CLI

The CLI is Turf's own agent: the same server and engine, driven by a model you bring
via [Docker Agent](https://github.com/docker/docker-agent). Supply the API key for
whichever provider you use, in the environment:

```sh
export ANTHROPIC_API_KEY=...     # for --model anthropic/claude-...
# or OPENAI_API_KEY / GOOGLE_API_KEY, etc.
```

```sh
# Interactive infrastructure management (TUI)
turf chat

# Deploy the HCL configuration in a directory (the current one, or -C <dir>)
turf -C ./path/to/config up

# Words after up/destroy steer the plan; they never bypass approval
turf -C ./path/to/config up replace aws_s3_bucket.assets

# Tear it back down
turf -C ./path/to/config destroy

# Unattended (CI, a pipe): nothing that needs your yes is applied without --yes
turf -C ./path/to/config up --yes

# Pick a model explicitly
turf --model anthropic/claude-sonnet-5 chat
```

Provider plugins are downloaded on demand and cached (shared with `tofu`) under
your user cache dir; no extra setup is required for common providers.

## Verifying this download

Every release artifact is cryptographically signed and recorded in the public
Sigstore transparency log. To verify (optional, but encouraged):

```sh
# The turf CLI — GitHub-native provenance + SBOM:
gh attestation verify turf --repo turfbuild/turf

# The turf-mcp-server binary — cosign, using the bundle shipped on the release
# (turf-engine and turf-builtin-provider verify the same way, with their own bundles):
ID='^https://github.com/turfbuild/turf-mcp-server/'
ISS=https://token.actions.githubusercontent.com
cosign verify-blob-attestation --type slsaprovenance1 \
  --bundle turf-mcp-server-<os>_<arch>.provenance.sigstore.json \
  --certificate-identity-regexp "$ID" --certificate-oidc-issuer "$ISS" \
  turf-mcp-server-<os>_<arch>
```

`checksums.txt` on the release is the no-tooling fallback. Full details and the
container-image recipe are in the project's `SECURITY.md`.

## Licensing

Turf is proprietary software under the Turf Community Edition License 1.0
(`LICENSE`, https://turf.build/license/v1/), which incorporates the Common
Paper Software License Agreement Standard Terms 1.1 (`STANDARD-TERMS.md`).
It is free for individuals, education and small businesses within the
Community limits. Government and larger organizations need a commercial
license: https://turf.build/licensing/. Anyone may evaluate Turf, not in
production, for 30 days. Accept the terms with `turf license accept` (or
`TURF_ACCEPT_EULA=yes`). The `turf` CLI is also open source under MPL-2.0.
Bundled third-party components and their licenses are listed in `NOTICE`, and
`turf license` prints all three files. Questions: licensing@turf.build.

Turf is the same binary for everyone, with the same features. There is no
license key and nothing to unlock: accepting the terms is a single command, and
a commercial license is a contract and a support relationship.

## Telemetry

Turf sends anonymous usage telemetry to Turf LLC by default: the version,
the platform, counts of resources, changes and runs, and an anonymous
installation id. It never sends resource contents, credentials, state,
variable values or names. The components Turf is built on send nothing.

Turn it off with `turf telemetry off`, then restart the engine
(`turf-mcp-server engine stop`, and the next request starts it again; `brew
services restart turf` for the service). `TURF_TELEMETRY=off` or
`DO_NOT_TRACK=1` in the engine's environment also turns it off: an engine a
server starts takes them from that server's environment; for the Homebrew
service that environment is `turf.env`, not your shell. With Homebrew's analytics off (`brew analytics off` or
`HOMEBREW_NO_ANALYTICS`), the service starts with it off. Under CI the engine
sends no installation id unless `TURF_INSTALLATION_ID` gives it one. The full
field list and the endpoint are at https://turf.build/telemetry.

Turf™ is a trademark of Turf LLC. Terraform® is a registered trademark of
HashiCorp, Inc. OpenTofu™ is a trademark of The Linux Foundation. Turf is not
affiliated with or endorsed by either.
