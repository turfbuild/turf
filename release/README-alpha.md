# Turf — alpha

Thanks for trying Turf. This is an early **alpha / pre-release** build shared for
evaluation — expect rough edges, and please don't redistribute the binaries.

Turf is a drop-in replacement for Terraform with agentic superpowers: full support
for Terraform HCL and the module registry, driven by an AI agent over the Model
Context Protocol. This archive contains four binaries:

- **`turf`** — the reference command-line client (open source, MPL-2.0).
- **`turf-mcp-server`** — the infrastructure MCP server (the core; evaluation
  license, see `LICENSE`).
- **`turf-engine`** and **`turf-builtin-provider`** — the Turf engine, which
  plans and applies, and its built-in provider (same license as the server).

The CLI launches the server as a subprocess, and the server connects to the
engine, which runs on your machine as a service, so all four need to be on
your `PATH`, side by side. The engine runs on **restate-server**, which is not
in this archive: install it from Restate (`brew install
restatedev/tap/restate-server`). Homebrew installs everything together.

## Install (Homebrew, macOS)

```sh
brew install turfbuild/tap/turf
```

This installs `turf`, `turf-mcp-server`, `turf-engine`, `turf-builtin-provider`,
and restate-server from Restate's tap. Verify:

```sh
turf --version
which turf-mcp-server turf-engine restate-server
brew services start turf        # the local engine, started at login from now on
```

To upgrade later: finish or cancel any rollout waiting for approval, run
`brew upgrade turf`, then `brew services restart turf` (an upgrade does not
restart a running service). To remove: `brew services stop turf`, then `brew
uninstall turf`.

## The local engine

`brew services start turf` runs this user's engine — a `restate-server` and a
`turf-engine` in `~/.turf/engine` — and every server (each `turf` command,
each MCP client) connects to it. It keeps running when they exit, so a rollout
waiting for your approval is still there next time. Without the service, a
terminal can run it in the foreground instead: `turf-mcp-server engine run`.

```sh
turf-mcp-server engine status   # where it runs, its console, its logs
```

Providers run in the engine, with the service's environment. Credentials in
files (`~/.aws/config`'s default profile, `~/.kube/config`) work as anywhere;
anything else goes in `~/.homebrew/services/turf.env` (`KEY=value` lines),
followed by `brew services restart turf`. With [mise](https://mise.jdx.dev)
installed, each configuration's providers also get its mise environment
(`mise.toml`, and `mise.local.toml` for per-machine settings such as
`AWS_PROFILE`): run `mise trust` and `mise install` in the directory first —
trusting a config lets it run its code when a plan is made. `TURF_MISE=off` in
`turf.env` turns that off. To use an engine you run yourself, set
`TURF_RESTATE_URL` to its restate-server ingress.

## Model credentials

The CLI drives an LLM via [Docker Agent](https://github.com/docker/docker-agent). Supply the
API key for whichever provider you use, in the environment:

```sh
export ANTHROPIC_API_KEY=...     # for --model anthropic/claude-...
# or OPENAI_API_KEY / GOOGLE_API_KEY, etc.
```

## Quick start

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

`turf-mcp-server` and Turf as a whole are provided for evaluation under the
PolyForm Free Trial License 1.0.0 (`LICENSE`). The `turf` CLI is open source
under MPL-2.0. Bundled third-party components and how to obtain their source are
listed in `NOTICE`. Source for the MPL-2.0 CLI is available on request —
eronwright@gmail.com.

## Feedback

This is an alpha for a small group — bug reports and impressions are very welcome.
Reach out directly.
