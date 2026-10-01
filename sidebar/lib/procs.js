'use strict';

// What a pane is running, for $agent and $tools.
//
// Agents and programs come from the table (agents.js). Neovim, Omarchy's
// editor, gets one extra step: it's labelled by what it's editing — the
// project's framework when one is detected (FastAPI, React, ...), else the
// filetype of the file in its current window, else plain "neovim". The
// running nvim is asked over its RPC socket.

const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFile } = require('node:child_process');

const agents = require('./agents');

// Nerd Font devicon + palette slot per language/framework.
const ICONS = {
  javascript: ['', 'yellow'],
  typescript: ['', 'blue'],
  html: ['', 'peach'],
  css: ['', 'blue'],
  sass: ['', 'mauve'],
  python: ['', 'blue'],
  lua: ['', 'blue'],
  rust: ['', 'peach'],
  go: ['', 'teal'],
  c: ['', 'blue'],
  cpp: ['', 'blue'],
  java: ['', 'red'],
  kotlin: ['', 'mauve'],
  swift: ['', 'peach'],
  ruby: ['', 'red'],
  php: ['', 'mauve'],
  zig: ['', 'peach'],
  dart: ['', 'blue'],
  elixir: ['', 'mauve'],
  haskell: ['', 'mauve'],
  csharp: ['', 'mauve'],
  markdown: ['', 'text'],
  json: ['', 'yellow'],
  yaml: ['', 'red'],
  toml: ['', 'peach'],
  bash: ['', 'green'],
  nix: ['', 'blue'],
  sql: ['', 'blue'],
  docker: ['', 'blue'],
  fastapi: ['', 'teal'],
  django: ['', 'green'],
  flask: ['', 'text'],
  react: ['', 'blue'],
  nextjs: ['', 'text'],
  vue: ['', 'green'],
  nuxt: ['', 'green'],
  svelte: ['', 'peach'],
  astro: ['', 'mauve'],
  angular: ['', 'red'],
  vite: ['', 'mauve'],
  express: ['', 'text'],
  electron: ['', 'blue'],
  htmx: ['', 'blue'],
};
const LABELS = { nextjs: 'next.js', cpp: 'c++', csharp: 'c#' };

const FILETYPES = {
  javascript: 'javascript', javascriptreact: 'react', typescript: 'typescript', typescriptreact: 'react',
  html: 'html', css: 'css', scss: 'sass', sass: 'sass', python: 'python', lua: 'lua', rust: 'rust', go: 'go',
  c: 'c', cpp: 'cpp', java: 'java', kotlin: 'kotlin', swift: 'swift', ruby: 'ruby', php: 'php', zig: 'zig',
  dart: 'dart', elixir: 'elixir', haskell: 'haskell', cs: 'csharp', markdown: 'markdown', json: 'json',
  jsonc: 'json', yaml: 'yaml', toml: 'toml', sh: 'bash', bash: 'bash', zsh: 'bash', nix: 'nix', sql: 'sql',
  dockerfile: 'docker', svelte: 'svelte', vue: 'vue', astro: 'astro',
};

// Framework markers, first match wins, most specific first.
const JS_FRAMEWORKS = [
  ['next', 'nextjs'], ['nuxt', 'nuxt'], ['@sveltejs/kit', 'svelte'], ['svelte', 'svelte'], ['astro', 'astro'],
  ['@angular/core', 'angular'], ['vue', 'vue'], ['react', 'react'], ['electron', 'electron'],
  ['express', 'express'], ['htmx.org', 'htmx'], ['vite', 'vite'],
];
const PY_FRAMEWORKS = [['fastapi', 'fastapi'], ['django', 'django'], ['flask', 'flask']];

function read(file) {
  try {
    return fs.readFileSync(file, 'utf8');
  } catch {
    return null;
  }
}

const projectCache = new Map(); // cwd -> { at, key }
const PROJECT_TTL_MS = 60_000;

function frameworkFor(cwd) {
  if (!cwd) return null;
  const hit = projectCache.get(cwd);
  if (hit && Date.now() - hit.at < PROJECT_TTL_MS) return hit.key;
  let key = null;
  const pkg = read(path.join(cwd, 'package.json'));
  if (pkg) {
    try {
      const json = JSON.parse(pkg);
      const deps = { ...json.dependencies, ...json.devDependencies };
      key = JS_FRAMEWORKS.find(([dep]) => dep in deps)?.[1] ?? null;
    } catch {
      // Unparseable package.json: no framework.
    }
  }
  if (!key) {
    let files = [];
    try {
      files = fs.readdirSync(cwd).filter((f) => /^(requirements.*\.txt|pyproject\.toml|Pipfile|setup\.cfg)$/.test(f));
    } catch {
      // Unreadable directory.
    }
    const text = files.map((f) => read(path.join(cwd, f)) ?? '').join('\n').toLowerCase();
    key = PY_FRAMEWORKS.find(([dep]) => new RegExp(`^\\s*["']?${dep}\\b`, 'm').test(text))?.[1] ?? null;
  }
  projectCache.set(cwd, { at: Date.now(), key });
  return key;
}

