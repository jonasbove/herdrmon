package main

// Species lookup: slugs, display names, the default mon for a device, and the
// selector's search.

import (
	"hash/fnv"
	"strconv"
	"strings"
)

type Species struct {
	Dex     int
	Slug    string // pret graphics/pokemon folder name: "ho_oh", "mr_mime"
	Display string // "Ho-oh", "Mr. Mime"
}

var speciesBySlug = func() map[string]Species {
	m := make(map[string]Species, len(gen3))
	for _, s := range gen3 {
		m[s.Slug] = s
	}
	return m
}()

// slugify: "Ho-Oh", "ho-oh", "Mr. Mime" -> "ho_oh", "mr_mime".
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	under := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			under = false
		case r == '\'':
		default:
			if !under && b.Len() > 0 {
				b.WriteByte('_')
				under = true
			}
		}
	}
	return strings.TrimSuffix(b.String(), "_")
}

// lookupSpecies accepts a slug, a display name or a national dex number.
func lookupSpecies(s string) (Species, bool) {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n >= 1 && n <= len(gen3) {
		return gen3[n-1], true
	}
	sp, ok := speciesBySlug[slugify(s)]
	if !ok {
		// a frames key with a form ("deoxys-attack", "unown-b") names its species
		if base, _, cut := strings.Cut(strings.TrimSpace(s), "-"); cut {
			if sp, ok = speciesBySlug[slugify(base)]; ok {
				sp.Slug = strings.ToLower(strings.TrimSpace(s))
			}
		}
	}
	return sp, ok
}

// machineMons: sensible "machine" Pokémon for devices whose name says nothing.
var machineMons = []string{
	// Order is part of the contract (font/fetch.py, sidebar/lib/species.js).
	"porygon", "porygon2", "magnemite", "magneton", "voltorb", "electrode", "geodude", "onix", "beldum", "metang", "baltoy", "claydol", "lunatone", "solrock", "registeel",
}

// defaultMon: the device name itself if it is a species, else a stable pick
// from machineMons by hash of the name.
func defaultMon(name string) string {
	if sp, ok := speciesBySlug[slugify(name)]; ok {
		return sp.Slug
	}
	h := fnv.New32a()
	h.Write([]byte(slugify(name)))
	return machineMons[h.Sum32()%uint32(len(machineMons))]
}

// searchSpecies: dex-ordered matches; a number matches its dex entry first,
// then names that start with the query, then names that contain it.
func searchSpecies(q string) []Species {
	q = strings.TrimSpace(q)
	if q == "" {
		return gen3[:]
	}
	var exact, prefix, sub []Species
	qs := slugify(q)
	n, numErr := strconv.Atoi(q)
	for _, s := range gen3 {
		switch {
		case numErr == nil && s.Dex == n:
			exact = append(exact, s)
		case numErr == nil && strings.HasPrefix(strconv.Itoa(s.Dex), q):
			prefix = append(prefix, s)
		case qs != "" && strings.HasPrefix(s.Slug, qs):
			prefix = append(prefix, s)
		case qs != "" && strings.Contains(s.Slug, qs):
			sub = append(sub, s)
		}
	}
	return append(append(exact, prefix...), sub...)
}
