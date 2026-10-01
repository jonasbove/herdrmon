# herdrmon on TUIOS

[TUIOS](https://tuios.dev) is a terminal multiplexer with a built-in
compatibility layer for Herdr's client socket API. This is an honest
evaluation of what that buys herdrmon, based on reading
[tuios.dev/docs](https://tuios.dev/docs) and its `herdr-compatibility`,
`hooks`, `sessions`, and `remote-hosts` pages (2026-10-01). Pages for
"agents" and "session-rail" as separate topics don't exist as such — agent
state shows up as fields on the compatibility socket and in hook env vars,
and "the rail" is a feature of the `sessions` page, not its own doc.

## The short version

- **The party TUI works against TUIOS.** Point it at the right socket and
  it can list panes, open workspaces, and read/write them, using the exact
  same Herdr client calls it already makes.
- **The sidebar plugin cannot run on TUIOS, full stop** — not "degraded",
  not "needs an adapter". TUIOS has no plugin system and hooks are
  write-only reactions with no way to push pane metadata, icons, or labels
  back into the UI. There is nothing to hook.

## What TUIOS actually is, protocol-wise

TUIOS has two separate protocols, and it's easy to conflate them:

1. **Its native control protocol**, on `$XDG_RUNTIME_DIR/tuios/tuios.sock`
   (protocol version 1, `hello` response shape
   `{"type":"hello","protocol":1,"min_protocol":1,"daemon_version":"0.8.0",...}`).
   This is TUIOS's own thing and has no relation to Herdr.
2. **A Herdr-compatibility socket**, on
   `$XDG_RUNTIME_DIR/tuios/tuios.sock.herdr` (falling back to
   `/tmp/tuios-<uid>/tuios.sock.herdr` if `$XDG_RUNTIME_DIR` isn't set).
   The docs state it plainly: "TUIOS follows the API of herdr **0.9.3**."

That second socket is the one herdrmon cares about.

### Detecting TUIOS vs. real Herdr

A `ping` on the compatibility socket answers with version string
`0.9.3+tuios` and a `server: "tuios"` field — real Herdr won't set
`server` to that value. herdrmon's party TUI should ping whatever socket
`HERDR_SOCKET_PATH` resolves to and branch on that field, rather than
assuming Herdr.

### What's supported on the compat socket

Confirmed from the docs: `session.snapshot`, `pane.read`,
`pane.send_text`, `pane.send_keys`, `pane.report_agent`,
`events.subscribe`, and list/CRUD operations for panes (rename, focus,
split, close), **tabs** (create, rename, focus, move, close), **workspaces**
(create, rename, close), and git worktrees. Pane IDs use Herdr's own form,
`w<session>:p<window>`, stable for the life of the session/window.

That `workspace: create` verb is the one that matters for herdrmon: opening
a new Herdr workspace on a picked device — the whole point of the party
menu — is a supported, first-class operation on the compat socket, not an
edge case. The docs even show existing Herdr-ecosystem tools working
against it unmodified, e.g. `COLLIE_MUX=herdr collie start` and plain
`herdr pane list` / `herdr api snapshot` invocations pointed at the TUIOS
socket.

### What's unsupported

Verbatim from the docs: "Other herdr methods, such as the **plugin** and
**layout** methods, answer the error `unsupported`." (The docs point to
TUIOS's own `AGENT_STATE.md` for a full method-by-method table, which
wasn't fetched here — it's internal to the TUIOS repo, not published under
`/docs`.)

### Permissions

Access is grant-based and pane-scoped: a tool running outside any pane
gets full user rights; a tool running inside a pane inherits that pane's
grants (`read`, `write`, `admin`, `respond`). Notably "`admin` does not
give `respond`" — a pane waiting on a prompt can only be typed into by
something holding `respond` specifically. Not a blocker for herdrmon
(the party TUI runs outside any pane, as a launcher), just worth knowing
if a future feature wants to script *into* an existing agent pane.

## Wiring herdrmon up

1. Resolve the socket from `HERDR_SOCKET_PATH` if set, else the normal
   Herdr default, else probe `$XDG_RUNTIME_DIR/tuios/tuios.sock.herdr`.
2. `ping` it. If `server == "tuios"`, log/display that herdrmon is running
   against TUIOS rather than Herdr (mismatched version expectations are a
   real source of confusing bugs otherwise).
3. Use the same `workspace.create` / pane calls herdrmon already issues
   against Herdr. No TUIOS-specific code path is needed beyond the
   detection step — this is the entire value of the compatibility layer.
4. Don't call anything under `plugin.*` or `layout.*` against this socket;
   they always fail with `unsupported`. herdrmon's TUI doesn't call those
   today, so this is a non-issue for the TUI specifically — it only
   matters for the sidebar plugin question below.

## Why the sidebar plugin is out, not "degraded"

The sidebar plugin's whole job is to push data *into* the UI: animate a
Pokémon per pane, and label panes with `$agent`/`$tools` tokens. That
needs a write path into TUIOS's rendering. Two candidate paths exist, and
both are dead ends:

- **The plugin API.** Doesn't exist for compat clients — `plugin.*` methods
  return `unsupported`, and the hooks page documents no separate plugin
  system either ("No plugin system is mentioned").
- **Hooks** (`after-new-window`, `after-focus-change`, `after-agent-state`,
  `after-command-finished`, and six others). These run `sh -c` on an event,
  with data passed *only* as environment variables
  (`TUIOS_EVENT`, `TUIOS_WINDOW_ID`, `TUIOS_WORKSPACE`, etc.) — "There are
  no arguments and no stdin." Critically, the docs state outright that hook
  scripts **cannot modify pane metadata, colors, icons, or titles.** A hook
  is a one-way trigger for side effects (notifications, logging, launching
  something), not a channel back into the rail.

The one near-miss is that the compat socket *does* support renaming a
tab (`tab.rename`), so an `after-agent-state` hook could in principle shell
out and rename the active tab to something like `Ho-oh — working`. That's
plain text, not a glyph from a custom COLRv0 font, not an animation, and
not the per-pane `$mon`/`$agent`/`$tools` token model the sidebar plugin
actually implements. It's a different, much poorer feature, and building
and maintaining a hook-triggered renamer just to get static text labels
isn't a good trade against the complexity of shipping and keeping it in
sync with herdrmon's config.

**Verdict: don't build a TUIOS adapter for the sidebar plugin.** The
capability gap isn't a missing convenience method, it's architectural —
TUIOS's extension points are all one-directional (react to an event, run a
command) and none of them write back into the rail. If TUIOS ever ships a
real plugin API, revisit this; until then the sidebar plugin stays a
Herdr-only feature, and that's a fine place for it to stay — it's
explicitly described in DESIGN.md as a standalone Herdr plugin, not a core
dependency of herdrmon.

## Bonus: remote hosts

Not asked for by the original brief, but worth noting since it surfaced
while reading the docs: TUIOS has its own native multi-machine support —
`tuios hosts add`, SSH-linked daemons per host, Tailscale auto-discovery,
and a unified rail across machines (`tuios attach --host build api`,
`tuios new-window deploy --host build`). This is a parallel, richer
system to herdrmon's own `[discovery] tailscale/ssh_config` device list,
not something herdrmon needs to integrate with — herdrmon's party menu
already does its own device discovery and just needs *a* socket to open a
workspace on, which the compat layer provides regardless of how that
remote host got into TUIOS's rail.

## Summary

| | Herdr | TUIOS (compat socket) |
|---|---|---|
| Party TUI (open a workspace) | ✅ native | ✅ `workspace.create`, confirmed supported |
| `herdr --machine` style remote open | ✅ | ✅ (TUIOS also has its own, richer remote-host model) |
| Sidebar plugin (animated mon + tokens) | ✅ native plugin | ❌ no plugin API, hooks can't write back to the UI |

Bottom line: set `HERDR_SOCKET_PATH` and herdrmon's TUI runs on TUIOS
today with no code changes beyond detecting which server answered the
`ping`. The sidebar plugin doesn't, and no reasonable amount of hook
scripting changes that.
