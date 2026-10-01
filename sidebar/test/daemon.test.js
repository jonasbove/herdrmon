'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { workspaces } = require('../lib/daemon');

test('the head of a workspace is its first agent pane by tab then pane', () => {
  const panes = [
    { pane_id: 'w1:p10', tab_id: 'w1:t1', workspace_id: 'w1', agent: 'codex' },
    { pane_id: 'w1:p2', tab_id: 'w1:t2', workspace_id: 'w1', agent: 'claude' },
    { pane_id: 'w1:p9', tab_id: 'w1:t1', workspace_id: 'w1', agent: 'claude' },
    { pane_id: 'w1:p1', tab_id: 'w1:t1', workspace_id: 'w1' },
    { pane_id: 'w2:p1', tab_id: 'w2:t1', workspace_id: 'w2' },
  ];
  const w = workspaces(panes);
  assert.equal(w.get('w1').head.pane_id, 'w1:p9');
  assert.equal(w.get('w2').head, null);
});
