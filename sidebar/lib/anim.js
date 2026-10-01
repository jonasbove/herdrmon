'use strict';

// Which frame a machine's Pokémon shows at time t. Pure: no I/O, no timers.
//
// Every frame is a function of the wall clock, so every workspace and every
// machine (NTP-synced) shows the same loop frame at the same moment:
//   - 125 ms tick for the fast loops (poison, sleep),
//   - 500 ms beat for the idle bob, which is also where one-shots start,
//   - one-shot frames 50 ms apart (the game's switch-out is ~0.4 s), handed
//     back all at once with a time for each so they can be sent ahead.
//
// What plays, highest priority first:
//   poison  loop while blocked (an agent is waiting on the user)
//   recall  once, when the block is answered, followed by
//   sendout once (also when a new agent appears)
//   tackle  once, when HEAD moves (a commit)
//   harden  once, when a remote-tracking ref moves (a push)
//   sleep   loop at night unless an agent is working
//   bob     otherwise
// After a recall the Pokémon stays in its ball until the next send-out.

const TICK_MS = 125;
const BOB_MS = 500;
const SHOT_MS = 50;
const ONE_SHOTS = ['sendout', 'recall', 'harden', 'tackle'];

function newState() {
  return { queue: [], shot: null, shotStart: 0, shotEnd: 0, parked: null, wasBlocked: false, fresh: true };
}

function enqueue(st, name, done = null) {
  if (!done && st.queue.some((e) => e.name === name)) return;
  st.queue.push({ name, done });
}

function seq(anim, name) {
  const a = anim[name];
  return Array.isArray(a) && a.length ? a : null;
}

// st: newState(); status: 'blocked' | 'working' | 'idle' | ...; anim: one
// species from frames.json; t: tick boundary (ms); night: boolean.
// -> { frame, busy }                      show this frame at t
//    { shot: [{at, frame}], busy, entry } a one-shot starts; entry.done is
//                                          the requester's marker, end its end
//    { hold: true, busy: true }           a one-shot is still playing
function pick(st, status, anim, t, night) {
  const tickNo = Math.floor(t / TICK_MS);
  const beat = Math.floor(t / BOB_MS);
  const idle = seq(anim, 'bob') ?? seq(anim, 'base');
  const loop = (name) => {
    const a = seq(anim, name) ?? idle;
    return a[tickNo % a.length];
  };
  const blocked = status === 'blocked';

  if (blocked && !st.shot) return { frame: loop('poison'), busy: true };
  if (!st.shot) {
    while (st.queue.length) {
      const next = st.queue.shift();
      const frames = seq(anim, next.name);
      if (!frames) continue; // the manifest lacks it: skip
      st.shot = next;
      st.shotStart = Math.ceil(t / BOB_MS) * BOB_MS;
      st.shotEnd = st.shotStart + frames.length * SHOT_MS;
      if (next.name === 'recall') st.parked = frames[frames.length - 1];
      if (next.name === 'sendout') st.parked = null;
      const shot = frames.map((frame, i) => ({ at: st.shotStart + i * SHOT_MS, frame }));
      return { shot, busy: true, entry: next, end: st.shotEnd };
    }
  }
  if (st.shot) {
    if (t < st.shotEnd) return { hold: true, busy: true };
    st.shot = null;
    return pick(st, status, anim, t, night);
  }
  if (st.parked) return { frame: st.parked, busy: false };
  if (night && status !== 'working' && seq(anim, 'sleep')) return { frame: loop('sleep'), busy: true };
  return { frame: idle[beat % idle.length], busy: false };
}

// Feed one observation of a workspace's state; queues the implied one-shots.
// scanned: false on the daemon's first look (existing agents aren't new).
function observe(st, { status, hasAgent, scanned }) {
  const blocked = status === 'blocked';
  if (st.wasBlocked && !blocked) {
    enqueue(st, 'recall');
    enqueue(st, 'sendout');
  }
  if (hasAgent && st.fresh && scanned) enqueue(st, 'sendout');
  if (hasAgent) st.fresh = false;
  st.wasBlocked = blocked;
}

// The rolled-up status of several agents: the one that most needs showing.
function rollup(statuses) {
  for (const s of ['blocked', 'working', 'done', 'idle']) if (statuses.includes(s)) return s;
  return statuses.length ? 'unknown' : 'none';
}

// Next boundary on the global grid of `period`, at least `prep` ms ahead.
function nextBoundary(now, period, prep, after = 0) {
  const next = Math.max(after + 1, now + prep);
  return Math.ceil(next / period) * period;
}

module.exports = { TICK_MS, BOB_MS, SHOT_MS, ONE_SHOTS, newState, enqueue, pick, observe, rollup, nextBoundary };
