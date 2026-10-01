'use strict';

// Which Pokémon a device is. Contract (DESIGN.md "Default mon"), shared with
// the party TUI and the font build:
//
//   1. The device's `mon` in config.toml, slugified.
//   2. Else, if the device name slugifies to a Gen 3 species, that species.
//   3. Else MACHINE_MONS[fnv1a32(slug) % MACHINE_MONS.length].
//
// slug: lowercase, every run of non [a-z0-9] characters -> "_", trimmed of
// "_" ("Ho-Oh" -> "ho_oh", "Mr. Mime" -> "mr_mime"). fnv1a32 is the standard
// 32-bit FNV-1a over the slug's UTF-8 bytes.

const SPECIES = require('./species.json');
const { fnv1a } = require('./paths');

const SPECIES_SET = new Set(SPECIES);

// Sensible "machine" mons, all Gen 3. Order is part of the contract.
const MACHINE_MONS = [
  'porygon',
  'porygon2',
  'magnemite',
  'magneton',
  'voltorb',
  'electrode',
  'geodude',
  'onix',
  'beldum',
  'metang',
  'baltoy',
  'claydol',
  'lunatone',
  'solrock',
  'registeel',
];

function slugify(name) {
  return String(name ?? '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '');
}

function isSpecies(name) {
  return SPECIES_SET.has(slugify(name));
}

function defaultMon(name) {
  const slug = slugify(name);
  if (SPECIES_SET.has(slug)) return slug;
  return MACHINE_MONS[fnv1a(slug) % MACHINE_MONS.length];
}

module.exports = { SPECIES, MACHINE_MONS, slugify, isSpecies, defaultMon };
