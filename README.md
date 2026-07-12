# Turf

A drop-in replacement for Terraform with agentic superpowers: full support for
Terraform HCL and the module registry, driven by an AI agent over the Model
Context Protocol.

This repository hosts the **alpha binary releases** of Turf — the `turf` CLI and
the `turf-mcp-server` — for early testers on macOS.

> **Alpha / pre-release.** This is early evaluation software. Expect rough edges,
> and please don't redistribute the binaries.

## Install (Homebrew, macOS and Linux)

```sh
brew install turfbuild/tap/turf
```

This installs both `turf` and `turf-mcp-server`. Verify:

```sh
turf --version
which turf-mcp-server
```

The CLI drives an LLM via [cagent](https://github.com/docker/cagent), so set the
API key for your provider (e.g. `ANTHROPIC_API_KEY`) before running `turf chat`.
See the README inside the release archive for a quick start.

## Examples

Runnable examples live in
[turfbuild/turf-examples](https://github.com/turfbuild/turf-examples) (private —
access shared with early testers). Each is an ordinary HCL configuration you drive
with `turf -C <dir>`:

- **Kubernetes** — a local kind cluster with a CRD + custom resource, or a Helm
  release (credential-free, Docker only).
- **Azure / GCP** — a multi-instance Azure resource-group module, and a GKE
  Autopilot cluster with custom VPC networking.
- **Language & features** — Terraform Actions, Turf-native actions
  (`turf_confirm` human gates + `turf_action` agent steps), and a
  staged-then-commit pattern.

The repo also carries kagent manifests for deploying `turf-mcp-server` in-cluster.

## Feedback & Support

Have a question, idea, or want to share how you're using Turf? Join the
conversation in
[GitHub Discussions](https://github.com/turfbuild/turf/discussions). Found a bug
or have a feature request? Open an
[issue](https://github.com/turfbuild/turf/issues).

## Licensing

`turf-mcp-server` and Turf as a whole are provided for evaluation under the
[PolyForm Free Trial License 1.0.0](./LICENSE). The `turf` CLI is open source
under the Mozilla Public License 2.0; source is available on request
(eronwright@gmail.com). Turf builds on the OpenTofu provider ecosystem (MPL-2.0);
the downstream forks Turf maintains are published under
[github.com/turfbuild](https://github.com/turfbuild). Bundled third-party
components are credited in the `NOTICE` file inside each release archive.
