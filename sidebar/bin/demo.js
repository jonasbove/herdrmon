#!/usr/bin/env node
'use strict';

// Play every animation on one workspace's Pokémon, in order: send-out,
// tackle, harden, sleep, poison, then recall + send-out. Target: the
// focused workspace (HERDR_WORKSPACE_ID when run as a plugin action), or
// `--workspace <id>`.

require('../lib/node-version').check();
const { spawnSync } = require('node:child_process');
const path = require('node:path');
const { request } = require('../lib/daemon');
const ipc = require('../lib/ipc');

// Poison last: when the forced block ends, the daemon's own "block answered"
// rule plays recall + send-out, exactly as it does for a real agent.
const STEPS = [
  { cmd: 'play', anim: 'sendout' },
  { cmd: 'play', anim: 'tackle' },
  { cmd: 'play', anim: 'harden' },
  { cmd: 'force', status: 'idle', night: true, ms: 2500 },
  { cmd: 'force', status: 'blocked', ms: 2500 },
];
const SETTLE_MS = 2500; // recall + send-out after the block

(async () => {
  const i = process.argv.indexOf('--workspace');
  let workspace = i >= 0 ? process.argv[i + 1] : process.env.HERDR_WORKSPACE_ID;
  if (!workspace) {
    const r = await ipc.call('pane.list', {});
    workspace = r?.result?.panes?.find((p) => p.focused)?.workspace_id;
  }
  if (!workspace) {
    console.error('herdrmon demo: no workspace (pass --workspace <id>)');
    process.exit(1);
  }
  spawnSync(process.execPath, [path.join(__dirname, 'start.js'), '--quiet'], { stdio: 'inherit' });
  for (const step of STEPS) {
    const r = await request({ ...step, workspace }, { timeout: 15000 });
    const what = step.anim ?? (step.night ? 'sleep' : 'poison');
    console.log(`${what}: ${r?.ok ? 'ok' : (r?.error ?? 'no answer')}`);
    if (r && !r.ok) process.exitCode = 1;
  }
  await new Promise((done) => setTimeout(done, SETTLE_MS));
  console.log('recall + sendout: ok');
})();
