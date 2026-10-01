'use strict';

// Node >= 18: fetch-free, but uses ?? / ??= / Array.prototype.at and friends.
function check() {
  const major = Number(process.versions.node.split('.')[0]);
  if (major < 18) {
    console.error(`herdrmon sidebar needs Node >= 18 (found ${process.versions.node})`);
    process.exit(1);
  }
}

module.exports = { check };
