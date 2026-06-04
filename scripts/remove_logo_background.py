"""Remove the solid dark background from the project logo.

Reads mixi.png (orange pixel character on a near-black background),
flood-fills the background from the image edges with a colour tolerance,
keeps only the largest connected foreground region (drops stray
decorations like the corner sparkle), and writes logo_transparent.png
with a proper alpha channel.

Usage: .claude/skills/.venv/bin/python3 scripts/remove_logo_background.py
"""

from collections import deque
from pathlib import Path

from PIL import Image

SRC = Path(__file__).resolve().parent.parent / "mixi.png"
DST = Path(__file__).resolve().parent.parent / "logo_transparent.png"

# Max squared RGB distance from the sampled corner colour that still
# counts as "background". The backdrop is uniform, so this stays tight
# enough to never eat into the orange character.
TOLERANCE_SQ = 60 ** 2


def flood_background(px, w, h, bg):
    """Return a set of (x, y) background pixels reachable from the edges."""
    seen = set()
    queue = deque()
    for x in range(w):
        queue.append((x, 0))
        queue.append((x, h - 1))
    for y in range(h):
        queue.append((0, y))
        queue.append((w - 1, y))
    while queue:
        x, y = queue.popleft()
        if (x, y) in seen or not (0 <= x < w and 0 <= y < h):
            continue
        r, g, b = px[x, y][:3]
        if (r - bg[0]) ** 2 + (g - bg[1]) ** 2 + (b - bg[2]) ** 2 > TOLERANCE_SQ:
            continue
        seen.add((x, y))
        queue.extend(((x + 1, y), (x - 1, y), (x, y + 1), (x, y - 1)))
    return seen


def largest_component(foreground, w, h):
    """Return the largest 4-connected region within the foreground set."""
    remaining = set(foreground)
    best = set()
    while remaining:
        start = remaining.pop()
        comp = {start}
        queue = deque([start])
        while queue:
            x, y = queue.popleft()
            for nb in ((x + 1, y), (x - 1, y), (x, y + 1), (x, y - 1)):
                if nb in remaining:
                    remaining.discard(nb)
                    comp.add(nb)
                    queue.append(nb)
        if len(comp) > len(best):
            best = comp
    return best


def main():
    im = Image.open(SRC).convert("RGBA")
    w, h = im.size
    px = im.load()

    bg = px[0, 0][:3]  # backdrop is uniform; the corner is representative
    background = flood_background(px, w, h, bg)
    foreground = {(x, y) for y in range(h) for x in range(w)} - background
    character = largest_component(foreground, w, h)

    out = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    out_px = out.load()
    for x, y in character:
        out_px[x, y] = px[x, y]

    out.save(DST)
    print(f"wrote {DST} ({len(character)} character px, "
          f"{len(foreground) - len(character)} stray px dropped)")


if __name__ == "__main__":
    main()
