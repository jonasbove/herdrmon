# /// script
# requires-python = ">=3.11"
# dependencies = ["fonttools", "skia-pathops", "pillow"]
# ///
"""Build "Herdrmon Icons": the mons' animations as a COLRv0 colour font.

Every animation frame from anims.py is a 35x17 canvas: five terminal cells of
7x17 px. Each cell becomes one glyph made of COLRv0 layers, one per colour,
so the terminal draws the sprite in its own colours (fg colour rules don't
tint it). A frame is then a run of five codepoints, and frames.json lists the
run for every frame of every animation of every mon.

Codepoints (allocated in mon order, so the same mons give the same font):
  stills     each mon's still frame, 5 codepoints in a BMP PUA gap that Nerd
             Fonts (v2 and v3), Font Awesome, Adwaita and Omarchy's icon font
             all leave empty (STILL_POOL)
  U+100000.. every other cell, deduplicated: an identical cell anywhere
             (across animations or mons) is one glyph

The build refuses to write a font whose codepoints overlap a glyph in any
other font fontconfig can see (fc-list), since the terminal might take that
glyph instead. --guard-exclude FAMILY skips a font you know isn't ahead of
this one in the fallback chain; --no-guard skips the check.

Pixel grid: JetBrains Mono at 12 px/em, cell 7x17 px with the baseline 13 px
from the top, so 1 px = 1000/12 units.

Run: uv run font/build.py [--config PATH] [--assets DIR] [--out-font PATH]
       [--out-frames PATH] [--mons a,b,c] [--no-guard] [--guard-exclude FAMILY]
"""

import argparse
import bisect
import json
import os
import pathlib
import shutil
import subprocess
import sys
import time

import pathops
from fontTools.fontBuilder import FontBuilder
from fontTools.misc.timeTools import epoch_diff
from fontTools.pens.boundsPen import BoundsPen
from fontTools.pens.t2CharStringPen import T2CharStringPen

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import anims  # noqa: E402
import fetch  # noqa: E402

FAMILY = "Herdrmon Icons"
PS_NAME = "HerdrmonIcons-Regular"
FRAMES_VERSION = 2
ADVANCE = 600             # one JetBrains Mono cell; sprites overhang like Nerd icons do

PX = 1000 / 12            # one screen pixel at 12 px/em
CELL_W = 7                # pixels per terminal cell
CELL_TOP = 13             # cell rows above the baseline (17 px cell: 13 + 4)
SPRITE_CELLS = anims.CELLS

# BMP PUA gaps for the stills, checked free in Nerd Fonts v2 + v3, Font
# Awesome 7, Adwaita Sans/Mono and Omarchy's font: 454 codepoints, 90 mons.
STILL_POOL = [(0xE959, 0xEA5F), (0xEC85, 0xECFF), (0xE6BC, 0xE6FF)]
ANIM_BASE, ANIM_LAST = 0x100000, 0x10FFFD     # Supplementary PUA-B
MAX_GLYPHS = 0xFFFF                            # OpenType numGlyphs is 16-bit


# ------------------------------------------------------------------- paths

def default_font():
    env = os.environ.get("HERDRMON_FONT")
    return pathlib.Path(env) if env else fetch.xdg("XDG_DATA_HOME", ".local/share") / "fonts" / "HerdrmonIcons.otf"


def default_frames():
    env = os.environ.get("HERDRMON_FRAMES")
    return pathlib.Path(env) if env else fetch.xdg("XDG_DATA_HOME", ".local/share") / "herdrmon" / "frames.json"


# ------------------------------------------------------------------- guard

