# herdrmon: packaging design (shared contract)

This is the design contract between herdrmon's parts.
It has three parts:

1. **party TUI**: the Go program at the repo root. It is a FireRed party menu of
   your devices, and opens a Herdr workspace on the device you pick.
2. **icon font**: `font/`. It takes Gen 3 Pokémon box icons, fetched from pret at
   install time, and bakes every animation into a COLRv0 font plus a
   `frames.json` manifest.
3. **sidebar plugin**: `sidebar/`. It is a standalone Herdr plugin that animates
   each machine's Pokémon in the Herdr sidebar and labels panes by agent/program.

Packaging lives in `omarchy/` (Omarchy shell plugin + installer), `flake.nix`
(Nix) and `docs/tuios.md`.

Decisions:
- Public repo (github.com/jonasbove/herdrmon), MIT licence for the code, so
  the no-art rule is absolute.
- The sidebar is one small standalone Herdr plugin (id `jonasbove.herdrmon`)
  that coexists with herdr-radar.
- Default keybind: **SUPER ALT, P**.

## CLI contract (who calls what)

- `herdrmon`: the party menu.
- `herdrmon pick`: the Pokémon selector.
- `herdrmon serve`: the HTTP `/status` endpoint.
- `herdrmon doctor`: checks deps, font, frames and config.
- `herdrmon paths`: prints the resolved paths as JSON.
- `font/build.py`: PEP 723 inline deps, so `uv run font/build.py` just works.
  Flags: `[--config PATH] [--assets DIR] [--out-font PATH] [--out-frames PATH]
  [--mons a,b,c] [--no-guard]`. It runs fetch first if assets are missing.
- `font/fetch.py`: also PEP 723. Flags: `[--assets DIR] [--mons a,b,c]
  [--emerald URL] [--firered URL]`. It downloads only what is needed, is
  idempotent, and writes an `assets/SOURCES.txt` listing the URL of every file.
- `omarchy/install.sh [--uninstall] [--yes]`: called by the bar widget and by humans.

Hard rules:
- No hard-coded hosts, mons, users, IPs, domains or `/home/...` paths. Everything
  comes from config, discovery or XDG paths.
- **No Nintendo art in git.** Sprites and animation assets are downloaded from
  pret's public repos by `herdrmon fetch` (or `font/fetch.py`) into the cache.
  `.gitignore` already excludes `font/assets/`, `*.otf` and `font/out/`. Never
  commit a PNG of a Pokémon or a built font.
- Lowercase technical names (`herdrmon`, `onix`). Capitalised display names
  (`Onix`, `Ho-oh`).
- Neovim gets first-class treatment, because it's an Omarchy default. Every other
  coding agent is data in a table, not code branches.

## Paths (XDG; every one can be overridden by env)

| what | default | env |
|---|---|---|
| config | `$XDG_CONFIG_HOME/herdrmon/config.toml` | `HERDRMON_CONFIG` |
| fetched assets | `$XDG_CACHE_HOME/herdrmon/assets/` | `HERDRMON_ASSETS` |
| built font | `$XDG_DATA_HOME/fonts/HerdrmonIcons.otf` | `HERDRMON_FONT` |
| frames manifest | `$XDG_DATA_HOME/herdrmon/frames.json` | `HERDRMON_FRAMES` |
| state (sidebar lead, debug) | `$XDG_STATE_HOME/herdrmon/` | |

Font family: **"Herdrmon Icons"**. Terminals use it as a fallback font after
the Nerd Font.

## config.toml (one file, read by all three parts)

