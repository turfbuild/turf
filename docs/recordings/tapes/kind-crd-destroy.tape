# kind-crd-destroy.tape — the teardown half of the marquee demo: `turf destroy`.
#
# Runs AFTER kind-crd-up.tape, against the SAME working dir — it consumes the live
# kind cluster + state that the up take created and records turf tearing it all down
# (custom resource → CRD → cluster). One approval gate: turf plans the full destroy,
# waits for approval, then applies.
#
# Render:  ./docs/recordings/render.sh kind-crd    (runs up first, THEN this)
# Running this tape alone errors unless an up state is already present (render.sh
# asserts it). After this take render.sh kind-delete's the cluster (safety) and
# removes the temp dir.
#
# Outputs the MP4 only; render.sh then runs trim_deadair.py to compress the
# "thinking" stretch and (re)generate the gif/webm from the trimmed mp4.

Output docs/recordings/out/kind-crd-destroy.mp4

Require turf

Set Shell "zsh"
Set FontSize 18
Set Width 1600
Set Height 950
Set Padding 20
Set Theme "Dracula"
Set WindowBar Colorful
Set BorderRadius 10
Set PlaybackSpeed 1.0
Set TypingSpeed 60ms

# The scenario dir (still holding the up take's live state) + model creds come from
# render.sh via the environment. cd in off-camera so we open on a clean prompt.
Hide
Type `cd "$TURF_REC_SCENARIO" && clear`  Enter
Show

# 1) Launch `turf destroy` in the lean TUI. destroy auto-delivers its runbook — turf
#    reads the existing state and plans the full teardown itself, no message to type.
Type "turf destroy --lean"  Sleep 400ms  Enter

# 2) The one approval gate — turf thinks, then parks the cursor on the [y] yes footer
#    with the destroy plan (the CR, the CRD, and the cluster, in dependency order).
#    @600s leaves room for a slower model (e.g. Sonnet) to plan the full teardown;
#    trim_deadair.py compresses the think spinner in post.
Wait+Line@600s /\[y\] yes/
Sleep 5s          # viewer reads the destroy plan
Type "y"

# 3) The teardown runs to completion (~15-30s: delete the CR, the CRD, then the
#    cluster — the kind cluster teardown itself takes ~10-15s). No cursor-line
#    "done" marker, so settle on a bounded sleep.
Sleep 30s

# 4) Exit the TUI cleanly and capture a final still.
Ctrl+C
Sleep 500ms
Screenshot docs/recordings/out/kind-crd-destroy.png