class Guard:
    """Codepoints claimed by the fonts fontconfig can see, minus our own
    family (an older build of this font) and the excluded families."""

    def __init__(self, exclude=()):
        if not shutil.which("fc-list"):
            raise SystemExit("build: fc-list not found (install fontconfig), or pass --no-guard")
        skip = {FAMILY.lower(), *(e.lower() for e in exclude)}
        out = subprocess.run(["fc-list", "--format", "%{family}\t%{file}\t%{charset}\n"],
                             capture_output=True, text=True, check=True).stdout
        ranges = []
        for line in out.splitlines():
            fam, _, rest = line.partition("\t")
            path, _, charset = rest.partition("\t")
            if {f.strip().lower() for f in fam.split(",")} & skip:
                continue
            for tok in charset.split():
                lo, _, hi = tok.partition("-")
                lo = int(lo, 16)
                hi = int(hi, 16) if hi else lo
                if hi >= 0xE000:          # only the PUAs matter here
                    ranges.append((lo, hi, fam.split(",")[0], path))
        self.ranges = ranges

    def taken(self, lo, hi):
        """Codepoints in lo..hi that some font has a glyph for."""
        return {cp for a, b, _, _ in self.ranges if a <= hi and b >= lo
                for cp in range(max(a, lo), min(b, hi) + 1)}

    def check(self, cps):
        cps = sorted(cps)
        clashes = {}
        for lo, hi, fam, path in self.ranges:
            hit = cps[bisect.bisect_left(cps, lo):bisect.bisect_right(cps, hi)]
            if hit:
                clashes.setdefault((fam, path), set()).update(hit)
        if clashes:
            lines = ["build: refusing to build: these codepoints already have glyphs in other fonts,"
                     " and a terminal may draw those instead of ours:"]
            for (fam, path), cl in sorted(clashes.items()):
                cl = sorted(cl)
                lines.append(f"  {fam!r} ({path}): {len(cl)} codepoints, U+{cl[0]:04X}..U+{cl[-1]:04X}")
            lines.append("Remove or disable that font, or, if it is never a fallback ahead of "
                         f"{FAMILY!r} in your terminal, pass --guard-exclude 'FAMILY' (or --no-guard).")
            raise SystemExit("\n".join(lines))


# --------------------------------------------------------------- codepoints

class Stills:
    """Allocates 5-codepoint runs from STILL_POOL, in order, skipping any
    codepoint another font claims (when the guard is on)."""

    def __init__(self, guard=None):
        taken = set().union(*(guard.taken(lo, hi) for lo, hi in STILL_POOL)) if guard else set()
        self.free = [cp for lo, hi in STILL_POOL for cp in range(lo, hi + 1) if cp not in taken]
        self.i = 0

    def take(self, mon):
        while self.i + SPRITE_CELLS <= len(self.free):
            run = self.free[self.i:self.i + SPRITE_CELLS]
            self.i += 1
            if run[-1] - run[0] == SPRITE_CELLS - 1:      # contiguous
                self.i += SPRITE_CELLS - 1
                return run[0]
        raise SystemExit(f"build: no room for {mon}'s still in the BMP PUA pool "
                         f"({sum(hi - lo + 1 for lo, hi in STILL_POOL)} codepoints); bake fewer mons")


# ------------------------------------------------------------------ glyphs

class Shape:
    """Build a pathops.Path through its pen (Path itself is not a pen)."""

    def __init__(self):
        self.path = pathops.Path()
        self.pen = self.path.getPen()


def rects_path(runs):
    """[(x, y, x1)] one-pixel-high runs (font pixel grid) -> one path."""
    out = None
    for x, y, x1 in runs:
        s = Shape()
        s.pen.moveTo((x * PX, y * PX))
        s.pen.lineTo(((x1 + 1) * PX, y * PX))
        s.pen.lineTo(((x1 + 1) * PX, (y + 1) * PX))
        s.pen.lineTo((x * PX, (y + 1) * PX))
        s.pen.closePath()
        out = s.path if out is None else pathops.op(out, s.path, pathops.PathOp.UNION)
    return out


def cell_runs(img, c):
    """Cell c of a frame -> [(rgba, runs)], one layer per colour.

    Canvas row r sits (CELL_TOP - 1 - r) px above the baseline. Pixels are
    merged into horizontal runs per colour."""
    runs = {}
    px = img.load()
    for r in range(img.height):
        y = CELL_TOP - 1 - r
        x = 0
        while x < CELL_W:
            p = px[c * CELL_W + x, r]
            if p[3] < 128:
                x += 1
                continue
            x1 = x
            while x1 + 1 < CELL_W and px[c * CELL_W + x1 + 1, r] == p:
                x1 += 1
            runs.setdefault(p[:3], []).append((x, y, x1))
            x = x1 + 1
    return [((*rgb, 255), tuple(rs)) for rgb, rs in sorted(runs.items())]


class Glyphs:
    """Cells -> glyphs. An identical cell is one glyph wherever it appears,
    and an identical layer shape is one glyph whatever its colour (the colour
    lives in the COLR record), which keeps numGlyphs down."""

    def __init__(self):
        self.cells = {}       # glyph name -> (cp, [(layer name, palette rgba)])
        self.by_key = {}      # cell pixels -> cp
        self.layers = {}      # runs -> layer glyph name
        self.next_cp = ANIM_BASE

    def cell(self, img, c, cp=None):
        key = img.crop((c * CELL_W, 0, (c + 1) * CELL_W, img.height)).tobytes()
        if cp is None and key in self.by_key:
            return self.by_key[key]
        if cp is None:
            cp, self.next_cp = self.next_cp, self.next_cp + 1
            if cp > ANIM_LAST:
                raise SystemExit("build: out of codepoints in Supplementary PUA-B (65,534 cells); bake fewer mons")
        parts = []
        for rgba, runs in cell_runs(img, c):
            name = self.layers.setdefault(runs, f"l{len(self.layers)}")
            parts.append((name, rgba))
        self.cells[f"u{cp:X}"] = (cp, parts)
        self.by_key.setdefault(key, cp)
        return cp

    def count(self):
        """numGlyphs: .notdef, space, the cells and the layer shapes."""
        return 2 + len(self.cells) + len(self.layers)


