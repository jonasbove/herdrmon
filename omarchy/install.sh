#!/usr/bin/env bash
# herdrmon installer / uninstaller.
#
# Usage: install.sh [--uninstall] [--yes] [--prefix DIR]
#
# Idempotent: re-running updates in place. Every file this script edits that
# it does not fully own gets a timestamped backup before the first edit, and
# every insertion is wrapped in a "managed block" with markers so a later run
# (or --uninstall) can find and remove exactly what it added, leaving the
# rest of the file untouched.
set -euo pipefail

# ---------------------------------------------------------------- identity
SIDEBAR_PLUGIN_ID="jonasbove.herdrmon"  # Herdr plugin id (sidebar/herdr-plugin.toml), same as the Omarchy plugin id
BIN_NAME="herdrmon"
FONT_FAMILY="Herdrmon Icons"
FONT_FILE="HerdrmonIcons.otf"
MARK="herdrmon"                      # managed-block marker tag

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"

# ------------------------------------------------------------------- args
UNINSTALL=0
ASSUME_YES=0
PREFIX="${HOME}/.local/bin"

usage() {
  cat <<EOF
Usage: $(basename "$0") [--uninstall] [--yes] [--prefix DIR]

  --uninstall   Remove everything this script installed. Safe to re-run.
  --yes         Don't prompt for confirmation.
  --prefix DIR  Where to install the $BIN_NAME binary (default: $PREFIX).
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --uninstall) UNINSTALL=1; shift ;;
    --yes) ASSUME_YES=1; shift ;;
    --prefix) PREFIX="${2:?--prefix needs a directory}"; shift 2 ;;
    --prefix=*) PREFIX="${1#--prefix=}"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "install.sh: unknown argument: $1" >&2; usage; exit 1 ;;
  esac
done

# --------------------------------------------------------------- logging
info()  { printf '  %s\n' "$*"; }
step()  { printf '\n==> %s\n' "$*"; }
warn()  { printf 'warning: %s\n' "$*" >&2; }
die()   { printf 'error: %s\n' "$*" >&2; exit 1; }

confirm() {
  local prompt="$1"
  [[ "$ASSUME_YES" -eq 1 ]] && return 0
  local reply
  read -r -p "$prompt [y/N] " reply </dev/tty || return 1
  [[ "$reply" =~ ^[Yy]$ ]]
}

# ------------------------------------------------------------------- xdg
: "${XDG_CONFIG_HOME:=$HOME/.config}"
: "${XDG_CACHE_HOME:=$HOME/.cache}"
: "${XDG_DATA_HOME:=$HOME/.local/share}"
: "${XDG_STATE_HOME:=$HOME/.local/state}"

CONFIG_FILE="${HERDRMON_CONFIG:-$XDG_CONFIG_HOME/herdrmon/config.toml}"
ASSETS_DIR="${HERDRMON_ASSETS:-$XDG_CACHE_HOME/herdrmon/assets}"
FONT_PATH="${HERDRMON_FONT:-$XDG_DATA_HOME/fonts/$FONT_FILE}"
FRAMES_PATH="${HERDRMON_FRAMES:-$XDG_DATA_HOME/herdrmon/frames.json}"
STATE_DIR="$XDG_STATE_HOME/herdrmon"

# ------------------------------------------------------- generic helpers
backup_file() {
  local f="$1"
  [[ -e "$f" ]] || return 0
  cp -p -- "$f" "$f.bak.$(date +%s)"
}

# Remove a previously-inserted managed block (between the marker lines) from
# a file, if present. Leaves everything else byte-for-byte unchanged.
strip_managed_block() {
  local file="$1" begin="$2" end="$3"
  [[ -f "$file" ]] || return 0
  grep -qF -- "$begin" "$file" 2>/dev/null || return 0
  local tmp
  tmp="$(mktemp)"
  awk -v b="$begin" -v e="$end" '
    $0 == b { skip=1; next }
    $0 == e { skip=0; next }
    !skip { print }
  ' "$file" > "$tmp"
  mv -- "$tmp" "$file"
}

# Append a managed block at end of file (creating the file if needed). Any
# previous block (same markers) is removed first, so this is idempotent.
append_managed_block() {
  local file="$1" comment="$2"; shift 2
  local begin="$comment >>> $MARK >>>" end="$comment <<< $MARK <<<"
  mkdir -p -- "$(dirname -- "$file")"
  if [[ -f "$file" ]]; then
    backup_file "$file"
    strip_managed_block "$file" "$begin" "$end"
  else
    : > "$file"
  fi
  {
    echo "$begin"
    printf '%s\n' "$@"
    echo "$end"
  } >> "$file"
}

