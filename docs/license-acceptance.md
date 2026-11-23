# License acceptance

From release 1.5, Turf's release archive (`turf`, `turf-mcp-server`, `turf-engine`,
`turf-builtin-provider` and `turf-ui`) is proprietary software under the
[Turf Community Edition License 1.0](https://turf.build/license/v1/) (`release/LICENSE`), which
incorporates the Common Paper Software License Agreement Standard Terms 1.1
(`release/STANDARD-TERMS.md`). Turf is free for individuals, education and small businesses within
the Community limits. Government and larger organizations need a commercial license
(https://turf.build/licensing/). Anyone may evaluate Turf, not in production, for 30 days.

The terms are accepted by an explicit act, once per machine or per environment. This page states
how every Turf binary checks for that acceptance. The `turf` CLI's own source in this repository
stays open source under MPL-2.0 (`LICENSE`); what you build from it is governed by MPL-2.0.

## How to accept

Any one of these accepts the terms, checked in this order:

1. **`--accept-eula`**, a flag on every `turf` command. It accepts for that run, and the CLI passes
   it on to the `turf-mcp-server` it starts as `TURF_ACCEPT_EULA=yes`.
2. **`TURF_ACCEPT_EULA=yes`** in the environment. Only `yes` accepts, in any letter case and with
   surrounding spaces trimmed. `true`, `1` and `y` do not: a set but different value is not
   acceptance, and the notice names it on a line of its own.
3. **The acceptance record**, `$TURF_HOME/license.json` (`~/.turf/license.json` unless `TURF_HOME`
   is set), written by `turf license accept` or by answering `yes` to the CLI's first-run question.
   Only `turf` and `turf-mcp-server` read it.

`turf license accept` is itself the acceptance: it asks nothing further. On a terminal, the first
gated `turf` command asks instead: it prints the notice, then `Type yes to accept: `, and records
a `yes`.

`turf license status` shows whether the terms are accepted, how, and when; `turf --version` and
`turf version` show the same in one `Terms:` line. Neither ever fails.

## The notice

Printed on standard error whenever the terms are not accepted:

```
Turf is proprietary software. To accept the Turf license terms (version 1.0,
https://turf.build/license/v1/), run `turf license accept` or set TURF_ACCEPT_EULA=yes.
Free for individuals, education and small businesses within the Community limits.
Government and larger organizations need a commercial license.
```

## The acceptance record

Written only by the `turf` CLI, atomically (a temporary file, then a rename), in a directory of
mode 0700, with mode 0600:

```json
{
  "terms": "turf-community-edition",
  "terms_version": "1.0",
  "terms_url": "https://turf.build/license/v1/",
  "accepted_at": "2026-11-23T10:00:00Z",
  "method": "command",
  "accepted_with": "turf 1.5.0",
  "previous": []
}
```

It is acceptance only if `terms` is `turf-community-edition`, `terms_version` is `MAJOR.MINOR` and
not below `1.0`, `accepted_at` is an RFC 3339 time, and `method` is `command` (`turf license
accept`) or `prompt` (the first-run question). A file that cannot be read, is not JSON, or fails
any of these is not acceptance, and `turf license status` says why. Unknown keys are kept.
Accepting again moves the earlier record into `previous`.

## Without acceptance

| Binary | What it does |
| --- | --- |
| `turf` (any command not listed below) | On a terminal (standard input and standard error are terminals, and `CI` is not set): prints the notice, asks `Type yes to accept: `, records a `yes` and carries on. Anything else, or the end of input, exits 77. Off a terminal, or under CI: prints the notice on standard error and exits **77**. It never asks then. |
| `turf-mcp-server` serving MCP (stdio or HTTP) | Starts, completes the handshake and lists its tools, prompts and resources. Every tool call, prompt and resource read returns the notice and a request to the agent to ask its user, as an error; the server's instructions lead with a "License not accepted" section. It checks the record again on each call, so `turf license accept` takes effect without a restart. |
| `turf-mcp-server engine run` (and the Homebrew service) | Stays up and starts nothing: `turf-mcp-server engine status` reports it awaiting license. It checks the record every 2 seconds and starts the engine once the terms are accepted. In a container (`TURF_DEPLOYMENT=container`) it prints the notice and exits 77. |
| `turf-mcp-server engine console` | Prints the notice and exits 77. |
| `turf-engine`, `turf-ui` | Print the notice and exit 77. They read the flag and the variable only, never the record. |
| `turf-builtin-provider` | Not checked: only the engine starts it. |

Exit code 77 always means the terms are not accepted.

### Always allowed

`--version` and `--help`/`-h` on every binary. In the `turf` CLI also `help`, `completion` (and
cobra's hidden `__complete`, `__completeNoDesc`), `version`, `license` with its subcommands
(`turf license accept`, `turf license status`) and `telemetry`. Also `turf-mcp-server engine
status`, and `turf-engine --identity-keygen`.

## CI and containers

Nothing accepts for you: the Homebrew formula, the container image and the example manifests carry
no acceptance. Accepting is yours to do, where Turf runs:

- **CI:** set `TURF_ACCEPT_EULA: "yes"` in the job's environment. A CI runner never gets the
  question; without the variable each gated command exits 77.
- **Containers:** pass `-e TURF_ACCEPT_EULA=yes` (or the equivalent in your manifest) to every Turf
  container. The record file is not read by `turf-engine` or `turf-ui`.
- **A script:** `turf license accept` once, or `turf --accept-eula …` per run.

## The Homebrew service

A variable set in your shell does not reach the Homebrew service: its environment is `turf.env`
(`~/.homebrew/services/turf.env`), not your shell. `turf license accept` does reach it, because the
service reads `~/.turf/license.json`: a service waiting for acceptance starts within a few seconds
of it. If you set `TURF_HOME` in your shell, the service still reads `~/.turf` unless `TURF_HOME` is
in its `turf.env` too.

## Telemetry

When usage telemetry is on, the engine's `engine.start` event carries `terms_version` (`1.0`) and
`terms_accepted_via` (`env`, `flag`, `command` or `prompt`). Nothing else about acceptance is sent,
and nothing at all when telemetry is off. See [telemetry.md](./telemetry.md).
