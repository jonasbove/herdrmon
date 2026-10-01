package main

// Colours come from the live Omarchy theme (colors.toml), mapped onto the
// FRLG party menu by role. Nothing is hard-coded to Catppuccin except the
// fallback used when no theme file can be read.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type RGB struct{ R, G, B float64 } // 0..255

func hex(s string) (RGB, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return RGB{}, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return RGB{}, false
	}
	return RGB{float64(v >> 16 & 255), float64(v >> 8 & 255), float64(v & 255)}, true
}

func mix(a, b RGB, t float64) RGB {
	t = clamp(t, 0, 1)
	return RGB{a.R + (b.R-a.R)*t, a.G + (b.G-a.G)*t, a.B + (b.B-a.B)*t}
}

func (c RGB) scale(k float64) RGB {
	return RGB{clamp(c.R*k, 0, 255), clamp(c.G*k, 0, 255), clamp(c.B*k, 0, 255)}
}

// saturate pushes a colour away from its grey (k>1) or towards it (k<1).
func (c RGB) saturate(k float64) RGB {
	g := 0.299*c.R + 0.587*c.G + 0.114*c.B
	return RGB{clamp(g+(c.R-g)*k, 0, 255), clamp(g+(c.G-g)*k, 0, 255), clamp(g+(c.B-g)*k, 0, 255)}
}

func (c RGB) gray() float64 { return 0.299*c.R + 0.587*c.G + 0.114*c.B }

