#!/usr/bin/env bash
# render.sh — drive a VHS tape to (re)generate a turf demo recording.
#
#   ./docs/recordings/render.sh [name ...]      # default: all tapes
#   ./docs/recordings/render.sh chat            # just tapes/chat.tape
#   ./docs/recordings/render.sh kind-crd        # the kind cluster example
#
# Each render RE-DRIVES turf against a live model — expect ~$0.30 in tokens and
# 1–3 min per tape (kind-crd is several minutes: it builds a real cluster). The
# `greeting` scenario is the LOCAL `random` provider (no cloud, nothing to clean
# up); `kind-crd` is sourced from the sibling turf-examples repo and creates a
# real kind cluster that this script tears down after the take. Commit the
# artifacts in out/ and only re-render when turf's UX (or branding) changes.
#
# Interactive tapes wait on turf's approval gate with `Wait+Line /\[y\] yes/`,
# which reads the CURSOR line (turf parks the cursor on the gate footer). That
# scope only exists in the VHS fork this script builds (see ensure_vhs) — mainline
# VHS has `Wait+Screen`, which reads the TOP of the buffer and goes blind once
# turf scrolls past one screenful.
#
# Requirements: turf, ttyd, ffmpeg, go, git on PATH; model credentials in the
# environment (e.g. ANTHROPIC_API_KEY / GOOGLE_API_KEY) and optionally TURF_MODEL.
# Point TURF_REC_MODEL at a local DMR model for $0 takes. kind-crd additionally
# needs Docker + kind + kubectl, and TURF_EXAMPLES_DIR (default: ../turf-examples).
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
REC="docs/recordings"

# Pin the model for repeatable takes (override with TURF_REC_MODEL, e.g. a
# google/… ref or a local dmr/… model). Default is Anthropic Sonnet 5 — it drives
# turf's cross-phase convergence (e.g. kind-crd) reliably; Gemini does not.
export TURF_MODEL="${TURF_REC_MODEL:-${TURF_MODEL:-anthropic/claude-sonnet-5}}"

command -v turf >/dev/null || { echo "turf not found on PATH"; exit 1; }

# ---------------------------------------------------------------------------
# ensure_vhs — build (once) and select the VHS fork that provides Wait+Line.
# Override with VHS_BIN=/path/to/vhs to use a prebuilt binary.
# ---------------------------------------------------------------------------
VHS_FORK_REPO="https://github.com/EronWright/vhs"
VHS_FORK_BRANCH="screen-settled"
ensure_vhs() {
  if [ -n "${VHS_BIN:-}" ]; then VHS="$VHS_BIN"; return; fi
  VHS="$PWD/$REC/.bin/vhs"
  [ -x "$VHS" ] && return
  command -v go  >/dev/null || { echo "go not found (needed to build the vhs fork)"; exit 1; }
  command -v git >/dev/null || { echo "git not found (needed to fetch the vhs fork)"; exit 1; }
  local src="$PWD/$REC/.cache/vhs"
  echo "▶ building vhs fork ($VHS_FORK_BRANCH) → $VHS (one-time)"
  if [ ! -d "$src/.git" ]; then
    rm -rf "$src"
    git clone --depth 1 --branch "$VHS_FORK_BRANCH" "$VHS_FORK_REPO" "$src"
  fi
  mkdir -p "$(dirname "$VHS")"
  ( cd "$src" && go build -o "$VHS" . )
}
ensure_vhs
command -v ttyd   >/dev/null || { echo "ttyd not found (brew install ttyd)"; exit 1; }
command -v ffmpeg >/dev/null || { echo "ffmpeg not found (brew install ffmpeg)"; exit 1; }

# ---------------------------------------------------------------------------
# Per-tape scenario wiring. Each tape names a scenario dir the tape `cd`s into
# (via $TURF_REC_SCENARIO) plus setup/teardown. Extend the cases as tapes grow.
# ---------------------------------------------------------------------------
KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-turf-crd-demo}"

scenario_dir() {
  case "$1" in
    kind-crd-*) echo "$PWD/$REC/.cache/kind-crd" ;;  # temp working copy (gitignored), shared up→destroy
    *)          echo "$PWD/$REC/scenarios/greeting" ;;
  esac
}

reset_greeting() {  # wipe generated state so the tape records a fresh create
  local dir="$1"
  rm -rf "$dir/.turf" "$dir/.terraform" "$dir/.terraform.tfstate.lock.info" "$dir"/terraform.tfstate*
}