def frame_string(glyphs, img, cells, row=None):
    if row is not None:
        img = img.crop((0, 17 * row, img.width, 17 * (row + 1)))
    return "".join(chr(glyphs.cell(img, c)) for c in range(cells))


def bake(mons, guard=None, log=print):
    """-> (Glyphs, manifest) for the mons, in order."""
    glyphs = Glyphs()
    stills = Stills(guard)
    manifest = {"version": FRAMES_VERSION, "cells": SPRITE_CELLS, "font": FAMILY,
                "mons": {}, "big": {}, "big3": {}, "names": {}}
    for mon in mons:
        t = time.monotonic()
        animations = anims.build(mon)
        # The still claims its BMP codepoints first, so every frame that
        # repeats one of its cells (most of them) uses those.
        base = stills.take(mon)
        still = animations["base"][0]
        for c in range(SPRITE_CELLS):
            glyphs.cell(still, c, base + c)
        manifest["mons"][mon] = {name: [frame_string(glyphs, img, SPRITE_CELLS) for img in frames]
                                 for name, frames in animations.items()}
        manifest["names"][mon] = anims.TABLE[mon]["name"]
        log(f"  {mon}: {time.monotonic() - t:.1f}s, {glyphs.count():,} glyphs so far")
        over_budget(glyphs, mons[:mons.index(mon) + 1], len(mons))
    # Big 2-row previews get their codepoints after every small frame, so
    # adding them never renumbers the sidebar's. "big3" is the same art on a
    # 3-row canvas, so it lands centred in an odd number of rows.
    for key, rows in (("big", anims.BIG_ROWS), ("big3", 3)):
        for mon in mons:
            manifest[key][mon] = {
                name: [[frame_string(glyphs, img, anims.BIG_CELLS, r) for r in range(rows)] for img in frames]
                for name, frames in anims.build_big(mon, rows).items()
            }
    return glyphs, manifest


# -------------------------------------------------------------------- font

def write_font(glyphs, path):
    paths, cmap, colr, palette = {}, {0x20: "space"}, {}, []
    for name, (cp, parts) in glyphs.cells.items():
        cmap[cp] = name
        colr[name] = []
        for lname, rgba in parts:
            if rgba not in palette:
                palette.append(rgba)
            colr[name].append((lname, palette.index(rgba)))
    for runs, lname in glyphs.layers.items():
        paths[lname] = rects_path(runs)
    # The base glyph's own outline (all its layers as one shape) is what a
    # renderer without COLR support draws.
    for name, (_, parts) in glyphs.cells.items():
        shapes = [paths[lname] for lname, _ in parts]
        base = shapes[0] if shapes else None
        for s in shapes[1:]:
            base = pathops.op(base, s, pathops.PathOp.UNION)
        paths[name] = base

    order = [".notdef", "space", *glyphs.cells, *glyphs.layers.values()]
    charstrings, metrics = {}, {}
    for name in order:
        pen = T2CharStringPen(ADVANCE, None)
        p = paths.get(name)
        lsb = 0
        if p is not None:
            p.draw(pen)
            b = BoundsPen(None)
            p.draw(b)
            lsb = int(b.bounds[0]) if b.bounds else 0
        metrics[name] = (ADVANCE, lsb)
        charstrings[name] = pen.getCharString()

    fb = FontBuilder(1000, isTTF=False)
    fb.setupGlyphOrder(order)
    fb.setupCharacterMap(cmap)
    fb.setupCFF(PS_NAME, {"FullName": FAMILY}, charstrings, {})
    fb.setupHorizontalMetrics(metrics)
    fb.setupHorizontalHeader(ascent=1020, descent=-300)
    fb.setupNameTable({"familyName": FAMILY, "styleName": "Regular"})
    fb.setupOS2(sTypoAscender=1020, sTypoDescender=-300, sTypoLineGap=0, usWinAscent=1020, usWinDescent=300)
    fb.setupPost(isFixedPitch=1)
    fb.setupCOLR(colr, version=0)
    fb.setupCPAL([[tuple(v / 255 for v in c) for c in palette]])
    # Reproducible: the same mons give the same bytes (SOURCE_DATE_EPOCH or 0).
    fb.font.recalcTimestamp = False
    fb.font["head"].created = fb.font["head"].modified = int(os.environ.get("SOURCE_DATE_EPOCH", 0)) - epoch_diff
    write_atomic(path, fb.save)


