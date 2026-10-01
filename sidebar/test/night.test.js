'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { byClock, plan } = require('../lib/night');

test('clock fallback spans midnight', () => {
  const at = (h) => new Date(2026, 9, 1, h, 30);
  assert.equal(byClock(at(23)), true);
  assert.equal(byClock(at(3)), true);
  assert.equal(byClock(at(7)), false);
  assert.equal(byClock(at(12)), false);
  assert.equal(byClock(at(13), 12, 14), true);
});

test('explicit command / empty command', () => {
  assert.deepEqual(plan({ night_command: 'true' }), { mode: 'command', command: 'true' });
  assert.deepEqual(plan({ night_command: '' }), { mode: 'clock' });
});
