#!/usr/bin/env python3
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""Fetch the Gen 3 art the icon font is built from, from pret's decomps.

The art is Nintendo's. This repo never contains it: this script downloads it
from pret/pokeemerald and pret/pokefirered into a local cache at build time.

What it fetches:
  * pret's own source tables (src/pokemon_icon.c, src/data/graphics/pokemon.h,
    species names, the National Dex order), parsed into species.json: every
    one of the 386 species plus the Unown and Deoxys forms, with its box icon
    path and icon palette index (gMonIconPaletteIndices).
  * the box icon sheet of each requested mon, and the three icon palettes
    (graphics/pokemon/icon_palettes/icon_palette_<n>.pal).
  * every battle-animation sprite anims.py draws (ANIM_ASSETS).

The cache mirrors the repos' own paths, so a local checkout works as well as
GitHub (--emerald file:///path/to/pokeemerald):

  assets/SOURCES.txt         every cached file and the URL it came from
  assets/species.json        the parsed species table
  assets/pokeemerald/<path>
  assets/pokefirered/<path>

Mon names ("specs"): the species slug, which is pret's folder name ("onix",
"ho_oh", "mr_mime", "nidoran_f"), optionally followed by "-<form>":
"deoxys-attack", "deoxys-defense", "deoxys-speed", "unown-b" ...
"unown-question_mark". A device's key is `mon` plus "-" + `form` when it has
one. Friendly spellings ("Ho-Oh", "Mr. Mime", "Nidoran♀") are accepted too.

Usage: uv run font/fetch.py [--assets DIR] [--mons a,b,c] [--all]
                            [--emerald URL] [--firered URL] [--config PATH]
