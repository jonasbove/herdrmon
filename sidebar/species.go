package main

// Which Pokémon a device is. Contract (DESIGN.md "Default mon"), shared with
// the party TUI and the font build:
//
//   1. The device's `mon` in config.toml, slugified.
//   2. Else, if the device name slugifies to a Gen 3 species, that species.
//   3. Else MachineMons[fnv1a32(slug) % len(MachineMons)].
//
// slug: lowercase, every run of non [a-z0-9] characters -> "_", trimmed of
// "_" ("Ho-Oh" -> "ho_oh", "Mr. Mime" -> "mr_mime"). fnv1a32 is the standard
// 32-bit FNV-1a over the slug's UTF-8 bytes. The species list and the
// machine mons come from internal/pokedex, shared with the party TUI.

import (
	"strings"

	"herdrmon/internal/pokedex"
)

func slugify(name string) string {
	var b strings.Builder
	under := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			under = false
			continue
		}
		if !under {
			b.WriteByte('_')
			under = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func isSpecies(name string) bool {
	_, ok := pokedex.BySlug(slugify(name))
	return ok
}

func defaultMon(name string) string {
	slug := slugify(name)
	if _, ok := pokedex.BySlug(slug); ok {
		return slug
	}
	return pokedex.MachineMon(slug)
}
