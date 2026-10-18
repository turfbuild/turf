# CLI release gate

A repeatable, hand-driven walk over the `turf` CLI as a user meets it: the released tarball in a
real terminal, a real model driving the server through the persona, and real approval dialogs.
It runs against a fresh `TURF_HOME` and its own local engine. It is the CLI's counterpart to
doof's [manual MCP suite](../../../doof/docs/development/testing/manual-mcp-e2e-suite.md), which
drives the same server through Claude Code.

**What this suite owns, and what it leaves to others:**

| Concern | Where it is tested |
|---|---|
| Renderer output for recorded v1 payloads, permission lists, session titles, config merge, the login protocol, model resolution | `go test ./...` in CI |
| The CLI's lock-step with the server: tool names, destructive annotations, target-arg schemas, titled tools | the live drift tests. **They skip in CI** (no server there), so P.5 runs them against the release's server |
| What the server's tools do: the approval loop, supersede, cancel, taint, turf actions, durability, redaction | doof's manual MCP suite and `make test-e2e` |
| **This suite:** the release artifact and its docs; the TUIs in a real terminal; the persona with a real model (when it asks, when it doesn't); the dialogs; `up`/`destroy`/`exec`, attended and headless; sessions and titles across processes; configuration, models, skills, branding; `login`; the Homebrew install | here |

**When to run it:** before publishing a release, between steps 7 (verify the signed artifacts)
and 8 (publish) of doof's release ritual (`doof/.claude/commands/release.md`), against the draft's
tarball. Suite 12 runs after step 10 (the Homebrew install). The **Gate** tier is the minimum for
every release. Run the **Full** tier for a minor release, or when the release changes the
CLI's tool layer (renderers, persona, permissions, titles), the docker-agent fork pin, or the
server's tools, prompts or skills.

---

## How to run

### Preflight (P)

Run from the root of a turf checkout. `$TAG` is the release (`v0.18.0`), and `$RUN` is this
run's directory. The suite uses its own `TURF_HOME`, so it never touches your `~/.turf`, your
saved theme, your sessions or your dev engine. Use a second terminal for the engine (P.4).

| # | Do | Expect |
|---|---|---|
| P.1 | `RUN=$PWD/scratchpad/gate-$TAG; mkdir -p $RUN/dist`; `gh release download $TAG --repo turfbuild/turf --dir $RUN --pattern "turf_${TAG}_$(go env GOOS)_$(go env GOARCH).tar.gz"`; `tar -C $RUN/dist -xzf $RUN/turf_*.tar.gz`; `ls $RUN/dist` | `turf`, `turf-mcp-server`, `turf-engine`, `turf-builtin-provider`, `LICENSE`, `NOTICE`, `README.md`. `scratchpad/` is gitignored. Step 7 of the ritual already verified the signatures; this suite does not repeat it. |
| P.2 | `restate-server --version` | ≥ 1.7.7 (the engine refuses older); 1.7.13 is the tested version. |
| P.3 | In **every** terminal of the run: `export TURF_HOME=$RUN/home PATH=$RUN/dist:$PATH`; `unset TURF_MODEL TURF_THEME TURF_LEAN TURF_MCP_SERVER TURF_SESSION_DB TURF_ALLOW_PATH TURF_TMP_DIR TF_CLI_ARGS`; `export TURF_TELEMETRY=off`; `which turf turf-mcp-server turf-engine` | All three resolve into `$RUN/dist`. `$RUN/home` does not exist yet: this is a first run. `TURF_TELEMETRY=off` keeps the release engine from reporting the gate's runs as usage; row 1.11 lifts it for one run that logs its events and sends none. |
| P.4 | Terminal 2: `turf-mcp-server engine run`. Terminal 1: `turf-mcp-server engine status` | The engine's stack lives in `$RUN/home/engine` on free ports, beside any dev engine. `status` reports it running, with the server and the engine both at `$TAG`, and names its console. |
| P.5 | `git checkout $TAG`; `go test -count=1 -v -run 'TestPermissionListsMatchServerAnnotations\|TestTurfToolTargetArgsMatchServerSchemas\|TestTitleCuratorToolsExistOnServer\|TestCreateAgentRuntimeWiresSessionStore' .` | Each test **runs** (no `SKIP`) and passes, against the `turf-mcp-server` P.3 put first on `PATH`. A `pre-approved "turf_…" is not a server tool (stale entry)` or `server tool "turf_…" is not pre-approved` line means the CLI and the server it ships with disagree on the tool set: a blocker. |
| P.6 | `D=<a doof checkout at the server's release commit>`; `cp -R $D/docs/development/testing/v1-suite/fixtures/{core,gates,import} $RUN/`; `cp -R ../turf-examples/integrations/turf-cli $RUN/`; `rm -f $RUN/turf-cli/.turf/*.db* $RUN/turf-cli/.turf/memory.lock` | The fixtures copied, without any local state an example directory carries. The checked-in ones are never run in place. The doof commit is the one in the server's provenance (ritual step 7). |
| P.7 | `export ANTHROPIC_API_KEY=…`; `export TURF_MODEL=anthropic/claude-sonnet-5` | Model credits are spent **only** in Suites 5–11 and row 12.3; everything before Suite 5 sends no model request. Suite 10 changes the model on purpose. |
| P.8 | A truecolor terminal (Ghostty, iTerm2, WezTerm, kitty) at least 120×40 | Row 4.7 also wants a 256-color one (Terminal.app). |
| P.9 | After the run: quit every turf; Ctrl-C the engine (terminal 2); `pgrep -fl 'turf-mcp-server\|turf-engine\|restate-server\|terraform-provider'`; `find $RUN -name .terraform.tfstate.lock.info` | No process of this run's left; no lock file. `grep -E 'level=(ERROR\|WARN)' $RUN/home/turf.debug.log`: each line explained (Suite 2's are expected). |