// The filetype of the file in the current window (or the first real file in
// the current tab: the focused window may be an explorer), plus nvim's cwd.
const NVIM_LUA = [
  'local function ft(w)',
  '  local b = vim.api.nvim_win_get_buf(w)',
  "  if vim.bo[b].buftype == '' and vim.api.nvim_buf_get_name(b) ~= '' then return vim.bo[b].filetype end",
  'end',
  'local r = ft(0)',
  'if not r then',
  '  for _, w in ipairs(vim.api.nvim_tabpage_list_wins(0)) do r = ft(w); if r then break end end',
  'end',
  "return vim.fn.getcwd() .. '\\t' .. (r or '')",
].join('\n');
const NVIM_EXPR = `luaeval('(function() ${NVIM_LUA.replace(/'/g, "''")} end)()')`;

const nvimCache = new Map(); // tui pid -> { at, value }
const NVIM_TTL_MS = 4000;

function runtimeDir() {
  return process.env.XDG_RUNTIME_DIR || `/run/user/${os.userInfo().uid}`;
}

function parentPid(pid) {
  const stat = read(`/proc/${pid}/stat`);
  return stat ? Number(stat.slice(stat.lastIndexOf(')') + 2).split(' ')[1]) : NaN;
}

// nvim's TUI process (the pane's foreground) talks to an embedded child that
// owns the socket, named after that child's pid: $XDG_RUNTIME_DIR/nvim.<pid>.0
function socketFor(tuiPid) {
  let names = [];
  try {
    names = fs.readdirSync(runtimeDir()).filter((n) => /^nvim\.\d+\.\d+$/.test(n));
  } catch {
    return null;
  }
  const hit = names.find((name) => {
    const pid = Number(name.split('.')[1]);
    return pid === tuiPid || parentPid(pid) === tuiPid;
  });
  return hit ? path.join(runtimeDir(), hit) : null;
}

function queryNvim(tuiPid) {
  const hit = nvimCache.get(tuiPid);
  if (hit && Date.now() - hit.at < NVIM_TTL_MS) return Promise.resolve(hit.value);
  const sock = tuiPid ? socketFor(tuiPid) : null;
  if (!sock) return Promise.resolve(null);
  let bin = 'nvim';
  try {
    bin = fs.readlinkSync(`/proc/${tuiPid}/exe`);
  } catch {
    // Fall back to PATH.
  }
  return new Promise((resolve) => {
    execFile(bin, ['--server', sock, '--remote-expr', NVIM_EXPR], { timeout: 1500 }, (error, stdout) => {
      let value = null;
      if (!error) {
        const [cwd, filetype] = String(stdout).trim().split('\t');
        value = { cwd, filetype };
      }
      nvimCache.set(tuiPid, { at: Date.now(), value });
      resolve(value);
    });
  });
}

// Framework beats language: "fastapi" says more about a repo than "python".
function neovimLabel(info, fallbackCwd) {
  const key = frameworkFor(info?.cwd || fallbackCwd) ?? FILETYPES[info?.filetype] ?? null;
  return key;
}

// -> { id, kind, glyph, label, colour, hide } or null.
// pane: a pane.list entry; proc: { pid, name, cwd } (foreground process).
async function describe(table, pane, proc, glyphs = 'nerd') {
  const hit = agents.classify(table, pane, proc?.name);
  if (!hit) return null;
  const out = { id: hit.id, kind: hit.kind, hide: Boolean(hit.entry.hide), ...agents.look(hit.entry, glyphs) };
  if (hit.id === 'neovim' && hit.kind === 'program') {
    const info = await queryNvim(proc?.pid).catch(() => null);
    const key = neovimLabel(info, proc?.cwd || pane.foreground_cwd || pane.cwd);
    if (key && ICONS[key]) {
      const [glyph, colour] = ICONS[key];
      if (glyphs !== 'text') out.glyph = glyph;
      out.colour = colour;
      out.label = LABELS[key] ?? key;
    }
  }
  return out;
}

module.exports = { describe, frameworkFor, neovimLabel, socketFor, ICONS, FILETYPES };
