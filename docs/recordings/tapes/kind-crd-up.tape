# kind-crd-up.tape — the marquee demo: `turf up` on the kind cluster example.
#
# turf's hardest convergence. The kubernetes provider binds to the kind cluster's
# computed endpoint (unknown until the cluster exists), and the custom resource's
# `Turf` kind doesn't exist until its CRD is applied. turf defers + reloads the
# provider to converge it in ONE `turf up` — surfacing as three phases (create the
# cluster + CRD, the CR after the provider reload, the final reconcile).
#
# NON-INTERACTIVE take (`--yes`): every approval prompt is auto-confirmed by turf's
# autoconfirm.go, so the run drives itself through all three phases with no human
# and no `[y]` gate to wait on. The model still calls user_prompt at each phase
# (the approval checkpoints still render), it just resolves to "yes" instantly.
# This is why the take no longer needs per-gate cursor detection — the one earlier
# failure mode (a tall phase-3 plan hiding the `[y] yes` footer from Wait+Line) is
# gone by construction.
#
# Render:  ./docs/recordings/render.sh kind-crd    (runs up, THEN destroy)
# render.sh copies the HCL from turf-examples into a temp dir, kind-deletes any
# stale cluster, builds a real kind cluster during this take, and hands the live
# cluster/state to kind-crd-destroy.
#
# Outputs the MP4 only; render.sh then runs trim_deadair.py to compress the long
# "thinking" stretches and (re)generate the gif/webm from the trimmed mp4.

Output docs/recordings/out/kind-crd-up.mp4

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

# The scenario dir (a fresh copy of the kind-crd HCL) + model creds come from
# render.sh via the environment. cd in off-camera so we open on a clean prompt.
Hide
Type `cd "$TURF_REC_SCENARIO" && clear`  Enter
Show

# 1) Launch `turf up --yes` in the lean TUI. `--yes` auto-confirms every approval,
#    so turf plans and applies all three convergence phases itself, then auto-quits
#    when the run ends (the up/destroy lean TUI exits on completion). The trailing
#    `echo; printf …; read` parks the cursor on a unique sentinel line the instant
#    turf exits — a deterministic "run complete" signal for the tape to wait on
#    (`read` blocks so the cursor rests on the sentinel; VHS reaps it at teardown).
Type `turf up --yes --lean; s=$?; echo; printf 'turf up exited %s' "$s"; read`  Sleep 400ms  Enter

# 2) Wait for the whole convergence to finish. @600s is generous headroom over the
#    ~1–3 min observed run (a Wait timeout would discard the recording); the sentinel
#    only appears once turf has fully exited, so there is nothing to match early.
#    trim_deadair.py compresses the think spinners in post.
Wait+Line@600s /turf up exited/

# 3) Hold on the final frame (turf's "Apply complete — 3 created" summary above the
#    sentinel) so a viewer can read it, then capture a still.
Sleep 3s
Screenshot docs/recordings/out/kind-crd-up.png
