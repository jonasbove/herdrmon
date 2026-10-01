'use strict';

// Colours by Herdr theme palette slot, not by hex.
//
// Sidebar token styles only take hex (`fg = "#rrggbb"`), and a token has one
// fg plus at most 16 `starts_with` rules. So a value carries its colour as N
// leading zero-width spaces (slot N in SLOTS), and `configure` writes one rule
// per slot with that slot's hex in the user's current Herdr theme (built-in
// palette + [theme.custom]). Zero-width spaces render as nothing and Herdr
// keeps them (it strips control characters only).
//
// Changing Herdr's theme means re-running `configure`; with theme.auto_switch
// the rules follow `theme.name` (or dark_name) only.

const fs = require('node:fs');
const toml = require('./toml');
const PALETTES = require('./herdr-palettes.json');

const ZW = '​';
// Order is the zero-width count: text = 1 ... peach = 12. Never reorder.
const SLOTS = ['text', 'subtext0', 'overlay0', 'overlay1', 'accent', 'mauve', 'green', 'yellow', 'red', 'blue', 'teal', 'peach'];
const INDEX = Object.fromEntries(SLOTS.map((s, i) => [s, i + 1]));

// Herdr's canonical_theme_name (src/config/theme.rs, v0.9.3).
const ALIASES = {
  'catppuccin-mocha': 'catppuccin',
  latte: 'catppuccin-latte',
  light: 'catppuccin-latte',
  tokyonight: 'tokyo-night',
  'tokyo-day': 'tokyo-night-day',
  'tokyonight-day': 'tokyo-night-day',
  'gruvbox-dark': 'gruvbox',
  onedark: 'one-dark',
  onelight: 'one-light',
  'solarized-dark': 'solarized',
  lotus: 'kanagawa-lotus',
  rosepine: 'rose-pine',
  'rosepine-dawn': 'rose-pine-dawn',
  dawn: 'rose-pine-dawn',
};

// Light built-in -> its dark sibling (Herdr's auto_switch fallback).
const DARK_SIBLING = {
  'catppuccin-latte': 'catppuccin',
  'tokyo-night-day': 'tokyo-night',
  'gruvbox-light': 'gruvbox',
  'one-light': 'one-dark',
  'solarized-light': 'solarized',
  'kanagawa-lotus': 'kanagawa',
  'rose-pine-dawn': 'rose-pine',
};

function canonicalTheme(name) {
  const n = String(name ?? 'catppuccin').toLowerCase().replace(/[ _]/g, '-');
  const c = ALIASES[n] ?? n;
  return c in PALETTES ? c : null;
}

function slotOf(colour) {
  return colour in INDEX ? colour : 'text';
}

function coloured(colour, text) {
  return ZW.repeat(INDEX[slotOf(colour)]) + text;
}

// Strip the colour prefix again (tests, debugging).
function uncoloured(value) {
  return String(value).replace(/^​+/, '');
}

// { name, custom } from Herdr's config.toml text. A file this parser can't
// read still yields the theme name through a plain regex.
function themeFromHerdrConfig(text) {
  try {
    const cfg = toml.parse(text);
    const t = cfg.theme ?? {};
    // auto_switch without dark_name: Herdr uses the built-in dark sibling.
    const name = t.auto_switch ? (t.dark_name ?? DARK_SIBLING[canonicalTheme(t.name)] ?? t.name) : t.name;
    // Shared overrides, then the dark-mode layer when auto-switching (the
    // rules can only follow one mode; dark is the common case).
    const { light: _light, dark, ...shared } = t.custom ?? {};
    const custom = { ...shared, ...(t.auto_switch ? dark : {}) };
    return { name: name ?? 'catppuccin', custom };
  } catch {
    const m = /^\s*name\s*=\s*"([^"]+)"/m.exec(String(text).split(/^\[theme\]\s*$/m)[1] ?? '');
    return { name: m?.[1] ?? 'catppuccin', custom: {} };
  }
}

function readHerdrTheme(file) {
  try {
    return themeFromHerdrConfig(fs.readFileSync(file, 'utf8'));
  } catch {
    return { name: 'catppuccin', custom: {} };
  }
}

const HEX = /^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/;

// slot -> hex for a theme. Unknown themes use Catppuccin (Herdr's default);
// slots without a hex (the `terminal` theme's `text` is Reset) are omitted.
function resolve(theme = { name: 'catppuccin', custom: {} }) {
  const base = PALETTES[canonicalTheme(theme.name) ?? 'catppuccin'];
  const out = { ...base };
  for (const [slot, v] of Object.entries(theme.custom ?? {})) {
    if (SLOTS.includes(slot) && typeof v === 'string' && HEX.test(v)) out[slot] = v;
  }
  return out;
}

// The `rules` array for a token: longest prefix first, since a value with
// five zero-width spaces also starts with four.
function rules(resolved) {
  return SLOTS.map((slot) => ({ slot, n: INDEX[slot] }))
    .filter(({ slot }) => resolved[slot])
    .sort((a, b) => b.n - a.n)
    .map(({ slot, n }) => ({ starts_with: ZW.repeat(n), fg: resolved[slot] }));
}

module.exports = { ZW, SLOTS, INDEX, PALETTES, canonicalTheme, coloured, uncoloured, themeFromHerdrConfig, readHerdrTheme, resolve, rules };
