# kind-crd-up.tape — the marquee demo: `turf up` on the kind cluster example.
#
# turf's hardest convergence. The kubernetes provider binds to the kind cluster's
# computed endpoint (unknown until the cluster exists), and the custom resource's
# `Turf` kind doesn't exist until its CRD is applied. turf defers + reloads the
# provider to converge it in ONE `turf up` — which surfaces as THREE approval gates
# (the phases): (1) create the cluster + CRD, (2) the CR after the provider reload,
# (3) the final reconcile. The tape waits for each gate, pauses so a viewer can read
# the plan, then approves.
#
# Render:  ./docs/recordings/render.sh kind-crd    (runs up, THEN destroy)
# render.sh copies the HCL from turf-examples into a temp dir, builds a real kind
# cluster during this take, and hands the live cluster/state to kind-crd-destroy.
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

# 1) Launch `turf up` in the lean TUI. up auto-delivers its runbook — no message to
#    type; turf reads the HCL, plans, and drives the whole convergence itself.
Type "turf up --lean"  Sleep 400ms  Enter

# 2) Phase 1 gate — turf thinks, then parks the cursor on the [y] yes footer. The
#    first plan (kind cluster + CRD) takes the longest to compute; @900s is generous
#    (a Wait timeout would discard the recording). trim_deadair.py compresses the
#    think spinner in post, so the long wait doesn't reach the final video.
Wait+Line@900s /\[y\] yes/
Sleep 5s          # viewer reads the plan
Type "y"
Sleep 3s          # let the gate footer clear before waiting on the next gate

# 3) Phase 2 gate — the cluster + CRD apply, turf reloads the kubernetes provider so
#    it re-discovers the new Turf API, then replans the custom resource and gates.
#    @900s (not 300s): a slower model (e.g. Sonnet) can take several minutes to walk
#    apply → provider-bounce → replan → gate; a tight timeout discards the recording.
Wait+Line@900s /\[y\] yes/
Sleep 5s
Type "y"
Sleep 3s

# 4) Phase 3 gate — the final reconcile plan (the CR, once the Turf API is
#    discovered). Same generous @900s — this is the span that overran a 300s cap.
Wait+Line@900s /\[y\] yes/
Sleep 5s
Type "y"

# 5) The last apply runs to completion (~15-25s). No cursor-line "done" marker
#    exists, so settle on a bounded sleep sized to the observed apply.
Sleep 30s

# 6) Exit the TUI cleanly and capture a final still.
Ctrl+C
Sleep 500ms
Screenshot docs/recordings/out/kind-crd-up.png