Without --mons it fetches the mons build.py would bake (see mons_from_config).
"""

import argparse
import concurrent.futures
import difflib
import json
import os
import pathlib
import re
import socket
import sys
import tomllib
import urllib.error
import urllib.request

EMERALD = "https://raw.githubusercontent.com/pret/pokeemerald/master"
FIRERED = "https://raw.githubusercontent.com/pret/pokefirered/master"
REPOS = ("pokeemerald", "pokefirered")

# pret source files the species table is parsed from (all pokeemerald).
SOURCE_FILES = {
    "icons": "src/pokemon_icon.c",
    "graphics": "src/data/graphics/pokemon.h",
    "names": "src/data/text/species_names.h",
    "dex": "include/constants/pokedex.h",
}
PALETTES = [f"graphics/pokemon/icon_palettes/icon_palette_{i}.pal" for i in range(3)]

# Battle-animation sprites used by anims.py: name -> pokeemerald path.
ANIM_ASSETS = {
    "circle_impact.png": "graphics/battle_anims/sprites/circle_impact.png",
    "impact.png": "graphics/battle_anims/sprites/impact.png",
    "letter_z.png": "graphics/battle_anims/sprites/letter_z.png",
    "metal_shine.png": "graphics/battle_anims/masks/metal_shine.png",
    "metal_shine.bin": "graphics/battle_anims/masks/metal_shine.bin",
    "particles.png": "graphics/battle_anims/sprites/particles.png",
    "poke.png": "graphics/balls/poke.png",
    "spark_2.png": "graphics/battle_anims/sprites/spark_2.png",
}

# Deoxys' other formes aren't in the icon table: each game ships one. Emerald
# has Speed, FireRed Attack, LeafGreen Defense (both in pokefirered).
DEOXYS_FORMS = {
    "speed": ("pokeemerald", "graphics/pokemon/deoxys/icon_speed.png"),
    "attack": ("pokefirered", "graphics/pokemon/deoxys/icon_attack.png"),
    "defense": ("pokefirered", "graphics/pokemon/deoxys/icon_defense.png"),
}
FORM_ALIASES = {
    "unown-a": "unown", "deoxys-normal": "deoxys",
    "unown-emark": "unown-exclamation_mark", "unown-qmark": "unown-question_mark",
    "unown-exclamation": "unown-exclamation_mark", "unown-question": "unown-question_mark",
    "unown-!": "unown-exclamation_mark", "unown-?": "unown-question_mark",
}

# Mons for devices whose name isn't a species (DESIGN.md "Default mon"). They
# are always baked, so whichever one a consumer's hash picks is in the font.
MACHINE_MONS = [  # order is part of the contract (species.go, sidebar/lib/species.js)
    "porygon", "porygon2", "magnemite", "magneton", "voltorb", "electrode", "geodude", "onix", "beldum", "metang", "baltoy", "claydol", "lunatone", "solrock", "registeel",
]

SPECIES_COUNT = 386


class FetchError(SystemExit):
    pass


# ------------------------------------------------------------------ paths

def xdg(var, default):
    return pathlib.Path(os.environ.get(var) or pathlib.Path.home() / default)


def default_assets():
    env = os.environ.get("HERDRMON_ASSETS")
    return pathlib.Path(env) if env else xdg("XDG_CACHE_HOME", ".cache") / "herdrmon" / "assets"


def default_config():
    env = os.environ.get("HERDRMON_CONFIG")
    return pathlib.Path(env) if env else xdg("XDG_CONFIG_HOME", ".config") / "herdrmon" / "config.toml"


def load_config(path):
    path = pathlib.Path(path) if path else default_config()
    if not path.exists():
        return {}
    try:
        return tomllib.loads(path.read_text())
    except tomllib.TOMLDecodeError as e:
        raise FetchError(f"{path}: {e}")


def repo_urls(config, emerald=None, firered=None):
    font = config.get("font", {})
    return {
        "pokeemerald": (emerald or font.get("pret_emerald") or EMERALD).rstrip("/"),
        "pokefirered": (firered or font.get("pret_firered") or FIRERED).rstrip("/"),
    }


def local(assets, repo, path):
    return pathlib.Path(assets) / repo / path


def anim_asset(assets, name):
    """Cached path of a battle-anim sprite (ANIM_ASSETS key)."""
    return local(assets, "pokeemerald", ANIM_ASSETS[name])


def palette_path(assets, index):
    return local(assets, "pokeemerald", PALETTES[index])


def icon_path(assets, entry):
    return local(assets, entry["repo"], entry["icon"])


# -------------------------------------------------------------- download

class Downloader:
    """Parallel, idempotent: a file already in the cache is not fetched again.
    Every file is recorded in SOURCES.txt with the URL it came from."""

    def __init__(self, assets, urls, workers=8):
        self.assets = pathlib.Path(assets)
        self.urls = urls
        self.workers = workers
        self.sources = self._read_sources()

    def _read_sources(self):
        f = self.assets / "SOURCES.txt"
        out = {}
        if f.exists():
            for line in f.read_text().splitlines():
                if line and not line.startswith("#") and "\t" in line:
                    rel, url = line.split("\t", 1)
                    out[rel] = url
        return out

    def write_sources(self):
        self.assets.mkdir(parents=True, exist_ok=True)
        lines = [
            "# Files fetched by herdrmon font/fetch.py: cache path <TAB> source URL.",
            "# Pokémon art is (c) Nintendo / Creatures / GAME FREAK, from pret's decompilations.",
            "# It is fetched for local use and is not part of, or redistributed by, herdrmon.",
        ]
        lines += [f"{rel}\t{url}" for rel, url in sorted(self.sources.items())]
        (self.assets / "SOURCES.txt").write_text("\n".join(lines) + "\n")

    def _one(self, repo, path):
        dest = local(self.assets, repo, path)
        rel = dest.relative_to(self.assets).as_posix()
        url = f"{self.urls[repo]}/{path}"
        if dest.exists() and dest.stat().st_size:
            self.sources.setdefault(rel, url)
            return False
        dest.parent.mkdir(parents=True, exist_ok=True)
        req = urllib.request.Request(url, headers={"User-Agent": "herdrmon-fetch"})
        with urllib.request.urlopen(req, timeout=30) as r:
            data = r.read()
        if not data:
            raise OSError(f"{url}: empty response")
        tmp = dest.with_name(dest.name + ".part")
        tmp.write_bytes(data)
        tmp.replace(dest)
        self.sources[rel] = url
        return True

    def get(self, items):
        """items: [(repo, path)]. -> number of files downloaded."""
        items = sorted(set(items))
        failed, fetched = [], 0
        with concurrent.futures.ThreadPoolExecutor(self.workers) as pool:
            futs = {pool.submit(self._one, repo, path): (repo, path) for repo, path in items}
            for fut in concurrent.futures.as_completed(futs):
                try:
                    fetched += fut.result()
                except (OSError, urllib.error.URLError) as e:
                    failed.append((futs[fut], e))
        self.write_sources()
        if failed:
            raise FetchError(explain(failed, self.urls))
        return fetched


def explain(failed, urls):
    (repo, path), err = failed[0]
    url = f"{urls[repo]}/{path}"
    offline = isinstance(err, urllib.error.URLError) and not isinstance(err, urllib.error.HTTPError) \
        or isinstance(err, (socket.timeout, TimeoutError, ConnectionError))
    lines = [f"fetch: could not download {len(failed)} file(s), e.g.", f"  {url}", f"  {err}"]
    if url.startswith("file:"):
        lines.append(f"Not in the local checkout: is {urls[repo]} a full {repo} checkout?")
    elif offline:
        lines.append("You seem to be offline (or GitHub is unreachable). Retry when online, or point "
                     "--emerald/--firered (config [font].pret_emerald/pret_firered) at a local "
                     "checkout: --emerald file:///path/to/pokeemerald")
    elif isinstance(err, urllib.error.HTTPError) and err.code == 404:
        lines.append("pret may have moved this file; check the repo, or pin --emerald/--firered to an older commit.")
    return "\n".join(lines)


# ---------------------------------------------------------- species table

def _table(src, name):
    """Body of a C array initialiser `name[] = { ... };`."""
    m = re.search(re.escape(name) + r"\[\]\s*=\s*\{(.*?)\n\};", src, re.S)
    if not m:
        raise FetchError(f"fetch: can't find {name} in pret's source; has pret changed it?")
    return m.group(1)


def parse_species(assets):
    """pret's source -> {key: entry}: the 386 species (dex order) and forms."""
    root = pathlib.Path(assets) / "pokeemerald"
    text = {k: (root / p).read_text(encoding="utf-8") for k, p in SOURCE_FILES.items()}
    icon_sym = dict(re.findall(r"\[SPECIES_(\w+)\]\s*=\s*(gMonIcon_\w+)", _table(text["icons"], "gMonIconTable")))
    pal_idx = {k: int(v) for k, v in re.findall(r"\[SPECIES_(\w+)\]\s*=\s*(\d+)", _table(text["icons"], "gMonIconPaletteIndices"))}
    sym_path = {s: p for s, p in re.findall(r"\b(gMonIcon_\w+)\[\]\s*=\s*INC\w+\(\s*\"([^\"]+)\"", text["graphics"])}
    names = dict(re.findall(r"\[SPECIES_(\w+)\]\s*=\s*_\(\"([^\"]*)\"\)", text["names"]))
    dex_enum = re.search(r"enum\s*\{(.*?)\};", text["dex"], re.S).group(1)
    dex = {n: i for i, n in enumerate(re.findall(r"NATIONAL_DEX_(\w+)", dex_enum))}
    # The enum runs on past the last species (NATIONAL_DEX_COUNT) into placeholders.
    last = dex[re.search(r"#define\s+NATIONAL_DEX_COUNT\s+NATIONAL_DEX_(\w+)", text["dex"]).group(1)]

    def png(sym):
        return re.sub(r"\.(4bpp|png)$", ".png", sym_path[sym])

    out = {}
    for const, n in sorted(dex.items(), key=lambda kv: kv[1]):
        if not 0 < n <= last:
            continue
        slug = const.lower()
        out[slug] = {"dex": n, "name": display_name(names.get(const, const)), "species": slug,
                     "form": "", "repo": "pokeemerald", "icon": png(icon_sym[const]),
                     "palette": pal_idx.get(const, 0)}
    if len(out) != SPECIES_COUNT:
        raise FetchError(f"fetch: parsed {len(out)} species from pret, expected {SPECIES_COUNT}")
    for const, sym in icon_sym.items():
        if const.startswith("UNOWN_"):
            path = png(sym)
            form = pathlib.PurePosixPath(path).parent.name
            out[f"unown-{form}"] = {**out["unown"], "form": form, "icon": path, "palette": pal_idx.get(const, 0)}
    for form, (repo, path) in DEOXYS_FORMS.items():
        out[f"deoxys-{form}"] = {**out["deoxys"], "form": form, "repo": repo, "icon": path}
    return out