def write_atomic(path, save):
    path = pathlib.Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(f".{path.name}.tmp")
    save(str(tmp))
    tmp.replace(path)


# -------------------------------------------------------------------- main

def budget(glyphs, mons):
    """-> (glyphs, glyphs a mon, how many mons would fit)."""
    n = glyphs.count()
    per = (n - 2) / max(1, len(mons))
    room = int((MAX_GLYPHS - 2) // per) if per else 0
    return n, per, room


def over_budget(glyphs, done, wanted):
    """Stop as soon as the font can't fit: numGlyphs is 16-bit."""
    n, per, room = budget(glyphs, done)
    if n > MAX_GLYPHS:
        raise SystemExit(f"build: {wanted} mons need more than OpenType's {MAX_GLYPHS:,} glyphs "
                         f"(~{per:.0f} glyphs a mon: at most ~{room} mons); bake fewer mons "
                         "(devices' mons + [font].extra + the machine pool, or --mons)")


def main(argv=None):
    ap = argparse.ArgumentParser(description=f"Build the {FAMILY} font and frames.json.")
    ap.add_argument("--config", type=pathlib.Path, default=None, help="config.toml (default $HERDRMON_CONFIG or $XDG_CONFIG_HOME/herdrmon/config.toml)")
    ap.add_argument("--assets", type=pathlib.Path, default=None, help="asset cache (default $HERDRMON_ASSETS or $XDG_CACHE_HOME/herdrmon/assets)")
    ap.add_argument("--out-font", type=pathlib.Path, default=None, help="default $HERDRMON_FONT or $XDG_DATA_HOME/fonts/HerdrmonIcons.otf")
    ap.add_argument("--out-frames", type=pathlib.Path, default=None, help="default $HERDRMON_FRAMES or $XDG_DATA_HOME/herdrmon/frames.json")
    ap.add_argument("--mons", help="comma-separated mons, instead of the config's (e.g. onix,ho_oh,deoxys-attack)")
    ap.add_argument("--no-guard", action="store_true", help="don't check codepoints against installed fonts")
    ap.add_argument("--guard-exclude", action="append", default=[], metavar="FAMILY",
                    help="font family the guard ignores (repeatable)")
    ap.add_argument("--emerald", help="pokeemerald base URL for fetch")
    ap.add_argument("--firered", help="pokefirered base URL for fetch")
    args = ap.parse_args(argv)

    assets = args.assets or fetch.default_assets()
    out_font = args.out_font or default_font()
    out_frames = args.out_frames or default_frames()
    config = fetch.load_config(args.config)
    urls = fetch.repo_urls(config, args.emerald, args.firered)

    table = fetch.species_table(fetch.Downloader(assets, urls))
    mons = fetch.parse_mons(args.mons, table) if args.mons else fetch.mons_from_config(config, table)
    if fetch.missing(assets, mons):
        fetch.fetch(assets, mons, urls)
    anims.configure(assets, mons, table)
    guard = None if args.no_guard else Guard(args.guard_exclude)

    t0 = time.monotonic()
    print(f"build: {len(mons)} mons: {', '.join(mons)}", file=sys.stderr)
    glyphs, manifest = bake(mons, guard, log=lambda s: print(s, file=sys.stderr))
    over_budget(glyphs, mons, len(mons))
    n, per, room = budget(glyphs, mons)
    cps = [cp for cp, _ in glyphs.cells.values()]
    if guard:
        guard.check(cps)
    write_font(glyphs, out_font)
    write_atomic(out_frames, lambda p: pathlib.Path(p).write_text(json.dumps(manifest, ensure_ascii=False, indent=1) + "\n"))
    anim_cps = [cp for cp in cps if cp >= ANIM_BASE]
    print(f"build: {n:,} glyphs ({len(glyphs.cells):,} cells + {len(glyphs.layers):,} layer shapes), "
          f"~{per:.0f} a mon: room for ~{room} mons in {MAX_GLYPHS:,}; "
          f"cells U+{min(anim_cps):X}..U+{max(anim_cps):X}; {time.monotonic() - t0:.0f}s", file=sys.stderr)
    print(out_font)
    print(out_frames)


if __name__ == "__main__":
    main()
