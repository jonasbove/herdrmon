'use strict';

// Git events per working directory: a commit (HEAD moved) or a push (a
// remote-tracking ref moved), since the last look. Polled at most every 2 s
// per cwd, never on the frame path.

const { execFile } = require('node:child_process');

const GIT_MS = 2000;
const seen = new Map(); // cwd -> { head, remotes, checked, pending }

function run(cwd, args) {
  return new Promise((resolve) => {
    execFile('git', ['-C', cwd, ...args], { timeout: 1500 }, (err, out) => resolve(err ? null : String(out).trim()));
  });
}

async function events(cwd, now = Date.now()) {
  const none = { commit: false, push: false };
  const g = seen.get(cwd);
  if (g && (g.pending || now - g.checked < GIT_MS)) return none;
  seen.set(cwd, { ...(g ?? { head: undefined, remotes: undefined }), checked: g?.checked ?? 0, pending: true });
  const top = await run(cwd, ['rev-parse', '--show-toplevel']);
  if (!top) {
    seen.set(cwd, { head: null, remotes: null, checked: now });
    return none;
  }
  const [head, remotes] = await Promise.all([
    run(top, ['rev-parse', 'HEAD']),
    run(top, ['for-each-ref', '--format=%(refname) %(objectname)', 'refs/remotes']),
  ]);
  seen.set(cwd, { head, remotes, checked: now });
  if (!g || g.head === undefined) return none;
  return {
    commit: g.head !== null && head !== null && head !== g.head,
    push: g.remotes !== null && remotes !== null && remotes !== g.remotes,
  };
}

module.exports = { events };
