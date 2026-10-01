# /// script
# requires-python = ">=3.11"
# dependencies = ["pillow"]
# ///
"""Animation frames for the machine Pokémon, reproduced from Pokémon Emerald.

Everything here comes from pret/pokeemerald: the Pokémon are the official box
icons, the effects use the game's own battle-animation sprites (fetched into
the asset cache by fetch.py; never committed), and every motion, timing and
colour blend is taken from the animation scripts and C code cited in each
function. Each effect is written as
a function of the game frame (60 fps), then sampled once per sidebar tick
(125 ms = 7.5 game frames) in the middle of the tick.

Two adaptations are unavoidable in a 17 px terminal row:
  * scale: box icons are half the size of battle sprites, so battle-space
    distances are halved. Effect sprites are drawn at the game's own size
    (the Poké Ball 1:1), except the ball particles and hit splat (halved) and
    the sleep Z (about a third), which would otherwise cover the whole row.
  * things that happen off the Pokémon aren't drawn: the white battle
    background flash on switch-out/in, and the target's shake on Tackle.

Every frame is a 35x17 RGBA canvas: 5 terminal cells of 7x17 px, with the
baseline 13 px from the top. build.py slices each frame into 5 glyphs. The
Pokémon faces left like an opponent-side battler, so it sits at the right
and lunges and drifts to the left, as the opponent's do in battle.

Animations (the sidebar plays them at 125 ms a frame):
  base     still
  bob      party-menu icon animation (pokemon_icon.c), two frames, always on;
           held 500 ms each (a 1 s cycle, synced across every pane)
  poison   Status_Poison, repeated while an agent waits on the user
  recall   Special_SwitchOutOpponentMon: into the ball
  sendout  the opponent's send-out: out of the ball (recall's reverse)
  harden   Move_HARDEN, on a push (backup)
  tackle   Move_TACKLE, on a new commit
  sleep    Status_Sleep, eyes shut, repeated while night light is on and
           it's idle
"""

import json
import math
import pathlib
import struct
import sys

from PIL import Image

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import fetch  # noqa: E402

CELLS = 5
W, H = 7 * CELLS, 17
TICK_FRAMES = 7.5          # game frames per 125 ms tick (loops)
SHOT_FRAMES = 3            # game frames per one-shot frame: 50 ms, 20 fps
SHOT_MS = 50
BOX = 0.5                  # box icon : battle sprite
# A box icon taller than this (both frames' bounding box) is scaled down to it
# for the 17 px row, so it has a pixel of room to bob: Onix 31x23, Ho-oh
# 31x24, Porygon 20x18 ... Geodude (13) and Plusle (16) stay 1:1. The big
# renders always use the native size.
FIT_HEIGHT = 16

# Set by configure(): the asset cache, fetch.py's species table, and the mon
# keys to build (species slug, or slug-form: "ho_oh", "deoxys-attack").
ASSETS = None
TABLE = {}
MONS = []


def configure(assets, mons, table=None):
    """Point anims at a fetched asset cache and choose the mons."""
    global ASSETS, TABLE, MONS, _STAR
    ASSETS = pathlib.Path(assets)
    TABLE = table or json.loads((ASSETS / "species.json").read_text())
    MONS = [fetch.resolve(m, TABLE) for m in mons]
    _STAR = None


# ------------------------------------------------------------- GBA colour

def c5(v):
    """8-bit channel -> 5-bit."""
    return v >> 3


def c8(v):
    """5-bit channel -> 8-bit (the way pret's PNG palettes expand)."""
    return (v << 3) | (v >> 2)


def blend_palette(rgb, coeff, target5):
    """BlendPalette(): c + ((target - c) * coeff >> 4), per 5-bit channel."""
    return tuple(c8(c + (((t - c) * coeff) >> 4)) for c, t in zip(map(c5, rgb), target5))


def grayscale(rgb):
    """SetGrayscaleOrOriginalPalette(): r = g = b = (r + g + b) / 3."""
    a = sum(map(c5, rgb)) // 3
    return (c8(a),) * 3


def pixels(img):
    """Flat pixel sequence (getdata() is deprecated from Pillow 12)."""
    return getattr(img, "get_flattened_data", img.getdata)()


def recolour(img, fn):
    out = img.copy()
    px = out.load()
    for y in range(out.height):
        for x in range(out.width):
            r, g, b, a = px[x, y]
            if a:
                px[x, y] = (*fn((r, g, b)), 255)
    return out


