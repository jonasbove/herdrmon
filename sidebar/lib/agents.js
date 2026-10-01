'use strict';

// Table-driven agent and program matching (agents.json + config overrides).
// No vendor gets a code branch; Neovim's extra detail lives in procs.js.

const titleCache = new Map();
function titleRe(src) {
  if (!titleCache.has(src)) {
    let re = null;
    try {
      re = new RegExp(src, 'i');
    } catch {
      // A bad user regex matches nothing.
    }
    titleCache.set(src, re);
  }
  return titleCache.get(src);
}

// "-zsh" -> "zsh", "/usr/bin/python3.12" -> "python3.12"
function procName(name) {
  return String(name ?? '')
    .replace(/^-/, '')
    .split('/')
    .pop()
    .toLowerCase();
}

function processMatches(list, name) {
  if (!name || !list?.length) return false;
  if (list.includes(name)) return true;
  // python3.12 -> python3 -> python
  const bare = name.replace(/[\d.]+$/, '');
  return bare !== name && list.includes(bare);
}

// -> { id, kind: 'agent'|'program', entry } or null.
// pane: { agent, title, terminal_title }, proc: foreground process name.
function classify(table, pane = {}, proc = '') {
  const groups = [
    ['agent', table.agents],
    ['program', table.programs],
  ];
  const agent = pane.agent ? String(pane.agent).toLowerCase() : '';
  if (agent) {
    for (const [kind, group] of groups) {
      for (const [id, entry] of Object.entries(group)) {
        if (entry.match?.agent?.includes(agent)) return { id, kind, entry };
      }
    }
  }
  const name = procName(proc);
  for (const [kind, group] of groups) {
    for (const [id, entry] of Object.entries(group)) {
      if (processMatches(entry.match?.process, name)) return { id, kind, entry };
    }
  }
  const titles = [pane.title, pane.terminal_title].filter(Boolean);
  for (const [kind, group] of groups) {
    for (const [id, entry] of Object.entries(group)) {
      const re = entry.match?.title ? titleRe(entry.match.title) : null;
      if (re && titles.some((t) => re.test(t))) return { id, kind, entry };
    }
  }
  // Herdr detected an agent we have no row for: show it as Herdr names it.
  if (agent) return { id: agent, kind: 'agent', entry: { label: pane.agent, glyph: '•', colour: 'text' } };
  return null;
}

// What to draw for an entry under the configured glyph mode.
function look(entry, glyphs = 'nerd') {
  const glyph = glyphs === 'text' ? (entry.text ?? entry.glyph) : entry.glyph;
  return { glyph: glyph ?? '•', label: entry.label ?? '', colour: entry.colour ?? 'text' };
}

module.exports = { classify, look, procName };