**A dry run before tagging** uses the same rows with source builds in place of the tarball:
`make build` here, `make build engine-bins` in doof, and `PATH=$PWD/bin:$D/bin:$PATH`. P.1's
contents check is then skipped.

### Tiers

| Tier | Suites | Needs | Model credits | Time |
|---|---|---|---|---|
| Gate | P, 1–5 | registry access (`hashicorp/random`, `hashicorp/local`), restate-server | Suite 5 | ~1 h |
| Full | 6–11 | the same, plus git | yes | ~2.5 h |
| Post-publish | 12 | Homebrew, a user without `~/.turf` (or with it moved aside) | 12.3 | ~20 min |

### Conventions

- **Expand every new timeline line** (Ctrl+O in the full TUI) and check both forms: the compact
  line and the detail block. The lean TUI always shows the detail.
- **Watch the session title** (the window title and the `/sessions` browser) and record each
  change. They are deterministic, and the rows give them exactly.
- **Answer dialogs exactly as the row says**: yes, no, or Esc. A dialog the row does not expect
  is a finding, and so is a missing one.
- **Verify on disk**, not only on screen: the statefile, `.turf/`, `$TURF_HOME`,
  `turf.debug.log`.
- **Paths are absolute.** `<core>` is `$RUN/core`, and so on. Every model suite starts in its
  fixture's directory unless the row says otherwise.
- **The agent's words vary; the tool calls and the timeline do not.** Prompt in plain language
  as the rows suggest, and judge the row on its Expect column, not on the prose around it.
