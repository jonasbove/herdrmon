'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const config = require('../lib/config');
const species = require('../lib/species');
const toml = require('../lib/toml');

test('slugify', () => {
  assert.equal(species.slugify('Ho-Oh'), 'ho_oh');
  assert.equal(species.slugify('Mr. Mime'), 'mr_mime');
  assert.equal(species.slugify('  ONIX.local '), 'onix_local');
});

test('species list is the 386 Gen 3 species', () => {
  assert.equal(species.SPECIES.length, 386);
  for (const s of ['bulbasaur', 'ho_oh', 'mr_mime', 'nidoran_f', 'deoxys', 'unown', 'farfetchd']) assert.ok(species.isSpecies(s), s);
  for (const m of species.MACHINE_MONS) assert.ok(species.isSpecies(m), m);
});

test('default mon: species name, else stable hash over machine mons', () => {
  assert.equal(species.defaultMon('Onix'), 'onix');
  assert.equal(species.defaultMon('ho-oh'), 'ho_oh');
  const a = species.defaultMon('workstation');
  assert.ok(species.MACHINE_MONS.includes(a));
  assert.equal(species.defaultMon('workstation'), a);
  assert.equal(species.defaultMon('WorkStation'), a);
  // fnv1a32("workstation") pinned so other implementations can check theirs
  const { fnv1a } = require('../lib/paths');
  assert.equal(fnv1a('workstation') % species.MACHINE_MONS.length, species.MACHINE_MONS.indexOf(a));
});

test('self and mon from config', () => {
  const c = config.resolve(toml.parse('self = "geodude"\n[[device]]\nname = "geodude"\nmon = "Tangela"\n'));
  assert.equal(c.self, 'geodude');
  assert.equal(c.mon, 'tangela');
  const d = config.resolve(toml.parse('self = "ho-oh"\n'));
  assert.equal(d.mon, 'ho_oh');
  const e = config.resolve({});
  assert.equal(e.self, species.slugify(config.hostname()));
});

test('sidebar defaults and overrides', () => {
  const c = config.resolve(toml.parse('[sidebar]\nnight_command = ""\nglyphs = "text"\n'));
  assert.equal(c.sidebar.night_command, '');
  assert.equal(c.sidebar.glyphs, 'text');
  assert.equal(c.sidebar.night_start, 22);
});

test('[agents.<id>] extends and overrides the table', () => {
  const raw = toml.parse(`
[agents.claude]
colour = "mauve"
[agents.myagent]
label = "Mine"
glyph = "◎"
process = "my-agent"
[agents.neovim]
label = "nv"
`);
  const t = config.agentTable(raw);
  assert.equal(t.agents.claude.colour, 'mauve');
  assert.equal(t.agents.claude.label, 'Claude Code');
  assert.deepEqual(t.agents.claude.match.agent, ['claude']);
  assert.deepEqual(t.agents.myagent.match.process, ['my-agent']);
  assert.equal(t.programs.neovim.label, 'nv');
  // the shipped table is not mutated
  assert.equal(config.agentTable({}).agents.claude.colour, 'peach');
});