# Insert a managed block into a JSONC object just before its final closing
# "}" (the format used by omarchy-menu.jsonc: a flat `{ "id": {...}, ... }`
# with the brace alone on the last non-blank line).
insert_jsonc_block() {
  local file="$1"; shift
  local begin="// >>> $MARK >>>" end="// <<< $MARK <<<"
  if [[ ! -f "$file" ]]; then
    mkdir -p -- "$(dirname -- "$file")"
    printf '{\n}\n' > "$file"
  fi
  backup_file "$file"
  strip_managed_block "$file" "  $begin" "  $end"
  local brace_line
  brace_line="$(grep -n '^}[[:space:]]*$' "$file" | tail -n1 | cut -d: -f1)"
  [[ -n "$brace_line" ]] || die "$file: no top-level closing '}' line found; edit it by hand"
  local tmp
  tmp="$(mktemp)"
  {
    head -n "$((brace_line - 1))" "$file"
    echo "  $begin"
    printf '  %s\n' "$@"
    echo "  $end"
    tail -n "+$brace_line" "$file"
  } > "$tmp"
  mv -- "$tmp" "$file"
}

# ------------------------------------------------------------- dep checks
check_deps() {
  step "Checking dependencies"
  local ok=1

  if command -v go >/dev/null 2>&1; then
    info "go: $(go version)"
  elif command -v mise >/dev/null 2>&1; then
    info "go: not on PATH, will use 'mise exec go@1.27.0'"
  else
    warn "no 'go' and no 'mise' found — can't build the $BIN_NAME binary from source"
    ok=0
  fi

  if command -v uv >/dev/null 2>&1; then
    info "uv: $(uv --version)"
  else
    warn "no 'uv' found — can't fetch/build the icon font"
    ok=0
  fi

  if command -v node >/dev/null 2>&1; then
    local major
    major="$(node --version | sed -E 's/^v([0-9]+).*/\1/')"
    if [[ "$major" -ge 18 ]]; then
      info "node: $(node --version)"
    else
      warn "node $(node --version) is older than 18 — the sidebar plugin may not run"
    fi
  else
    warn "no 'node' found — the sidebar plugin needs it (herdr itself is still optional)"
  fi

  if command -v herdr >/dev/null 2>&1; then
    info "herdr: found, the sidebar plugin will be linked in"
  else
    info "herdr: not found — skipping the sidebar plugin (herdrmon still works standalone)"
  fi

  [[ "$ok" -eq 1 ]] || die "missing required dependencies, aborting"
}

# ---------------------------------------------------------------- binary
install_binary() {
  step "Building $BIN_NAME"
  mkdir -p -- "$PREFIX"
  local out="$PREFIX/$BIN_NAME"
  (
    cd -- "$REPO_ROOT"
    if command -v go >/dev/null 2>&1; then
      go build -o "$out" .
    else
      mise exec go@1.27.0 -- go build -o "$out" .
    fi
  )
  info "installed $out"
  case ":$PATH:" in
    *":$PREFIX:"*) ;;
    *) warn "$PREFIX is not on PATH — add it to your shell profile" ;;
  esac
}

uninstall_binary() {
  local out="$PREFIX/$BIN_NAME"
  if [[ -e "$out" ]]; then
    rm -f -- "$out"
    info "removed $out"
  fi
}

# ---------------------------------------------------------------- config
install_config() {
  step "Config"
  if [[ -f "$CONFIG_FILE" ]]; then
    info "$CONFIG_FILE already exists, leaving it"
    return
  fi
  mkdir -p -- "$(dirname -- "$CONFIG_FILE")"
  cat > "$CONFIG_FILE" <<'EOF'
# herdrmon config. See README.md for the full reference.

# Which device am I? Default: hostname, lowercased, without the domain.
# self = "onix"

[font]
extra = []
pret_emerald = "https://raw.githubusercontent.com/pret/pokeemerald/master"
pret_firered = "https://raw.githubusercontent.com/pret/pokefirered/master"

[discovery]
tailscale = true
ssh_config = false
herdr = true
EOF
  info "wrote $CONFIG_FILE"
}

