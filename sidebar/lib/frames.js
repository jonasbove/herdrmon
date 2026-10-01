'use strict';

// frames.json (DESIGN.md "frames.json"): species slug -> animation -> frames,
// each frame a string of icon-font codepoints. A font rebuild renumbers the
// codepoints, so the file is re-read whenever its mtime changes; a file that
// fails to parse (mid-write) keeps the last good copy.

const fs = require('node:fs');
const { spawnSync } = require('node:child_process');

const CHECK_MS = 2000;

// Is a font family visible to fontconfig? Without fc-list (macOS, Windows)
// there's no way to ask, so assume yes.
function fontInstalled(family) {
  if (!family) return true;
  try {
    const r = spawnSync('fc-list', [':', 'family'], { encoding: 'utf8', timeout: 5000 });
    if (r.error) return true;
    return String(r.stdout)
      .split('\n')
      .some((line) => line.split(',').some((f) => f.trim() === family));
  } catch {
    return true;
  }
}

class Frames {
  constructor(file, { checkFont = fontInstalled } = {}) {
    this.file = file;
    this.checkFont = checkFont;
    this.data = null;
    this.mtime = 0;
    this.checked = 0;
    this.fontOk = false;
  }

  load() {
    try {
      const mtime = fs.statSync(this.file).mtimeMs;
      const data = JSON.parse(fs.readFileSync(this.file, 'utf8'));
      if (!data || typeof data.mons !== 'object') return false;
      this.data = data;
      this.mtime = mtime;
      this.fontOk = this.checkFont(data.font);
      return true;
    } catch {
      return false;
    }
  }

  // Cheap enough for every tick: stats the file at most every CHECK_MS.
  refresh(now = Date.now()) {
    if (now - this.checked < CHECK_MS) return false;
    this.checked = now;
    try {
      if (fs.statSync(this.file).mtimeMs !== this.mtime) return this.load();
    } catch {
      // File gone: keep what we have.
    }
    return false;
  }

  // The animations for a species slug. Older manifests key some species with
  // hyphens ("ho-oh"); accept both spellings.
  anim(mon) {
    const mons = this.data?.mons;
    if (!mons || !mon || !this.fontOk) return null;
    const a = mons[mon] ?? mons[mon.replace(/_/g, '-')] ?? mons[mon.replace(/-/g, '_')];
    return a && Array.isArray(a.bob ?? a.base) ? a : null;
  }
}

module.exports = { Frames, fontInstalled };
