#!/usr/bin/env python3
"""Hand-built pixel-font "TURF" wordmark for turf's lean-TUI welcome banner.

This is a from-scratch 8-bit wordmark — NOT a conversion of the logo raster —
so the letters stay crisp instead of muddy. Each glyph is a small bitmap; the
script auto-outlines the letter mass in dark green, shades the body top→bottom
(highlight → grass → shadow), and renders to Unicode upper-half-block cells
(one char = two vertical pixels). Background pixels use the terminal's default
color (SGR 39/49), so the banner blends with any theme background.

Lines are switch-only (no mid-line SGR reset) so they survive lipgloss's
Foreground wrapper in leantui; each line ends with a single reset. Truecolor
terminal required. Run: python3 gen_banner.py assets/turf-banner.ansi
"""
import sys

# 6x9 pixel glyphs. '#' = letter body, '.' = empty.
GLYPHS = {
    "T": [
        "######",
        "######",
        "..##..",
        "..##..",
        "..##..",
        "..##..",
        "..##..",
        "..##..",
        "..##..",
    ],
    "U": [
        "##..##",
        "##..##",
        "##..##",
        "##..##",
        "##..##",
        "##..##",
        "##..##",
        "##..##",
        ".####.",
    ],
    "R": [
        "#####.",
        "##..##",
        "##..##",
        "##..##",
        "#####.",
        "##.##.",
        "##..##",
        "##..##",
        "##..##",
    ],
    "F": [
        "######",
        "######",
        "##....",
        "##....",
        "#####.",
        "#####.",
        "##....",
        "##....",
        "##....",
    ],
}

WORD = "TURF"
GLYPH_W, GLYPH_H = 6, 9
GAP = 2          # columns between glyphs (their outlines meet, reading as one wordmark)
MARGIN = 1       # transparent-then-outlined margin around the whole word

# Brand greens, shaded by row band for a crisp 8-bit gradient.
HILITE = (170, 222, 80)
GRASS  = (112, 182, 40)
SHADOW = (58, 120, 22)
OUTLINE = (16, 40, 10)

def body_color(row):
    if row <= 1:
        return HILITE
    if row <= 5:
        return GRASS
    return SHADOW

def build_mask():
    """Return a 2D grid of cell kinds: None=empty, ('b',row)=body at glyph row."""
    inner_w = len(WORD) * GLYPH_W + (len(WORD) - 1) * GAP
    W = inner_w + 2 * MARGIN
    H = GLYPH_H + 2 * MARGIN
    grid = [[None] * W for _ in range(H)]
    x0 = MARGIN
    for ch in WORD:
        g = GLYPHS[ch]
        for r in range(GLYPH_H):
            for c in range(GLYPH_W):
                if g[r][c] == "#":
                    grid[MARGIN + r][x0 + c] = ("b", r)
        x0 += GLYPH_W + GAP
    return grid, W, H

def colorize(grid, W, H):
    """Map kinds to RGB or None (transparent). Adds a dark outline hugging bodies."""
    px = [[None] * W for _ in range(H)]
    for y in range(H):
        for x in range(W):
            k = grid[y][x]
            if k is not None:
                px[y][x] = body_color(k[1])
    # outline: any empty cell 8-adjacent to a body cell
    for y in range(H):
        for x in range(W):
            if grid[y][x] is not None:
                continue
            near = any(
                0 <= y + dy < H and 0 <= x + dx < W and grid[y + dy][x + dx] is not None
                for dy in (-1, 0, 1) for dx in (-1, 0, 1)
            )
            if near:
                px[y][x] = OUTLINE
    return px

def sgr(top, bot):
    # Pick the glyph so a *transparent* half emits no ink. A transparent cell
    # must not draw ▀ — its foreground (SGR 39 = terminal default) is usually
    # light, so ▀ would paint a stripe. Cases:
    #   both empty  -> space   (nothing drawn)
    #   top only    -> ▀ fg=top,  bg=default
    #   bottom only -> ▄ fg=bot,  bg=default
    #   both        -> ▀ fg=top,  bg=bot
    def fg(c):
        return f"38;2;{c[0]};{c[1]};{c[2]}"
    if top is None and bot is None:
        return "\x1b[39;49m "
    if bot is None:
        return f"\x1b[{fg(top)};49m▀"
    if top is None:
        return f"\x1b[{fg(bot)};49m▄"
    return f"\x1b[{fg(top)};48;2;{bot[0]};{bot[1]};{bot[2]}m▀"

def render(px, W, H):
    if H % 2:                      # pad to even so every cell has a bottom pixel
        px.append([None] * W)
        H += 1
    lines = []
    for row in range(0, H, 2):
        cells = [sgr(px[row][x], px[row + 1][x]) for x in range(W)]
        lines.append("".join(cells) + "\x1b[0m")
    return lines

def main():
    out = sys.argv[1] if len(sys.argv) > 1 else "turf-banner.ansi"
    grid, W, H = build_mask()
    px = colorize(grid, W, H)
    lines = render(px, W, H)
    with open(out, "w", encoding="utf-8") as f:
        f.write("\n".join(lines) + "\n")
    print(f"wrote {out}: {W} cols x {len(lines)} rows", file=sys.stderr)

if __name__ == "__main__":
    main()
