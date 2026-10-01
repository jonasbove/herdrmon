'use strict';

// herdrmon's config.toml, as the sidebar needs it: which device this is,
// which Pokémon it is, the agent table and the [sidebar] knobs.
//
//   self = "onix"                       # default: hostname, lowercased, no domain
//   [[device]] name = "onix"  mon = "onix"
//   [agents.myagent]                    # add or override an agents.json entry
//   label = "My Agent"  glyph = "◎"  colour = "teal"
//   match = { agent = ["myagent"], process = ["myagent"], title = "^my" }
//   [sidebar]
//   night_command = "test -e ~/.night"  # exit 0 = night; "" = clock only
//   night_start = 22  night_end = 7      # hours for the clock fallback
//   glyphs = "nerd"                     # or "text" for plain Unicode marks
//   fallback_glyph = "◓"                # $mon when frames/font are missing
//   lead_ms = 0                         # shift this machine's frames in time

const fs = require('node:fs');
const os = require('node:os');

const toml = require('./toml');
const paths = require('./paths');
const species = require('./species');
const AGENTS = require('../agents.json');

const SIDEBAR_DEFAULTS = {
  night_command: null, // null = auto (hyprsunset on Hyprland, else clock)
  night_below_k: 6000,
  night_start: 22,
  night_end: 7,
  glyphs: 'nerd',
  fallback_glyph: '◓',
  lead_ms: 0,
};

function hostname() {
  return os.hostname().toLowerCase().split('.')[0];
}

function readRaw(file = paths.configPath()) {
  let text;
  try {
    text = fs.readFileSync(file, 'utf8');
  } catch {
    return { raw: {}, error: null, missing: true };
  }
  try {
    return { raw: toml.parse(text), error: null, missing: false };
  } catch (error) {
    return { raw: {}, error, missing: false };
  }
}

const asList = (v) => (v === undefined || v === null ? [] : Array.isArray(v) ? v.map(String) : [String(v)]);

// agents.json merged with config.toml [agents.<id>]. An id that names a
// built-in agent or program is merged into it; a new id becomes an agent.
function agentTable(raw = {}) {
  const table = {
    agents: Object.fromEntries(Object.entries(AGENTS.agents).map(([k, v]) => [k, { ...v, match: { ...v.match } }])),
    programs: Object.fromEntries(Object.entries(AGENTS.programs).map(([k, v]) => [k, { ...v, match: { ...v.match } }])),
  };
  for (const [id, user] of Object.entries(raw.agents ?? {})) {
    if (!user || typeof user !== 'object') continue;
    const group = id in table.programs ? table.programs : table.agents;
    const base = group[id] ?? { label: id, glyph: '•', colour: 'text', match: {} };
    const match = { ...base.match };
    const um = { ...(user.match ?? {}) };
    for (const k of ['agent', 'process', 'title']) if (user[k] !== undefined) um[k] = user[k];
    if (um.agent !== undefined) match.agent = asList(um.agent);
    if (um.process !== undefined) match.process = asList(um.process);
    if (um.title !== undefined) match.title = String(um.title);
    if (!Object.keys(match).length) match.agent = [id];
    const { match: _m, agent: _a, process: _p, title: _t, ...rest } = user;
    group[id] = { ...base, ...rest, match };
  }
  return table;
}

function resolve(raw = {}) {
  const self = species.slugify(raw.self || hostname()) || 'localhost';
  const devices = Array.isArray(raw.device) ? raw.device : [];
  const me = devices.find((d) => d && species.slugify(d.name) === self);
  const base = me?.mon ? species.slugify(me.mon) : species.defaultMon(self);
  // frames.json keys carry the form: "deoxys-attack", "unown-b".
  const form = species.slugify(me?.form || '');
  const mon = form && !base.includes('-') ? `${base}-${form}` : base;
  return {
    self,
    mon,
    form: me?.form || '',
    agents: agentTable(raw),
    sidebar: { ...SIDEBAR_DEFAULTS, ...(raw.sidebar ?? {}) },
  };
}

function load(file) {
  const { raw, error, missing } = readRaw(file);
  return { ...resolve(raw), error, missing };
}

module.exports = { load, resolve, readRaw, agentTable, hostname, SIDEBAR_DEFAULTS };
