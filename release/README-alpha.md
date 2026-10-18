# Turf

Turf™ is a self-hosted, autonomous infrastructure-as-code tool, built on
OpenTofu™. It deploys the stack an AI factory runs on and keeps it that way,
with an agent overseeing.

Turf is free for noncommercial use. Commercial use, including a company's
internal use, needs a license: https://turf.build/licensing.

Turf runs ordinary Terraform® configurations, with the module registry and every
provider, driven by an AI agent over the Model Context Protocol. This archive
contains four binaries:

- **`turf`** — the reference command-line client (open source, MPL-2.0).
- **`turf-mcp-server`** — the infrastructure MCP server (the core; see `LICENSE`).
- **`turf-engine`** and **`turf-builtin-provider`** — the Turf engine, which
  plans and applies, and its built-in provider (same license as the server).

The CLI launches the server as a subprocess, and the server connects to the
engine, which runs on your machine as a service, so all four need to be on
your `PATH`, side by side. The engine runs on **restate-server**, which is not
in this archive: install it from Restate (`brew install
restatedev/tap/restate-server`). Homebrew installs everything together.

## Install (Homebrew, macOS)

```sh
brew install restatedev/tap/restate-server turfbuild/tap/turf
```

This installs `turf`, `turf-mcp-server`, `turf-engine`, `turf-builtin-provider`,
and restate-server from Restate's tap. Homebrew loads a formula from a third-party
tap only when you name it, so the command names both. Verify:

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

Turf is free for noncommercial use under the PolyForm Noncommercial License
1.0.0 (`LICENSE`). Companies can evaluate Turf free for 60 days and license it
for production at https://turf.build/licensing. The `turf` CLI is open source
under MPL-2.0. Bundled third-party components and their licenses are listed in
`NOTICE`, and `turf license` prints both files. Questions: licensing@turf.build.

Turf is the same binary for everyone, with the same features. There is no
license key and nothing to unlock: a commercial license is a contract and a
support relationship.

## Telemetry

Turf sends anonymous usage telemetry to Turf LLC by default: the version,
the platform, counts of resources, changes and runs, and an anonymous
installation id. It never sends resource contents, credentials, state,
variable values or names. The components Turf is built on send nothing.

Turn it off with `turf telemetry off`, then restart the engine
(`brew services restart turf`, or restart `turf-mcp-server engine run`).
`TURF_TELEMETRY=off` or `DO_NOT_TRACK=1` in the engine's environment also
turns it off; for the Homebrew service that environment is `turf.env`, not
your shell. With Homebrew's analytics off (`brew analytics off` or
`HOMEBREW_NO_ANALYTICS`), the service starts with it off. The full field list
and the endpoint are at https://turf.build/telemetry.

Turf™ is a trademark of Turf LLC. Terraform® is a registered trademark of
HashiCorp, Inc. OpenTofu™ is a trademark of The Linux Foundation. Turf is not
affiliated with or endorsed by either.