def display_name(text):
    """"HO-OH" -> "Ho-oh", "MR. MIME" -> "Mr. Mime", "FARFETCH'D" -> "Farfetch'd"."""
    return " ".join(w[:1].upper() + w[1:].lower() for w in text.split(" "))


def species_table(dl, refresh=False):
    """Cached species.json, or fetch pret's tables and parse them."""
    f = dl.assets / "species.json"
    if f.exists() and not refresh:
        return json.loads(f.read_text())
    dl.get([("pokeemerald", p) for p in SOURCE_FILES.values()])
    table = parse_species(dl.assets)
    f.write_text(json.dumps(table, ensure_ascii=False, indent=1) + "\n")
    return table


# ---------------------------------------------------------------- names

def resolve(spec, table):
    """A mon name as a user may write it -> its key in the table."""
    s = spec.strip().lower()
    s = s.replace("♀", "_f").replace("♂", "_m").replace(". ", "_").replace(".", "").replace("'", "").replace("’", "")
    s = s.replace(" ", "_")
    s = FORM_ALIASES.get(s, s)
    candidates = [s, s.replace("-", "_")]
    if "-" in s:
        base, form = s.rsplit("-", 1)
        candidates.append(f"{base.replace('-', '_')}-{form}")
    for c in candidates:
        c = FORM_ALIASES.get(c, c)
        if c in table:
            return c
    near = difflib.get_close_matches(s, list(table), n=3)
    hint = f" (did you mean {', '.join(near)}?)" if near else ""
    raise FetchError(f"unknown mon {spec!r}{hint}")


