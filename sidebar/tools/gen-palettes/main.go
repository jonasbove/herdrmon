// gen-palettes regenerates data/herdr-palettes.json, Herdr's built-in theme
// palettes, from Herdr's src/app/state.rs (Palette::<theme>()). Colour
// values only. Run by hand when Herdr adds or changes a theme:
//
//	go run ./tools/gen-palettes <herdr>/src/app/state.rs
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Text-ish palette slots only: those are what sidebar tokens can wear.
var slots = []string{"accent", "text", "subtext0", "overlay0", "overlay1", "mauve", "green", "yellow", "red", "blue", "teal", "peach"}

// The `terminal` theme uses ANSI names; sidebar fg must be hex, so these are
// fixed xterm approximations (the real colour follows the terminal's palette).
var ansi = map[string]string{
	"Reset": "", "Black": "#000000", "Red": "#cd0000", "Green": "#00cd00", "Yellow": "#cdcd00", "Blue": "#0000ee",
	"Magenta": "#cd00cd", "Cyan": "#00cdcd", "Gray": "#e5e5e5", "DarkGray": "#7f7f7f", "LightRed": "#ff0000",
	"LightGreen": "#00ff00", "LightYellow": "#ffff00", "LightBlue": "#5c5cff", "LightMagenta": "#ff00ff",
	"LightCyan": "#00ffff", "White": "#ffffff",
}

var (
	reName  = regexp.MustCompile(`"([a-z-]+)" => Some\(Self::(\w+)\(\)\)`)
	reFn    = regexp.MustCompile(`pub fn (\w+)\(\) -> Self \{\s*Self \{([\s\S]*?)\n\s*\}\s*\n\s*\}`)
	reField = regexp.MustCompile(`(\w+): Color::(?:Rgb\((\d+), (\d+), (\d+)\)|(\w+))`)
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./tools/gen-palettes <herdr>/src/app/state.rs")
		os.Exit(2)
	}
	src, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	names := map[string]string{}
	for _, m := range reName.FindAllStringSubmatch(string(src), -1) {
		names[m[2]] = m[1]
	}
	isSlot := map[string]bool{}
	for _, s := range slots {
		isSlot[s] = true
	}
	// Written by hand to keep the order of state.rs and a one-space indent.
	var b strings.Builder
	b.WriteString("{")
	first := true
	var themes []string
	for _, m := range reFn.FindAllStringSubmatch(string(src), -1) {
		theme := names[m[1]]
		if theme == "" {
			continue
		}
		themes = append(themes, theme)
		var fields []string
		for _, f := range reField.FindAllStringSubmatch(m[2], -1) {
			if !isSlot[f[1]] {
				continue
			}
			v := ansi[f[5]]
			if f[5] == "" {
				r, _ := strconv.Atoi(f[2])
				g, _ := strconv.Atoi(f[3])
				bl, _ := strconv.Atoi(f[4])
				v = fmt.Sprintf("#%02x%02x%02x", r, g, bl)
			}
			if v != "" {
				k, _ := json.Marshal(f[1])
				fields = append(fields, fmt.Sprintf("\n  %s: %q", k, v))
			}
		}
		if !first {
			b.WriteString(",")
		}
		first = false
		k, _ := json.Marshal(theme)
		fmt.Fprintf(&b, "\n %s: {%s\n }", k, strings.Join(fields, ","))
	}
	b.WriteString("\n}\n")
	// Run from sidebar/, like the build.
	out := filepath.Join("data", "herdr-palettes.json")
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("palettes: %s\n", strings.Join(themes, " "))
}
