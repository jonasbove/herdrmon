#!/usr/bin/env node
'use strict';

// Print the sidebar snippet that uses $mon / $agent / $tools, and optionally
// write it into Herdr's config.toml as a managed block.
//
//   node bin/configure.js              print only
//   node bin/configure.js --write      write the block (backup first)
//   node bin/configure.js --uninstall  stop the daemon, remove the block
//   --reload                           then `herdr server reload-config`
//   --herdr-config PATH                another config.toml (default: Herdr's)

require('../lib/node-version').check();
const fs = require('node:fs');
const { spawnSync } = require('node:child_process');

const paths = require('../lib/paths');
const palette = require('../lib/palette');
const managed = require('../lib/managed');
const toml = require('../lib/toml');
const { request } = require('../lib/daemon');

const args = process.argv.slice(2);
const flag = (f) => args.includes(f);
const opt = (f) => {
  const i = args.indexOf(f);
  return i >= 0 ? args[i + 1] : undefined;
};
const file = opt('--herdr-config') ?? paths.herdrConfigPath();

function readConfig() {
  try {
    return fs.readFileSync(file, 'utf8');
  } catch {
    return '';
  }
}

function write(next, prev) {
  if (next === prev) {
    console.log(`${file}: already up to date`);
    return true;
  }
  try {
    toml.parse(next);
  } catch (e) {
    console.error(`refusing to write ${file}: result would not parse (${e.message})`);
    return false;
  }
  if (prev) fs.copyFileSync(file, `${file}.bak.${Math.floor(Date.now() / 1000)}`);
  paths.ensureDir(require('node:path').dirname(file));
  fs.writeFileSync(file, next);
  console.log(`${file}: written${prev ? ' (backup alongside)' : ''}`);
  return true;
}

function reload() {
  const bin = process.env.HERDR_BIN_PATH || 'herdr';
  const r = spawnSync(bin, ['server', 'reload-config'], { encoding: 'utf8', timeout: 10000 });
  if (r.status === 0) console.log('herdr: config reloaded');
  else console.log(`herdr: reload-config failed (${(r.stderr || r.error?.message || '').trim()}); reload from Herdr's settings`);
}

(async () => {
  const text = readConfig();

  if (flag('--uninstall')) {
    await request({ cmd: 'stop' }, { timeout: 8000 });
    if (managed.split(text)[1] === null) console.log(`${file}: no herdrmon block`);
    else write(managed.apply(text, null), text);
    if (flag('--reload')) reload();
    return;
  }

  const theme = palette.readHerdrTheme(file);
  const resolved = palette.resolve(theme);
  const { block, skipped, snippet, entries } = managed.build(text, resolved, theme.name);

  if (!flag('--write')) {
    console.log(`# Sidebar layout for herdrmon (theme: ${theme.name}). Add to ${file},`);
    console.log('# or run the plugin action "configure" to write it as a managed block.\n');
    console.log(snippet);
  }
  if (skipped.length) {
    console.log(`\n# ${skipped.map((s) => `[ui.sidebar.${s}]`).join(' and ')} already defined in ${file}`);
    console.log('# (by herdr-radar or by you), so herdrmon leaves it alone. Add these token');
    console.log('# entries to its rows where you want them:');
    for (const [name, e] of Object.entries(entries)) console.log(`#   $${name}: ${e}`);
  }
  if (flag('--write')) {
    const ok = write(managed.apply(text, block), text);
    if (!ok) process.exitCode = 1;
    else if (flag('--reload')) reload();
  }
})();