# ---------------------------------------------------------- source sprites

def _snap(img, palette):
    """Hard alpha, every opaque pixel snapped to the source palette."""
    px = img.load()
    for y in range(img.height):
        for x in range(img.width):
            r, g, b, a = px[x, y]
            if a < 128:
                px[x, y] = (0, 0, 0, 0)
            else:
                c = min(palette, key=lambda p: (p[0] - r) ** 2 + (p[1] - g) ** 2 + (p[2] - b) ** 2)
                px[x, y] = (*c, 255)
    return img


def read_pal(path):
    """JASC-PAL -> [(r, g, b)]."""
    lines = pathlib.Path(path).read_text().split()
    n = int(lines[2])
    vals = list(map(int, lines[3:3 + 3 * n]))
    return [tuple(vals[i:i + 3]) for i in range(0, 3 * n, 3)]


def icon_sheet(mon):
    """The box icon sheet (32x64, two frames) in its icon palette
    (gMonIconPaletteIndices -> icon_palette_<n>.pal); index 0 is clear."""
    entry = TABLE[mon]
    im = Image.open(fetch.icon_path(ASSETS, entry))
    pal = read_pal(fetch.palette_path(ASSETS, entry["palette"]))
    out = Image.new("RGBA", im.size)
    src, dst = im.load(), out.load()
    for y in range(im.height):
        for x in range(im.width):
            i = src[x, y]
            if i:
                dst[x, y] = (*pal[i], 255)
    return out


def load_mon(mon, native=False):
    """-> (frame1, frame2) cropped to the same box, both RGBA. native=True
    skips the downscale (the big 2-row renders)."""
    sheet = icon_sheet(mon)
    f1, f2 = sheet.crop((0, 0, 32, 32)), sheet.crop((0, 32, 32, 64))
    b1, b2 = f1.getbbox(), f2.getbbox()
    box = (min(b1[0], b2[0]), min(b1[1], b2[1]), max(b1[2], b2[2]), max(b1[3], b2[3]))
    height = FIT_HEIGHT if box[3] - box[1] > FIT_HEIGHT and not native else None
    palette = sorted({p[:3] for p in pixels(f1) if p[3]})
    out = []
    for f in (f1, f2):
        f = f.crop(box)
        if height and f.height != height:
            w = round(f.width * height / f.height)
            f = _snap(f.resize((w, height), Image.BOX), palette)
        out.append(f)
    return out


def load_sprite(name, frame=0, size=None):
    """Official battle-anim sprite: palette index 0 is transparent."""
    im = Image.open(fetch.anim_asset(ASSETS, f"{name}.png"))
    if size:
        fw, fh = size
        im = im.crop((0, frame * fh, fw, (frame + 1) * fh))
    rgba = im.convert("RGBA")
    px, idx = rgba.load(), im.load()
    for y in range(im.height):
        for x in range(im.width):
            if idx[x, y] == 0:
                px[x, y] = (0, 0, 0, 0)
    return rgba


# ------------------------------------------------------------------ canvas

class Stage:
    """The Pokémon on the canvas: right-aligned, vertically centred."""

    def __init__(self, sprite):
        self.sprite = sprite
        self.w, self.h = sprite.size
        self.x0 = W - self.w - 1
        self.y0 = (H - self.h + 1) // 2
        self.cx = self.x0 + self.w / 2      # battler centre (anim coords)
        self.cy = self.y0 + self.h / 2
        self.bottom = self.y0 + self.h

    def frame(self, sprite=None, dx=0, dy=0):
        c = Image.new("RGBA", (W, H))
        s = sprite or self.sprite
        c.alpha_composite(s, (self.x0 + dx, self.y0 + dy)) if self._fits(s, dx, dy) else c.paste(s, (self.x0 + dx, self.y0 + dy), s)
        return c

    def _fits(self, s, dx, dy):
        x, y = self.x0 + dx, self.y0 + dy
        return x >= 0 and y >= 0 and x + s.width <= W and y + s.height <= H


