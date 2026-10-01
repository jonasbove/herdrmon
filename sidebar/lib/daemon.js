'use strict';

// The resident daemon: one per Herdr server, for as long as that server runs.
//
// It owns three kinds of pane metadata under its own source:
//   $mon    this machine's animated Pokémon, on the first agent pane of each
//           workspace (the row that heads the workspace's group in the Agents
//           panel), and as workspace metadata for the Spaces panel.
//   $agent  vendor glyph + name, on every agent pane.
//   $tools  the other programs in the pane's tab (one muted token), plus
//           $tool_1..$tool_4 each in its own colour; workspaces get $tools
//           for everything running in them.
//
// One Pokémon per workspace, not per pane: its state is the workspace's
// rolled-up agent state (blocked beats working beats idle), and git events
// and send-outs from any of its agents play on it.
//
// Single instance: the daemon listens on a control socket (paths.controlSocket,
// one per Herdr socket). A second start finds it answering and exits; a stale
// socket file from a crash is replaced. The same socket takes `stop`, `ping`,
// `play` and `force` requests (bin/stop.js, bin/demo.js).

const fs = require('node:fs');
const net = require('node:net');
const path = require('node:path');

const paths = require('./paths');
const ipc = require('./ipc');
const anim = require('./anim');
const config = require('./config');
const procs = require('./procs');
const palette = require('./palette');
const git = require('./git');
const { Frames } = require('./frames');
const { Night } = require('./night');

const SOURCE = 'plugin:jonasbove.herdrmon';
const PREP_MS = 260; // pane.list (0-100 ms) + POLL_MS before each boundary
const TOOLS_MS = 2000;
const CONFIG_MS = 5000;
const GONE_LIMIT = 20; // consecutive failed pane.list calls before exiting
const TOOL_SLOTS = 4;

/* --------------------------------------------------------------- logging */

const stateDir = () => paths.ensureDir(paths.stateDir());
function log(line) {
  try {
    const file = path.join(stateDir(), 'daemon.log');
    try {
      if (fs.statSync(file).size > 256 * 1024) fs.truncateSync(file, 0);
    } catch {
      // No log yet.
    }
    fs.appendFileSync(file, `${new Date().toISOString()} ${line}\n`);
  } catch {
    // Logging never takes the daemon down.
  }
}
const debugOn = () => fs.existsSync(path.join(paths.stateDir(), 'debug'));

/* ---------------------------------------------------------- control socket */

function request(msg, { timeout = 3000, sock = paths.controlSocket() } = {}) {
  return new Promise((resolve) => {
    let body = '';
    let done = false;
    const finish = (v) => {
      if (done) return;
      done = true;
      c.destroy();
      resolve(v);
    };
    const c = net.connect({ path: sock });
    c.setTimeout(timeout, () => finish(null));
    c.on('error', () => finish(null));
    c.on('connect', () => c.write(`${JSON.stringify(msg)}\n`));
    c.on('data', (d) => {
      body += d;
      const nl = body.indexOf('\n');
      if (nl >= 0) {
        try {
          finish(JSON.parse(body.slice(0, nl)));
        } catch {
          finish(null);
        }
      }
    });
  });
}

// Resolves with a listening server, or null when another daemon owns it.
async function lock(handler) {
  const sock = paths.controlSocket();
  paths.ensureDir(path.dirname(sock));
  const listen = () =>
    new Promise((resolve, reject) => {
      const server = net.createServer(handler);
      server.once('error', reject);
      server.listen(sock, () => resolve(server));
    });
  try {
    return await listen();
  } catch (error) {
    if (error.code !== 'EADDRINUSE') throw error;
  }
  if (await request({ cmd: 'ping' }, { timeout: 1500 })) return null;
  try {
    fs.unlinkSync(sock); // stale: its daemon is gone
  } catch {
    // Raced with another starter; listen below decides.
  }
  try {
    return await listen();
  } catch {
    return null;
  }
}

/* ------------------------------------------------------------------ order */

const num = (id) => String(id ?? '').split(/[^0-9]+/).filter(Boolean).map(Number);
function byId(a, b) {
  const x = num(a);
  const y = num(b);
  for (let i = 0; i < Math.max(x.length, y.length); i++) {
    if ((x[i] ?? -1) !== (y[i] ?? -1)) return (x[i] ?? -1) - (y[i] ?? -1);
  }
  return 0;
}
const paneOrder = (a, b) => byId(a.tab_id, b.tab_id) || byId(a.pane_id, b.pane_id);