# ---------------------------------------------------------------- font
install_font() {
  step "Icon font"
  mkdir -p -- "$ASSETS_DIR" "$(dirname -- "$FONT_PATH")" "$(dirname -- "$FRAMES_PATH")"
  (
    cd -- "$REPO_ROOT/font"
    # build.py fetches whatever art is missing first, so this is the only
    # call needed.
    uv run build.py \
      --config "$CONFIG_FILE" \
      --assets "$ASSETS_DIR" \
      --out-font "$FONT_PATH" \
      --out-frames "$FRAMES_PATH"
  )
  info "built $FONT_PATH"
  info "built $FRAMES_PATH"
  if command -v fc-cache >/dev/null 2>&1; then
    fc-cache -f "$(dirname -- "$FONT_PATH")" >/dev/null
    info "refreshed the fontconfig cache"
  fi
}

uninstall_font() {
  rm -f -- "$FONT_PATH"
  rm -f -- "$FRAMES_PATH"
  rm -rf -- "$ASSETS_DIR"
}

# ---------------------------------------- terminal fallback font wiring
# The codepoints herdrmon actually uses depend on which mons got baked in,
# so they're read back out of frames.json rather than hard-coded here. Only
# private-use codepoints (>= U+E000) are considered, so this can't pick up
# stray ordinary text if frames.json is ever malformed.
codepoint_ranges() {
  [[ -f "$FRAMES_PATH" ]] || return 0
  command -v python3 >/dev/null 2>&1 || return 0
  python3 - "$FRAMES_PATH" <<'PY'
import json, sys

def walk(x):
    if isinstance(x, str):
        for ch in x:
            yield ord(ch)
    elif isinstance(x, list):
        for i in x:
            yield from walk(i)
    elif isinstance(x, dict):
        for v in x.values():
            yield from walk(v)

try:
    with open(sys.argv[1], encoding="utf-8") as fh:
        data = json.load(fh)
except Exception:
    sys.exit(0)

cps = sorted({c for c in walk(data) if c >= 0xE000})
ranges, start, prev = [], None, None
for c in cps:
    if start is None:
        start = prev = c
    elif c == prev + 1:
        prev = c
    else:
        ranges.append((start, prev))
        start = prev = c
if start is not None:
    ranges.append((start, prev))

for a, b in ranges:
    print(f"U+{a:04X}-U+{b:04X}" if a != b else f"U+{a:04X}")
PY
}

# foot: one `font=` line in [main], a comma-separated fallback chain. There's
# no per-line comment syntax that survives inline, so this edits the value in
# place (idempotent via the "already contains FONT_FAMILY" check) instead of
# using a marker block.
patch_foot() {
  local f="$XDG_CONFIG_HOME/foot/foot.ini"
  [[ -f "$f" ]] || return 0
  grep -q -- "^\[main\]" "$f" || return 0
  if grep -q -- "font=.*$FONT_FAMILY" "$f"; then
    info "foot: already has $FONT_FAMILY"
    return
  fi
  backup_file "$f"
  local size
  size="$(sed -n 's/^font=.*:size=\([0-9]\+\).*/\1/p' "$f" | head -n1)"
  size="${size:-9}"
  local tmp
  tmp="$(mktemp)"
  awk -v fam="$FONT_FAMILY" -v size="$size" '
    /^font=/ && !done { print $0 "," fam ":size=" size; done=1; next }
    { print }
  ' "$f" > "$tmp"
  mv -- "$tmp" "$f"
  info "foot: added $FONT_FAMILY to foot.ini font= fallback chain"
}

unpatch_foot() {
  local f="$XDG_CONFIG_HOME/foot/foot.ini"
  [[ -f "$f" ]] || return 0
  grep -q -- "$FONT_FAMILY" "$f" || return 0
  backup_file "$f"
  local tmp
  tmp="$(mktemp)"
  sed -E "s/,${FONT_FAMILY}:size=[0-9]+//" "$f" > "$tmp"
  mv -- "$tmp" "$f"
  info "foot: removed $FONT_FAMILY from foot.ini"
}