def draw_affine(canvas, sprite, cx, cy, scale=1.0, degrees=0.0):
    """Nearest-neighbour rot/scale of `sprite` about its centre, placed with
    its centre at (cx, cy): what the GBA's affine OAM does."""
    if scale <= 0:
        return canvas
    out = canvas.copy()
    dst = out.load()
    src = sprite.load()
    sw, sh = sprite.size
    t = math.radians(degrees)
    cos, sin = math.cos(t), math.sin(t)
    for y in range(H):
        for x in range(W):
            # screen -> sprite space (inverse rotation; GBA +angle is CCW)
            ux, uy = x + 0.5 - cx, y + 0.5 - cy
            sx = (cos * ux - sin * uy) / scale + sw / 2
            sy = (sin * ux + cos * uy) / scale + sh / 2
            if 0 <= sx < sw and 0 <= sy < sh:
                p = src[int(sx), int(sy)]
                if p[3]:
                    dst[x, y] = p
    return out


def draw_affine_ss(canvas, sprite, cx, cy, scale, degrees=0.0, n=4, cover=0.4):
    """draw_affine, supersampled n x n: a pixel is drawn when at least `cover`
    of it is covered, in the colour most of it has. Used where the game's own
    sprite has to be drawn smaller than the GBA would, so it stays legible."""
    out = canvas.copy()
    dst = out.load()
    src = sprite.load()
    sw, sh = sprite.size
    t = math.radians(degrees)
    cos, sin = math.cos(t), math.sin(t)
    for y in range(H):
        for x in range(W):
            hits = {}
            for j in range(n):
                for i in range(n):
                    ux, uy = x + (i + 0.5) / n - cx, y + (j + 0.5) / n - cy
                    sx = (cos * ux - sin * uy) / scale + sw / 2
                    sy = (sin * ux + cos * uy) / scale + sh / 2
                    if 0 <= sx < sw and 0 <= sy < sh:
                        p = src[int(sx), int(sy)]
                        if p[3]:
                            hits[p] = hits.get(p, 0) + 1
            if hits and sum(hits.values()) >= cover * n * n:
                dst[x, y] = max(hits, key=hits.get)
    return out


def scaled_mon(stage, sprite, scale):
    """The battler drawn at `scale`, bottom-anchored like
    SetBattlerSpriteYOffsetFromYScale."""
    c = Image.new("RGBA", (W, H))
    h = stage.h * scale
    return draw_affine(c, sprite, stage.cx, stage.bottom - h / 2, scale)


def sample(fn, total_frames, ticks=None):
    """Sample a game-frame function once per tick, mid-tick."""
    n = ticks or math.ceil(total_frames / TICK_FRAMES)
    return [fn(k * TICK_FRAMES + TICK_FRAMES / 2) for k in range(n)]


def sample_shot(fn, total_frames):
    """One-shots (recall, sendout, harden, tackle) are sampled every
    SHOT_FRAMES game frames (50 ms) at the start of each step, so the
    first frame is the effect's first game frame. The radar schedules each
    frame on its own, so they aren't tied to the 125 ms loop tick."""
    n = math.ceil(total_frames / SHOT_FRAMES)
    return [fn(k * SHOT_FRAMES) for k in range(n)]


# ------------------------------------------------------------- animations

def bob(stage, f2):
    """Party menu, full HP (pokemon_icon.c sAnim_0): frame 0 and 1, 6 game
    frames each. 100 ms doesn't fit a 125 ms tick; one tick each."""
    return [stage.frame(), stage.frame(f2)]


def party_bounce(stage, f2):
    """Party menu, selected slot (pokefirered party_menu.c
    SpriteCB_BouncePartyMonIcon): while frame 0 shows the icon sits 3 px up,
    while frame 1 shows 1 px down. The 17 px row leaves less room than the
    game's slot, so each offset is capped at the free rows above/below the
    sprite (no clipping)."""
    up = min(3, stage.y0)
    down = min(1, H - stage.bottom)
    return [stage.frame(dy=-up), stage.frame(f2, dy=down)]


