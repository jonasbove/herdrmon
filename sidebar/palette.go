package main

// Colours by Herdr theme palette slot, not by hex.
//
// Sidebar token styles only take hex (`fg = "#rrggbb"`), and a token has one
// fg plus at most 16 `starts_with` rules. So a value carries its colour as N
// leading zero-width spaces (slot N in slots), and `configure` writes one
// rule per slot with that slot's hex in the user's current Herdr theme
// (built-in palette + [theme.custom]). Zero-width spaces render as nothing
// and Herdr keeps them (it strips control characters only).
//
// Changing Herdr's theme means re-running `configure`; with theme.auto_switch
// the rules follow `theme.name` (or dark_name) only.

import (
	_ "embed"
	"os"
	"regexp"
	"strings"
)

const zw = "​"

// slots: the order is the zero-width count, text = 1 ... peach = 12. Never
// reorder.
var slots = []string{"text", "subtext0", "overlay0", "overlay1", "accent", "mauve", "green", "yellow", "red", "blue", "teal", "peach"}

var slotIndex = func() map[string]int {
	m := map[string]int{}
	for i, s := range slots {
		m[s] = i + 1
	}
	return m
}()

//go:embed data/herdr-palettes.json
var palettesJSON []byte

// palettes: Herdr's built-in themes, slot -> hex (tools/gen-palettes).
var palettes, paletteNames = func() (map[string]map[string]string, []string) {
	v, err := decodeOrdered(palettesJSON)
	if err != nil {
		panic(err)
	}
	t := v.(*Table)
	out := map[string]map[string]string{}
	for _, name := range t.Keys() {
		p := map[string]string{}
		sub := t.Sub(name)
		for _, slot := range sub.Keys() {
			s, _ := sub.Get(slot)
			p[slot] = s.(string)
		}
		out[name] = p
	}
	return out, t.Keys()
}()

// themeAliases: Herdr's canonical_theme_name (src/config/theme.rs, v0.9.3).
var themeAliases = map[string]string{
	"catppuccin-mocha": "catppuccin",
	"latte":            "catppuccin-latte",
	"light":            "catppuccin-latte",
	"tokyonight":       "tokyo-night",
	"tokyo-day":        "tokyo-night-day",
	"tokyonight-day":   "tokyo-night-day",
	"gruvbox-dark":     "gruvbox",
	"onedark":          "one-dark",
	"onelight":         "one-light",
	"solarized-dark":   "solarized",
	"lotus":            "kanagawa-lotus",
	"rosepine":         "rose-pine",
	"rosepine-dawn":    "rose-pine-dawn",
	"dawn":             "rose-pine-dawn",
}

// darkSibling: light built-in -> its dark sibling (Herdr's auto_switch
// fallback).
var darkSibling = map[string]string{
	"catppuccin-latte": "catppuccin",
	"tokyo-night-day":  "tokyo-night",
	"gruvbox-light":    "gruvbox",
	"one-light":        "one-dark",
	"solarized-light":  "solarized",
	"kanagawa-lotus":   "kanagawa",
	"rose-pine-dawn":   "rose-pine",
}

// canonicalTheme: the built-in palette a theme name means, or "".
func canonicalTheme(name any) string {
	s := "catppuccin"
	if name != nil {
		s = jsString(name)
	}
	n := strings.NewReplacer(" ", "-", "_", "-").Replace(strings.ToLower(s))
	if a, ok := themeAliases[n]; ok {
		n = a
	}
	if _, ok := palettes[n]; ok {
		return n
	}
	return ""
}

func slotOf(colour string) string {
	if _, ok := slotIndex[colour]; ok {
		return colour
	}
	return "text"
}

// coloured: text carrying a palette slot.
func coloured(colour, text string) string {
	return strings.Repeat(zw, slotIndex[slotOf(colour)]) + text
}

// uncoloured strips the colour prefix again (tests, debugging).
func uncoloured(value string) string {
	return strings.TrimLeft(value, zw)
}

// HerdrTheme: the theme name and [theme.custom] overrides from Herdr's
// config.toml.
type HerdrTheme struct {
	Name   any // usually a string; whatever the config holds
	Custom *Table
}

func (t HerdrTheme) NameString() string { return jsString(t.Name) }

var (
	reThemeHeader = regexp.MustCompile(`(?m)^\[theme\]\s*$`)
	reThemeName   = regexp.MustCompile(`(?m)^\s*name\s*=\s*"([^"]+)"`)
)

// themeFromHerdrConfig reads the theme from Herdr's config.toml text. A file
// this parser can't read still yields the theme name through a plain regex.
func themeFromHerdrConfig(text string) HerdrTheme {
	cfg, err := ParseToml(text)
	if err != nil {
		var section string
		if locs := reThemeHeader.FindAllStringIndex(text, 2); len(locs) > 0 {
			end := len(text)
			if len(locs) > 1 {
				end = locs[1][0]
			}
			section = text[locs[0][1]:end]
		}
		name := any("catppuccin")
		if m := reThemeName.FindStringSubmatch(section); m != nil {
			name = m[1]
		}
		return HerdrTheme{Name: name, Custom: NewTable()}
	}
	t := cfg.Sub("theme")
	get := func(k string) any { v, _ := t.Get(k); return v }
	auto := truthy(get("auto_switch"))
	name := get("name")
	if auto {
		switch {
		case get("dark_name") != nil:
			name = get("dark_name")
		case darkSibling[canonicalTheme(get("name"))] != "":
			name = darkSibling[canonicalTheme(get("name"))]
		}
	}
	if name == nil {
		name = "catppuccin"
	}
	// Shared overrides, then the dark-mode layer when auto-switching (the
	// rules can only follow one mode; dark is the common case).
	custom := NewTable()
	ct := t.Sub("custom")
	for _, k := range ct.Keys() {
		if k == "light" || k == "dark" {
			continue
		}
		v, _ := ct.Get(k)
		custom.Set(k, v)
	}
	if auto {
		dark := ct.Sub("dark")
		for _, k := range dark.Keys() {
			v, _ := dark.Get(k)
			custom.Set(k, v)
		}
	}
	return HerdrTheme{Name: name, Custom: custom}
}

func readHerdrTheme(file string) HerdrTheme {
	b, err := os.ReadFile(file)
	if err != nil {
		return HerdrTheme{Name: "catppuccin", Custom: NewTable()}
	}
	return themeFromHerdrConfig(string(b))
}

var reHexColour = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// resolvePalette: slot -> hex for a theme. Unknown themes use Catppuccin
// (Herdr's default); slots without a hex (the `terminal` theme's `text` is
// Reset) are omitted.
func resolvePalette(theme HerdrTheme) map[string]string {
	name := canonicalTheme(theme.Name)
	if name == "" {
		name = "catppuccin"
	}
	out := map[string]string{}
	for k, v := range palettes[name] {
		out[k] = v
	}
	for _, slot := range theme.Custom.Keys() {
		v, _ := theme.Custom.Get(slot)
		s, ok := v.(string)
		if _, isSlot := slotIndex[slot]; isSlot && ok && reHexColour.MatchString(s) {
			out[slot] = s
		}
	}
	return out
}

type rule struct{ StartsWith, Fg string }

// paletteRules: the `rules` array for a token, longest prefix first, since a
// value with five zero-width spaces also starts with four.
func paletteRules(resolved map[string]string) []rule {
	var out []rule
	for i := len(slots) - 1; i >= 0; i-- {
		if hex := resolved[slots[i]]; hex != "" {
			out = append(out, rule{strings.Repeat(zw, i+1), hex})
		}
	}
	return out
}
