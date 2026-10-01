'use strict';

// Herdr's socket API: newline-delimited JSON, one request per connection.
// Cheaper than spawning the CLI per write, and parallel by construction.

const net = require('node:net');
const { herdrSocket } = require('./paths');

const TIMEOUT_MS = 4000;
const MAX_IN_FLIGHT = 16;

function pipePath() {
  const sock = herdrSocket();
  return process.platform === 'win32' ? `\\\\.\\pipe\\${sock}` : sock;
}

function exchange(method, params, writeDelay) {
  return new Promise((resolve) => {
    let settled = false;
    let body = '';
    const finish = (value) => {
      if (settled) return;
      settled = true;
      stream.destroy();
      resolve(value);
    };
    const stream = net.connect({ path: pipePath() });
    const send = () => stream.write(`${JSON.stringify({ id: 'herdrmon', method, params })}\n`);
    stream.setTimeout(TIMEOUT_MS, () => finish(null));
    stream.on('error', () => finish(null));
    stream.on('connect', () => (writeDelay ? setTimeout(send, writeDelay) : send()));
    stream.on('data', (chunk) => {
      body += chunk;
      const nl = body.indexOf('\n');
      if (nl < 0) return;
      try {
        finish(JSON.parse(body.slice(0, nl)));
      } catch {
        finish(null);
      }
    });
  });
}

let inFlight = 0;
const waiters = [];

// The parsed reply (which may carry an `error`), or null if the transport failed.
async function call(method, params = {}) {
  if (inFlight >= MAX_IN_FLIGHT) await new Promise((wake) => waiters.push(wake));
  inFlight += 1;
  try {
    return await exchange(method, params, 0);
  } finally {
    inFlight -= 1;
    waiters.shift()?.();
  }
}

// A call Herdr reads at a chosen moment, for animation frames.
//
// Herdr accepts a connection at once but polls it for its request line every
// ~100 ms. A request written a little after connecting always misses the
// first poll, so Herdr reads it exactly POLL_MS after the connect: connecting
// POLL_MS before `at` lands it on `at` (measured spread ~1 ms, herdr-radar).
const POLL_MS = 100.6;
const WRITE_GAP_MS = 40;

function callAt(method, params, at) {
  return new Promise((resolve) => {
    setTimeout(() => exchange(method, params, WRITE_GAP_MS).then(resolve), Math.max(0, at - POLL_MS - Date.now()));
  });
}

module.exports = { call, callAt, POLL_MS };
