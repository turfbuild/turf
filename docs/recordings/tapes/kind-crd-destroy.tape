# kind-crd-destroy.tape — the teardown half of the marquee demo: `turf destroy`.
#
# Runs AFTER kind-crd-up.tape, against the SAME working dir — it consumes the live
# kind cluster + state that the up take created and records turf tearing it all down
# (custom resource → CRD → cluster).
#
# NON-INTERACTIVE take (`--yes`): the teardown is auto-approved (turf's
# autoconfirm.go), so turf plans the full destroy and applies it with no human and
# no `[y]` gate. The tape just launches and waits for the run to finish.
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

# 1) Launch `turf destroy --yes` in the lean TUI. destroy auto-delivers its runbook
#    (no message to type) and `--yes` auto-approves the teardown, so turf reads the
#    state, plans the full reverse-dependency destroy, and applies it — then
#    auto-quits. The `echo; printf …; read` parks the cursor on a unique sentinel
#    line the instant turf exits, a deterministic "run complete" signal.
Type `turf destroy --yes --lean; s=$?; echo; printf 'turf destroy exited %s' "$s"; read`  Sleep 400ms  Enter

# 2) Wait for the teardown to finish (delete the CR, the CRD, then the cluster — the
#    kind cluster teardown itself takes ~10–15s). @300s is generous over the ~1–2 min
#    observed run; the sentinel only appears once turf has fully exited.
Wait+Line@300s /turf destroy exited/

# 3) Hold on the final frame (turf's "Destroy complete — 3 destroyed" summary) so a
#    viewer can read it, then capture a still.
Sleep 3s
Screenshot docs/recordings/out/kind-crd-destroy.png
