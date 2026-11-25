# turf-mcp-server

The MCP server of Turf™, a self-hosted, autonomous infrastructure-as-code tool,
built on OpenTofu®. It gives an AI agent infrastructure-as-code tools over
ordinary Terraform® configurations (`.tf` files): plan, apply, destroy and the
rest of Terraform's commands.

## Run

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

## Requirements

- macOS or Linux, on arm64 or x64. There is no Windows build.
- Node.js 18 or later.

## What gets installed

- A small launcher, which finds the package for your platform and runs it.
- One platform package, `@turfbuild/turf-mcp-server-<os>-<cpu>`, holding the
  server, the Turf engine, its built-in provider and the Turf console. It is an
  optional dependency, so an install that omits optional dependencies leaves
  the server out.
- `restate-server`, which the engine runs on. It is not included in Turf's
  packages: it comes from Restate's own npm package,
  `@restatedev/restate-server`, installed as a dependency.

The first install downloads roughly 150 MB.

The server runs a local Turf engine for the current user, started the first
time a tool needs it. The engine's data lives in `~/.turf`.

## Configuration

Set these in the environment the server starts in.

| Variable | What it does |
|----------|--------------|
| `TURF_HOME` | Where the local engine keeps its data. Default `~/.turf`. Keep the path short. |
| `TURF_RESTATE_URL` | Points the server at an engine running elsewhere, instead of a local one. |
| `TURF_TIER` | Which tools the server offers: `read`, `plan`, `apply` or `full`. Default `full`. |

## Telemetry

The Turf engine sends anonymous usage telemetry to Turf LLC by default.
`DO_NOT_TRACK=1` in the environment turns it off, and so does
`turf telemetry off`. The field list is at https://turf.build/telemetry.

Restate's package, `@restatedev/restate-server`, reports anonymous install
analytics through Scarf at install time. `DO_NOT_TRACK=1` or
`SCARF_ANALYTICS=false` in the environment turns that off.

## Other ways to install

- Homebrew: `brew install restatedev/tap/restate-server turfbuild/tap/turf`
- Container image: `ghcr.io/turfbuild/turf-mcp-server`

## Documentation

- Docs: https://turf.build/docs/
- Issues: https://github.com/turfbuild/turf/issues

See the LICENSE file in this package.

Turf is a trademark of Turf LLC. Terraform is a registered trademark of
HashiCorp, Inc. (an IBM company). OpenTofu is a registered trademark of LF
Projects, LLC.
