# Herdrmon sidebar (Herdr plugin `jonasbove.herdrmon`)

Your machine's Pokémon in the [Herdr](https://herdr.dev) sidebar, animated on
the same clock on every machine, plus a vendor label for every agent and the
programs running beside it.

- **`$mon`**: this machine's Pokémon. It sits on the first agent pane of each
  workspace, which heads that workspace's group in the Agents panel, and also
  on the workspace itself in the Spaces panel. It bobs like the party menu,
  turns **poisoned** while an agent is blocked on you, plays **recall** and
  **send-out** when you answer, **tackles** on a git commit and **hardens** on
  a push. At night it **sleeps** unless an agent is working.
- **`$agent`**: the agent's glyph and name, e.g. `✻ Claude Code`, in a colour
  from your Herdr theme.
- **`$tools`**: the other programs in the pane's tab. Neovim is shown by
  what it's editing: the project's framework (`react`, `fastapi`, …), else the
  file's language. On a workspace, `$tools` lists everything running in it.
  `$tool_1` … `$tool_4` carry the same items one per token, each in its own
  colour. Herdr caps a token value at 80 characters, so a busy tab's `$tools`
  is cut short; the `$tool_N` tokens aren't.

It's standalone. It works alone, next to stock
[herdr-radar](https://github.com/hhdebb/herdr-radar), or alongside the rest of
herdrmon (the party menu and the icon font).

## Requirements

- Herdr ≥ 0.9.0 and Go ≥ 1.26 to build it. The plugin is one Go binary
  (`bin/herdrmon-sidebar`, standard library only); `herdr plugin install`
  builds it, offline, with the manifest's `[[build]]` step.
- For the sprites: the **Herdrmon Icons** font and its `frames.json`, built by
  herdrmon's `font/build.py` (see the repo README). Without them `$mon` falls
  back to a plain `◓`.
- A Nerd Font for the program glyphs (Omarchy ships one). Set
  `[sidebar] glyphs = "text"` to use plain Unicode instead.

## Install

```sh
herdr plugin install jonasbove/herdrmon/sidebar
herdr plugin action invoke jonasbove.herdrmon.configure
```

From a checkout (`plugin link` doesn't run build steps, so build first):

```sh
cd /path/to/herdrmon/sidebar
CGO_ENABLED=0 go build -trimpath -o bin/herdrmon-sidebar .
herdr plugin link /path/to/herdrmon/sidebar
bin/herdrmon-sidebar configure                              # print the snippet
herdr plugin action invoke jonasbove.herdrmon.configure    # or write it
herdr plugin action invoke jonasbove.herdrmon.start
```

The daemon starts by itself with every Herdr server (a startup hook), and
comes back when an agent is detected if it ever crashes. Each Herdr server
gets its own daemon, so named sessions work too.

## Actions

| action | what it does |
|---|---|
| `configure` | Writes the sidebar layout into Herdr's `config.toml` between `# >>> herdrmon sidebar block` markers, with a backup first, then reloads. |
| `unconfigure` | Stops the daemon, clears its tokens, removes the block and reloads. |
| `start` / `stop` | Starts or stops the daemon. `stop` clears every token it published. |
| `demo` | Plays every animation on the focused workspace: send-out, tackle, harden, sleep, poison, then recall and send-out. |

`bin/herdrmon-sidebar configure` with no flags only prints. `--write`, `--reload`,
`--uninstall` and `--herdr-config PATH` do what they say; `--no-backup` skips
the `config.toml.bak.<time>` copy (for callers that keep their own, like
BovéOS's `bove-theme-sync`, which re-runs `configure` on every theme switch).

### Next to herdr-radar (or your own layout)

TOML can't define a table twice. If `[ui.sidebar.agents]` or
`[ui.sidebar.spaces]` already exists outside herdrmon's block (radar's managed
block writes `[ui.sidebar.agents]`), `configure` leaves that table alone. It
writes only the tables nobody else owns, and prints the `$mon` / `$agent` /
`$tools` token entries for you to paste into the existing rows. Radar's tokens
(`$host`, `$logo`, …) and herdrmon's never collide. If the existing table is
inside radar's own managed block, radar rewrites that block when it
reconfigures and the pasted entries are lost; TOML gives a second plugin no
way to extend another table's `rows`.

## The layout it writes

```toml
[ui.sidebar.agents]
rows = [
  # $mon only shows on each workspace's head row
  [{ token = "$mon", ... }, "state_icon", "machine", "workspace", "tab"],
  [{ token = "$agent", ... }, { token = "$tools", ... }],
]

[ui.sidebar.spaces]
rows = [
  ["state_icon", { token = "$mon", ... }, "workspace"],
  ["branch", "git_status"],
  [{ token = "$tools", ... }],
]
```

The `...` stands for `fg` and twelve `rules`. Herdr only takes hex colours in
sidebar styles, so each value starts with N zero-width spaces naming a palette
slot (text, subtext0, overlay0, overlay1, accent, mauve, green, yellow, red,
blue, teal, peach). `configure` turns each slot into a `starts_with` rule with
that slot's hex in your current Herdr theme: the built-in palette plus
`[theme.custom]`. **Re-run `configure` after changing Herdr's theme.** With
`auto_switch`, the colours come from `dark_name`, or else from the dark
sibling of `name`, which is what Herdr itself does.

A wider sidebar shows more of the second line: `[ui] sidebar_width = 34`.

## Configuration (`~/.config/herdrmon/config.toml`)

This is the same file as the rest of herdrmon. The sidebar reads:

```toml
self = "onix"              # which device this is (default: hostname)

[[device]]
name = "onix"
mon = "onix"               # species slug; default: the name if it is a Gen 3
                           # species, else a stable pick from machine-like mons

[sidebar]
night_command = ""         # unset: hyprsunset on Hyprland, else the clock
                           # "cmd": run with sh -c, exit 0 means night
                           # "": the clock only
night_start = 22           # clock fallback, local hours
night_end = 7
night_below_k = 6000       # hyprsunset temperature that counts as night
glyphs = "nerd"            # or "text"
fallback_glyph = "◓"       # $mon without frames or font
lead_ms = 0                # shift this machine's frames (calibration)

# Add an agent, or override any entry in agents.json:
[agents.myagent]
label = "My Agent"
glyph = "◎"
colour = "teal"            # a palette slot name
match = { agent = ["myagent"], process = ["my-agent"], title = "^my-agent" }
```

Paths follow XDG, and env overrides them: `HERDRMON_CONFIG`, `HERDRMON_FRAMES`
(default `$XDG_DATA_HOME/herdrmon/frames.json`), `HERDRMON_STATE`. Herdr's
own config is read from `HERDR_CONFIG_PATH`, or
`$XDG_CONFIG_HOME/herdr/config.toml`.

## Agents table

[`agents.json`](agents.json) holds both kinds of entry. **agents** are claude,
codex, opencode, gemini, hermes, aider, goose, amp, cursor-agent, crush, qwen,
copilot, pi, kiro, droid, cline, devin, antigravity, oh-my-pi, kimi, kilo,
grok, mastra, qoder, letta, maki and muse. **programs** are neovim, vim, helix,
git, ssh, python, node, go, cargo, docker, top, file managers, and shells,
which are hidden from `$tools`.

A pane matches the first entry whose `match` fits, checked in order:

1. Herdr's detected agent id.
2. The foreground process name.
3. A title regex.

## How it behaves

- **One Pokémon per workspace.** Its state rolls up the workspace's agents:
  blocked beats working beats idle. Git events from any of the workspace's
  agents play on it.
- **Shared clock.** Frames come from the wall clock: a 125 ms tick for
  poison and sleep, a 500 ms beat for the bob and for one-shot starts, and
  50 ms per one-shot frame. They are written to land on the boundary, so
  machines synced by NTP flip together.
- **Font rebuilds.** `frames.json` is re-read when its mtime changes. If the
  manifest names a font that fontconfig can't find, `$mon` falls back to the
  plain glyph rather than drawing the wrong codepoints.
- **Head row and sort order.** The head row is the first agent pane by tab,
  then pane id. That is the first row in Herdr's grouped Agents order. In
  priority order a blocked agent may sort above it, and the Spaces panel
  always shows `$mon`.
- **Collapsed sidebar.** Each workspace also gets `$mon_rail`: the same
  frame cropped to the cells the Pokémon uses at rest, so it is the same
  width in every frame (a send-out, recall or tackle that leaves those cells
  keeps the last frame that fitted). Stock Herdr ignores it; BovéOS's Herdr
  patch (`[ui.sidebar.collapsed]`) draws it alone in the collapsed rail,
  outlined in the colour of the workspace's status icon.
- **Logs.** The log is `daemon.log` in the plugin's state directory. Touch
  `debug` in the same directory to log every frame write with its lateness.

## Development

```sh
cd sidebar        # CGO_ENABLED=0 where there is no C compiler
go test .          # unit tests, golden tests against the old Node.js plugin, e2e
go test -short .   # the same without the ~12 s daemon run against a fake Herdr
go vet .
go run ./tools/gen-palettes <herdr>/src/app/state.rs   # regenerate data/herdr-palettes.json
```

`data/herdr-palettes.json` holds Herdr's built-in theme colours, from
`src/app/state.rs`. The 386 Gen 3 species slugs and the default-mon pick
come from `internal/pokedex`, shared with the party menu. Neither contains
any art.

The plugin was Node.js up to 0.1.0. `testdata/golden/` holds that
version's answers for the same inputs (TOML parsing and errors, palettes,
config, agent matching, animation frames, the managed block, every
`configure` flag on sample configs, and a scripted daemon session against
a fake Herdr socket), and the Go tests hold this port to them byte for
byte.

MIT licence.
