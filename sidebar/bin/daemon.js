#!/usr/bin/env node
'use strict';

// The daemon itself (bin/start.js launches this detached). Exits at once if
// another daemon already serves this Herdr socket.

require('../lib/node-version').check();
require('../lib/daemon')
  .run()
  .then((started) => {
    if (!started) process.exit(0);
  })
  .catch((e) => {
    console.error(e?.stack ?? e);
    process.exit(1);
  });
