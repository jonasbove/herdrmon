'use strict';

// The sidebar snippet and the managed block in Herdr's config.toml.
//
// The block sits between `# >>> herdrmon sidebar block` and
// `# <<< herdrmon sidebar block`. Nothing outside the markers is ever
// changed. TOML forbids defining a table twice, so a [ui.sidebar.agents] or
// [ui.sidebar.spaces] that already exists elsewhere (herdr-radar's managed
// block, or the user's own) is left alone: the block only carries the tables
// nobody else defines, and `configure` prints the token entries to add to the
// existing ones by hand.

const toml = require('./toml');
const palette = require('./palette');

const BEGIN = '# >>> herdrmon sidebar block';
const END = '# <<< herdrmon sidebar block';

const q = (s) =>
  `"${String(s).replace(/[\\"]/g, (c) => `\\${c}`).replace(/[\u0000-\u001f\u007f​]/g, (c) => `\\u${c.codePointAt(0).toString(16).toUpperCase().padStart(4, '0')}`)}"`;

function inlineTable(obj) {
  const parts = Object.entries(obj).map(([k, v]) => {
    if (Array.isArray(v)) return `${k} = [${v.map((x) => (typeof x === 'object' ? inlineTable(x) : q(x))).join(', ')}]`;
    if (typeof v === 'boolean' || typeof v === 'number') return `${k} = ${v}`;
    return `${k} = ${q(v)}`;
  });
  return `{ ${parts.join(', ')} }`;
}

// A coloured token: default fg plus the palette rules.
function token(name, fg, resolved) {
  const t = { token: `$${name}` };
  if (resolved[fg]) t.fg = resolved[fg];
  t.rules = palette.rules(resolved);
  return t;
}

function entry(e) {
  return typeof e === 'string' ? q(e) : inlineTable(e);
}

function rowsToml(rows) {
  return `rows = [\n${rows.map((r) => `  [${r.map(entry).join(', ')}],`).join('\n')}\n]`;
}

// The layouts, for a resolved palette (slot -> hex).
function layouts(resolved) {
  const mon = token('mon', 'overlay1', resolved);
  const agent = token('agent', 'text', resolved);
  const tools = token('tools', 'subtext0', resolved);
  return {
    agents: [[mon], ['state_icon', 'machine', 'workspace', 'tab'], [agent, tools]],
    spaces: [['state_icon', mon, 'workspace'], ['branch', 'git_status'], [tools]],
  };
}

// Split a config into [before, block, after]; block is null when absent.
function split(text) {
  const lines = String(text).split('\n');
  const b = lines.findIndex((l) => l.trim() === BEGIN);
  const e = b < 0 ? -1 : lines.findIndex((l, i) => i > b && l.trim() === END);
  if (b < 0 || e < 0) return [String(text), null, ''];
  return [lines.slice(0, b).join('\n'), lines.slice(b, e + 1).join('\n'), lines.slice(e + 1).join('\n')];
}

function withoutBlock(text) {
  const [before, block, after] = split(text);
  if (block === null) return before;
  const joined = `${before.replace(/\n+$/, '')}\n${after.replace(/^\n+/, '')}`;
  return joined.replace(/^\n+/, '').replace(/\n*$/, '\n');
}

// Which sidebar tables are defined outside our block.
function taken(text) {
  const rest = withoutBlock(text);
  try {
    const sb = toml.parse(rest).ui?.sidebar ?? {};
    return { agents: 'agents' in sb, spaces: 'spaces' in sb };
  } catch {
    const has = (name) => new RegExp(`^\\s*\\[\\s*ui\\.sidebar\\.${name}\\s*[\\].]`, 'm').test(rest);
    return { agents: has('agents'), spaces: has('spaces') };
  }
}

// -> { block: string|null, skipped: ['agents'|'spaces'], snippet: string }
function build(text, resolved, themeName) {
  const l = layouts(resolved);
  const busy = taken(text);
  const tables = [];
  const skipped = [];
  for (const name of ['agents', 'spaces']) {
    if (busy[name]) skipped.push(name);
    else tables.push(`[ui.sidebar.${name}]\n${rowsToml(l[name])}`);
  }
  const head = `${BEGIN}\n# Written by the herdrmon sidebar plugin (configure). Colours follow the\n# Herdr theme "${themeName}"; re-run configure after changing theme.\n`;
  const block = tables.length ? `${head}${tables.join('\n\n')}\n${END}` : null;
  const snippet = ['agents', 'spaces'].map((n) => `[ui.sidebar.${n}]\n${rowsToml(l[n])}`).join('\n\n');
  return { block, skipped, snippet, entries: { mon: entry(token('mon', 'overlay1', resolved)), agent: entry(token('agent', 'text', resolved)), tools: entry(token('tools', 'subtext0', resolved)) } };
}

// The new config text with our block replaced, added or (block null) removed.
function apply(text, block) {
  const [before, existing, after] = split(text);
  if (existing === null) {
    if (block === null) return text;
    return before.trim() ? `${before.replace(/\n*$/, '\n')}\n${block}\n` : `${block}\n`;
  }
  if (block === null) return withoutBlock(text);
  return `${before}${before ? '\n' : ''}${block}\n${after.replace(/^\n/, '')}`;
}

module.exports = { BEGIN, END, layouts, build, apply, split, taken, withoutBlock, rowsToml };
