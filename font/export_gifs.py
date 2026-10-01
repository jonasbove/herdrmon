# /// script
# requires-python = ">=3.11"
# dependencies = ["pillow"]
# ///
"""Export the machine-Pokémon animations as GIFs for review.

One GIF per mon and animation, plus overview.gif with every animation side by
side, looping in sync, at the radar's timing: one-shots 50 ms a frame
(SHOT_MS), loops 125 ms, the bob 500 ms. GIF frames are 25 ms, which divides
all three. The background is Catppuccin Mocha base.

Run: uv run font/export_gifs.py OUT_DIR [--assets DIR] [--mons a,b,c] [--config PATH]
"""

import pathlib
import sys

from PIL import Image, ImageDraw, ImageFont

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import anims  # noqa: E402

BG = (30, 30, 46, 255)       # Mocha base
FG = (205, 214, 244)         # Mocha text
SUB = (127, 132, 156)        # Mocha overlay1
STEP = 25                    # GIF frame time; divides 125 (loop tick) and 50 (one-shot)
SCALE = 8
ORDER = ["bob", "poison", "recall", "sendout", "harden", "tackle", "sleep"]
SHOTS = {"recall", "sendout", "harden", "tackle"}
HOLD = {"bob": 500}          # ms per frame (BOB_MS in mon.js)
LABEL = {"bob": "working (bob)", "poison": "blocked (poison)", "recall": "recall", "sendout": "send out",
         "harden": "push/backup (harden)", "tackle": "commit (tackle)", "sleep": "night light (sleep)"}


def ticks(name, frames):
    """Expand frames to one entry per STEP ms, at the radar's timing."""
    ms = HOLD.get(name, anims.SHOT_MS if name in SHOTS else 125)
    return [f for f in frames for _ in range(ms // STEP)]


def flat(img, scale=SCALE):
    bg = Image.new("RGBA", img.size, BG)
    bg.alpha_composite(img)
    return bg.resize((img.width * scale, img.height * scale), Image.NEAREST)


def save(path, frames):
    rgb = [f.convert("RGB") for f in frames]
    rgb[0].save(path, save_all=True, append_images=rgb[1:], duration=STEP, loop=0, optimize=False, disposal=1)


def main(out):
    out.mkdir(parents=True, exist_ok=True)
    built = {mon: anims.build(mon) for mon in anims.MONS}

    for mon, animations in built.items():
        for name in ORDER:
            seq = ticks(name, animations[name])
            if name in ("recall", "sendout", "harden", "tackle"):
                seq = seq + [animations["base"][0]] * (750 // STEP)   # pause before it repeats
            save(out / f"{mon}_{name}.gif", [flat(f) for f in seq])

    # overview: rows = mons, columns = animations, one shared timeline
    length = max(len(ticks(n, built[m][n])) + 750 // STEP for m in built for n in ORDER)
    cw, ch = anims.W * SCALE, anims.H * SCALE
    pad, head, side = 24, 40, 150
    W = side + len(ORDER) * (cw + pad) + pad
    H = head + len(built) * (ch + pad) + pad
    try:
        font = ImageFont.load_default(size=18)
    except TypeError:
        font = ImageFont.load_default()
    frames = []
    for t in range(length):
        canvas = Image.new("RGBA", (W, H), BG)
        d = ImageDraw.Draw(canvas)
        for c, name in enumerate(ORDER):
            d.text((side + c * (cw + pad) + pad, 12), LABEL[name], fill=FG, font=font)
        for r, (mon, animations) in enumerate(built.items()):
            y = head + r * (ch + pad) + pad // 2
            d.text((16, y + ch // 2 - 10), mon.capitalize(), fill=SUB, font=font)
            for c, name in enumerate(ORDER):
                seq = ticks(name, animations[name])
                if name not in ("poison", "bob", "sleep"):
                    seq = seq + [animations["base"][0]] * (length - len(seq))
                img = seq[t % len(seq)]
                canvas.alpha_composite(flat(img), (side + c * (cw + pad) + pad, y))
        frames.append(canvas)
    save(out / "overview.gif", frames)
    return sorted(p.name for p in out.glob("*.gif"))


if __name__ == "__main__":
    import argparse

    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("out", type=pathlib.Path)
    ap.add_argument("--assets", type=pathlib.Path, default=None)
    ap.add_argument("--config", type=pathlib.Path, default=None)
    ap.add_argument("--mons", help="comma-separated mons (default: from config)")
    args = ap.parse_args()
    assets = args.assets or anims.fetch.default_assets()
    config = anims.fetch.load_config(args.config)
    urls = anims.fetch.repo_urls(config)
    table = anims.fetch.species_table(anims.fetch.Downloader(assets, urls))
    mons = anims.fetch.parse_mons(args.mons, table) if args.mons else anims.fetch.mons_from_config(config, table)
    anims.fetch.fetch(assets, mons, urls)
    anims.configure(assets, mons, table)
    for name in main(args.out):
        print(args.out / name)
