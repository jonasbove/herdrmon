'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { Frames } = require('../lib/frames');

test('loads, accepts hyphen/underscore keys, reloads on mtime, keeps last good', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'herdrmon-frames-'));
  const file = path.join(dir, 'frames.json');
  fs.writeFileSync(file, JSON.stringify({ version: 2, cells: 5, font: 'X', mons: { 'ho-oh': { bob: ['a', 'b'] } } }));
  let fontOk = true;
  const f = new Frames(file, { checkFont: () => fontOk });
  assert.ok(f.load());
  assert.deepEqual(f.anim('ho_oh').bob, ['a', 'b']);
  assert.equal(f.anim('onix'), null);

  fs.writeFileSync(file, '{ "mons": { broken');
  fs.utimesSync(file, new Date(), new Date(Date.now() + 5000));
  f.refresh(Date.now() + 10000);
  assert.deepEqual(f.anim('ho_oh').bob, ['a', 'b']);

  fs.writeFileSync(file, JSON.stringify({ mons: { onix: { bob: ['c'] } } }));
  fs.utimesSync(file, new Date(), new Date(Date.now() + 10000));
  f.refresh(Date.now() + 20000);
  assert.deepEqual(f.anim('onix').bob, ['c']);

  fontOk = false;
  f.load();
  assert.equal(f.anim('onix'), null, 'font missing -> no frames -> plain glyph');
  fs.rmSync(dir, { recursive: true });
});