// HSL, h in degrees.
func (c RGB) hsl() (h, s, l float64) {
	r, g, b := c.R/255, c.G/255, c.B/255
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	l = (mx + mn) / 2
	if mx == mn {
		return 0, 0, l
	}
	d := mx - mn
	if l > .5 {
		s = d / (2 - mx - mn)
	} else {
		s = d / (mx + mn)
	}
	switch mx {
	case r:
		h = (g - b) / d
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	return h * 60, s, l
}

func fromHSL(h, s, l float64) RGB {
	h = math.Mod(math.Mod(h, 360)+360, 360) / 360
	s, l = clamp(s, 0, 1), clamp(l, 0, 1)
	if s == 0 {
		return RGB{l * 255, l * 255, l * 255}
	}
	q := l * (1 + s)
	if l >= .5 {
		q = l + s - l*s
	}
	p := 2*l - q
	f := func(t float64) float64 {
		t = math.Mod(t+1, 1)
		switch {
		case t < 1./6:
			return p + (q-p)*6*t
		case t < .5:
			return q
		case t < 2./3:
			return p + (q-p)*(2./3-t)*6
		}
		return p
	}
	return RGB{f(h+1./3) * 255, f(h) * 255, f(h-1./3) * 255}
}

func (c RGB) light(dl float64) RGB {
	h, s, l := c.hsl()
	return fromHSL(h, s, l+dl)
}

// lean rotates c's hue towards target's by at most deg degrees (shortest way
// round), keeping it saturated: a pastel theme mixed in RGB turns to grey.
func (c RGB) lean(target RGB, deg, sat float64) RGB {
	h, s, l := c.hsl()
	th, _, _ := target.hsl()
	d := math.Mod(th-h+540, 360) - 180
	if math.Abs(d) > deg {
		d = math.Copysign(deg, d)
	}
	return fromHSL(h+d, clamp(s*sat, 0, 1), l)
}

func (c RGB) fg() string {
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", int(c.R+.5), int(c.G+.5), int(c.B+.5))
}
func (c RGB) bg() string {
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", int(c.R+.5), int(c.G+.5), int(c.B+.5))
}

// Theme: the Omarchy base colours.
type Theme struct {
	Bg, DarkBg, DarkerBg, LighterBg, Fg, DarkFg, Muted, Accent RGB
	Red, Yellow, Orange, Green, Cyan, Blue, Magenta, Brown     RGB
	Dark                                                       bool
	// HP: the meaning colours (good/caution/danger, the bar kinds), picked
	// from the theme by hue rather than by key name; see semantic().
	HP    hpPalette
	path  string
	mtime time.Time
}

// themePath: the live Omarchy theme, or HERDRMON_THEME (a theme name like
// "lumon", or a path to a colors.toml) to preview another one.
func themePath() string {
	if v := os.Getenv("HERDRMON_THEME"); v != "" {
		if strings.Contains(v, "/") {
			return v
		}
		for _, dir := range []string{".config/omarchy/themes", ".local/share/omarchy/themes"} {
			p := filepath.Join(os.Getenv("HOME"), dir, v, "colors.toml")
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return filepath.Join(os.Getenv("HOME"), ".local/state/omarchy/current/theme/colors.toml")
}

var fallback = map[string]string{
	"background": "#1e1e2e", "dark_background": "#161622", "darker_background": "#101019",
	"lighter_background": "#313244", "foreground": "#cdd6f4", "dark_foreground": "#6c7086",
	"muted": "#585b70", "accent": "#89b4fa", "red": "#f38ba8", "yellow": "#f9e2af",
	"orange": "#fab387", "green": "#a6e3a1", "cyan": "#94e2d5", "blue": "#89b4fa",
	"magenta": "#f5c2e7", "brown": "#7b5b55", "mode": "dark",
}

func loadTheme() *Theme { return loadThemeFrom(themePath()) }

func loadThemeFrom(path string) *Theme {
	t := &Theme{path: path}
	kv := map[string]string{}
	for k, v := range fallback {
		kv[k] = v
	}
	if b, err := os.ReadFile(t.path); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if k, v, ok := strings.Cut(l, "="); ok {
				kv[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
			}
		}
		if st, err := os.Stat(t.path); err == nil {
			t.mtime = st.ModTime()
		}
	}
	get := func(k string, alt ...string) RGB {
		for _, key := range append([]string{k}, alt...) {
			if c, ok := hex(kv[key]); ok {
				return c
			}
		}
		c, _ := hex(fallback[k])
		return c
	}
	t.Bg, t.Fg = get("background"), get("foreground")
	t.DarkBg = get("dark_background", "background")
	t.DarkerBg = get("darker_background", "dark_background", "background")
	t.LighterBg = get("lighter_background", "selection")
	t.DarkFg = get("dark_foreground", "muted")
	t.Muted = get("muted", "dark_foreground")
	t.Accent = get("accent", "blue")
	t.Red, t.Yellow, t.Green = get("red"), get("yellow"), get("green")
	t.Orange = get("orange", "bright_red", "red")
	t.Cyan, t.Blue, t.Magenta = get("cyan"), get("blue"), get("magenta")
	t.Brown = get("brown", "dark_foreground")
	_, _, bgL := t.Bg.hsl()
	t.Dark = kv["mode"] != "light" && bgL < .5
	t.HP = semantic(kv, t.Dark)
	return t
}

/* ------------------------------------------------------ meaning colours */

// hpPalette: colours that must keep their meaning in every theme.
type hpPalette struct{ Red, Orange, Yellow, Green, Cyan, Blue RGB }

// A meaning colour's hue window (degrees) and the theme keys tried first.
type role struct {
	h, w float64 // centre and half-width
	keys []string
}

var roles = [...]role{
	{0, 20, []string{"red", "bright_red"}},
	{27, 12, []string{"orange"}},
	{48, 13, []string{"yellow", "bright_yellow"}},
	{118, 33, []string{"green", "bright_green"}},
	{180, 22, []string{"cyan", "bright_cyan"}},
	{215, 25, []string{"blue", "bright_blue", "accent"}},
}

// semantic picks the meaning colours from a theme. Omarchy themes don't all
// keep "red" red: lumon's red/yellow/green are all blues, matte-black's green
// is amber and its blue orange, solitude and vantablack are grey, hackerman's
// red is green. So each role takes, in order:
//  1. its own key, if that colour is saturated and inside the role's hue
//     window;
//  2. any other theme colour that is;
//  3. a colour made at the role's hue, with the theme's typical saturation
//     and its own key's lightness, so it still sits in the theme's style.
//
// Lightness is then kept readable on the theme's background.
func semantic(kv map[string]string, dark bool) hpPalette {
	type cand struct {
		c       RGB
		h, s, l float64
	}
	var pool []cand
	var sats, lights []float64
	for _, k := range []string{"red", "orange", "yellow", "green", "cyan", "blue", "magenta", "accent",
		"bright_red", "bright_yellow", "bright_green", "bright_cyan", "bright_blue", "bright_magenta"} {
		c, ok := hex(kv[k])
		if !ok {
			continue
		}
		h, sat, l := c.hsl()
		pool = append(pool, cand{c, h, sat, l})
		if sat >= .3 && l > .12 && l < .92 {
			sats = append(sats, sat)
			lights = append(lights, l)
		}
	}
	median := func(xs []float64, def float64) float64 {
		if len(xs) == 0 {
			return def
		}
		ys := append([]float64(nil), xs...)
		for i := range ys {
			for j := i + 1; j < len(ys); j++ {
				if ys[j] < ys[i] {
					ys[i], ys[j] = ys[j], ys[i]
				}
			}
		}
		return ys[len(ys)/2]
	}
	satT := clamp(median(sats, .5), .45, .85)
	lT := median(lights, .65)
	lo, hi := .52, .80 // readable on a dark background
	if !dark {
		lo, hi = .36, .56
	}
	usable := func(c cand) bool { return c.s >= .3 && c.l > .12 && c.l < .92 }
	dist := func(a, b float64) float64 { return math.Abs(math.Mod(a-b+540, 360) - 180) }
	pick := func(r role) RGB {
		var out RGB
		found := false
		for _, k := range r.keys {
			if c, ok := hex(kv[k]); ok {
				h, sat, l := c.hsl()
				if usable(cand{c, h, sat, l}) && dist(h, r.h) <= r.w {
					out, found = c, true
					break
				}
			}
		}
		if !found {
			best := r.w + 1
			for _, c := range pool {
				if usable(c) && dist(c.h, r.h) < best {
					best, out, found = dist(c.h, r.h), c.c, true
				}
			}
		}
		if !found {
			l := lT
			if len(sats) < 3 {
				// a greyscale theme (vantablack, white, solitude): its
				// greys carry no meaning, so use one even lightness
				l = .66
				if !dark {
					l = .44
				}
			} else if c, ok := hex(kv[r.keys[0]]); ok {
				if _, _, kl := c.hsl(); kl > .12 && kl < .92 {
					l = kl
				}
			}
			out = fromHSL(r.h, satT, l)
		}
		h, sat, l := out.hsl()
		// pastel and muted themes keep their character, but a meaning colour
		// needs enough colour to be read as one
		return fromHSL(h, math.Max(sat, .55), clamp(l, lo, hi))
	}
	return hpPalette{pick(roles[0]), pick(roles[1]), pick(roles[2]), pick(roles[3]), pick(roles[4]), pick(roles[5])}
}

// turn rotates the hue by deg (positive = towards yellow/green/blue), scales
// saturation and shifts lightness, keeping the result readable.
func (c RGB) turn(deg, sat, dl float64) RGB {
	h, s, l := c.hsl()
	return fromHSL(h+deg, clamp(s*sat, 0, 1), clamp(l+dl, .2, .9))
}

func (t *Theme) changed() bool {
	st, err := os.Stat(themePath())
	return err == nil && !st.ModTime().Equal(t.mtime)
}

/* ---------------------------------------------------------------- bars */

type BarKind int

const (
	NET BarKind = iota // connection: leans blue at every stage
	SYS                // machine health: the plain FRLG green/yellow/red
	BAT                // battery: leans yellow at every stage
)

var barTags = [...]string{"NET", "SYS", "BAT"}

// stages returns the good / mid / low colours of a bar kind. The three kinds
// share FRLG's green -> yellow -> red meaning, each pulled towards its own
// hue so the bars stay apart at every level.
func (t *Theme) stages(k BarKind) (good, mid, low RGB) {
	// SYS is the reference: the theme's green / yellow / red (by meaning,
	// see semantic). NET turns each stage towards blue and pales it, BAT
	// turns it towards yellow/orange and deepens it. The turns are fixed
	// angles in a fixed direction, so they work whatever the theme's hues.
	p := t.HP
	good, mid, low = p.Green, p.Yellow, p.Red
	gh, _, _ := good.hsl()
	yh, _, _ := mid.hsl()
	// On a dark background NET is the palest bar and BAT the deepest; on a
	// light one the bars must stay darker than their pale tracks, so NET
	// keeps the theme's lightness and BAT goes deeper still.
	netL, batL := .06, -.11
	if !t.Dark {
		netL, batL = -.02, -.14
	}
	if t.Dark {
		// a very pale caution colour (vantablack, catppuccin) loses its hue;
		// keep it at most 78 % light so NET's lime and SYS's amber differ
		if h, sat, l := mid.hsl(); l > .78 {
			mid = fromHSL(h, sat, .78)
		}
	}
	switch k {
	case NET:
		// cool: teal / limey cream / rose
		return good.turn(clamp(math.Max(gh+38, 172), 0, 195)-gh, 1.05, netL),
			mid.turn(44, .95, netL-.05), // lime
			low.turn(-40, 1.0, netL+.04)
	case BAT:
		// golden: yellow-green / deep gold / burnt orange
		bgood := clamp(gh-40, yh+24, gh)
		lh, _, _ := low.hsl()
		lowTo := 29.0 // burnt orange, never as far as amber
		if lh > 180 {
			lh -= 360
		}
		return good.turn(bgood-gh, 1.3, batL),
			mid.turn(4, 1.5, batL-.03),
			low.turn(clamp(lowTo-lh, 18, 31), 1.3, batL+.02)
	}
	// SYS danger: the theme's red, pulled to a true red if it leans orange
	lh, _, _ := low.hsl()
	if lh < 180 && lh > 0 {
		low = low.turn(-math.Min(10, lh), 1, 0)
	}
	return good, mix(mid, p.Orange, .25), low
}

// tag: the colour of a bar's NET/SYS/BAT pill, the purest hue of its kind.
func (t *Theme) tag(k BarKind) RGB {
	switch k {
	case NET:
		return t.HP.Blue
	case BAT:
		return t.HP.Yellow
	}
	return t.HP.Green
}

// track: the empty part of a bar, a deep shade of its kind, so even an empty
// or fainted bar still says which one it is.
func (t *Theme) track(k BarKind) (top, bot RGB) {
	c := t.tag(k)
	return mix(t.DarkerBg, c, .16), mix(t.DarkerBg, c, .08)
}

// level: FRLG thresholds (green > 50 %, yellow > 20 %, red), blended over a
// few percent so a draining bar changes colour smoothly instead of snapping.
func (t *Theme) level(k BarKind, v float64) RGB {
	good, mid, low := t.stages(k)
	switch {
	case v >= .54:
		return good
	case v > .46:
		return mix(mid, good, (v-.46)/.08)
	case v >= .23:
		return mid
	case v > .17:
		return mix(low, mid, (v-.17)/.06)
	}
	return low
}

// smooth 0..1 -> 0..1
func ease(x float64) float64 { x = clamp(x, 0, 1); return x * x * (3 - 2*x) }

func pulse(x float64) float64 { return .5 + .5*math.Cos(2*math.Pi*x) }