def device_spec(dev):
    """A [[device]]'s mon spec, or None when it has none of its own."""
    mon = (dev.get("mon") or "").strip()
    form = (dev.get("form") or "").strip()
    if not mon and form:
        mon = (dev.get("name") or "").strip()  # name = "deoxys", form = "attack"
    if not mon:
        return None
    return f"{mon}-{form}" if form else mon


def mons_from_config(config, table):
    """The mons to bake, in a stable order: each device's mon (or its name,
    if that's a species), this machine's name if it's a species and not a
    configured device, then [font].extra, then MACHINE_MONS (the pool a
    device without a species gets its mon from)."""
    out = []

    def add(key):
        if key not in out:
            out.append(key)

    def add_name(name):
        try:
            add(resolve(name, table))
        except FetchError:
            pass

    devices = config.get("device", [])
    for dev in devices:
        spec = device_spec(dev)
        if spec:
            add(resolve(spec, table))
        else:
            add_name(dev.get("name", ""))
    me = (config.get("self") or socket.gethostname().split(".")[0]).lower()
    if me and me not in {(d.get("name") or "").lower() for d in devices}:
        add_name(me)
    for spec in config.get("font", {}).get("extra", []):
        add(resolve(spec, table))
    for spec in MACHINE_MONS:
        add(resolve(spec, table))
    return out


def parse_mons(arg, table):
    keys = []
    for spec in arg.split(","):
        if spec.strip():
            k = resolve(spec, table)
            if k not in keys:
                keys.append(k)
    if not keys:
        raise FetchError("--mons: no mons given")
    return keys


# ------------------------------------------------------------------ main

def fetch(assets, keys, urls, refresh=False, quiet=False):
    """Make sure everything needed to bake `keys` is cached. -> table."""
    dl = Downloader(assets, urls)
    table = species_table(dl, refresh)
    keys = [resolve(k, table) for k in keys]
    items = [("pokeemerald", p) for p in PALETTES]
    items += [("pokeemerald", p) for p in ANIM_ASSETS.values()]
    items += [(table[k]["repo"], table[k]["icon"]) for k in keys]
    n = dl.get(items)
    if not quiet:
        print(f"fetch: {len(keys)} mon(s), {len(set(items))} files in {dl.assets} ({n} downloaded)", file=sys.stderr)
    return table


def missing(assets, keys):
    """True when fetch has something to do for these keys (no network)."""
    assets = pathlib.Path(assets)
    f = assets / "species.json"
    if not f.exists():
        return True
    table = json.loads(f.read_text())
    paths = [palette_path(assets, i) for i in range(3)] + [anim_asset(assets, n) for n in ANIM_ASSETS]
    for k in keys:
        try:
            paths.append(icon_path(assets, table[resolve(k, table)]))
        except FetchError:
            return True
    return not all(p.exists() and p.stat().st_size for p in paths)


def main(argv=None):
    ap = argparse.ArgumentParser(description="Fetch Gen 3 box icons and battle-anim sprites from pret.")
    ap.add_argument("--assets", type=pathlib.Path, default=None, help="cache dir (default $HERDRMON_ASSETS or $XDG_CACHE_HOME/herdrmon/assets)")
    ap.add_argument("--mons", help="comma-separated mons (default: from config)")
    ap.add_argument("--all", action="store_true", help="every species and form (for a selector's previews)")
    ap.add_argument("--config", type=pathlib.Path, default=None, help="config.toml (default $HERDRMON_CONFIG or $XDG_CONFIG_HOME/herdrmon/config.toml)")
    ap.add_argument("--emerald", help=f"pokeemerald base URL (default {EMERALD}); file:// works")
    ap.add_argument("--firered", help=f"pokefirered base URL (default {FIRERED}); file:// works")
    ap.add_argument("--refresh", action="store_true", help="re-read pret's species tables")
    args = ap.parse_args(argv)

    assets = args.assets or default_assets()
    config = load_config(args.config)
    urls = repo_urls(config, args.emerald, args.firered)
    table = species_table(Downloader(assets, urls), args.refresh)
    if args.all:
        keys = list(table)
    elif args.mons:
        keys = parse_mons(args.mons, table)
    else:
        keys = mons_from_config(config, table)
    fetch(assets, keys, urls)
    print(assets)


if __name__ == "__main__":
    main()
