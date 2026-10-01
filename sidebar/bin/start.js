#!/usr/bin/env node
'use strict';

// Start the daemon for this Herdr server if it isn't running, and return.
// Startup hooks and the agent-detected watchdog both run this; it must exit
// quickly, so the daemon is spawned detached.

require('../lib/node-version').check();
const fs = require('node:fs');
const path = require('node:path');
const { spawn } = require('node:child_process');
const paths = require('../lib/paths');
const { request } = require('../lib/daemon');

(async () => {
  const quiet = process.argv.includes('--quiet') || Boolean(process.env.HERDR_PLUGIN_EVENT);
  const alive = await request({ cmd: 'ping' }, { timeout: 1500 });
  if (alive) {
    if (!quiet) console.log(`herdrmon sidebar: already running (pid ${alive.pid}, ${alive.self} = ${alive.mon})`);
    return;
  }
  const dir = paths.ensureDir(paths.stateDir());
  const err = fs.openSync(path.join(dir, 'daemon.stderr'), 'a');
  const child = spawn(process.execPath, [path.join(__dirname, 'daemon.js')], {
    detached: true,
    stdio: ['ignore', 'ignore', err],
    windowsHide: true,
    cwd: path.join(__dirname, '..'),
  });
  child.unref();
  // Wait until it answers, so `start && demo` works.
  for (let i = 0; i < 30; i++) {
    await new Promise((r) => setTimeout(r, 100));
    const up = await request({ cmd: 'ping' }, { timeout: 500 });
    if (up) {
      if (!quiet) console.log(`herdrmon sidebar: started (pid ${up.pid}, ${up.self} = ${up.mon}${up.frames ? '' : ', no frames: plain glyph'})`);
      return;
    }
  }
  console.error(`herdrmon sidebar: daemon did not come up; see ${path.join(dir, 'daemon.stderr')} and daemon.log`);
  process.exitCode = 1;
})();