# kitty: symbol_map directives, one per codepoint range.
patch_kitty() {
  local f="$XDG_CONFIG_HOME/kitty/kitty.conf"
  [[ -f "$f" ]] || return 0
  local ranges=() r
  while IFS= read -r r; do [[ -n "$r" ]] && ranges+=("symbol_map $r $FONT_FAMILY"); done < <(codepoint_ranges)
  if [[ "${#ranges[@]}" -eq 0 ]]; then
    warn "kitty: no frames.json yet, skipping symbol_map (re-run install.sh after the font builds)"
    return
  fi
  append_managed_block "$f" "#" "${ranges[@]}"
  info "kitty: wrote ${#ranges[@]} symbol_map line(s)"
}

# ghostty: font-codepoint-map directives, one per codepoint range.
patch_ghostty() {
  local f="$XDG_CONFIG_HOME/ghostty/config"
  [[ -f "$f" ]] || return 0
  local lines=() r
  while IFS= read -r r; do [[ -n "$r" ]] && lines+=("font-codepoint-map = $r=$FONT_FAMILY"); done < <(codepoint_ranges)
  if [[ "${#lines[@]}" -eq 0 ]]; then
    warn "ghostty: no frames.json yet, skipping font-codepoint-map (re-run install.sh after the font builds)"
    return
  fi
  append_managed_block "$f" "#" "${lines[@]}"
  info "ghostty: wrote ${#lines[@]} font-codepoint-map line(s)"
}

# alacritty: no explicit fallback-font config at all — it falls through to
# fontconfig for glyphs missing in the primary font, so installing and
# fc-cache'ing the font is enough. Deliberately not touching alacritty.toml.
note_alacritty() {
  [[ -f "$XDG_CONFIG_HOME/alacritty/alacritty.toml" ]] || return 0
  info "alacritty: no config change needed, it uses fontconfig fallback"
}

terminal_fallbacks() {
  step "Terminal font fallback"
  patch_foot
  patch_kitty
  patch_ghostty
  note_alacritty
}

undo_terminal_fallbacks() {
  unpatch_foot
  strip_managed_block "$XDG_CONFIG_HOME/kitty/kitty.conf" "# >>> $MARK >>>" "# <<< $MARK <<<"
  strip_managed_block "$XDG_CONFIG_HOME/ghostty/config" "# >>> $MARK >>>" "# <<< $MARK <<<"
}

# --------------------------------------------------------------- sidebar
# sidebar/ is a pure-Node Herdr plugin with no npm dependencies and no build
# step (see sidebar/README.md), so linking it is all the setup it needs.
link_sidebar() {
  step "Herdr sidebar plugin"
  if ! command -v herdr >/dev/null 2>&1; then
    info "herdr not found, skipping"
    return
  fi
  if [[ ! -d "$REPO_ROOT/sidebar" ]]; then
    warn "sidebar/ not present in this checkout, skipping"
    return
  fi
  if herdr plugin list 2>/dev/null | grep -qF -- "- $SIDEBAR_PLUGIN_ID "; then
    info "already linked"
  elif herdr plugin link "$REPO_ROOT/sidebar" >/dev/null; then
    info "linked $REPO_ROOT/sidebar into herdr"
  else
    warn "'herdr plugin link' failed, skipping the rest of the sidebar setup"
    return
  fi
  # configure writes/updates the managed sidebar block in Herdr's config.toml
  # and reloads; safe and idempotent to re-run (e.g. after a theme change).
  if herdr plugin action invoke "$SIDEBAR_PLUGIN_ID.configure"; then
    info "configured the sidebar layout"
  else
    warn "sidebar 'configure' action failed — run it by hand: herdr plugin action invoke $SIDEBAR_PLUGIN_ID.configure"
  fi
  # The daemon also auto-starts via a startup hook on the next Herdr server
  # start; start it now too so it's running immediately on this install.
  herdr plugin action invoke "$SIDEBAR_PLUGIN_ID.start" \
    || warn "sidebar 'start' action failed — run it by hand: herdr plugin action invoke $SIDEBAR_PLUGIN_ID.start"
}

unlink_sidebar() {
  command -v herdr >/dev/null 2>&1 || return 0
  herdr plugin list 2>/dev/null | grep -qF -- "- $SIDEBAR_PLUGIN_ID " || return 0
  # Both actions need a running herdr server (unlike `link`/`list`), so they
  # commonly fail on a machine where herdr isn't running right now — report
  # that honestly instead of claiming success either way.
  herdr plugin action invoke "$SIDEBAR_PLUGIN_ID.unconfigure" \
    || warn "sidebar 'unconfigure' action failed (herdr not running?) — its managed config.toml block may be left behind"
  if herdr plugin unlink "$SIDEBAR_PLUGIN_ID"; then
    info "unlinked the sidebar plugin from herdr"
  else
    warn "'herdr plugin unlink' failed (herdr not running?) — run it by hand: herdr plugin unlink $SIDEBAR_PLUGIN_ID"
  fi
}

