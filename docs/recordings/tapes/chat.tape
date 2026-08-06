# chat.tape — the interactive hero demo.
#
# A human types a natural-language request into `turf chat --lean` and turf
# opens a workspace, plans, waits for approval, then applies — all against the
# LOCAL `random` provider (no cloud, no spend beyond model tokens).
#
# Render:  ./docs/recordings/render.sh chat
# (render.sh resets the scenario to a fresh create and exports your model creds.)
#
# NOTE: rendering re-drives turf against a live model — each render costs ~$0.30
# in tokens and takes 1–3 min. Commit the rendered out/chat.mp4|gif and only
# re-render when turf's UX changes.

Output docs/recordings/out/chat.gif
Output docs/recordings/out/chat.mp4
Output docs/recordings/out/chat.webm

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

# The scenario dir + model creds are provided by render.sh via the environment.
# cd in off-camera so the recording opens on a clean prompt.
Hide
Type `cd "$TURF_REC_SCENARIO" && clear`  Enter
Show

# 1) Launch the lean TUI (renders to scrollback — the clean capture target).
Type "turf chat --lean"  Sleep 400ms  Enter
Wait+Line@30s /Type a message/
Sleep 1s

# 2) A human asks, in plain English, for the deploy.
Type "Deploy this configuration: open a workspace, make a plan, and apply it once I approve."
Sleep 800ms
Enter

# 3) turf plans, then asks for approval — the one interactive gate (tools are
#    pre-approved, so this is the only prompt). Wait+Line reads the CURSOR line;
#    turf parks the cursor on the gate footer, so this matches however far turf
#    has scrolled (Wait+Screen reads the top of the buffer and goes blind).
Wait+Line@300s /\[y\] yes/
Sleep 2.5s
Type "y"

# 4) Apply runs to completion. There is no cursor-line "done" marker (the editor
#    placeholder shows both mid-run and when idle), so settle on a bounded sleep
#    sized to the fast local `random` apply rather than a blind Wait+Screen.
Sleep 20s

# 5) Exit the TUI cleanly.
Ctrl+C
Sleep 500ms
Screenshot docs/recordings/out/chat.png