```toml
# Which device am I? Default: hostname, lowercased, without the domain.
# self = "onix"

[font]
# Mons to bake in addition to those named by devices (e.g. for agents).
extra = []
# Where pret lives. Override with a mirror or a local checkout (file://).
pret_emerald = "https://raw.githubusercontent.com/pret/pokeemerald/master"
pret_firered = "https://raw.githubusercontent.com/pret/pokefirered/master"

[discovery]
tailscale = true      # add every tailnet peer (`tailscale status --json`; works with Headscale)
ssh_config = false    # add Host entries from ~/.ssh/config
herdr = true          # add every `herdr machine list` entry

[[device]]
name = "geodude"           # key and display name (Display = capitalised, or `display = "Ho-oh"`)
mon = "geodude"            # species slug (pret folder name, e.g. "ho_oh", "deoxys", "mr_mime"); default: see below
transport = "ssh"          # "local" | "tailscale" | "ssh" | "http" | "none"
address = "geodude"        # ssh target / tailnet name or IP / http(s) base URL
herdr_machine = "Geodude"  # herdr --machine label; default = matched from `herdr machine list`; "-" = no Herdr
form = ""                  # optional sprite variant, e.g. "attack" for Deoxys (FRLG)

[device.battery]           # optional: where BAT comes from
source = "sysfs"           # "sysfs" (local or over ssh) | "http" | "homeassistant" | "none"
# homeassistant: url = "http://ha:8123", entity = "sensor.phone_battery_level", token_file = "~/.config/herdrmon/ha.token"
```

**Default mon** for a device that has no `mon`: if the device name is a Gen 3
species slug, use that species. Otherwise pick deterministically from a hash of
the name, over a small curated list of sensible "machine" mons (Porygon,
Magnemite, Voltorb, Geodude, Onix, Beldum, Bronzor-equivalents in Gen 3, …).
`herdrmon pick` is the **Pokémon selector**: an interactive TUI (reuse the
party-menu look) listing devices. For each one you search or scroll all 386 Gen 3
species with a live preview, then write `mon = ...` into `config.toml` and
rebuild the font.

## Transports: NET / SYS / BAT / level

| transport | discovery | NET | SYS + level (uptime) | BAT |
|---|---|---|---|---|
| local | self | 1.0 | read /proc directly | sysfs |
| tailscale | `tailscale status --json` | `tailscale ping` (direct vs DERP) | over ssh if reachable, else unknown | per [device.battery] |
| ssh | config / ~/.ssh/config | TCP connect time to port 22 (or `ssh -O check`) | `ssh host sh -c '<probe script>'` | sysfs over ssh |
| http | config | GET time to `<address>/status` | the JSON from `/status` | the JSON from `/status` |
| none | config | unknown | unknown | unknown |