// workspace_id -> { agents: [pane...], head: pane|null }
function workspaces(panes) {
  const out = new Map();
  for (const p of panes) {
    if (!out.has(p.workspace_id)) out.set(p.workspace_id, { agents: [], head: null });
    if (p.agent) out.get(p.workspace_id).agents.push(p);
  }
  for (const w of out.values()) {
    w.agents.sort(paneOrder);
    w.head = w.agents[0] ?? null;
  }
  return out;
}

/* ----------------------------------------------------------------- daemon */

async function run() {
  let cfg = config.load();
  let cfgMtime = 0;
  let cfgChecked = 0;
  const frames = new Frames(paths.framesPath());
  frames.load();
  let night = new Night(cfg.sidebar);

  const ws = new Map(); // workspace_id -> anim state (+ force)
  const written = new Map(); // "p:<pane>" | "w:<ws>" -> last $mon value (null = cleared)
  const heads = new Map(); // workspace_id -> head pane id
  const waiting = []; // { ws, entry, reply } play requests awaiting their end
  let scanned = false;
  let stopped = false;
  let timer = null;
  let gone = 0;

  const lead = () => Number(process.env.HERDRMON_LEAD_MS ?? cfg.sidebar.lead_ms ?? 0) || 0;
  const clock = () => Date.now() + lead();
  const stateFor = (id) => {
    if (!ws.has(id)) ws.set(id, { ...anim.newState(), force: null });
    return ws.get(id);
  };

  function reloadConfig(now) {
    if (now - cfgChecked < CONFIG_MS) return;
    cfgChecked = now;
    let m = 0;
    try {
      m = fs.statSync(paths.configPath()).mtimeMs;
    } catch {
      // No config file: defaults.
    }
    if (m === cfgMtime) return;
    cfgMtime = m;
    cfg = config.load();
    night = new Night(cfg.sidebar);
    if (cfg.error) log(`config: ${cfg.error.message}`);
  }

  /* ------------------------------------------------------- $mon frames */

  function monValue(frame) {
    return frame;
  }

  async function step(target) {
    if (stopped) return;
    const now = Date.now();
    reloadConfig(now);
    frames.refresh(now);
    const isNight = night.check(now);
    const species = frames.anim(cfg.mon);
    let busy = false;
    const writes = [];
    const put = (key, at, value) => {
      if (written.get(key) === value) return;
      written.set(key, value);
      const [kind, id] = [key.slice(0, 1), key.slice(2)];
      const params =
        kind === 'p'
          ? { pane_id: id, source: SOURCE, tokens: { mon: value } }
          : { workspace_id: id, source: SOURCE, tokens: { mon: value } };
      writes.push({ at, method: kind === 'p' ? 'pane.report_metadata' : 'workspace.report_metadata', params });
    };

    const listed = await ipc.call('pane.list', {});
    const panes = listed?.result?.panes;
    if (!Array.isArray(panes)) {
      gone += 1;
      if (gone >= GONE_LIMIT) {
        log('herdr socket unreachable; exiting');
        process.exit(0);
      }
      return schedule(anim.BOB_MS, target);
    }
    gone = 0;

    const groups = workspaces(panes);
    for (const [id, w] of groups) {
      const st = stateFor(id);
      let status = anim.rollup(w.agents.map((p) => p.agent_status));
      if (st.force && now > st.force.until) st.force = null;
      if (st.force?.status) status = st.force.status;
      anim.observe(st, { status, hasAgent: w.agents.length > 0, scanned });

      for (const p of w.agents) {
        const cwd = p.foreground_cwd || p.cwd;
        if (!cwd) continue;
        git
          .events(cwd, now)
          .then((ev) => {
            if (ev.commit) anim.enqueue(st, 'tackle');
            if (ev.push) anim.enqueue(st, 'harden');
          })
          .catch(() => {});
      }

      // The head moved (closed, reordered): take the Pokémon off the old row.
      const head = w.head?.pane_id ?? null;
      const old = heads.get(id);
      if (old && old !== head && panes.some((p) => p.pane_id === old)) put(`p:${old}`, target, null);
      heads.set(id, head);
      const keys = [`w:${id}`, ...(head ? [`p:${head}`] : [])];

      if (!species) {
        const value = palette.coloured('overlay1', cfg.sidebar.fallback_glyph);
        for (const k of keys) put(k, target, value);
        continue;
      }
      const nightNow = st.force?.night ?? isNight;
      const r = anim.pick(st, status, species, target, nightNow);
      busy ||= Boolean(r.busy) || st.queue.length > 0;
      if (r.entry) {
        const entry = r.entry;
        setTimeout(() => {
          for (let i = waiting.length - 1; i >= 0; i--) {
            if (waiting[i].entry === entry) waiting.splice(i, 1)[0].reply({ ok: true, done: entry.name });
          }
        }, Math.max(0, r.end - clock()));
      }
      if (r.hold) continue;
      const timed = r.shot ?? [{ at: target, frame: r.frame }];
      for (const { at, frame } of timed) for (const k of keys) put(k, at, monValue(frame));
    }

    // Rows that carry a $mon from us but no longer head anything.
    const headSet = new Set([...heads.values()].filter(Boolean));
    for (const p of panes) {
      const k = `p:${p.pane_id}`;
      if (!headSet.has(p.pane_id) && p.tokens?.mon !== undefined && written.get(k) !== null) put(k, target, null);
    }
    for (const id of [...ws.keys()]) if (!groups.has(id)) ws.delete(id);
    for (const id of [...heads.keys()]) if (!groups.has(id)) heads.delete(id);
    scanned = true;

    if (stopped) return;
    for (const w of writes) {
      const real = w.at - lead();
      ipc.callAt(w.method, w.params, real).then((reply) => {
        if (debugOn()) log(`${w.method} ${JSON.stringify(w.params.tokens)} late ${Date.now() - real}ms ${reply?.error ? JSON.stringify(reply.error) : ''}`);
      });
    }
    schedule(busy ? anim.TICK_MS : anim.BOB_MS, target);
  }

  function schedule(period, after = 0) {
    if (stopped) return;
    const next = anim.nextBoundary(clock(), period, PREP_MS, after);
    timer = setTimeout(() => step(next).catch((e) => log(`step: ${e.stack ?? e}`)), Math.max(0, next - PREP_MS - clock()));
  }

  /* ------------------------------------------------- $agent and $tools */

  const toolsWritten = new Map(); // key -> JSON
  let toolsTimer = null;

  function toolTokens(list, indent) {
    const t = { tools: null };
    for (let i = 0; i < TOOL_SLOTS; i++) t[`tool_${i + 1}`] = null;
    if (!list.length) return t;
    t.tools = palette.coloured('subtext0', list.map((d) => `${d.glyph} ${d.label}`).join('  '));
    list.slice(0, TOOL_SLOTS).forEach((d, i) => {
      t[`tool_${i + 1}`] = palette.coloured(d.colour, `${i === 0 ? indent : ''}${d.glyph} ${d.label}`);
    });
    if (list.length > TOOL_SLOTS) t[`tool_${TOOL_SLOTS}`] += ` +${list.length - TOOL_SLOTS + 1}`;
    return t;
  }
  const unique = (list) => list.filter((d, i) => d && !d.hide && list.findIndex((e) => e && e.label === d.label) === i);

  async function tools() {
    if (stopped) return;
    try {
      const listed = await ipc.call('pane.list', {});
      const panes = listed?.result?.panes;
      if (!Array.isArray(panes)) return;
      const infos = await Promise.all(
        panes.map((p) =>
          ipc
            .call('pane.process_info', { pane_id: p.pane_id })
            .then((r) => r?.result?.process_info?.foreground_processes?.[0] ?? null)
            .catch(() => null),
        ),
      );
      const descs = await Promise.all(
        panes.map((p, i) => procs.describe(cfg.agents, p, infos[i], cfg.sidebar.glyphs).catch(() => null)),
      );
      const writes = [];
      const send = (kind, id, tokens) => {
        const key = `${kind}:${id}`;
        const json = JSON.stringify(tokens);
        if (toolsWritten.get(key) === json) return;
        toolsWritten.set(key, json);
        writes.push(
          kind === 'p'
            ? ipc.call('pane.report_metadata', { pane_id: id, source: SOURCE, tokens })
            : ipc.call('workspace.report_metadata', { workspace_id: id, source: SOURCE, tokens }),
        );
      };
      const order = panes.map((p, i) => ({ p, d: descs[i] })).sort((a, b) => paneOrder(a.p, b.p));
      for (const { p, d } of order) {
        if (!p.agent) continue;
        const agent = d ? palette.coloured(d.colour, `${d.glyph} ${d.label}`) : null;
        const others = unique(order.filter((o) => o.p.tab_id === p.tab_id && o.p.pane_id !== p.pane_id).map((o) => o.d));
        send('p', p.pane_id, { agent, ...toolTokens(others, '') });
      }
      const spaces = new Map();
      for (const { p, d } of order) {
        if (!spaces.has(p.workspace_id)) spaces.set(p.workspace_id, []);
        spaces.get(p.workspace_id).push(d);
      }
      for (const [id, list] of spaces) send('w', id, toolTokens(unique(list), ''));
      const alive = new Set([...panes.map((p) => `p:${p.pane_id}`), ...[...spaces.keys()].map((w) => `w:${w}`)]);
      for (const k of [...toolsWritten.keys()]) if (!alive.has(k)) toolsWritten.delete(k);
      await Promise.all(writes);
    } catch (e) {
      log(`tools: ${e.stack ?? e}`);
    } finally {
      if (!stopped) toolsTimer = setTimeout(tools, TOOLS_MS);
    }
  }

  /* -------------------------------------------------------- control */

  async function clearAll() {
    const empty = { mon: null, agent: null, tools: null };
    for (let i = 0; i < TOOL_SLOTS; i++) empty[`tool_${i + 1}`] = null;
    const listed = await ipc.call('pane.list', {});
    const panes = listed?.result?.panes ?? [];
    const wsIds = new Set(panes.map((p) => p.workspace_id));
    await Promise.all([
      ...panes.map((p) => ipc.call('pane.report_metadata', { pane_id: p.pane_id, source: SOURCE, tokens: empty })),
      ...[...wsIds].map((id) => ipc.call('workspace.report_metadata', { workspace_id: id, source: SOURCE, tokens: empty })),
    ]);
  }

  async function shutdown(server) {
    stopped = true;
    clearTimeout(timer);
    clearTimeout(toolsTimer);
    // Frames are sent up to PREP_MS ahead of their boundary; let those land
    // first, or they'd repaint a token right after it was cleared.
    await new Promise((done) => setTimeout(done, PREP_MS + ipc.POLL_MS + 150));
    await clearAll().catch(() => {});
    server.close();
    try {
      fs.unlinkSync(paths.controlSocket());
    } catch {
      // Already gone.
    }
  }

  let server;
  function handle(conn) {
    let body = '';
    conn.on('error', () => {});
    conn.on('data', async (d) => {
      body += d;
      const nl = body.indexOf('\n');
      if (nl < 0) return;
      let msg = {};
      try {
        msg = JSON.parse(body.slice(0, nl));
      } catch {
        // Treated as an unknown command.
      }
      const reply = (v) => {
        try {
          conn.end(`${JSON.stringify(v)}\n`);
        } catch {
          // Requester gone.
        }
      };
      if (msg.cmd === 'ping') return reply({ ok: true, pid: process.pid, mon: cfg.mon, self: cfg.self, frames: Boolean(frames.anim(cfg.mon)) });
      if (msg.cmd === 'stop') {
        await shutdown(server);
        reply({ ok: true });
        return setTimeout(() => process.exit(0), 50);
      }
      if (msg.cmd === 'play' && msg.workspace && anim.ONE_SHOTS.includes(msg.anim)) {
        if (!frames.anim(cfg.mon)) return reply({ ok: false, error: `no frames for ${cfg.mon} (or font missing)` });
        const entry = { name: msg.anim, done: 'request' };
        stateFor(msg.workspace).queue.push(entry);
        waiting.push({ entry, reply });
        return undefined;
      }
      if (msg.cmd === 'force' && msg.workspace) {
        const st = stateFor(msg.workspace);
        st.force = { status: msg.status ?? null, night: msg.night ?? null, until: Date.now() + (Number(msg.ms) || 2000) };
        return setTimeout(() => reply({ ok: true }), Number(msg.ms) || 2000);
      }
      return reply({ ok: false, error: 'unknown command' });
    });
  }

  server = await lock(handle);
  if (!server) return false;
  log(`started pid ${process.pid} self=${cfg.self} mon=${cfg.mon} frames=${frames.file} font=${frames.fontOk} night=${night.plan.mode}`);
  for (const sig of ['SIGTERM', 'SIGINT', 'SIGHUP']) {
    process.on(sig, () => shutdown(server).finally(() => process.exit(0)));
  }
  process.on('uncaughtException', (e) => {
    log(`uncaught: ${e.stack ?? e}`);
    process.exit(1);
  });
  process.on('unhandledRejection', (e) => log(`unhandled: ${e?.stack ?? e}`));
  schedule(anim.TICK_MS);
  tools();
  return true;
}

module.exports = { run, request, SOURCE, workspaces, paneOrder };