def poison(stage):
    """data/battle_anim_scripts.s Status_Poison:
      createvisualtask AnimTask_ShakeMon2, 2, ANIM_ATTACKER, 1, 0, 18, 2
      blend_color_cycle ... delay=2, num_blends=2, 0 -> 12, RGB(30, 0, 31)
    ShakeMon2 sets x2 = +1, then flips its sign every 3 frames, 18 times.
    The palette fade steps y by 2 every delay+1 = 3 frames: 0 -> 12 in 18
    frames, back to 0 in 18. Repeated every 2 s (16 ticks) while blocked;
    in battle it plays once a turn."""
    purple = (30, 0, 31)

    def at(f):
        f = int(f)
        x2 = 0 if f >= 54 else (1 if (f // 3) % 2 == 0 else -1)
        if f < 18:
            y = 2 * (f // 3)
        elif f < 36:
            y = 12 - 2 * ((f - 18) // 3)
        else:
            y = 0
        tinted = recolour(stage.sprite, lambda c: blend_palette(c, y, purple)) if y else stage.sprite
        return stage.frame(tinted, dx=x2)

    return sample(at, 54, ticks=16)


# Poké Ball open particles (battle_anim_throw.c): particles.png frames 0-2
# (a white/orange dash: diagonal, vertical, horizontal) in the circle_impact
# palette, animated 0,1,2,0(hflip),2,1 at ONE game frame each, so each
# particle spins ten times a second and the eye sees a twinkle. Sampled at
# 8 fps a tick would catch one random dash (the "streaks"), and the 8x8 dash
# can't be halved without turning into a blob. So each particle is drawn as
# the twinkle's core: the dash's white centre pixel with its orange edge
# colour on the four arms, taken from the sprite's own palette.
def _particle_star():
    pal = Image.open(fetch.anim_asset(ASSETS, "circle_impact.png")).getpalette()
    white = (*pal[3:6], 255)       # index 1
    orange = (*pal[6:9], 255)      # index 2
    star = Image.new("RGBA", (3, 3))
    px = star.load()
    px[1, 1] = white
    for x, y in ((1, 0), (0, 1), (2, 1), (1, 2)):
        px[x, y] = orange
    return star


_STAR = None


def ball_particles(canvas, f, ox, oy):
    """AnimateBallOpenParticles + PokeBallOpenParticleAnimation: one particle
    a game frame for 16 frames; particle i leaves at angle (i % 8) * 32/256
    of a turn with radius 2 px a frame, and is gone at radius 50. Each one
    starts a frame later and an eighth of a turn further round, so the 16
    make two pinwheel spirals, 8 frames apart. x2 = Sin, y2 = Cos."""
    global _STAR
    if _STAR is None:
        _STAR = _particle_star()
    out = canvas
    for i in range(16):
        age = int(f) - i
        if age < 0:
            continue
        r = 2 * age
        # The game's run to radius 50 would scatter them over the whole
        # row and the next; they're dropped after 12 frames (12 px here).
        if r >= 50 or age >= 12:
            continue
        a = ((i % 8) * 32) / 256 * 2 * math.pi
        x = ox + math.sin(a) * r * BOX
        y = oy + math.cos(a) * r * BOX
        out = draw_affine(out, _STAR, round(x - 0.5) + 0.5, round(y - 0.5) + 0.5)
    return out


PINK = (31, 22, 30)        # gBallOpenFadeColors[BALL_POKE]


BALL_X = 8                 # ball centre x: in the free space left of the Pokémon


def _ball(stage):
    """The Poké Ball's three frames (closed, open, blank) and its centre y.
    It sits left of the Pokémon (BALL_X), not on it: a 16 px ball drawn
    over a 16 px Pokémon hides it, so the shrink into the ball can't be seen.
    The opponent faces left, toward the trainer, so its ball comes from
    that side."""
    return [load_sprite("poke", i, (16, 16)) for i in range(3)], stage.cy


def recall(stage):
    """Into the ball, in the game's order. The switch-out
    (Special_SwitchOutOpponentMon) pulls the Pokémon into a ball that is off
    screen with the trainer; the sidebar shows the ball, so the order is the
    one the game uses when the ball is on screen, the capture
    (battle_anim_throw.c SpriteCB_Ball_Arc -> SpriteCB_Ball_MonShrink_Step),
    with the switch-out's timings:

      f 0   the ball opens (StartSpriteAnim(sprite, 1) = sBallAnimSeq1:
            frame 1 then 2, 5 frames each), AnimateBallOpenParticles bursts
            from the ball (x, y - 5), LaunchBallFadeMonTask(FALSE): the
            Poké Ball colour RGB(31, 22, 30) blends in, coeff 0 -> 16, one a
            frame.
      f 10  the Pokémon shrinks toward the ball: rotscale 0x100 += 0x30 a
            frame until >= 0x2D0 (AnimTask_SwitchOutShrinkMon; scale =
            256/param), and slides down onto it (MON_SHRINK_STEP: y2 eases
            the distance to the ball over 28 frames), then it's gone.
      then  the ball closes (sBallAnimSeq2: frame 1, then 0, 5 frames each)
            and stays closed.

    The ball is left of the Pokémon (see _ball), the spot the send-out
    opens it from, so recall + sendout join up.
    """
    ball, ball_y = _ball(stage)
    empty = Image.new("RGBA", (W, H))
    bx, by = BALL_X, ball_y           # where sendout's ball is, so they join
    shrink_end = 10 + math.ceil((0x2D0 - 0x100) / 0x30)   # 10 + 13 = f 23
    close_at = shrink_end + 2

    # sBallAnimSeq1's second frame is blank (the ball "opens" by vanishing
    # in the send-out); here the ball must stay visible as the target, so it
    # holds the open frame until sBallAnimSeq2 closes it (open 5, closed).
    def ball_frame(f):
        if f < close_at + 5:
            return ball[1]              # open
        return ball[0]

    def at(f):
        f = int(f)
        coeff = min(16, f)
        img = empty
        if f < shrink_end:
            tinted = recolour(stage.sprite, lambda c: blend_palette(c, coeff, PINK)) if coeff else stage.sprite
            if f < 10:
                img = stage.frame(tinted)
            else:
                k = f - 10 + 1
                param = 0x100 + 0x30 * k
                scale = 0x100 / param
                # Slide into the ball as it shrinks (MON_SHRINK_STEP: the
                # distance to the ball, eased linearly over the shrink).
                t = min(1.0, k / 13)
                cx = stage.cx + (bx - stage.cx) * t
                cy = stage.cy + (by - stage.cy) * t
                img = draw_affine(empty, tinted, cx, cy, scale)
        b = ball_frame(f)
        if b.getbbox():
            img = draw_affine(img, b, bx, by)
        # The burst is out of the ball, behind nothing: drawn last.
        return ball_particles(img, f, bx, by - 5 * BOX)

    closed = draw_affine(empty, ball[0], bx, by)
    return sample_shot(at, close_at + 10) + [closed]


def sendout(stage):
    """The opponent's send-out (pokeball.c): the reverse of recall.

      Task_DoPokeballSendOutAnim, POKEBALL_OPPONENT_SENDOUT: the ball appears
        on the battler (y + 24; here at its centre) and waits 16 frames
        (SpriteCB_OpponentMonSendOut).
      SpriteCB_ReleaseMonFromBall: the ball opens (sBallAnimSeq1: frame 1
        then 2, 5 frames each) and vanishes, particles burst,
        LaunchBallFadeMonTask(TRUE) starts the Pokémon fully pink, and
        BATTLER_AFFINE_EMERGE grows it from 0x28 by 0x12 a frame for 12
        frames while HandleBallAnimEnd lifts it: y2 = data1 >> 8, data1 from
        0x1000 down 288 a frame. The pink holds while the background fades
        (16 frames), then fades out 16 -> 0, one a frame.
    """
    ball, ball_y = _ball(stage)
    empty = Image.new("RGBA", (W, H))
    bx = BALL_X

    def at(f):
        f = int(f)
        img = empty
        if f < 16:
            return draw_affine(img, ball[0], bx, ball_y)
        g = f - 16
        if g < 10:
            img = draw_affine(img, ball[1 if g < 5 else 2], bx, ball_y)
        k = g + 1
        scale = min(0x100, 0x28 + 0x12 * k) / 0x100
        y2 = max(0, (0x1000 - 288 * k) >> 8)
        coeff = 16 if g < 16 else max(0, 16 - (g - 16))
        tinted = recolour(stage.sprite, lambda c: blend_palette(c, coeff, PINK)) if coeff else stage.sprite
        if scale < 1:
            # Out of the ball and over to its place while it grows (the
            # sidebar's ball is beside it; in battle the ball is on it).
            t = min(1.0, k / 12)
            cx = bx + (stage.cx - bx) * t
            cy = ball_y + (stage.cy - ball_y) * t
            img = draw_affine(img, tinted, cx, cy + y2 * BOX * (1 - t), scale)
        else:
            img = Image.alpha_composite(img, stage.frame(tinted, dy=round(y2 * BOX)))
        return ball_particles(img, g, bx, ball_y)

    return sample_shot(at, 16 + 32) + [stage.frame()]


def _shine_mask():
    """masks/metal_shine.png through its tilemap: the 64x64 shine texture,
    True where the (white) band is."""
    tiles = Image.open(fetch.anim_asset(ASSETS, "metal_shine.png"))
    data = fetch.anim_asset(ASSETS, "metal_shine.bin").read_bytes()
    ent = struct.unpack(f"<{len(data) // 2}H", data)
    mask = Image.new("L", (64, 64))
    for i, e in enumerate(ent):
        row, col = divmod(i, 32)
        if row >= 8 or col >= 8:
            continue
        t = e & 0x3FF
        tile = tiles.crop(((t % 8) * 8, (t // 8) * 8, (t % 8) * 8 + 8, (t // 8) * 8 + 8))
        if e & 0x400:
            tile = tile.transpose(Image.FLIP_LEFT_RIGHT)
        if e & 0x800:
            tile = tile.transpose(Image.FLIP_TOP_BOTTOM)
        # palette index 1 is the white band. Map indices, not colours: point()
        # on a P image remaps the palette, which inverted this mask once.
        band = Image.new("L", tile.size)
        band.putdata([255 if v == 1 else 0 for v in pixels(tile)])
        mask.paste(band, (col * 8, row * 8))
    return mask


def harden(stage):
    """Move_HARDEN: metallic_shine permanent=FALSE (AnimTask_MetallicShine,
    battle_anim_dark.c). The Pokémon turns grayscale; the shine BG, windowed
    to the Pokémon's shape, starts at BG1_X = -x + 96, BG1_Y = -y + 32 and
    scrolls 4 px a frame; after 128 px it wraps. Two passes with the shine,
    then the palette is restored and a third, invisible pass runs out the
    task. Blend BLDALPHA(8, 12): shine * 8/16 + Pokémon * 12/16."""
    mask = _shine_mask().load()
    gray = recolour(stage.sprite, grayscale)
    gray_px = gray.load()

    def at(f):
        f = int(f)
        if f >= 64:
            return stage.frame()
        g = f % 32
        img = gray.copy()
        px = img.load()
        for y in range(stage.h):
            for x in range(stage.w):
                if not gray_px[x, y][3]:
                    continue
                # battle-space offset from the battler centre, halved
                bx = (stage.x0 + x + 0.5 - stage.cx) / BOX
                by = (stage.y0 + y + 0.5 - stage.cy) / BOX
                u = int(bx + 96 - 4 * g)
                v = int(by + 32)
                if 0 <= u < 64 and 0 <= v < 64 and mask[u, v]:
                    r, gg, b, _ = gray_px[x, y]
                    px[x, y] = tuple(c8(min(31, (31 * 8 + c5(c) * 12) >> 4)) for c in (r, gg, b)) + (255,)
        return stage.frame(img)

    return sample_shot(at, 96)


def tackle(stage):
    """Move_TACKLE:
      createsprite gHorizontalLungeSpriteTemplate, ANIM_ATTACKER, 2, 4, 4
        -> 4 frames at 4 px a frame toward the target, 4 frames back
      delay 6
      create_basic_hitsplat_sprite ... relative_to=ANIM_TARGET, animation=2
        -> impact.png at scale 0xB0/0x100 for 8 frames, alpha 12/16 over
           the background (setalpha 12, 8), on the target. The target is
           off to the left, so the splat lands at the left edge.
    """
    impact = load_sprite("impact")

    def at(f):
        f = int(f)
        dx = -4 * f if f <= 4 else (-4 * (8 - f) if f < 8 else 0)
        img = stage.frame(dx=round(dx * BOX))
        if 6 <= f < 14:
            splat = Image.new("RGBA", impact.size)
            splat.paste(impact, (0, 0))
            alpha = splat.split()[3].point(lambda a: 192 if a else 0)
            splat.putalpha(alpha)
            layer = draw_affine(Image.new("RGBA", (W, H)), splat, 5.5, stage.cy, 0xB0 / 0x100 * BOX)
            img = Image.alpha_composite(img, layer)
        return img

    return sample_shot(at, 14 + 7)


Z_SCALE = 0.3


def close_eyes(img):
    """The box icons' only pure-white pixels are the eye glints: shut them
    with the sprite's darkest colour."""
    out = img.copy()
    px = out.load()
    opaque = [px[x, y][:3] for y in range(out.height) for x in range(out.width) if px[x, y][3]]
    dark = min(opaque, key=sum)
    for y in range(out.height):
        for x in range(out.width):
            if px[x, y][3] and min(px[x, y][:3]) >= 240:
                px[x, y] = (*dark, 255)
    return out


def sleep(stage):
    """Status_Sleep: two gSleepLetterZSpriteTemplate sprites, 30 frames
    apart, from the attacker's centre offset (4, -10) (opponent side: x - 4).
    AnimSleepLetterZ_Step: y2 = -(sum of 0..t) / 40, x2 = data4 / 10 with
    data4 -= 2 a frame (drifts left on the opponent side), gone after 61
    frames. Affine (gSleepLetterZAffineAnimCmds2, opponent side): scale
    0x14, rotation +30, then +8 scale and -1 rotation a frame for 24 frames.
    Repeated every 2 s while it's night, with the Pokémon's eyes shut (the
    game leaves the sprite alone; this is ours, so it reads as asleep at
    icon size). Size and rise are reduced to fit the row (see below)."""
    z = load_sprite("letter_z")
    asleep = stage.frame(close_eyes(stage.sprite))
    # The row is 17 px tall and the Z alone is ~17 px at the game's scale,
    # rising 45 px: here it's drawn at Z_SCALE (at most ~5 px) and its rise is
    # squeezed into the space above the head, keeping the game's easing (t^2).
    rise_total = 60 * 61 // 2 / 40
    headroom = stage.cy - 10 * BOX - 3

    def z_at(canvas, t):
        if t < 0 or t > 60:
            return canvas
        t = int(t)
        k = min(t, 24)
        scale = (0x14 + 8 * k) / 0x100
        rot = (30 - k) * 256 / 65536 * 360
        x = stage.cx - 4 * BOX - (2 * t // 10) * BOX
        y = stage.cy - 10 * BOX - (t * (t + 1) / 2 / 40) / rise_total * headroom
        return draw_affine_ss(canvas, z, x, y, scale * Z_SCALE, rot)

    def at(f):
        img = asleep
        img = z_at(img, f)
        img = z_at(img, f - 30)
        return img

    return sample(at, 91, ticks=16)


# Status_Paralysis (data/battle_anim_scripts.s):
#   createvisualtask AnimTask_ShakeMon2, 2, ANIM_ATTACKER, 1, 0, 10, 1
#   call ElectricityEffect
PAR_SPARKS = [  # ElectricityEffect: (x, y, variant), one createsprite each
    (5, 0, 0), (-5, 10, 1), (15, 20, 2), (-15, -10, 0),
    (25, 0, 1), (-8, 8, 2), (2, -8, 0), (-20, 15, 1),
]
PAR_SPACING = 4   # "delay 2": the script resumes on the 4th frame after it
PAR_LIFE = 7      # gElectricitySpriteTemplate: duration 5 + create/destroy


def paralysis(stage, scale=BOX):
    """Status_Paralysis. AnimTask_ShakeMon2(1, 0, 10, 1): x2 = +1 at once,
    then the sign flips every 2 frames (delay 1), 10 times: 19 frames.
    ElectricityEffect: eight spark_2.png sprites (16x16; frame = variant,
    variant 1 h-flipped, 2 v-flipped) at the battler centre + (x, y), one
    every 4 frames, each shown for 7. The x offsets drop out in the game
    (AnimElectricity's SetAnimSpriteInitialXOffset only applies them when
    attacker and target differ, and a status anim targets itself), so all
    sparks sit on the centre line. Drawn at box scale (8x8); their y spread
    (-10..20, halved) is squeezed into the 17 px row."""
    sheet = [load_sprite("spark_2", i, (16, 16)) for i in range(3)]
    sheet[1] = sheet[1].transpose(Image.FLIP_LEFT_RIGHT)
    sheet[2] = sheet[2].transpose(Image.FLIP_TOP_BOTTOM)
    ys = [y * scale for _, y, _ in PAR_SPARKS]
    lo, hi = min(ys), max(ys)
    half = 8 * scale   # half the 16 px spark at this scale: keep it inside
    room_up, room_down = stage.cy - half, H - half - stage.cy
    k = min(1.0, room_up / -lo if lo < 0 else 1.0, room_down / hi if hi > 0 else 1.0)
    total = PAR_SPACING * (len(PAR_SPARKS) - 1) + PAR_LIFE

    def at(f):
        f = int(f)
        x2 = 0 if f >= 19 else (1 if (f // 2) % 2 == 0 else -1)
        img = stage.frame(dx=x2)
        for i, (_, y, var) in enumerate(PAR_SPARKS):
            t = f - i * PAR_SPACING
            if 0 <= t < PAR_LIFE:
                img = draw_affine_ss(img, sheet[var], stage.cx, stage.cy + y * scale * k, scale)
        return img

    return sample_shot(at, total)


def build(mon):
    f1, f2 = load_mon(mon)
    stage = Stage(f1)
    return {
        "base": [stage.frame()],
        "bob": bob(stage, f2),
        "poison": poison(stage),
        "recall": recall(stage),
        "sendout": sendout(stage),
        "harden": harden(stage),
        "tackle": tackle(stage),
        "sleep": sleep(stage),
        "party": party_bounce(stage, f2),   # party menu: selected slot
        "paralysis": paralysis(stage),      # party menu: can't run Herdr
    }


def calm(frames):
    """Scaling 1.5x turns the icon's 1 px frame-to-frame shift into 2-3 px,
    which looked jumpy (Onix). Shift every later frame vertically so its
    top edge is at most 1 px from the first frame's."""
    top0 = frames[0].getbbox()[1]
    out = [frames[0]]
    for f in frames[1:]:
        bb = f.getbbox()
        d = bb[1] - top0
        shift = d - max(-1, min(1, d))
        if shift:
            g = Image.new("RGBA", f.size, (0, 0, 0, 0))
            g.paste(f, (0, -shift))
            f = g
        out.append(f)
    return out


BIG_ROWS = 2   # party-menu previews: two 17 px rows
BIG_CELLS = 7  # by seven 7 px cells: a 49x34 canvas


def build_big(mon, rows=BIG_ROWS):
    """The party menu's big preview: the box icon at its native size, as in the
    game (Geodude 22x13 up to Ho-Oh 31x24), centred on a 49x34 canvas. Same
    game animations as the small ones; the bob and the selected bounce move
    at most 1 px (calm)."""
    global W, H
    small = W, H
    W, H = 7 * BIG_CELLS, 17 * rows   # rows=3: the same art, centred half a row lower
    try:
        f1, f2 = load_mon(mon, native=True)
        k = 1   # native size, as in the game (scaling to a common box was tried and rolled back)
        stage = Stage(f1)
        stage.x0 = (W - stage.w) // 2
        stage.y0 = (H - stage.h + 1) // 2
        stage.cx, stage.cy = stage.x0 + stage.w / 2, stage.y0 + stage.h / 2
        stage.bottom = stage.y0 + stage.h
        bounce = [stage.frame(dy=-min(1, stage.y0)), stage.frame(f2)]
        return {
            "base": [stage.frame()],
            "bob": calm(bob(stage, f2)),
            "party": calm(bounce),
            "paralysis": paralysis(stage, scale=BOX * k),
        }
    finally:
        W, H = small


def preview(path, scale_by=8):
    rows = []
    for mon in MONS:
        for name, frames in build(mon).items():
            rows.append(frames)
    cols = max(len(r) for r in rows)
    sheet = Image.new("RGBA", ((W + 2) * cols, (H + 2) * len(rows)), (30, 30, 46, 255))
    for r, frames in enumerate(rows):
        for c, img in enumerate(frames):
            sheet.alpha_composite(img, (c * (W + 2) + 1, r * (H + 2) + 1))
    sheet.resize((sheet.width * scale_by, sheet.height * scale_by), Image.NEAREST).save(path)
    return path


def main(argv=None):
    import argparse

    ap = argparse.ArgumentParser(description="Render every animation of some mons to one preview PNG.")
    ap.add_argument("out", type=pathlib.Path, help="PNG to write")
    ap.add_argument("--assets", type=pathlib.Path, default=None)
    ap.add_argument("--config", type=pathlib.Path, default=None)
    ap.add_argument("--mons", help="comma-separated mons (default: from config)")
    args = ap.parse_args(argv)
    assets = args.assets or fetch.default_assets()
    config = fetch.load_config(args.config)
    table = fetch.species_table(fetch.Downloader(assets, fetch.repo_urls(config)))
    mons = fetch.parse_mons(args.mons, table) if args.mons else fetch.mons_from_config(config, table)
    fetch.fetch(assets, mons, fetch.repo_urls(config))
    configure(assets, mons, table)
    print(preview(args.out))


if __name__ == "__main__":
    main()
