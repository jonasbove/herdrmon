'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const p = require('../lib/palette');
const managed = require('../lib/managed');
const toml = require('../lib/toml');

test('every built-in Herdr theme resolves', () => {
  assert.equal(Object.keys(p.PALETTES).length, 18);
  for (const name of Object.keys(p.PALETTES)) {
    const r = p.resolve({ name, custom: {} });
    assert.ok(r.green && r.red && r.blue, name);
  }
  assert.equal(p.canonicalTheme('Catppuccin Mocha'), 'catppuccin');
  assert.equal(p.canonicalTheme('dawn'), 'rose-pine-dawn');
  assert.equal(p.canonicalTheme('nope'), null);
});

test('theme name and custom overrides from Herdr config', () => {
  const t = p.themeFromHerdrConfig('[theme]\nname = "nord"\n[theme.custom]\npeach = "#123456"\nred = "red"\n');
  const r = p.resolve(t);
  assert.equal(r.peach, '#123456');
  assert.equal(r.red, p.PALETTES.nord.red); // non-hex ignored
  const auto = p.themeFromHerdrConfig('[theme]\nauto_switch = true\nname = "x"\ndark_name = "gruvbox"\n[theme.custom.dark]\nteal = "#000"\n');
  assert.equal(auto.name, 'gruvbox');
  assert.equal(auto.custom.teal, '#000');
  assert.equal(p.themeFromHerdrConfig('[theme]\nauto_switch = true\nname = "latte"\n').name, 'catppuccin');
});

test('colour prefixes and rules are longest first', () => {
  const v = p.coloured('peach', 'x');
  assert.equal(v, '\u200b'.repeat(12) + 'x');
  assert.equal(p.uncoloured(v), 'x');
  assert.equal(p.coloured('nonsense', 'x'), '\u200bx');
  const rules = p.rules(p.resolve());
  assert.equal(rules.length, 12);
  assert.ok(rules.every((r, i) => i === 0 || r.starts_with.length < rules[i - 1].starts_with.length));
  // the first rule matching a value is its own slot
  const hit = rules.find((r) => v.startsWith(r.starts_with));
  assert.equal(hit.fg, p.resolve().peach);
});

test('managed block: write, rewrite, coexist, remove', () => {
  const r = p.resolve();
  const base = '[theme]\nname = "catppuccin"\n';
  const one = managed.apply(base, managed.build(base, r, 'catppuccin').block);
  const parsed = toml.parse(one);
  assert.equal(parsed.ui.sidebar.agents.rows[0][0].token, '$mon');
  assert.equal(parsed.ui.sidebar.spaces.rows[0][1].token, '$mon');
  assert.equal(managed.apply(one, managed.build(one, r, 'catppuccin').block), one);
  assert.equal(managed.apply(one, null), base);
  // radar (or the user) already owns [ui.sidebar.agents]
  const radar = `${base}\n# >>> herdr-radar sidebar block\n[ui.sidebar.agents]\nrows = [["agent"]]\n# <<< herdr-radar sidebar block\n`;
  const b = managed.build(radar, r, 'catppuccin');
  assert.deepEqual(b.skipped, ['agents']);
  const merged = managed.apply(radar, b.block);
  const m = toml.parse(merged);
  assert.deepEqual(m.ui.sidebar.agents.rows, [['agent']]);
  assert.ok(m.ui.sidebar.spaces);
  assert.ok(merged.includes('# >>> herdr-radar sidebar block'));
  // both owned: nothing to write
  const both = `${radar}[ui.sidebar.spaces]\nrows = [["workspace"]]\n`;
  assert.equal(managed.build(both, r, 'catppuccin').block, null);
});
