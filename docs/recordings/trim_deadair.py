#!/usr/bin/env python3
"""trim_deadair.py — compress turf's "thinking" dead-air in a recording.

turf recordings are captured at true 1x so the scrolling stays readable, but the
model's think phases play back as long stretches where the whole screen is static
except a tiny animated spinner. This tool finds those low-motion stretches with
ffmpeg's `freezedetect` (a noise tolerance tuned ABOVE the spinner's per-frame
delta, so a spinning-but-idle screen still reads as "frozen") and CUTS each one
down to a short, still-visible pause — keeping the "turf thinks, then I approve"
beat without the multi-minute wait. Active output (scrolling, tool results) is
high-motion, never matches a freeze, and stays untouched at 1x.

Usage:
    trim_deadair.py IN.mp4 [OUT.mp4]

With one arg it trims IN.mp4 in place. It then regenerates the sibling .gif and
.webm from the trimmed mp4. If no freeze longer than the cap is found (or anything
goes wrong), the inputs are left untouched and it exits 0 (best-effort).

Tunables (env):
    TRIM_NOISE     freezedetect noise tolerance, ratio 0..1 (default 0.003).
                   Higher = more tolerant = more of the spinner counts as frozen.
    TRIM_MIN_STILL only freezes at least this many seconds are trimmed (default 2.5).
    TRIM_CAP       seconds of each trimmed freeze to KEEP (default 3.0).
    TRIM_GIF_FPS   fps for the regenerated gif (default 25).
"""
import os, re, sys, subprocess, tempfile, shutil

NOISE     = os.environ.get("TRIM_NOISE", "0.003")
MIN_STILL = float(os.environ.get("TRIM_MIN_STILL", "2.5"))
CAP       = float(os.environ.get("TRIM_CAP", "3.0"))
GIF_FPS   = os.environ.get("TRIM_GIF_FPS", "25")


def run(cmd):
    return subprocess.run(cmd, capture_output=True, text=True)


def probe_duration(path):
    r = run(["ffprobe", "-v", "error", "-show_entries", "format=duration",
             "-of", "default=nk=1:nw=1", path])
    try:
        return float(r.stdout.strip())
    except ValueError:
        return None


def detect_freezes(path):
    """Return [(start, end), ...] of frozen stretches >= MIN_STILL seconds."""
    r = run(["ffmpeg", "-hide_banner", "-i", path,
             "-vf", f"freezedetect=n={NOISE}:d={MIN_STILL}",
             "-map", "0:v:0", "-f", "null", "-"])
    log = r.stderr
    starts = [float(m) for m in re.findall(r"freeze_start:\s*([0-9.]+)", log)]
    ends   = [float(m) for m in re.findall(r"freeze_end:\s*([0-9.]+)", log)]
    # freeze_start without a matching freeze_end means the freeze ran to EOF.
    dur = probe_duration(path)
    while len(ends) < len(starts) and dur is not None:
        ends.append(dur)
    return list(zip(starts, ends))


def keep_intervals(freezes, duration):
    """Complement of the cut regions. Each freeze longer than CAP has its tail
    (start+CAP .. end) removed; everything else is kept at 1x."""
    cuts = [(s + CAP, e) for (s, e) in freezes if (e - s) > CAP + 0.05]
    cuts.sort()
    keep, t = [], 0.0
    for cs, ce in cuts:
        if cs > t:
            keep.append((t, cs))
        t = max(t, ce)
    if t < duration:
        keep.append((t, duration))
    return keep, cuts


def build_trimmed(inp, outp, keep):
    expr = "+".join(f"between(t,{a:.3f},{b:.3f})" for a, b in keep)
    vf = f"select='{expr}',setpts=N/FRAME_RATE/TB"
    r = run(["ffmpeg", "-hide_banner", "-y", "-i", inp, "-vf", vf,
             "-an", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-crf", "18",
             "-preset", "veryfast", outp])
    return r.returncode == 0


def regen_gif(mp4, gif):
    with tempfile.TemporaryDirectory() as td:
        pal = os.path.join(td, "palette.png")
        if run(["ffmpeg", "-hide_banner", "-y", "-i", mp4, "-vf",
                f"fps={GIF_FPS},palettegen=stats_mode=diff", pal]).returncode != 0:
            return False
        return run(["ffmpeg", "-hide_banner", "-y", "-i", mp4, "-i", pal,
                    "-lavfi", f"fps={GIF_FPS}[x];[x][1:v]paletteuse=dither=bayer:bayer_scale=5:diff_mode=rectangle",
                    gif]).returncode == 0


def regen_webm(mp4, webm):
    return run(["ffmpeg", "-hide_banner", "-y", "-i", mp4, "-an",
                "-c:v", "libvpx-vp9", "-b:v", "0", "-crf", "30",
                "-row-mt", "1", webm]).returncode == 0


def main():
    if len(sys.argv) < 2:
        print(__doc__); return 2
    inp = sys.argv[1]
    outp = sys.argv[2] if len(sys.argv) > 2 else inp
    if not os.path.isfile(inp):
        print(f"trim_deadair: no such file: {inp}"); return 1

    dur = probe_duration(inp)
    if dur is None:
        print("trim_deadair: could not probe duration — leaving untouched"); return 0

    freezes = detect_freezes(inp)
    keep, cuts = keep_intervals(freezes, dur)
    removed = sum(e - s for s, e in cuts)
    print(f"trim_deadair: {len(freezes)} freeze(s) ≥{MIN_STILL}s, "
          f"trimming {len(cuts)} (removing {removed:.1f}s of {dur:.1f}s)")

    if not cuts:
        print("trim_deadair: nothing to trim")
        if outp != inp and not build_passthrough(inp, outp):
            return 1
    else:
        tmp = tempfile.NamedTemporaryFile(suffix=".mp4", delete=False,
                                          dir=os.path.dirname(os.path.abspath(outp)) or ".").name
        if not build_trimmed(inp, tmp, keep):
            os.path.exists(tmp) and os.remove(tmp)
            print("trim_deadair: trim encode failed — leaving artifacts untouched"); return 1
        shutil.move(tmp, outp)
        print(f"trim_deadair: wrote {outp} ({probe_duration(outp):.1f}s)")

    # Always (re)generate the gif/webm siblings from the final mp4 so callers can
    # have the tape emit mp4 only (a multi-minute 1x gif would be huge/slow).
    base = os.path.splitext(outp)[0]
    if regen_gif(outp, base + ".gif"):
        print(f"trim_deadair: wrote {base}.gif")
    if regen_webm(outp, base + ".webm"):
        print(f"trim_deadair: wrote {base}.webm")
    return 0


def build_passthrough(inp, outp):
    return run(["ffmpeg", "-hide_banner", "-y", "-i", inp, "-c", "copy", outp]).returncode == 0


if __name__ == "__main__":
    sys.exit(main())
