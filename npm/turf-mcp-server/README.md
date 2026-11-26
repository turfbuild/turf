# turf-mcp-server

The MCP server of Turf™, a self-hosted, autonomous infrastructure-as-code tool,
built on OpenTofu®. It gives an AI agent infrastructure-as-code tools over
ordinary Terraform® configurations (`.tf` files): plan, apply, destroy and the
rest of Terraform's commands.

## Install

```sh
npx add-mcp@2 @turfbuild/turf-mcp-server --name turf -g
# the first start downloads Turf: about a minute
```

This registers the server with the agents on this machine (Claude Code, Claude
Desktop, Cursor, Codex, Gemini CLI, VS Code): it finds the ones you have and
lets you pick. To run the server yourself:

```sh
npx -y @turfbuild/turf-mcp-server
```

The server speaks MCP over stdio. In an MCP client's configuration:

```json
{
  "mcpServers": {
    "turf": {
      "command": "npx",
      "args": ["-y", "@turfbuild/turf-mcp-server"]
    }
  }
}
```

## Accept the license

Turf is proprietary software under the Turf Community Edition License 1.0
(https://turf.build/license/v1/). It is free for individuals, education and
small businesses within the Community limits. Government and larger
organizations need a commercial license: https://turf.build/licensing/.
Accept the terms once:

```sh
npx -y @turfbuild/turf-mcp-server license accept
```

This records your acceptance in `~/.turf/license.json`. Until then the server
connects and lists its tools, and every call returns the license notice.
Acceptance is read at the next call, with no restart. Whichever npx command
runs Turf first downloads it: run this line before your agent has started Turf
and the minute passes here, in the terminal, and the agent's first start is
quick. `TURF_ACCEPT_EULA=yes`
in the server entry's `env` is the other way: setting it accepts the terms,
for that server and the engine it starts.

## Requirements

- Node.js 18 or later.
- macOS or Linux, on arm64 or x64.

## The engine

The server starts a local Turf engine for the current user when a tool first
needs it. The engine keeps running when the client quits, so a rollout waiting
for approval is still there next time. Its data lives in `~/.turf`.

```sh
npx -y @turfbuild/turf-mcp-server engine status
npx -y @turfbuild/turf-mcp-server engine stop
```

Providers run in the engine, in a small fixed environment. Credentials in
their usual files (the default AWS profile, `~/.kube/config`, gcloud's
application-default credentials, the `az` login) work with nothing set. When a
provider needs a variable, name it in the server entry's `env`: the variable,
and `TURF_PROVIDER_ENV` naming it.

```json
"env": {
  "AWS_PROFILE": "prod",
  "TURF_PROVIDER_ENV": "AWS_PROFILE"
}
```

Restart the agent after a change: the list is read when the server starts.
This is the provider environment, the variables the server sends by name: it
sends them, with their values, when a run starts, and `status` shows the names,
never a value. `PATH` and the engine's own variables are never sent. A
configuration's own settings (a profile or a kubeconfig path that belongs to
that directory) go in its `mise.toml` or `mise.local.toml`, with
[mise](https://mise.jdx.dev) installed.

## First start and upgrades

- The first start takes about a minute: npm downloads about 180 MB and unpacks
  about 550 MB. A client that waits 30 seconds for a server may give up on that
  first start: reconnect once. Later starts take several seconds.
- An unpinned entry asks npm at every start, so a new release is installed the
  next time the client starts the server. That start takes about half a minute.
  The engine that is running stays the old one until it is stopped: the
  `engine stop` line above ends it, and the next call starts the new one.
  Across a minor release (1.5 to 1.6) the server refuses the older engine and
  names that remedy; after a patch release it keeps using the running engine
  and says nothing.
- To hold a version, pin it in the entry: `@turfbuild/turf-mcp-server@1.5.0`.

## What gets installed

- A small launcher, which finds the package for your platform and runs it.
- One platform package, `@turfbuild/turf-mcp-server-<os>-<cpu>`, holding the
  server, the Turf engine, its built-in provider and the Turf console. It is an
  optional dependency, so an install that omits optional dependencies leaves
  the server out.
- `restate-server`, which the engine runs on. It is not included in Turf's
  packages: it comes from Restate's own npm package,
  `@restatedev/restate-server`, installed as a dependency.

The packages are published with npm provenance from the `turfbuild/turf`
repository's publish workflow, and carry the release's own binaries.

## Configuration

Set these in the environment the server starts in: the server entry's `env`.

| Variable | What it does |
|----------|--------------|
| `TURF_HOME` | Where the local engine keeps its data. Default `~/.turf`. Keep the path short. |
| `TURF_RESTATE_URL` | Points the server at an engine running elsewhere, instead of a local one. |
| `TURF_TIER` | Which tools the server offers: `read`, `plan`, `apply` or `full`. Default `full`. |
| `TURF_PROVIDER_ENV` | The variables the server sends to providers, by name: a comma-separated list of `NAME`, `NAME=VALUE` or `PREFIX_*`. Nothing is sent unless named. |

## Telemetry

The Turf engine sends anonymous usage telemetry to Turf LLC by default. To turn
it off, put `DO_NOT_TRACK=1` in the `env` of the server's entry in each agent
that has one, reconnect the agent so its server restarts with it, then run the
`engine stop` line above: the next call starts the engine with telemetry off.
With the `turf` CLI, which comes with the Homebrew install, `turf telemetry
off` and the same `engine stop` do it too. The field list is at
https://turf.build/telemetry.

Restate's package, `@restatedev/restate-server`, reports anonymous install
analytics through Scarf, where npm installs the package: that may be the shell
that runs `license accept`. `DO_NOT_TRACK=1` (or `SCARF_ANALYTICS=false`) in
that shell covers it.

## Other ways to install

- Homebrew, which adds the `turf` CLI and an engine that starts at login:
  `brew install restatedev/tap/restate-server turfbuild/tap/turf`
- Container image: `ghcr.io/turfbuild/turf-mcp-server`

## Documentation

- Docs: https://turf.build/docs/
- Issues: https://github.com/turfbuild/turf/issues

See the LICENSE file in this package.

Turf™ is a trademark of Turf LLC. Terraform® is a registered trademark of
HashiCorp, Inc. OpenTofu® is a registered trademark of LF Projects, LLC. Turf is
not affiliated with or endorsed by either.
