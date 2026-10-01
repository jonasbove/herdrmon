'use strict';

// Where things live. Every path follows XDG and can be overridden by env,
// per DESIGN.md's path table.

const os = require('node:os');
const path = require('node:path');
const fs = require('node:fs');

const home = () => os.homedir();
const xdg = (name, fallback) => process.env[name] || path.join(home(), fallback);

// herdrmon's own config.toml (shared with the party TUI and the font build).
function configPath() {
  return process.env.HERDRMON_CONFIG || path.join(xdg('XDG_CONFIG_HOME', '.config'), 'herdrmon', 'config.toml');
}

// The frames manifest written by the font build.
function framesPath() {
  return process.env.HERDRMON_FRAMES || path.join(xdg('XDG_DATA_HOME', '.local/share'), 'herdrmon', 'frames.json');
}

// Durable state: the one Herdr gives the plugin, else herdrmon's XDG state dir.
function stateDir() {
  return (
    process.env.HERDRMON_STATE ||
    process.env.HERDR_PLUGIN_STATE_DIR ||
    path.join(xdg('XDG_STATE_HOME', '.local/state'), 'herdrmon', 'sidebar')
  );
}

// Herdr's own config.toml. HERDR_CONFIG_PATH is Herdr's override.
function herdrConfigPath() {
  return process.env.HERDR_CONFIG_PATH || path.join(xdg('XDG_CONFIG_HOME', '.config'), 'herdr', 'config.toml');
}

// The socket of the Herdr server to talk to. Herdr injects HERDR_SOCKET_PATH
// into plugin commands and panes; a bare shell gets the default session's.
function herdrSocket() {
  return process.env.HERDR_SOCKET_PATH || path.join(xdg('XDG_CONFIG_HOME', '.config'), 'herdr', 'herdr.sock');
}

// 32-bit FNV-1a, hex. Used for the lock name and (in species.js) default mons.
function fnv1a(text) {
  let h = 0x811c9dc5;
  for (const byte of Buffer.from(String(text), 'utf8')) {
    h ^= byte;
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h >>> 0;
}

// The daemon's lock and control socket: one per Herdr server, so two named
// sessions each get their own daemon. Kept short (sun_path is ~108 bytes),
// hence the runtime dir and a hash rather than the state dir.
function controlSocket(sock = herdrSocket()) {
  const base = process.env.XDG_RUNTIME_DIR || os.tmpdir();
  return path.join(base, 'herdrmon', `sidebar-${fnv1a(sock).toString(16)}.sock`);
}

function ensureDir(dir) {
  try {
    fs.mkdirSync(dir, { recursive: true });
  } catch {
    // Callers degrade gracefully.
  }
  return dir;
}

module.exports = { configPath, framesPath, stateDir, herdrConfigPath, herdrSocket, controlSocket, ensureDir, fnv1a };