- **Record as you go** (the [results template](#results-template)). A finding is `F-n` with a
  severity: blocker, defect, minor, doc, or env.

---

## Suite 1: The artifact

No model, no engine.

| # | Do | Expect |
|---|---|---|
| 1.1 | `turf --version`; `turf version`; `turf-mcp-server --version` | `turf version $TAG`; `turf $TAG`; `$TAG`. Never `dev`, never a `-dirty` suffix. |
| 1.2 | `file $RUN/dist/*` | Every binary is for this host's OS and architecture. |
| 1.3 | `turf --help` | Commands: `chat`, `completion`, `destroy`, `exec`, `help`, `license`, `login`, `logout`, `telemetry`, `up`, `version`. `--model`'s default shows P.7's `TURF_MODEL` (the variable feeds the flag). |
| 1.4 | README's flags table against `turf --help` | Every flag in `--help` is in the table, and every default agrees. `--theme` defaults to `stadium`. |
| 1.5 | `source <(turf completion zsh)`; `turf <TAB>`; `turf up --<TAB>` | Subcommands complete, then `up`'s flags (`--yes`, `--auto-approve`, and the persistent ones). |
| 1.6 | `$RUN/dist/README.md` (the tarball's README, `release/README-alpha.md`): run every command it shows, as written | Each one works as the text says. In particular, `turf up` and `turf destroy` take **instructions** as positional arguments, not a path: a README that shows `turf up ./path/to/config` is a doc finding. |
| 1.7 | Optional. `gh release download $TAG --pattern "turf_${TAG}_linux_amd64.tar.gz"`, extract to `$RUN/linux`; `docker run --rm --platform linux/amd64 -v $RUN/linux:/t debian:stable-slim /t/turf --version`, then `… /t/turf chat` | The version; then `Error: turf-mcp-server not found on PATH. …` (exit 1). The release builds with CGO off: no missing `libc`. |
| 1.8 | Optional, on Apple Silicon. The darwin_amd64 tarball: `arch -x86_64 $RUN/amd64/turf --version` | The version, under Rosetta. |
| 1.9 | `turf telemetry status`; `turf telemetry off`; `turf --version`; `turf telemetry on`; `cat $RUN/home/telemetry.json` | `status` shows `on`, the installation id P.4's `engine run` wrote into `$RUN/home/telemetry.json`, and a note that `TURF_TELEMETRY=off` (P.3) is set in this shell. `off` says the change takes effect when the engine restarts; `--version`'s last line is then `Telemetry: off (turf telemetry on to enable)`. `on` keeps the id. The file holds `enabled` and `installation_id`, mode 0600. |
| 1.10 | `turf-mcp-server engine status` | `telemetry       off; installation <1.9's id>`: P.3's `TURF_TELEMETRY=off` reached the engine through terminal 2's environment. |
| 1.11 | Ctrl-C P.4's engine. Terminal 2: `TURF_TELEMETRY= TURF_TELEMETRY_DEBUG=1 turf-mcp-server engine run`. Terminal 1: `grep 'telemetry event' $RUN/home/engine/engine.log` | One `engine.start` event, logged and not sent, with `installation_id` (1.9's), `version` `$TAG`, `deployment` `local`, and no path, hostname or address in it. Then Ctrl-C it and start terminal 2 again as P.4 has it, with P.3's environment. |

---

## Suite 2: Startup and its errors

No model request: every row fails before one could be sent. Run from an empty directory
(`mkdir $RUN/empty && cd $RUN/empty`). Each command exits 1 and prints `Error: ` and the text
shown.

| # | Do | Expect |
|---|---|---|
| 2.1 | `env PATH=/usr/bin:/bin $RUN/dist/turf chat` | `turf-mcp-server not found on PATH. Install it and ensure it is on PATH, or set TURF_MCP_SERVER / --mcp-server`. No TUI. |
| 2.2 | `turf --mcp-server /nonexistent chat` | `turf-mcp-server not found at "/nonexistent": stat /nonexistent: no such file or directory` |
| 2.3 | `turf --model claude chat` | `could not start the model "claude": invalid model "claude": not a model defined in turf.yaml, not "auto", and not a valid provider/model reference: …`, then the guidance block: set a provider key, or run a local model with Docker Model Runner declared in `turf.yaml` with `provider_opts.context_size: 65536`, and the providers link. |
| 2.4 | `turf --model bogus/x chat` | `could not start the model "bogus/x": unknown provider type "bogus" …`, then the same guidance. |
| 2.5 | `env -u ANTHROPIC_API_KEY turf --model anthropic/claude-sonnet-5 chat` | `could not start the model "anthropic/claude-sonnet-5": ANTHROPIC_API_KEY environment variable is required`, then the guidance. |
| 2.6 | `printf 'models: [unclosed\n' > $TURF_HOME/turf.yaml`; `turf chat`; then `rm $TURF_HOME/turf.yaml` | `parsing turf config <$TURF_HOME>/turf.yaml: [1:9] sequence end token ']' not found`, with the line and a caret under the fault. |
| 2.7 | `turf --no-session --continue` | `cannot resume a session with --no-session set` |
| 2.8 | `turf -C /nonexistent chat` | `chdir /nonexistent: no such file or directory` |
| 2.9 | `turf --worktree chat` (the empty directory is not a git repository); `turf --worktree-base main chat` | ``--worktree requires <dir> to be inside a git repository (run `git init` first)``; `--worktree-base requires --worktree` |
| 2.10 | `turf up -c`; `turf upp` | `unknown shorthand flag: 'c' in -c` (resume is for `chat` and `exec`, never `up`/`destroy`); `unknown command "upp" for "turf"`, with `Did you mean this? up`. |
| 2.11 | `turf exec - </dev/null`; `turf exec </dev/null` | `no message received on stdin: …`; `no message provided and stdin is not a terminal: …` (both worded in docker-agent's terms: [known gaps](#known-gaps)). |
| 2.12 | `ls $TURF_HOME`; `ls -a $RUN/empty` | `turf.debug.log` (every subcommand opens it, before anything else). `.turf/sessions.db` exists only where a run got as far as building its runtime (2.11's did). |

---

## Suite 3: `login` and `logout`

No account needed. Isolate the credentials file: `export HOME=$RUN/loginhome; unset
XDG_CONFIG_HOME; mkdir -p $HOME` in a terminal of its own (P.3's environment too), and close it
after. Rows 3.4, 3.5 and 3.7 reach the network (service discovery; 3.4 also checks the token).

| # | Do | Expect |
|---|---|---|
| 3.1 | `turf login`; `turf logout` | `Error: the login command expects exactly one argument: the host to log in to`; `… the host to log out of`. There is no default host, as with `tofu`. |
| 3.2 | `echo x \| turf login app.terraform.io` | ``Error: turf login is an interactive command and cannot run with stdin redirected. To supply a token non-interactively, pipe it to `turf login --token-stdin app.terraform.io` `` |
| 3.3 | `turf login 'bad host!'` | `Error: the given hostname "bad host!" is not valid: idna: disallowed rune U+0020` |
| 3.4 | `printf bogus \| turf login --token-stdin app.terraform.io`; `printf '' \| turf login --token-stdin app.terraform.io` | `Error: the token is invalid for app.terraform.io` (the token was checked against the host and nothing was written); `Error: no token was given on stdin`. `ls $HOME/.terraform.d` fails: nothing created. |
| 3.5 | `turf login app.terraform.io` on the terminal; answer `no` to the confirmation | It names the file it will write, with its full path, and asks for a literal `yes` **before** any browser opens or token is asked for. `no` gives `Error: login cancelled: the login was not confirmed`; nothing written. |
| 3.6 | `turf logout app.terraform.io` | `No credentials for app.terraform.io are stored.`, exit 0. |
| 3.7 | `printf fake-token \| turf login --token-stdin registry.opentofu.org`; `ls -la $HOME/.terraform.d`; `cat $HOME/.terraform.d/credentials.tfrc.json` | `Success! turf has saved an API token for registry.opentofu.org.` (the host offers no `tfe.v2` to check the token against). The directory is `drwx------`, the file `-rw-------`; the JSON is two-space indented, with no trailing newline, holding `credentials["registry.opentofu.org"].token`. |
| 3.8 | `TF_TOKEN_registry_opentofu_org=x turf logout registry.opentofu.org`; `cat` the file | It names the file, then `Note: the environment variable TF_TOKEN_registry_opentofu_org is still set …`, then `Success! turf has removed the stored API token for registry.opentofu.org.` The file is `{"credentials": {}}`. |
| 3.9 | Optional, with an HCP Terraform or Scalr account: `turf login <host>`, answer `yes`, paste a real token | `Success!`; then a `tofu init` of a configuration with a `remote` backend on that host (and `HOME` the same) finds the token. `turf logout <host>`, and the same `tofu init` fails with "Required token could not be found". |

---

## Suite 4: The TUI before any message

The engine is running (P.4). **Send no message in this suite**: the model key is needed only to
construct the provider, and nothing here calls it. Start in `<core>`.

| # | Do | Expect |
|---|---|---|
| 4.1 | `turf` | The full TUI. Status bar and window title: `turf $TAG`. The `stadium` theme (`$TURF_HOME` is fresh). The welcome message, `Welcome to Turf — your infrastructure management agent.` The command it names, `/guide`, is in the command palette (`/`) under the MCP prompts, beside `/up` and `/destroy`. |
| 4.2 | `/tools` | Every turf tool sits under a `turf · <group>` category (`config`, `workspace`, `run`, `apply`, `state`, `provider`, `skills`) with a verb-first title: `Init Configuration`, `Deploy`, `Apply Plan`, `Cancel Run`, `Show`, `Taint Resource`, …. **No tool in a bare `turf` group**: that is a server tool the CLI does not know (drift; compare P.5). turf's tools do not read as an external MCP integration. Beside them: the think, todo, memory and filesystem tools, and `user_prompt`. |
| 4.3 | `/settings` → Appearance → Theme | The five terrain themes (`meadow`, `stadium`, `sonora`, `lunar`, `surf`) beside docker-agent's (`calm-roots`, `tokyo-night`, …). Pick `lunar`: it applies at once. `/exit`; `turf`: still `lunar` (saved in `$TURF_HOME/config.yaml`). `/exit`; `turf --theme surf`: `surf` for this run; `/exit`; `turf`: `lunar` again. `turf --theme nope`: the stock theme, no error on screen; `turf.debug.log` has the warning. Set `stadium` back before going on. |
| 4.4 | `/sessions` | The browser, empty ("No sessions found"), or the notice "No previous sessions found". |
| 4.5 | `/model` | A picker listing models (the models.dev catalog filtered by the keys you have, plus any local Docker Model Runner models). Esc closes it without switching. |
| 4.6 | While 4.1 runs, in another terminal: `ps eww $(pgrep -n turf-mcp-server) \| tr ' ' '\n' \| grep TELEMETRY_ENABLED`. Then quit, relaunch with `turf --no-telemetry`, and repeat | `TELEMETRY_ENABLED=false` both times: the agent runtime's own telemetry is always off. `--no-telemetry` is accepted, hidden from `--help`, and changes nothing. |
| 4.7 | `turf --lean`; `/help`; `/exit`. Repeat in a 256-color terminal | The normal screen (no alternate screen): the green, outlined **TURF** wordmark in half-block pixels, then `Type a message, press / for commands, Ctrl+C to quit.` No welcome message (lean has none). `/help` lists lean's own commands (`/new`, `/compact`, `/model`, `/clear`, `/exit`, …). In 256 colors, record how the wordmark looks: it is truecolor art and may be approximated. |
| 4.8 | Quit each way: `/exit`, then Ctrl+C at an empty prompt, then close the window | The shell comes back clean (cursor shown, no leftover alternate screen, echo on). `pgrep -fl turf-mcp-server` lists none of this suite's servers. |

---

## Suite 5: Smoke, the whole loop in `turf chat`

The gate's core. `cd <core>; turf`. This is the configuration doof's smoke suite uses: `fan`'s
count is unknown until `seed` exists, so a deploy takes **two rounds** (4 creates, 1 deferred,
then 2 creates). Expand every line (Ctrl+O). Before 5.1, note where the debug log ends:
`N=$(wc -l < $TURF_HOME/turf.debug.log)` (Suite 2 left ERROR lines in it on purpose).

| # | Do | Expect |
|---|---|---|
| 5.1 | "Deploy this configuration." | Skills lines first (the server's deployment skill), then `Init Configuration  <core> · backend local (default) · N provider(s) · 1 variable(s) · 3 output(s)`. Expanded: the providers with their constraints and the version init installed (`hashicorp/local ~> 2.5 → v2.x`, `hashicorp/random ~> 3.6 → v3.x`), no builtin `terraform`, and the lock file `created`. **Title: `core · init`.** Then `Open Workspace`: `default · <core> · 0 resource(s)`. |
| 5.2 | (continues) | `Deploy  p-xxxxxxxx · round 1 · +4 · 1 deferred · awaiting approval`, with `+4` in the theme's create color and `awaiting approval` in its warning color. Expanded: `plan:` with the plan's resource URI (`turf://workspaces/default/convergences/p-…/rounds/1/plan`) and a short digest (muted) and an expiry about 24 h ahead, and round 1. **Title: `core · plan +4 ~0 -0`.** |
| 5.3 | (continues) | The agent states the plan (4 creates, 1 deferred, no deletes) and **approves it itself, with no dialog**: creates only, which is low-risk under the server's rule. A dialog here is ⚠️ (over-cautious), not ❌. Then `Apply Plan  p-… · deploy · round 2 · +2 · awaiting approval` (**title `core · plan +2 ~0 -0`**), approved the same way, then `Apply Plan  p-… · deploy · converged · 2 rounds · 6 applied`. Expanded: rounds 1 and 2 (`applied` 4 and 2), and `outputs:` finalized `fan`, `password`, `pet`. **Title: `core · applied +6 ~0 -0`.** |
| 5.4 | "Show me the outputs." Then "What is the password? Show it to me." | `Read Outputs` lists `fan`, `pet`, and `password = (sensitive value)`. Asked for the value, the agent reads it again with the sensitive values revealed: it may say the password in its reply, but **the timeline line still reads `(sensitive value)`**, expanded too. The timeline redacts at display. |
| 5.5 | "List the state with values." | `List State`: 6 resources in address order (`local_file.marker`, `random_password.db`, `random_pet.name`, `terraform_data.fan[0]`, `terraform_data.fan[1]`, `terraform_data.seed`). Expanded: values, with `random_password.db`'s `result` and `bcrypt_hash` as `(sensitive value)`. |
| 5.6 | "Plan with prefix v1b and show me the plan in detail before applying anything." | (If the agent offers to edit `main.tf` rather than pass a variable, its configuration-edit dialog comes first: answer yes. Either way is correct.) `Deploy  p-… · round 1 · +2 ~… -2 · 2 replaced · awaiting approval`. Then `Show`: rows led by `-/+` for `random_pet.name` and `local_file.marker`, each with a `forces replacement` note, and `~` rows for the in-place updates. New ids read `(known after apply)`. The password is not shown as a change. **Title: `core · plan +2 ~… -2`.** |
| 5.7 | (continues) | A replace is not low-risk, so **the agent asks**: a dialog with a yes/no choice that summarizes the counts. Answer **no**. The agent applies nothing and asks how to adjust. Say "cancel it". `Cancel Run  p-… · deploy · cancelled`. Nothing changed: `cat <core>/out/default/marker.txt` still holds the `v1-` pet name. |
| 5.8 | "Destroy everything." | `Destroy  d-xxxxxxxx · round 1 · -6 · awaiting approval`. **Title: `core · planned teardown -6`.** **The dialog always appears for a destroy**, whatever the plan. Answer **yes**. `Apply Plan  d-… · destroy · done · 6 applied`. **Title: `core · destroyed -6`.** `<core>/out/default/` is empty. |
| 5.9 | Scroll the timeline with the mouse wheel and the trackpad; drag the window down to 80 columns, then back | Smooth scrolling. At 80 columns the compact lines are cut with `…`, never wrapped into garbage, and widen back. |
| 5.10 | `/exit`; `ls <core>/.turf`; `tail -n +$((N+1)) $TURF_HOME/turf.debug.log \| grep -c level=ERROR` | `memory.db`, `sessions.db` (and the SQLite sidecars). Zero ERROR lines since 5.1. No orphan `turf-mcp-server`. |
| 5.11 | `cd /tmp; turf -C <core>`, then `/up set the prefix to v3` | The first message is the server's `/up` prompt for `<core>` (the working directory after `-C`), with a **User Instructions** section quoting `set the prefix to v3`. It never says `Deploy … at "set the prefix to v3"` (rc.1 finding F9). Creates only, so the agent approves itself; converged, 6 applied, and the marker holds a `v3-` name. |
| 5.12 | `/destroy`, then `/exit` | The `/destroy` prompt for `<core>`, with no User Instructions section. The dialog; **yes**; `done · 6 applied`. |

---

## Suite 6: The `up` and `destroy` subcommands

`<core>`, after Suite 5 (nothing deployed). With a terminal on stdout, `up` and `destroy` run the
**lean** TUI, whatever `--lean` says, and exit when the agent's turn ends.

| # | Do | Expect |
|---|---|---|
| 6.1 | `turf up` | The lean TUI. The first message is the server's `/up` prompt, rendered with `config_path` = `<core>` (the cwd). The agent runs init → open → up → apply × 2 on its own (low-risk creates, as 5.3). `converged · 2 rounds · 6 applied`; the process exits 0. `/sessions` in a later `turf` lists it, titled `core · applied +6 ~0 -0`. |
| 6.2 | `turf up set the prefix to v2` | The words reach the prompt as its instructions. The plan replaces the pet and the marker (prefix `v2`), so **the dialog appears** (lean: Enter or `y` accepts, `n` declines, Esc cancels). Press `y`. Converged; the marker holds a `v2-` name. |
| 6.3 | `cd /tmp; turf -C <core> up` | The same as a run from `<core>`: `config_path` is `<core>`. With no instructions the plan returns the prefix to its default, `v1`: a replace, so the dialog. `y`; converged. |
| 6.4 | `turf destroy`; press `n` at the dialog. Then `turf destroy`; press `y` | The first: the destroy plan (`-6`), the dialog, declined: nothing destroyed (`state_list` in a later run still lists 6). The second: `done · 6 applied`. |
| 6.5 | `turf up --yes` | No dialog at any point. If the agent calls `user_prompt` anyway, it is answered for you: a `User Prompt (auto-confirmed)` line. Converged; the title is `core · applied +6 ~0 -0`, or `core · applied 6 change(s)` when the rounds ran without stopping. |
| 6.6 | `turf up --yes \| tee $RUN/up.log`; `echo $?` | Text mode: `Calling turf_…(…)` and `… response` lines, the agent's prose, no TUI. A no-change convergence. Exit 0. |
| 6.7 | `turf destroy \| tee $RUN/destroy.log` (**no `--yes`**) | Nobody can answer, so the destroy question is **declined**: a `User Prompt (declined: unattended)` call whose response is `"action":"decline"` with a reason naming `--yes`. Nothing is destroyed (a later `turf exec "list the state"` still lists 6). The agent ends by saying the destroy is waiting for approval and that `--yes` would approve it. Exit 0. |
| 6.8 | `turf destroy --yes \| tee $RUN/destroy-yes.log` | The question, if asked, is auto-confirmed (`"auto_confirmed": true`); `done · 6 applied`. Approval without a person takes `--yes`. |

---

## Suite 7: `exec`

`<core>`, nothing deployed.

| # | Do | Expect |
|---|---|---|
| 7.1 | `turf exec "which providers does this configuration need?"` | Text mode, an answer naming `hashicorp/random` and `hashicorp/local`. Exit 0. |
| 7.2 | `echo "list the workspaces" \| turf exec -` | The message is read from stdin; `Calling turf_list` in the output. |
| 7.3 | `turf exec --json "init this configuration" \| tee $RUN/exec.jsonl \| jq -c .type \| sort \| uniq -c` | One JSON event per line; `tool_call` events among the types. |
| 7.4 | `turf exec --hide-tool-calls "init this configuration"` | Only the agent's prose: no `Calling` lines. |
| 7.5 | `turf exec --session gate-1 "remember the word 'heliotrope' for later"`; `turf exec --session gate-1 "what word did I ask you to remember?"`; `turf exec -c "and spell it backwards"` | `heliotrope`; then its reverse. The first call created a session with the ID `gate-1`; the third continued the most recent. |
| 7.6 | `turf exec --model bogus/x hi; echo $?` | The model error on stderr, exit 1. |
| 7.7 | `turf exec` on a terminal, no message | A prompt loop opened by `------- Welcome to turf! -------` and `> `. One exchange, then Ctrl+D exits. |

---

## Suite 8: Turf actions and the other run kinds

A fresh `turf` in each fixture's directory.

| # | Do | Expect |
|---|---|---|
| 8.1 | `cd <gates>; turf`; "Deploy this." | `Deploy  p-… · round 1 · +2 · 2 to invoke · awaiting approval`. Expanded: `invocations:` with `↯ action.turf_action.db_gate` (`before_create` of `terraform_data.db`, on failure halt) and `↯ action.turf_action.verify` (`after_create` of `terraform_data.app`, continue), in the configuration's spelling. |
| 8.2 | (continues) | Approved (by the agent or by you), then `Apply Plan  p-… · deploy · round 1 · awaiting 1 action(s)`. Expanded: `actions waiting:` with `db_gate`'s key, caller, event, prompt, `if_failed`, and its inputs. The agent asks you through the **dialog**, since the gate's prompt asks a person. Answer **no**, and give a reason when it asks for one. `Apply Plan  p-… · deploy · failed · 1 failed` in the error color; expanded, the round's failure carries your reason. `List State`: nothing. |
| 8.3 | "Try again; this time I'll approve the gate." | The gate's dialog: **yes**. Then the `verify` action: the agent checks it itself (no dialog). `converged · … · 2 applied · 2 invoked`. |
| 8.4 | "Invoke the db_gate action." | `Invoke Action  a-… · round 1 · 1 to invoke · awaiting approval`. **The dialog always appears for an invoke plan.** Yes; `Apply Plan  a-… · invoke · round 1 · awaiting 1 action(s)`, and the gate's own question: yes; `done · 1 invoked`. |
| 8.5 | `cd <import>; turf`; "Import random_string.cli with id abcdefgh1234." | `Init Configuration` ends `· 1 to import` (the configuration's `import` block). `Import Resources  i-… · round 1 · 1 to import · awaiting approval`. **The dialog always appears for an import plan.** Yes; `done · 1 applied`. Then "Deploy the configuration": the `import {}` block's `random_string.block` imports (`1 to import`); converged. |
| 8.6 | `cd <core>; turf`; "Deploy, then taint terraform_data.seed and plan again." | After the deploy converges: `Taint Resource  terraform_data.seed · tainted · serial N`, with **no dialog** (a taint applies nothing by itself). The next `Deploy` replaces `seed`, and `Show` gives its row a `-/+` and the reason `tainted`. A replace: **the dialog**. Yes; converged. |
| 8.7 | `rm <core>/out/default/marker.txt`; "Refresh the state." | `Refresh State  r-… · round 1 · no changes · 1 drifted · awaiting approval`. **No dialog**: a refresh-only plan needs no confirmation. `Show` (ask for it) lists `local_file.marker` under `changed outside Turf`. Applied: `done`. "Deploy": the marker comes back (`+1`). |
| 8.8 | "Plan with prefix v1b, but don't apply it yet." Then, in another terminal, change `random_password.db`'s `length` to 21 in `<core>/main.tf`. Then "Apply that plan; I approve." | The apply's result is a convergence line, `Apply Plan  p-… · deploy · stale`, neutral (not red); expanded, round 1 reads `cancelled · … · approved`, and beneath it `stale: the configuration changed after it was planned`. Not raw JSON, and not a bare error line. **Revert the edit.** |
| 8.9 | "Open a workspace named staging, list the workspaces, then delete staging." | `Open Workspace  staging · …`; `List Workspaces` shows `default` and `staging`. Before the delete, its **own dialog**, warning that deletion is irreversible, separate from any plan approval. Yes; `Delete Workspace` reports it deleted. |
| 8.10 | "Plan with prefix v1b." Leave it at `awaiting approval` and `/exit`. Then `turf -c`; "Pick up where we left off." | The resumed session replays its timeline. The new `Open Workspace` line shows `holds p-… · deploy · awaiting approval · round 1`: the convergence waited in the engine. The agent re-reads the plan (an `Apply Plan` with no digest re-vends it) and asks before applying it (replaces). Answer yes; converged. "Plan with prefix v1" and apply, to restore. |

---

## Suite 9: Sessions, titles, memory

| # | Do | Expect |
|---|---|---|
| 9.1 | `cd <core>; turf`; `/sessions` | Suite 5's and the later sessions, under this workspace, with their titles (`core · destroyed -6`, …). Pick Suite 5's: it loads **in place** (no tabs, no "session spawning is not available"), and its timeline renders through turf's renderers, not raw JSON. |
| 9.2 | `/exit`; `turf --continue` | The most recent session resumes, transcript and all. |
| 9.3 | `/title Gate smoke`; "Deploy." (let it converge); `/exit`; `turf -c` | The title stays `Gate smoke` through the deploy's milestones and after the resume: a human title wins. |
| 9.4 | `/clear`; "hello"; `/exit`; `turf`; `/sessions` | The cleared session is a new one; it lists as `Untitled` (a chat-only session gets no title). After `/clear`, a turf tool call still runs without a permission prompt. |
| 9.5 | `turf --no-session`; `/sessions` | `No session store configured`. Nothing new in `<core>/.turf/sessions.db`. |
| 9.6 | `turf --session-db $RUN/alt.db exec "hello"`; `ls -l $RUN/alt.db` | The store is created there. |
| 9.7 | `turf`; "Remember that our staging prefix is stg." `/clear`; "What's our staging prefix?" | `stg`, from the memory tool. `<core>/.turf/memory.db` exists. |
| 9.8 | `turf --no-memory`; `/tools` | No memory tools listed. |

---

## Suite 10: Configuration, models, skills, branding

`cd $RUN/turf-cli` (the `turf-examples` integration copied in P.6), unless the row says otherwise.

| # | Do | Expect |
|---|---|---|
| 10.1 | `unset TURF_MODEL`; `turf` | The model comes from `.turf/turf.yaml` (its `first_available` list resolves to Anthropic with only that key set): `/model` marks it. |
| 10.2 | `turf --model anthropic/claude-haiku-5-5`; then `TURF_MODEL=anthropic/claude-haiku-5-5 turf` | `/model` marks Haiku both times: the flag wins over `turf.yaml`, and so does the environment variable. |
| 10.3 | `cd $RUN/empty; turf --model auto` | With only `ANTHROPIC_API_KEY` set, an Anthropic model. Record which. |
| 10.4 | `/model`, switch to another model; "Which model are you?"; `/exit`; `turf` | The switch holds for that session only: the relaunch is back on 10.1's model, and `.turf/turf.yaml` is unchanged (`diff` it against the `turf-examples` original). |
| 10.5 | `turf`; `/skills`; "What tags does our tagging policy require?" | `/skills` lists `tagging-policy` (the project skill). The agent loads it with `read_skill` (no `turf_` prefix) and answers from it and its `references/tags.md`. |
| 10.6 | Copy that skill to `$TURF_HOME/skills/tagging-policy/` and change its `description:`; `turf`; `/skills` | The project's copy wins (its description is the one shown). Remove the global copy. |
| 10.7 | `mkdir -p $RUN/brand/.turf`; copy only the `branding:` block of `../turf-examples/integrations/scalr/chat/.turf/turf.yaml`, and its banner file, into `$RUN/brand/.turf/` (its `mcps:` need a Scalr account); `cd $RUN/brand; turf`; `/exit`; `turf --lean` | The full TUI: the `surf` theme and the brand's welcome message. The lean TUI: the brand's banner. Both still say **turf**: status bar, window title, and the agent's name above its replies. "What special instructions were you given?" reflects the brand's `additional_instructions`. |
| 10.8 | `mkdir -p $RUN/shared $RUN/elsewhere`; `cd <core>; turf --allow-path $RUN/shared`; "Write a file hello.txt saying hi into $RUN/shared, then another into $RUN/elsewhere." | The first write runs without a permission prompt. The second is refused by the filesystem sandbox. `ls $RUN/elsewhere` is empty. |
| 10.9 | Optional. A `turf.yaml` `mcps:` entry for a stdio MCP server you have (e.g. `npx -y @modelcontextprotocol/server-everything`); ask the agent to use one of its tools | Its tools appear in `/tools` and **prompt for permission** (only turf's own tools and the trusted builtins are pre-approved). |

---

## Suite 11: `--worktree`

`cp -R <core> $RUN/core-git; cd $RUN/core-git; git init -q; git add -A; git commit -qm init`.

| # | Do | Expect |
|---|---|---|
| 11.1 | `turf --worktree` | stderr: `Using git worktree $TURF_HOME/worktrees/<name> (branch <branch>)`. The status bar: `turf $TAG · <branch>`. |
| 11.2 | "Add a variable named `owner` with default `gate` to main.tf." | (The configuration-edit dialog: yes.) `git -C $TURF_HOME/worktrees/<name> diff` shows the edit; `git -C $RUN/core-git status` is clean. |
| 11.3 | `/exit` | The worktree is dirty: `Remove it and discard this work? … (y/N):`. Enter: `Keeping git worktree …`; it is still there. |
| 11.4 | `ls $RUN/core-git/.turf`; `ls $TURF_HOME/worktrees/<name>/.turf` | `sessions.db` at the launch directory (the anchor); `memory.db` in the worktree. |
| 11.5 | `turf --worktree=gate-clean --worktree-base HEAD`; `/exit` without changing anything | `Removed git worktree …`: a clean worktree goes away by itself. |

---

## Suite 12: Post-publish, the Homebrew install

After ritual step 10. Close every terminal from P.3 (this suite uses the real `~/.turf`, so
move yours aside first: `mv ~/.turf ~/.turf.gate-bak`).

| # | Do | Expect |
|---|---|---|
| 12.1 | `brew uninstall turf` (if installed); `brew install restatedev/tap/restate-server turfbuild/tap/turf`; `which turf turf-mcp-server turf-engine restate-server`; `turf --version` | All four resolve into Homebrew's prefix; `turf version $TAG`. |
| 12.2 | `brew services start turf`; `turf-mcp-server engine status` | Running; the server and the engine both at `$TAG`. |
| 12.3 | `cp -R <core> /tmp/gate-core; cd /tmp/gate-core`; follow the installed README's quick start **as written** | It works as written; the deploy converges. |
| 12.4 | `brew services stop turf`; `turf up` | The agent's first engine call fails with the server's message that the local Turf engine is not running and `brew services start turf` starts it. It reads as a sentence, not a stack trace. `brew services start turf` again. |
| 12.5 | `cd /tmp/gate-core; turf destroy --yes`; `brew services stop turf`; `rm -rf ~/.turf; mv ~/.turf.gate-bak ~/.turf`; `brew services start turf` | Your own setup restored. |

---

## Known gaps

Stated so a run does not rediscover them; none is a step.

- **docker-agent shows through** in a few places the fork does not re-brand: `/getting-started`
  ("Learn docker agent by doing"), the feedback entries in the command palette, and `exec`'s
  stdin errors (2.11), which say `docker agent run <agent>`. The unknown-provider error (2.4)
  names a Go API.
- **`/title` with no argument** fails with `title regeneration not available`: turf composes
  titles itself and wires no LLM titler, by design. `/title <text>` works (9.3).
- **No progress while a round runs.** docker-agent sends no MCP `progressToken` and relays no
  progress, so a running `Deploy`/`Apply Plan` shows its spinner and `planning…`/`applying…`.
- **The lean TUI has no welcome message and no Ctrl+O** (its tool lines are always expanded).
- **`--worktree name` with a space** makes `name` a positional argument (an unknown command
  for bare `turf`, instructions for `up`). Use `--worktree=name`.
- **No voice input.** Release builds use the `no_audio` tag, so there is no `/speak`.
- **OAuth login** (`login.v1`): no real host publishes it; 3.5 and 3.9 take the token path.
- **Windows** is not covered.

---

## Results template

```
Date / turf $TAG @ commit / doof (server, engine) @ commit / restate-server version / model / terminal:
Tester:
Tiers run:

| # | Observed | Verdict (✅ / ⚠️ / ❌ / skipped) |
|---|---|---|
| P.1 | … | |

Findings
| id | sev | row | finding |
|---|---|---|---|
| F-1 | | | |
```

## Recorded runs

### Run 0: the offline rows, dry run, 2026-10-13

Source builds, not a release: turf `v0.17.0-4-gd49790e` (branch `m3-v1-tools`), with the server
`v0.18.0-alpha.1-5-g885785f0-dirty` (doof branch `skills/load-deployment-skill`, uncommitted).
No engine, no TUI, no model request. These rows were run while the suite was being written, to
set its expected strings.

| # | Observed | Verdict |
|---|---|---|
| P.5 | `TestTurfToolTargetArgsMatchServerSchemas`, `TestTitleCuratorToolsExistOnServer` and `TestCreateAgentRuntimeWiresSessionStore` ran and passed, with or without a model key. `TestPermissionListsMatchServerAnnotations` failed: `turf_skill_core`, `turf_skill_codified` and `turf_skill_demo` are stale, and `turf_skill_deployment` is not pre-approved. That server comes from an unmerged skills branch, so this is not a release pairing, but the CLI must follow before that branch ships. | ❌ |
| 1.1, 1.3, 1.5 | As written. | ✅ |
| 1.4 | README's table gives `--theme`'s default as `meadow` (it is `stadium`) and lacks `--allow-path`, `--debug`, `--lean`, `--no-telemetry`, `--plugin-cache-dir`, `--worktree`, `--worktree-base`. | ❌ |
| 1.6 | `release/README-alpha.md` shows `turf up ./path/to/config` and `turf destroy ./path/to/config`. It also describes two binaries, where doof's copy of the same file describes four and the local engine. | ❌ |
| 2.1–2.11 | As written (the strings in this file are the observed ones). | ✅ |
| 3.1–3.4, 3.6–3.8 | As written; 3.7's file `-rw-------` in a `drwx------` directory. | ✅ |

Findings: F-1 (blocker for the release that ships the skills change) P.5; F-2 (doc) 1.4; F-3
(doc) 1.6.

Since then: the skills branch merged (doof #156, #157), and turf #64 brings the CLI along (F-1;
P.5's four tests pass against doof `main` at `29b5ddf6`). The change that added this suite fixes
F-2 and F-3, bundles `turf-engine` and `turf-builtin-provider` in the tarball (P.1), and makes an
unattended run without `--yes` decline the agent's approval questions (6.7).
