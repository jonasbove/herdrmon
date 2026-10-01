'use strict';

// Is it night? (The Pokémon sleeps unless an agent is working.)
//
//   [sidebar] night_command = "..."  run with sh -c; exit 0 = night, else day
//   night_command = ""               clock only
//   unset (auto)                     Hyprland with hyprsunset on PATH: night
//                                    while `hyprctl hyprsunset temperature`
//                                    is below night_below_k (Omarchy's night
//                                    light test); otherwise the clock
//   clock                            night_start:00 to night_end:00 local
//
// Checked at most every 30 s, off the frame path.

const fs = require('node:fs');
const path = require('node:path');
const { execFile } = require('node:child_process');

const CHECK_MS = 30000;

function onPath(bin) {
  return (process.env.PATH ?? '')
    .split(path.delimiter)
    .some((dir) => dir && fs.existsSync(path.join(dir, bin)));
}

function byClock(date, start = 22, end = 7) {
  const h = date.getHours();
  return start > end ? h >= start || h < end : h >= start && h < end;
}

function hyprlandRunning() {
  if (process.env.HYPRLAND_INSTANCE_SIGNATURE) return true;
  try {
    const dir = `${process.env.XDG_RUNTIME_DIR || `/run/user/${process.getuid()}`}/hypr`;
    return fs.readdirSync(dir).length > 0;
  } catch {
    return false;
  }
}

// -> { command: string|null, mode: 'command'|'hyprsunset'|'clock' }
function plan(sidebar) {
  const cmd = sidebar.night_command;
  if (typeof cmd === 'string') return cmd.trim() ? { mode: 'command', command: cmd } : { mode: 'clock' };
  if (onPath('hyprctl') && onPath('hyprsunset') && hyprlandRunning()) {
    // The daemon may have been started without Hyprland's env: find the
    // newest instance the way omarchy's scripts do.
    return {
      mode: 'hyprsunset',
      command:
        'HYPRLAND_INSTANCE_SIGNATURE=${HYPRLAND_INSTANCE_SIGNATURE:-$(ls -t "${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/hypr" | head -1)} hyprctl hyprsunset temperature',
    };
  }
  return { mode: 'clock' };
}

class Night {
  constructor(sidebar) {
    this.sidebar = sidebar;
    this.plan = plan(sidebar);
    this.on = false;
    this.checked = 0;
    this.pending = false;
  }

  check(now = Date.now()) {
    if (this.pending || now - this.checked < CHECK_MS) return this.on;
    this.checked = now;
    const { night_start: start, night_end: end, night_below_k: below } = this.sidebar;
    if (this.plan.mode === 'clock') {
      this.on = byClock(new Date(now), start, end);
      return this.on;
    }
    this.pending = true;
    execFile('sh', ['-c', this.plan.command], { timeout: 8000 }, (err, out) => {
      this.pending = false;
      if (this.plan.mode === 'command') {
        this.on = !err;
        return;
      }
      // hyprsunset not running answers with an error: that's daytime.
      const k = err ? NaN : parseInt(String(out).match(/\d+/)?.[0] ?? '', 10);
      this.on = Number.isFinite(k) && k < below;
    });
    return this.on;
  }
}

module.exports = { Night, byClock, plan };
