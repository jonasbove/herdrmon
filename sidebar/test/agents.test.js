'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const agents = require('../lib/agents');
const config = require('../lib/config');
const AGENTS = require('../agents.json');
const { SLOTS } = require('../lib/palette');

const table = config.agentTable({});

test('ships every agent DESIGN.md lists', () => {
  const need = ['claude', 'codex', 'opencode', 'gemini', 'hermes', 'aider', 'goose', 'amp', 'cursor-agent', 'crush', 'qwen', 'copilot', 'pi', 'kiro', 'droid', 'cline'];
  for (const n of need) {
    const hit = agents.classify(table, {}, n);
    assert.ok(hit && hit.kind === 'agent', n);
  }
  for (const n of ['nvim', 'vim', 'hx']) assert.equal(agents.classify(table, {}, n).kind, 'program', n);
});

test('every colour is a Herdr palette slot', () => {
  for (const group of [AGENTS.agents, AGENTS.programs]) {
    for (const [id, e] of Object.entries(group)) assert.ok(SLOTS.includes(e.colour), `${id}: ${e.colour}`);
  }
});

test('Herdr agent id wins over the process', () => {
  // hermes runs as python underneath
  assert.equal(agents.classify(table, { agent: 'hermes' }, 'python3').id, 'hermes');
  assert.equal(agents.classify(table, { agent: 'cursor' }, 'node').id, 'cursor');
});

test('process names: login dash, paths, version suffixes', () => {
  assert.equal(agents.classify(table, {}, '-zsh').id, 'shell');
  assert.equal(agents.classify(table, {}, '/usr/bin/python3.12').id, 'python');
  assert.equal(agents.classify(table, {}, 'cursor-agent').id, 'cursor');
  assert.equal(agents.classify(table, {}, 'unknownthing'), null);
});

test('title regex', () => {
  assert.equal(agents.classify(table, { title: 'aider v0.80' }, 'python3').id, 'python');
  assert.equal(agents.classify(table, { title: 'aider v0.80' }, '').id, 'aider');
});

test('unknown Herdr agent shows as reported', () => {
  const hit = agents.classify(table, { agent: 'newbot' }, '');
  assert.equal(hit.entry.label, 'newbot');
});

test('glyph modes', () => {
  assert.equal(agents.look(AGENTS.programs.neovim, 'nerd').glyph, '\ue6ae');
  assert.equal(agents.look(AGENTS.programs.neovim, 'text').glyph, 'ν');
  assert.equal(agents.look(AGENTS.agents.claude, 'text').glyph, '✻');
});
