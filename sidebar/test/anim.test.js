'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const a = require('../lib/anim');

const mk = (name, n) => Array.from({ length: n }, (_, i) => `${name}${i}`);
const ANIM = { base: ['b'], bob: mk('bob', 2), poison: mk('poison', 4), sleep: mk('sleep', 4), sendout: mk('out', 3), recall: mk('rec', 3), tackle: mk('tk', 2), harden: mk('hd', 2) };

test('bob follows the 500 ms beat of the wall clock', () => {
  const st = a.newState();
  assert.equal(a.pick(st, 'idle', ANIM, 0, false).frame, 'bob0');
  assert.equal(a.pick(st, 'idle', ANIM, 375, false).frame, 'bob0');
  assert.equal(a.pick(st, 'idle', ANIM, 500, false).frame, 'bob1');
  assert.equal(a.pick(st, 'idle', ANIM, 1000, false).frame, 'bob0');
  // two independent states agree at the same instant
  assert.equal(a.pick(a.newState(), 'idle', ANIM, 1500, false).frame, a.pick(a.newState(), 'idle', ANIM, 1500, false).frame);
});

test('blocked loops poison on the 125 ms tick and is busy', () => {
  const st = a.newState();
  const r = a.pick(st, 'blocked', ANIM, 250, false);
  assert.equal(r.frame, 'poison2');
  assert.equal(r.busy, true);
});

test('night sleeps unless working', () => {
  assert.equal(a.pick(a.newState(), 'idle', ANIM, 125, true).frame, 'sleep1');
  assert.equal(a.pick(a.newState(), 'working', ANIM, 0, true).frame, 'bob0');
});

test('one-shot starts on the next beat, frames 50 ms apart, then holds', () => {
  const st = a.newState();
  a.enqueue(st, 'tackle');
  const r = a.pick(st, 'idle', ANIM, 625, false);
  assert.deepEqual(r.shot, [{ at: 1000, frame: 'tk0' }, { at: 1050, frame: 'tk1' }]);
  assert.equal(r.end, 1100);
  assert.equal(a.pick(st, 'idle', ANIM, 1000, false).hold, true);
  assert.equal(a.pick(st, 'idle', ANIM, 1500, false).frame, 'bob1');
});

test('answering a block plays recall then sendout; recall parks in the ball', () => {
  const st = a.newState();
  a.observe(st, { status: 'blocked', hasAgent: true, scanned: false });
  assert.equal(a.pick(st, 'blocked', ANIM, 0, false).frame, 'poison0');
  a.observe(st, { status: 'idle', hasAgent: true, scanned: true });
  assert.deepEqual(st.queue.map((e) => e.name), ['recall', 'sendout']);
  const r1 = a.pick(st, 'idle', ANIM, 100, false);
  assert.equal(r1.entry.name, 'recall');
  assert.equal(st.parked, 'rec2');
  const r2 = a.pick(st, 'idle', ANIM, r1.end, false);
  assert.equal(r2.entry.name, 'sendout');
  assert.equal(st.parked, null);
});

test('parked stays in the ball until a sendout', () => {
  const st = a.newState();
  a.enqueue(st, 'recall');
  const r = a.pick(st, 'idle', ANIM, 0, false);
  assert.equal(a.pick(st, 'idle', ANIM, r.end + 500, false).frame, 'rec2');
});

test('a new agent after the first scan is sent out; existing ones are not', () => {
  const st = a.newState();
  a.observe(st, { status: 'none', hasAgent: false, scanned: false });
  a.observe(st, { status: 'idle', hasAgent: true, scanned: true });
  assert.deepEqual(st.queue.map((e) => e.name), ['sendout']);
  const old = a.newState();
  a.observe(old, { status: 'idle', hasAgent: true, scanned: false });
  assert.equal(old.queue.length, 0);
});

test('missing animations fall back to bob; unknown one-shots are skipped', () => {
  const thin = { bob: ['x', 'y'] };
  assert.equal(a.pick(a.newState(), 'blocked', thin, 0, false).frame, 'x');
  const st = a.newState();
  a.enqueue(st, 'harden');
  assert.equal(a.pick(st, 'idle', thin, 500, true).frame, 'y');
});

test('rollup and grid', () => {
  assert.equal(a.rollup(['idle', 'blocked', 'working']), 'blocked');
  assert.equal(a.rollup(['done', 'working']), 'working');
  assert.equal(a.rollup([]), 'none');
  assert.equal(a.nextBoundary(1000, 125, 260), 1375);
  assert.equal(a.nextBoundary(1000, 500, 260, 1500), 2000);
});
