# Herdrmon Icons

The icon font: every animation of your devices' Pokémon, baked into a COLRv0
colour font (`HerdrmonIcons.otf`) plus `frames.json`, which lists the
codepoints of each frame for the party menu and the Herdr sidebar.

## Build

```sh
uv run font/build.py            # mons from ~/.config/herdrmon/config.toml
fc-cache -f                     # then add "Herdrmon Icons" as a fallback font
```

`build.py` fetches whatever art is missing first (`fetch.py`), so a fresh
machine needs only network access to GitHub, [uv](https://docs.astral.sh/uv/)
and fontconfig. A dozen mons take about ten seconds.

Which mons get baked:

- each `[[device]]`'s `mon` (plus `-<form>` when it sets `form`), or the
  device's name when that is a species;
- this machine's name (config `self`, else the hostname) when it is a species
  and not already a `[[device]]`;
- `[font].extra`;
- a small pool of "machine" mons (Porygon, Magnemite, Voltorb, Geodude, Onix,
  Beldum, Baltoy, Nosepass), which devices without a species of their own get
  their mon from;
- or exactly `--mons a,b,c` instead of all of the above.

Mons are pret's species slugs: `onix`, `ho_oh`, `mr_mime`, `nidoran_f`,
`farfetchd`, optionally with a form: `deoxys-attack`, `deoxys-defense`,
`deoxys-speed`, `unown-b` … `unown-question_mark`. Friendly spellings
(`Ho-Oh`, `Mr. Mime`, `Nidoran♀`) work on the command line too. That string
is also the mon's key in `frames.json`.

| flag | default |
|---|---|
| `--config PATH` | `$HERDRMON_CONFIG` or `$XDG_CONFIG_HOME/herdrmon/config.toml` |
| `--assets DIR` | `$HERDRMON_ASSETS` or `$XDG_CACHE_HOME/herdrmon/assets/` |
| `--out-font PATH` | `$HERDRMON_FONT` or `$XDG_DATA_HOME/fonts/HerdrmonIcons.otf` |
| `--out-frames PATH` | `$HERDRMON_FRAMES` or `$XDG_DATA_HOME/herdrmon/frames.json` |
| `--mons a,b,c` | from the config |
| `--guard-exclude FAMILY` | (repeatable) a font family the overlap guard ignores |
| `--no-guard` | check nothing against installed fonts |

The build refuses to write a font whose codepoints another installed font
also covers (`fc-list`), since your terminal might draw that font's glyph
instead. If the clashing font is never ahead of Herdrmon Icons in your
terminal's fallback chain, pass `--guard-exclude 'That Family'`.

Limits: OpenType allows 65,535 glyphs and a mon costs roughly 800–1,000 (its
frames' cells plus one glyph per colour layer shape), so about 65–80 mons fit
in one font; the build says how many when it finishes and stops early with a
clear message if they don't fit. The same mons always give the same font,
byte for byte (`SOURCE_DATE_EPOCH` sets its timestamp).

Other tools:

- `uv run font/fetch.py [--mons a,b,c | --all]` only downloads. `--all` fetches
  every species and form, e.g. for a selector's previews.
  `assets/species.json` lists all of them with National Dex number, display
  name and icon palette.
- `uv run font/anims.py out.png --mons pikachu` renders every animation to a
  PNG sheet; `uv run font/export_gifs.py out_dir --mons pikachu` makes GIFs.

Offline, or to pin the art: point `--emerald`/`--firered` (or the config's
`[font].pret_emerald`/`pret_firered`) at local checkouts of pret's repos as
`file:///path/to/pokeemerald`.

## Licence and the art

The code here is MIT. The art isn't ours and isn't in this repo. The box
icons, palettes and battle-animation sprites are © Nintendo / Creatures /
GAME FREAK. `fetch.py` downloads them at build time from pret's public
decompilation projects ([pokeemerald](https://github.com/pret/pokeemerald),
[pokefirered](https://github.com/pret/pokefirered)) into your local cache,
and `assets/SOURCES.txt` records where each file came from. The font you
build contains that art. It's for your own use, so don't redistribute it.
