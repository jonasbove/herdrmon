// Package pokedex is the species data shared by the party TUI (the repo
// root) and the Herdr sidebar plugin (sidebar/): the 386 Gen 3 species and
// the default-mon contract (DESIGN.md "Default mon").
package pokedex

import "hash/fnv"

type Species struct {
	Dex     int
	Slug    string // pret graphics/pokemon folder name: "ho_oh", "mr_mime"
	Display string // "Ho-oh", "Mr. Mime"
}

// MachineMons: sensible "machine" Pokémon for devices whose name says
// nothing. Order is part of the contract (font/fetch.py, the sidebar).
var MachineMons = []string{
	"porygon", "porygon2", "magnemite", "magneton", "voltorb", "electrode", "geodude", "onix", "beldum", "metang", "baltoy", "claydol", "lunatone", "solrock", "registeel",
}

var bySlug = func() map[string]Species {
	m := make(map[string]Species, len(Gen3))
	for _, s := range Gen3 {
		m[s.Slug] = s
	}
	return m
}()

// BySlug: the species with this exact slug.
func BySlug(slug string) (Species, bool) {
	s, ok := bySlug[slug]
	return s, ok
}

// FNV1a: 32-bit FNV-1a over the UTF-8 bytes of s.
func FNV1a(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

// MachineMon: the stable pick from MachineMons for a device slug.
func MachineMon(slug string) string {
	return MachineMons[FNV1a(slug)%uint32(len(MachineMons))]
}
