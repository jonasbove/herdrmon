'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { parse, TomlError } = require('../lib/toml');

test('tables, arrays of tables, dotted keys', () => {
  const t = parse(`
# comment
self = "onix" # trailing
[font]
extra = ["porygon", 'tangela']
[[device]]
name = "geodude"
mon = "geodude"
[device.battery]
source = "sysfs"
[[device]]
name = "ho-oh"
display = "Ho-oh"
a.b.c = 1
`);
  assert.equal(t.self, 'onix');
  assert.deepEqual(t.font.extra, ['porygon', 'tangela']);
  assert.equal(t.device.length, 2);
  assert.equal(t.device[0].battery.source, 'sysfs');
  assert.equal(t.device[1].display, 'Ho-oh');
  assert.equal(t.device[1].a.b.c, 1);
});

test('values: numbers, bools, inline tables, multi-line arrays, escapes', () => {
  const t = parse(`
n = 1_000
f = -2.5e3
yes = true
x = 0x1F
s = "tab\\tq\\"\\u200B"
lit = 'C:\\path'
ml = """
line1
line2"""
rows = [
  ["state_icon", { token = "$mon", fg = "#fff", rules = [{ starts_with = "\\u200B", fg = "#000" }] }],
  ["agent"], # comment
]
when = 2026-10-01T10:00:00Z
`);
  assert.equal(t.n, 1000);
  assert.equal(t.f, -2500);
  assert.equal(t.yes, true);
  assert.equal(t.x, 31);
  assert.equal(t.s, 'tab\tq"\u200b');
  assert.equal(t.lit, 'C:\\path');
  assert.equal(t.ml, 'line1\nline2');
  assert.equal(t.rows[0][1].rules[0].starts_with, '\u200b');
  assert.equal(t.rows[1][0], 'agent');
  assert.equal(t.when, '2026-10-01T10:00:00Z');
});

test('errors carry a line number', () => {
  assert.throws(() => parse('a = 1\na = 2\n'), (e) => e instanceof TomlError && e.line === 2);
  assert.throws(() => parse('[x]\n[x.y\n'), TomlError);
  assert.throws(() => parse('s = "open\n'), TomlError);
});
