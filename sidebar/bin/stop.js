#!/usr/bin/env node
'use strict';

// Stop the daemon for this Herdr server; it clears every token it wrote.

const { request } = require('../lib/daemon');

(async () => {
  const r = await request({ cmd: 'stop' }, { timeout: 8000 });
  console.log(r ? 'herdrmon sidebar: stopped' : 'herdrmon sidebar: not running');
})();