setup_scenario() {
  local name="$1" dir="$2"
  case "$name" in
    kind-crd-up)
      # Fresh working copy: the up take starts from a clean tree and no cluster.
      local ex="${TURF_EXAMPLES_DIR:-$PWD/../turf-examples}/terraform/kubernetes/kind-crd"
      [ -d "$ex" ] || { echo "kind-crd example not found at $ex (set TURF_EXAMPLES_DIR)"; exit 1; }
      command -v kind    >/dev/null || { echo "kind not found on PATH";    exit 1; }
      command -v kubectl >/dev/null || { echo "kubectl not found on PATH"; exit 1; }
      docker info >/dev/null 2>&1   || { echo "Docker is not running (kind needs it)"; exit 1; }
      kind delete cluster --name "$KIND_CLUSTER_NAME" >/dev/null 2>&1 || true
      rm -rf "$dir"; mkdir -p "$dir"
      cp "$ex"/*.tf "$dir"/          # HCL only; turf-examples stays authoritative
      ;;
    kind-crd-destroy)
      # Reuse the LIVE state the up take just created — no reset. destroy tears it
      # down. Assert that state exists so a lone `render.sh kind-crd-destroy` fails
      # loudly instead of recording an empty teardown.
      [ -d "$dir/.turf" ] || {
        echo "kind-crd-destroy needs the up state at $dir (.turf missing)."
        echo "Run 'render.sh kind-crd-up' first, or 'render.sh kind-crd' to record the pair."
        exit 1
      }
      ;;
    *)
      setup_scenario_greeting "$dir" ;;
  esac
}
setup_scenario_greeting() { reset_greeting "$1"; }

teardown_scenario() {
  local name="$1" dir="$2"
  case "$name" in
    kind-crd-up)
      : ;;   # no-op: leave the live cluster + state for the destroy take
    kind-crd-destroy)
      kind delete cluster --name "$KIND_CLUSTER_NAME" >/dev/null 2>&1 || true  # safety net
      rm -rf "$dir" ;;
    *)
      reset_greeting "$dir" ;;   # leave the tree clean after the take
  esac
}

# Requested names (explicit args, else every tape). `kind-crd` is a convenience
# alias for the coupled pair. The destroy take consumes the live cluster/state the
# up take creates, so kind-crd-up MUST run before kind-crd-destroy — enforce that
# regardless of the glob/arg order (a bare glob sorts destroy first, alphabetically).
requested=("$@")
if [ ${#requested[@]} -eq 0 ]; then
  requested=()
  for t in "$REC"/tapes/*.tape; do requested+=("$(basename "$t" .tape)"); done
fi

expanded=()
for name in ${requested[@]+"${requested[@]}"}; do
  case "$name" in
    kind-crd) expanded+=(kind-crd-up kind-crd-destroy) ;;
    *)        expanded+=("$name") ;;
  esac
done

# Stable-partition kind-crd-destroy to the end so up always precedes it.
tapes=()
for name in ${expanded[@]+"${expanded[@]}"}; do
  [ "$name" = "kind-crd-destroy" ] || tapes+=("$name")
done
for name in ${expanded[@]+"${expanded[@]}"}; do
  [ "$name" = "kind-crd-destroy" ] && tapes+=("$name")
done

for name in "${tapes[@]}"; do
  tape="$REC/tapes/$name.tape"
  [ -f "$tape" ] || { echo "no such tape: $tape"; exit 1; }

  dir="$(scenario_dir "$name")"
  export TURF_REC_SCENARIO="$dir"
  setup_scenario "$name" "$dir"

  echo "▶ rendering $name (model=$TURF_MODEL, scenario=$dir)"
  "$VHS" "$tape"

  # kind-crd tapes record at 1x and emit mp4 ONLY (a multi-minute 1x gif would be
  # huge); trim the "thinking" dead-air, then (re)generate the gif/webm siblings.
  case "$name" in
    kind-crd-*)
      # Cap every static stretch (think spinner AND the read-the-plan pause, which
      # are indistinguishable by motion) to ~5s. TRIM_NOISE may need raising if a
      # take's spinner isn't caught — see trim_deadair.py.
      TRIM_MIN_STILL="${TRIM_MIN_STILL:-4}" TRIM_CAP="${TRIM_CAP:-5}" \
        python3 "$REC/trim_deadair.py" "$REC/out/$name.mp4" ;;
  esac

  teardown_scenario "$name" "$dir"
  echo "✓ $name → $REC/out/"
done
