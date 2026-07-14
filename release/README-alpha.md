# Turf — alpha

Thanks for trying Turf. This is an early **alpha / pre-release** build shared for
evaluation — expect rough edges, and please don't redistribute the binaries.

Turf is a drop-in replacement for Terraform with agentic superpowers: full support
for Terraform HCL and the module registry, driven by an AI agent over the Model
Context Protocol. This archive contains two binaries:

- **`turf`** — the reference command-line client (open source, MPL-2.0).
- **`turf-mcp-server`** — the infrastructure MCP server (the core; evaluation
  license, see `LICENSE`).

The CLI launches the server as a subprocess, so both need to be on your `PATH`.
Homebrew installs them together.

## Install (Homebrew, macOS)

```sh
brew install turfbuild/tap/turf
```

This installs both `turf` and `turf-mcp-server`. Verify:

```sh
turf --version
which turf-mcp-server
```

To upgrade later: `brew upgrade turf`. To remove: `brew uninstall turf`.

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

# Deploy from an HCL configuration directory
turf up ./path/to/config

# Tear it back down
turf destroy ./path/to/config

# Pick a model explicitly
turf --model anthropic/claude-sonnet-4-6 chat
```

Provider plugins are downloaded on demand and cached (shared with `tofu`) under
your user cache dir; no extra setup is required for common providers.

## Verifying this download

Every release artifact is cryptographically signed and recorded in the public
Sigstore transparency log. To verify (optional, but encouraged):

```sh
# The turf CLI — GitHub-native provenance + SBOM:
gh attestation verify turf --repo turfbuild/turf

# The turf-mcp-server binary — cosign, using the bundle shipped on the release:
ID='^https://github.com/turfbuild/turf-mcp-server/'
ISS=https://token.actions.githubusercontent.com
cosign verify-blob-attestation \
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