# -------------------------------------------------------------- om. menu
install_menu_entry() {
  step "Omarchy menu entry"
  local f="$XDG_CONFIG_HOME/omarchy/extensions/omarchy-menu.jsonc"
  local action="omarchy-launch-or-focus-tui herdrmon"
  insert_jsonc_block "$f" \
    "\"herdrmon\": {\"icon\":\"\\udb81\\udc1d\",\"label\":\"Herdrmon\",\"aliases\":[\"party\"],\"action\":\"$action\"},"
  info "added an entry to $f"
}

uninstall_menu_entry() {
  local f="$XDG_CONFIG_HOME/omarchy/extensions/omarchy-menu.jsonc"
  strip_managed_block "$f" "  // >>> $MARK >>>" "  // <<< $MARK <<<"
}

# --------------------------------------------------------------- keybind
# This machine's Hyprland (0.56) uses a Lua config; Omarchy 3.x ships a plain
# bindings.conf. Detect which is actually live: hyprland.lua requiring
# hypr.bindings (-> bindings.lua) wins if both exist, since that's what a Lua
# install actually sources.
install_keybind() {
  step "Hyprland keybind (SUPER ALT, P)"
  local hypr="$XDG_CONFIG_HOME/hypr"
  local lua="$hypr/bindings.lua"
  local conf="$hypr/bindings.conf"
  local cmd="omarchy-launch-or-focus-tui herdrmon"

  if [[ -f "$hypr/hyprland.lua" && -f "$lua" ]]; then
    if grep -q -- "$MARK" "$lua" 2>/dev/null; then
      info "already added to $lua"
      return
    fi
    backup_file "$lua"
    {
      echo ""
      echo "-- >>> $MARK >>>"
      echo "o.bind(\"SUPER + ALT + P\", \"Herdrmon\", \"$cmd\")"
      echo "-- <<< $MARK <<<"
    } >> "$lua"
    info "added to $lua"
  elif [[ -f "$conf" ]]; then
    if grep -q -- "$MARK" "$conf" 2>/dev/null; then
      info "already added to $conf"
      return
    fi
    backup_file "$conf"
    {
      echo ""
      echo "# >>> $MARK >>>"
      echo "bindd = SUPER ALT, P, Herdrmon, exec, $cmd"
      echo "# <<< $MARK <<<"
    } >> "$conf"
    info "added to $conf"
  else
    cat <<EOF
Could not find a plain $lua or $conf to edit. Add this keybind by hand:

  Lua config:   o.bind("SUPER + ALT + P", "Herdrmon", "$cmd")
  bindings.conf: bindd = SUPER ALT, P, Herdrmon, exec, $cmd
EOF
  fi
}

uninstall_keybind() {
  local hypr="$XDG_CONFIG_HOME/hypr"
  strip_managed_block "$hypr/bindings.lua" "-- >>> $MARK >>>" "-- <<< $MARK <<<"
  strip_managed_block "$hypr/bindings.conf" "# >>> $MARK >>>" "# <<< $MARK <<<"
}

# ------------------------------------------------------------------ main
do_install() {
  check_deps
  install_binary
  install_config
  install_font
  terminal_fallbacks
  link_sidebar
  install_menu_entry
  install_keybind
  step "Done"
  info "run: $BIN_NAME"
  info "or press SUPER ALT, P (reload Hyprland config / Omarchy shell to pick it up)"
}

do_uninstall() {
  confirm "Remove herdrmon (binary, font, config stays, menu entry, keybind)?" || { echo "aborted"; exit 1; }
  uninstall_binary
  uninstall_font
  undo_terminal_fallbacks
  unlink_sidebar
  uninstall_menu_entry
  uninstall_keybind
  step "Done"
  info "$CONFIG_FILE was left in place; remove it by hand if you want it gone too"
}

mkdir -p -- "$STATE_DIR"
if [[ "$UNINSTALL" -eq 1 ]]; then
  do_uninstall
else
  do_install
fi
