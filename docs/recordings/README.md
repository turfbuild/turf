# turf demo recordings

Automatable terminal recordings of `turf` for the website, promo material, and
the Discussions showcase. Authored with [VHS](https://github.com/charmbracelet/vhs):
each `.tape` is a script that launches turf, types a real request, and records
the session to GIF / MP4 / WebM.

## Why it looks the way it does

- **Target the lean TUI** (`turf chat --lean`): it renders to normal scrollback
  (no alternate screen), so the capture is clean and every tool result stays
  expanded. The banner + timeline renderers still show.
- **Local `random` provider** (`scenarios/greeting`): a no-cloud, cached,
  instant plot. Recordings touch **no real infrastructure** and cost nothing to
  provision — only model tokens are spent.
- **One interactive gate**: turf pre-approves its tools, so the only prompt in a
  run is the plan-approval `[y] yes`. The tape waits for it, pauses so a viewer
  can read the plan, then approves — showing the plan → approve → apply loop.

## Render

```sh
brew install ttyd ffmpeg go            # one-time (VHS itself is built for you)
./docs/recordings/render.sh chat       # re-drive turf and rebuild out/chat.*
./docs/recordings/render.sh kind-crd   # the kind cluster example: up THEN destroy
./docs/recordings/render.sh            # all tapes
```

Provide model credentials in the environment (`ANTHROPIC_API_KEY`,
`GOOGLE_API_KEY`, …). `render.sh` sets up the tape's scenario, pins `TURF_MODEL`,
and tears the scenario down afterward.

### The VHS fork (Wait+Line)

Interactive tapes detect turf's approval gate with **`Wait+Line /\[y\] yes/`**,
which reads the **cursor line** — turf parks the cursor on the gate footer, so the
match works no matter how far turf has scrolled. Mainline VHS only has
`Wait+Screen`, which reads the **top of the buffer** and goes blind after one
screenful. So `render.sh` builds and uses a small fork on first run:
[`EronWright/vhs@screen-settled`](https://github.com/EronWright/vhs/tree/screen-settled)
→ `docs/recordings/.bin/vhs` (both `.bin/` and `.cache/` are gitignored). Override
with `VHS_BIN=/path/to/vhs`.

### The kind-crd scenario (the marquee demo: up + destroy)

`kind-crd` records the Kubernetes cluster example — turf's hardest convergence
(the kubernetes provider binds to the cluster's computed endpoint, and the custom
resource's kind doesn't exist until its CRD applies; turf defers + reloads the
provider to converge it in one `/up`). Its HCL is sourced from the sibling
**turf-examples** repo (`TURF_EXAMPLES_DIR`, default `../turf-examples`), copied
into a gitignored temp dir to record in. It needs **Docker + kind + kubectl**.

It is **two coupled tapes** — `kind-crd-up.tape` and `kind-crd-destroy.tape` —
that share **one live working dir**: the up take creates the real cluster + state,
and the destroy take consumes that same cluster/state to record the teardown. So
they MUST run in order, up before destroy. `render.sh kind-crd` is a convenience
that expands to `kind-crd-up kind-crd-destroy` and enforces that order; running
`kind-crd-destroy` alone errors unless an up state is already present. After the
destroy take, `render.sh` `kind delete cluster`s (safety net) and removes the temp
dir. Several minutes per run — the up take builds a real cluster.

**Pacing (1x + trimmed think pause).** These tapes record at true **1x** so the
scrolling stays readable, which means turf's multi-minute "thinking" phases would
otherwise play as dead air. After each kind-crd take, `render.sh` runs
`trim_deadair.py` on the mp4, which uses ffmpeg `freezedetect` to find the
low-motion think stretches (the screen is static except a tiny spinner) and cut
each down to a short, still-visible pause — keeping the "turf thinks → I approve"
beat without the wait — then regenerates the gif/webm from the trimmed mp4. Active
output (scrolling, tool results) is high-motion and stays at 1x. Tune with
`TRIM_NOISE` / `TRIM_MIN_STILL` / `TRIM_CAP` (see the script header). `chat` is
fast/local and is not trimmed.

## Cost model — capture vs. render

Rendering **re-drives turf against a live model**: each take is a real agent
loop (~$0.30 in tokens, 1–3 min, and non-deterministic in wording/timing). So:

- **Commit the artifacts** in `out/` and embed those. Don't wire a live render
  into the website build.
- **Re-render only when turf's UX changes.** Take the best of a few runs.
- For **$0 takes**, point at a local model: `TURF_REC_MODEL=dmr/ai/qwen3
  ./docs/recordings/render.sh chat` (requires Docker Model Runner).

## Layout

```
docs/recordings/
  tapes/            *.tape          VHS scripts (chat, kind-crd-up, kind-crd-destroy)
  scenarios/        greeting/       local no-cloud plot the tapes run against
  out/              *.mp4 .gif      committed artifacts embedded elsewhere
  render.sh         setup scenario → drive tape → trim → clean up
  trim_deadair.py   compress turf's "thinking" dead-air (kind-crd takes)
```

## Adding a tape

1. Drop `tapes/<name>.tape` (copy `chat.tape`). Output to `out/<name>.*`.
2. Wait on **cursor-line markers** with `Wait+Line`, not fixed sleeps — turf's
   model latency varies 45–210 s. The reliable cursor-line markers are
   `Type a message` (input ready) and `[y] yes` (approval gate). There is **no**
   cursor-line "done" marker — the editor placeholder shows both mid-run and when
   idle — so settle completion on a **bounded `Sleep`** sized to the apply, not a
   `Wait`. Give each `Wait` a generous `@timeout` (a `Wait` timeout discards the
   whole recording).
3. New scenario? Add a `case` to `scenario_dir`/`setup_scenario`/`teardown_scenario`
   in `render.sh`. Anything with real infra (like `kind-crd`) must tear it down.
4. `./docs/recordings/render.sh <name>` and eyeball `out/<name>.gif`.
