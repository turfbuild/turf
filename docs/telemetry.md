# Telemetry

Turf™ sends anonymous usage telemetry to Turf LLC by default. This page lists everything it
sends, what it never sends, and how to turn it off. turf.build/telemetry is built from it.

## Who sends it

The Turf engine sends it: `turf-engine`, which the `turf` Homebrew service runs. The `turf`
CLI and `turf-mcp-server` send none of their own. The components Turf is built on send nothing
either: restate-server's telemetry and the embedded agent runtime's are always off.

## Turning it off

| Where Turf runs | How |
| --- | --- |
| The Homebrew service | `turf telemetry off`, then `brew services restart turf`. Or put `TURF_TELEMETRY=off` or `DO_NOT_TRACK=1` in `~/.homebrew/services/turf.env` and restart the service. With Homebrew's analytics off (`brew analytics off` or `HOMEBREW_NO_ANALYTICS`), the service starts with telemetry off. |
| `turf-mcp-server engine run` in a terminal | `turf telemetry off` before you start it, or `TURF_TELEMETRY=off` or `DO_NOT_TRACK=1` in that terminal. |
| A container or CI | `TURF_TELEMETRY=off` or `DO_NOT_TRACK=1` in the engine's environment, or `turf-engine --telemetry=off`. |

To install with telemetry off from the start, run the installer with `--no-telemetry`
(`curl -fsSL https://get.turf.build/install.sh | bash -s -- --no-telemetry`), or with
`TURF_TELEMETRY=off` or `DO_NOT_TRACK=1` in your shell. It runs `turf telemetry off` before the
engine starts, so the setting covers the Homebrew service too.

`turf telemetry off` writes the setting to `$TURF_HOME/telemetry.json` (`~/.turf` by default).
`turf-mcp-server engine run`, which the Homebrew service runs, reads it each time it starts the
engine, so a change takes effect at the next start. A setting that is not valid JSON, or whose
`enabled` is not `true` or `false`, starts the engine with telemetry off until
`turf telemetry on` or `off` rewrites it. A `turf-engine` you run yourself, in a container or CI,
reads no file: use the variables or the flag. `TURF_TELEMETRY` takes precedence over
`DO_NOT_TRACK`, and both take precedence over the setting. Setting either in your shell does not
reach the Homebrew service, whose environment is `turf.env`. That file is
`$XDG_CONFIG_HOME/homebrew/services/turf.env` if you set `XDG_CONFIG_HOME`, and Homebrew skips it
if it is group- or world-writable.

`turf telemetry status` shows the setting and your installation id.
`turf-mcp-server engine status` shows whether the running engine sends.
`TURF_TELEMETRY_DEBUG=1` in the engine's environment writes each event to the engine's log
instead of sending it, so you can read exactly what would leave.

## What it sends

There are three events. Each one is a single JSON `POST` to
`https://telemetry.turf.build/v1/events`, whose only headers beyond the HTTP basics are
`Content-Type: application/json` and `User-Agent: turf-engine/<version> (<platform>; <arch>)`.

**On every event:**

| Field | Meaning |
| --- | --- |
| `schema` | The event schema's version, `1` |
| `event` | `engine.start`, `engine.daily` or `convergence` |
| `installation_id` | A random UUID generated for your installation, stored in `telemetry.json`. It identifies an installation, not a person. An engine with no setting, such as one in a container or CI, sends none unless `TURF_INSTALLATION_ID` gives it one. |
| `version` | Turf's version |
| `platform`, `arch` | The operating system and CPU architecture, such as `darwin` and `arm64` |
| `deployment` | `local` (the per-user engine), `container` or `standalone` |
| `install_method` | `homebrew`, `archive` or `container` |
| `ci` | Whether the engine runs under a CI system |

**`engine.start`**, once each time the engine starts:

| Field | Meaning |
| --- | --- |
| `restate_version` | The restate-server version the engine runs on |
| `scopes`, `mise`, `plugin_cache`, `request_identity` | Whether each engine feature is on |

**`engine.daily`**, every 24 hours the engine has been running, idle or not:

| Field | Meaning |
| --- | --- |
| `uptime_days` | Days since the engine started |
| `convergences_24h`, `converged_24h`, `failed_24h` | Runs in the last day: all, finished, failed |
| `workspaces_24h`, `workspaces` | How many workspaces ran in the last day, and since the engine started |
| `managed_resources`, `managed_bucket` | Managed resources across those workspaces, exact and bucketed |

**`convergence`**, once per run (`turf up`, a destroy, a refresh, an import, an action invoke):

| Field | Meaning |
| --- | --- |
| `mode` | `up`, `destroy`, `refresh`, `import`, `invoke`, or `other` |
| `outcome` | `converged`, `stalled`, `exhausted`, `failed`, `declined`, `stale`, `cancelled`, `error`, or `other` |
| `failure` | For a failed run: `plan`, `apply`, `checks`, `refused` or `engine`; else `none` |
| `refused` | For a run refused over a Terraform® construct Turf does not run, the construct's name (such as `provisioners`). Always one of a fixed list, never your configuration's text. |
| `rounds`, `deferred` | How many plan-and-apply rounds the run took, and the changes it deferred between them |
| `auto_approve`, `approvals_asked`, `approvals_auto`, `declined` | How plans were approved |
| `creates`, `updates`, `replaces`, `deletes`, `reads`, `noops`, `taints`, `forgets`, `imports`, `moves`, `drifted` | How many planned changes of each kind, summed over the run's rounds |
| `applied`, `failed`, `cancelled` | How many changes applied, failed or were cancelled |
| `actions`, `actions_failed`, `turf_actions` | How many actions ran, failed, and were Turf's own |
| `providers`, `modules` | The most providers and module instances any round's plan used. Never which. |
| `managed_resources`, `data_sources` | How many the workspace holds afterwards |
| `backend` | The state backend's type, such as `local` or `s3`, one of the backends built into OpenTofu™; any other is `other` |
| `duration_s`, `plan_s`, `apply_s`, `wait_s` | Seconds the run took, spent planning, applying, and waiting for approval |
| `changes_bucket`, `managed_bucket`, `duration_bucket` | The same numbers in ranges, such as `6-25` or `1-5m`; `duration_bucket` is `unknown` for a run that failed before its plan was ready |

## What it never sends

- Resource addresses, names or ids, and the values or contents of anything you manage.
- Workspace names, state, plans, configuration files or paths, and module sources.
- Provider names or registry hosts.
- Variable or output values, credentials, and backend configuration.
- Error messages, diagnostics, prompts and model output.
- Hostnames, user names and cloud account ids.

## Where it goes

Events go to `telemetry.turf.build`, a hostname Turf LLC controls, which Scarf operates for
Turf LLC. Like any HTTP request, an event arrives from your network's IP address. Scarf uses
the address to estimate the organization and the approximate location the event comes from,
then discards it ([how](https://docs.scarf.sh/company-matching/)). For the address, Scarf is
Turf LLC's data processor under a data processing agreement, and acts only on Turf LLC's
instructions. Scarf's terms of use also give Scarf a license to use, and to share, the rest of
what it receives for its own purposes, such as running and improving its services
([section 4](https://about.scarf.sh/terms-of-use/#4-data)). Turf LLC uses events to understand
how Turf is used, to see which installations are active, and to size commercial licenses.

Terraform® is a registered trademark of HashiCorp, Inc. OpenTofu™ is a trademark of The Linux
Foundation. Turf is not affiliated with or endorsed by either.