`herdrmon serve [--listen 127.0.0.1:7643]` exposes `/status` as JSON. The
fields are `{uptime_s, mem_free, disk_free, load1, ncpu, temp_c, battery, herdr}`,
so any device can be probed over HTTP (put it behind your tailnet or a reverse
proxy; there's no auth beyond the bind address).

## frames.json (contract between font build, TUI and sidebar)

This keeps the current shape and adds a version field:

```json
{ "version": 2, "cells": 5, "font": "Herdrmon Icons",
  "mons":  { "<device-or-mon key>": { "base": [..], "bob": [..], "poison": [..], "recall": [..],
                                       "sendout": [..], "harden": [..], "tackle": [..],
                                       "sleep": [..], "party": [..], "paralysis": [..] } },
  "big":   { "<key>": { "base": [..], "bob": [..], "party": [..], "paralysis": [..] } },
  "big3":  { "<key>": { ... same ... } } }
```

- Each animation is a list of frames, and each frame is a string of codepoints
  (`cells` wide for `mons`; for `big`/`big3`, a list of row strings per frame).
- Keys are **species slugs**. Devices map to species through config, so two
  devices may share a species.
- Codepoints: stills in BMP PUA gaps; animation frames from U+100000 upward. The
  build **must refuse** if any codepoint overlaps a glyph in a font fontconfig can
  see (`fc-list`). This guard already exists in `build.py`; keep it.
- Consumers reload the file when its mtime changes.

## Sidebar plugin (`sidebar/`, Herdr plugin id `jonasbove.herdrmon`)

- It's a standalone Herdr plugin (its own `herdr-plugin.toml`) that coexists with
  stock herdr-radar or with no radar at all.
- It publishes pane metadata tokens under its own source:
  - `$mon`: the machine's animated Pokémon, on the pane that heads each
    workspace group.
  - `$agent`: the vendor glyph and name.
  - `$tools`: the programs in the tab (neovim labelled by framework or filetype).
- The `configure` action prints and optionally writes a sidebar line that uses
  those tokens.
- Agents come from a table (`sidebar/agents.json`): match on Herdr's detected
  `agent`, the process name or the title regex, giving `{label, glyph, colour}`.
  Ship at least: claude, codex, opencode, gemini, hermes, aider, goose, amp,
  cursor-agent, crush, qwen, copilot, pi, kiro, droid, cline, plus neovim/vim/helix.
  The user can extend it via `config.toml [agents.<id>]`.
- Animations follow the existing `mon.js` behaviour (it's in
  `sidebar/reference/`):
  - Wall-clock tick grid: 125 ms tick, 500 ms bob beat.
  - States: blocked = poison; night = sleep; one-shots for git events and
    send-out/recall.
  - The night signal must be generic: a configurable command, defaulting to
    hyprsunset on Hyprland if present, otherwise local time 22:00–07:00.
  - Nothing may ssh to a hard-coded host.

## Omarchy packaging (`omarchy/` + repo-root `manifest.json`)

- The repo root is installable with `omarchy plugin add <git-url>` as an Omarchy
  shell plugin.
  - `kinds: ["bar-widget"]`: a small Poké Ball bar widget. Clicking it launches
    `herdrmon` in a floating terminal (`omarchy-launch-or-focus-tui`).
  - If the binary or font is missing, the widget offers "Set up" instead, which
    runs `omarchy/install.sh` in a terminal.
- `omarchy/install.sh` is idempotent and safe to re-run. It:
  - checks deps (go or a prebuilt binary, uv, node ≥ 18, herdr optional);
  - builds/installs the binary to `~/.local/bin`;
  - writes a default `config.toml` if absent;
  - runs fetch and build for the font, then `fc-cache`;
  - adds the font as a fallback in foot / ghostty / kitty / alacritty configs
    where they exist (backup first, managed block markers);
  - links the sidebar plugin into Herdr if `herdr` exists;
  - adds an Omarchy menu entry via `~/.config/omarchy/extensions/omarchy-menu.jsonc`
    (managed block) and a Hyprland keybind snippet (default `SUPER ALT, P`) in
    the user's hypr config dir, printed rather than forced if the config isn't
    a plain include.
  - `install.sh --uninstall` reverses all of it.

## Nix (`flake.nix`)

- `packages.herdrmon`: `buildGoModule`.
- `packages.herdrmon-icons`: the font + frames.json, built in a derivation from
  **pinned `fetchFromGitHub` pret/pokeemerald + pokefirered** (fixed hashes) and a
  `mons` list argument (override via `.override { mons = [...]; }`). The art never
  enters this repo, only the derivation.
- `packages.herdrmon-sidebar`: the plugin directory.
- `homeManagerModules.default`: `programs.herdrmon.{enable, settings (→config.toml), mons, sidebar.enable}`.
- `devShells.default`: go, uv/python+fonttools+pillow+skia-pathops, node.

## TUIOS (`docs/tuios.md`)

TUIOS answers Herdr's socket API (v0.9.3) on
`$XDG_RUNTIME_DIR/tuios/tuios.sock.herdr`, but returns `unsupported` for plugin
methods. So:
- The party TUI should work against TUIOS by setting `HERDR_SOCKET_PATH`.
  Detect TUIOS (ping answers `server: "tuios"`) and use the socket API, or
  `tuios` CLI equivalents, for workspace creation.
- The sidebar plugin can't run as a TUIOS plugin. Evaluate whether a TUIOS hook
  (`after-agent-state`) plus pane metadata gives anything useful, and document
  the honest answer.
