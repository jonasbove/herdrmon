# Herdrmon

A Pokémon FireRed-style party menu for your machines (tailnet, ssh or http),
living in your Omarchy bar. Pick a device, Enter opens a [Herdr](https://herdr.dev) workspace on it.
Offline machines are FAINTED, machines without Herdr are PARalyzed, and three
HP bars (NET / SYS / BAT) track network, system load and battery live, themed
from your current Omarchy colour scheme.

It has three parts, all optional beyond the first:

- **party TUI** (repo root, Go) — the menu itself: `herdrmon`.
- **icon font** (`font/`) — bakes Gen 3 Pokémon box-icon animations, fetched
  from [pret](https://github.com/pret)'s disassembly projects at install
  time, into a small COLRv0 font plus a `frames.json` manifest.
- **sidebar plugin** (`sidebar/`) — a standalone
  [Herdr](https://herdr.dev) plugin that animates each machine's Pokémon in
  the Herdr sidebar and labels panes by coding agent / program. Needs Herdr;
  everything else works without it.

- The lead slot is this machine. Offline devices are FAINTED ("has no energy
  left to battle!"). Devices without Herdr are PAR: their icon plays
  Status_Paralysis, and choosing one says "is paralyzed! It can't move!".
- Three HP bars: NET, SYS (the worst of free RAM, disk, load and temperature)
  and BAT. Lv = days of uptime. A probe that can't answer shows `--`.
- Badges: PAR (no Herdr), BLK (an agent waits on you), WRK (an agent is
  working), SLP (night), FNT (offline).
- Colours come live from the Omarchy theme (`HERDRMON_THEME=<name>` to preview
  another). Sprites come from the "Herdrmon Icons" font and its `frames.json`
  (see `font/`). They are built on your machine from pret's repos; no art is
  shipped here.


## Screenshot

*(screenshots/GIFs go here — hosted outside this repo, since no Pokémon
artwork is ever committed; see [Licence and art notice](#licence-and-art-notice))*

## Install

### Omarchy

```sh
omarchy plugin add https://github.com/jonasbove/herdrmon.git
```

This adds a Poké Ball widget to your bar. Click it: if herdrmon isn't set up
yet, it runs the installer in a terminal; once it's installed, click opens
the party menu. You can also run the installer directly:

```sh
git clone https://github.com/jonasbove/herdrmon.git
~/.config/omarchy/plugins/jonasbove.herdrmon/omarchy/install.sh
```

`omarchy/install.sh --uninstall` reverses everything it did. See
[`omarchy/install.sh`](omarchy/install.sh) for exactly what it touches
(binary, font, terminal configs, Omarchy menu entry, Hyprland keybind) —
every edit is backed up and wrapped in a marked, idempotent block.

### Nix

```sh
nix run github:jonasbove/herdrmon#herdrmon
```

Or with home-manager:

```nix
{
  inputs.herdrmon.url = "github:jonasbove/herdrmon";

  # in your home-manager config:
  imports = [ inputs.herdrmon.homeManagerModules.default ];
  programs.herdrmon = {
    enable = true;
    sidebar.enable = true; # needs Herdr
    mons = [ "porygon" "magnemite" ]; # extra mons to bake in, beyond your devices
    settings = {
      discovery.tailscale = true;
      device = [
        { name = "geodude"; transport = "ssh"; address = "geodude"; }
      ];
    };
  };
}
```

`packages.herdrmon-icons` (the font) is its own derivation, built from pinned
`fetchFromGitHub` pulls of pret/pokeemerald and pret/pokefirered — the art
never enters this repo, only the Nix derivation that fetches and bakes it.
Override the mon list with `herdrmon-icons.override { mons = [ ... ]; }`.

### Manual

```sh
mise exec go@1.27.0 -- go build -o herdrmon . && install -m 755 herdrmon ~/.local/bin/
cd font && uv run fetch.py && uv run build.py && fc-cache -f
```

## Usage

| command | does |
|---|---|
| `herdrmon` | open the party menu |
| `herdrmon pick` | the Pokémon selector — assign a species to a device |
| `herdrmon serve [--listen addr]` | expose this machine's `/status` as JSON |
| `herdrmon doctor` | check deps, font, frames and config |
| `herdrmon paths` | print the resolved paths as JSON |

Default keybind: **SUPER ALT, P**.


## Devices and transports

Discovery merges this machine, every tailnet peer (`tailscale status --json`,
skipped quietly if tailscale isn't installed), `~/.ssh/config` Hosts
(opt-in), and every `herdr machine list` entry. `[[device]]` entries in
`~/.config/herdrmon/config.toml` add devices or override what was found:

```toml
[discovery]
tailscale = true
ssh_config = false
herdr = true

[night]               # SLP: exit 0 = night. Default: hyprsunset, else 22-07
# command = "test $(date +%H) -ge 23"
start = 22
end = 7

[[device]]
name = "geodude"
mon = "geodude"       # herdrmon pick writes this
transport = "ssh"     # local | tailscale | ssh | http | none
address = "geodude"

[[device]]
name = "phone"
transport = "none"
[device.battery]
source = "homeassistant"
url = "http://homeassistant.local:8123"
entity = "sensor.phone_battery_level"
token_file = "~/.config/herdrmon/ha.token"
```

| transport | NET | SYS + Lv | BAT |
|---|---|---|---|
| local | full | /proc | sysfs |
| tailscale | `tailscale ping` (relay capped at half) | over ssh | sysfs over ssh |
| ssh | TCP connect time to port 22 | over ssh | sysfs over ssh |
| http | GET time of `<address>/status` | the `/status` JSON | the `/status` JSON |
| none | `--` | `--` | `--` |

A device without a `mon` gets its own name if that is a species (`ho-oh` is
Ho-oh), otherwise a stable pick from a few "machine" mons.

## Config reference

One file, read by all three parts: `~/.config/herdrmon/config.toml`
(override with `$HERDRMON_CONFIG`).

```toml
# self = "onix"             # which device am I? default: hostname, lowercased

[font]
extra = []                  # extra mons to bake in beyond those named by devices
pret_emerald = "https://raw.githubusercontent.com/pret/pokeemerald/master"
pret_firered = "https://raw.githubusercontent.com/pret/pokefirered/master"

[discovery]
tailscale = true            # add every tailnet peer
ssh_config = false          # add Host entries from ~/.ssh/config
herdr = true                # add every `herdr machine list` entry

[[device]]
name = "geodude"            # key and display name
mon = "geodude"              # species slug; default: see "Default mon" below
transport = "ssh"            # "local" | "tailscale" | "ssh" | "http" | "none"
address = "geodude"           # ssh target / tailnet name or IP / http(s) base URL
herdr_machine = "Geodude"     # herdr --machine label; "-" = no Herdr
form = ""                     # optional sprite variant (e.g. Deoxys forms)

[device.battery]
source = "sysfs"              # "sysfs" | "http" | "homeassistant" | "none"
```

**Default mon**: if a device's name is a Gen 3 species slug, that species is
used; otherwise one is picked deterministically from a small curated list of
"machine" mons (Porygon, Magnemite, Voltorb, Geodude, Onix, …). Run
`herdrmon pick` to search or browse all 386 Gen 3 species with a live preview
instead.

### Paths

Every path is XDG-based and overridable by environment variable:

| what | default | env |
|---|---|---|
| config | `$XDG_CONFIG_HOME/herdrmon/config.toml` | `HERDRMON_CONFIG` |
| fetched assets | `$XDG_CACHE_HOME/herdrmon/assets/` | `HERDRMON_ASSETS` |
| built font | `$XDG_DATA_HOME/fonts/HerdrmonIcons.otf` | `HERDRMON_FONT` |
| frames manifest | `$XDG_DATA_HOME/herdrmon/frames.json` | `HERDRMON_FRAMES` |
| state | `$XDG_STATE_HOME/herdrmon/` | — |

## Sidebar plugin

`sidebar/` is a standalone Herdr plugin (`jonasbove.herdrmon`) that animates
each machine's Pokémon in Herdr's sidebar (`$mon`) and labels panes by agent
and program (`$agent`, `$tools`, `$tool_1..4`). It works with or without
herdr-radar. See [sidebar/README.md](sidebar/README.md).

## Font

`font/` fetches the box icons and battle-animation assets from pret and bakes
every animation into a COLR font, "Herdrmon Icons", plus `frames.json`.
About 65–80 Pokémon fit in one font. See [font/README.md](font/README.md).

## TUIOS

With `HERDR_SOCKET_PATH` pointing at TUIOS's socket, or with no herdr
installed and TUIOS running, workspaces are created through TUIOS's Herdr API
(`workspace.create`). TUIOS has no `--machine`: a remote device needs
`tuios_host = "<tuios hosts name>"` and opens with
`tuios new --host <host> <label> --detach`. Force a backend with
`mux = "herdr"` or `mux = "tuios"`.

The sidebar plugin does not work on TUIOS, which has no plugin API. See
[docs/tuios.md](docs/tuios.md).

## Licence and art notice

Code in this repo is MIT-licensed — see [LICENSE](LICENSE).

**No Pokémon artwork is ever committed to this repo.** Sprites and animation
frames are downloaded at install time from
[pret](https://github.com/pret)'s public Pokémon disassembly projects
(pokeemerald, pokefirered) directly onto your machine, and baked into a local
font there — never stored or redistributed here. Pokémon is a trademark of
Nintendo, Game Freak and Creatures Inc.; this project is an unofficial fan
tool with no affiliation to any of them.

## Credits

- [pret](https://github.com/pret) — the pokeemerald and pokefirered
  disassembly projects this reads Gen 3 box-icon sprites from.
- [Herdr](https://herdr.dev) — the terminal workspace manager herdrmon opens
  machines into, and whose plugin API the sidebar plugin extends.
- [Omarchy](https://omarchy.org) — the desktop this packages a bar widget
  and installer for.


## Build

    go build -o herdrmon . && install -m 755 herdrmon ~/.local/bin/
    go vet ./... && go test ./...    # -short skips the pty end-to-end test

