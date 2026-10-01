#!/usr/bin/env node
'use strict';

// Regenerates the two data files the plugin ships, from upstream sources:
//
//   lib/species.json        Gen 3 species slugs (pret folder names), from
//                           pokeemerald include/constants/species.h
//   lib/herdr-palettes.json Herdr's built-in theme palettes, from herdr's
//                           src/app/state.rs (Palette::<theme>())
//
// Names and colour values only; no art. Run by hand when either changes:
//   node tools/gen-data.js <species.h> <state.rs>

const fs = require('node:fs');
const path = require('node:path');

const [speciesH, stateRs] = process.argv.slice(2);
if (!speciesH || !stateRs) {
  console.error('usage: node tools/gen-data.js <species.h> <state.rs>');
  process.exit(2);
}
const lib = path.join(__dirname, '..', 'lib');

// Species 1..411 in internal order, minus the OLD_UNOWN placeholders and EGG:
// the 386 that have a folder under graphics/pokemon/.
const species = [];
for (const m of fs.readFileSync(speciesH, 'utf8').matchAll(/^#define SPECIES_(\w+) (\d+)\s*$/gm)) {
  const [, name, num] = m;
  if (name === 'NONE' || name === 'EGG' || name.startsWith('OLD_UNOWN')) continue;
  if (Number(num) > 411) continue;
  species.push(name.toLowerCase());
}
fs.writeFileSync(path.join(lib, 'species.json'), JSON.stringify(species) + '\n');
console.log(`species: ${species.length}`);

// Text-ish palette slots only: those are what sidebar tokens can wear.
const SLOTS = ['accent', 'text', 'subtext0', 'overlay0', 'overlay1', 'mauve', 'green', 'yellow', 'red', 'blue', 'teal', 'peach'];
// The `terminal` theme uses ANSI names; sidebar fg must be hex, so these are
// fixed xterm approximations (the real colour follows the terminal's palette).
const ANSI = {
  Reset: null, Black: '#000000', Red: '#cd0000', Green: '#00cd00', Yellow: '#cdcd00', Blue: '#0000ee',
  Magenta: '#cd00cd', Cyan: '#00cdcd', Gray: '#e5e5e5', DarkGray: '#7f7f7f', LightRed: '#ff0000',
  LightGreen: '#00ff00', LightYellow: '#ffff00', LightBlue: '#5c5cff', LightMagenta: '#ff00ff',
  LightCyan: '#00ffff', White: '#ffffff',
};
const hex = (n) => Number(n).toString(16).padStart(2, '0');
const src = fs.readFileSync(stateRs, 'utf8');
const names = Object.fromEntries(
  [...src.matchAll(/"([a-z-]+)" => Some\(Self::(\w+)\(\)\)/g)].map(([, theme, fn]) => [fn, theme]),
);
const palettes = {};
for (const m of src.matchAll(/pub fn (\w+)\(\) -> Self \{\s*Self \{([\s\S]*?)\n\s*\}\s*\n\s*\}/g)) {
  const [, fn, body] = m;
  const theme = names[fn];
  if (!theme) continue;
  const p = {};
  for (const f of body.matchAll(/(\w+): Color::(?:Rgb\((\d+), (\d+), (\d+)\)|(\w+))/g)) {
    const [, slot, r, g, b, named] = f;
    if (!SLOTS.includes(slot)) continue;
    const v = named ? ANSI[named] : `#${hex(r)}${hex(g)}${hex(b)}`;
    if (v) p[slot] = v;
  }
  palettes[theme] = p;
}
fs.writeFileSync(path.join(lib, 'herdr-palettes.json'), JSON.stringify(palettes, null, 1) + '\n');
console.log(`palettes: ${Object.keys(palettes).join(' ')}`);
