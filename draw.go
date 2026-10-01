package main

// Drawing: a cell canvas (truecolour), FRLG-style slots, gradient HP bars and
// the Harden shine. Bars use the upper-half block so every cell is two pixels
// tall: a lit top row over a deeper bottom row, like the game's two-tone bar.

import (
	"math"
	"strings"
	"time"
)

type Cell struct {
	ch     string
	fg, bg RGB
	bold   bool
}

type Canvas struct {
	w, h  int
	cells []Cell
}

func newCanvas(w, h int, bg func(x, y int) RGB) *Canvas {
	c := &Canvas{w: w, h: h, cells: make([]Cell, w*h)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c.cells[y*w+x] = Cell{ch: " ", bg: bg(x, y)}
		}
	}
	return c
}

func (c *Canvas) at(x, y int) *Cell {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return nil
	}
	return &c.cells[y*c.w+x]
}

// text writes s keeping each cell's background.
func (c *Canvas) text(x, y int, s string, fg RGB, bold bool) int {
	for _, r := range s {
		if cl := c.at(x, y); cl != nil {
			cl.ch, cl.fg, cl.bold = string(r), fg, bold
		}
		x++
	}
	return x
}

// pill: text on its own background, padded by a space each side.
func (c *Canvas) pill(x, y int, s string, fg, bg RGB) int {
	for i, r := range " " + s + " " {
		if cl := c.at(x+i, y); cl != nil {
			cl.ch, cl.fg, cl.bg, cl.bold = string(r), fg, bg, true
		}
	}
	return x + len(s) + 2
}

func (c *Canvas) String() string {
	var b strings.Builder
	for y := 0; y < c.h; y++ {
		var pf, pb RGB
		var pbold, first = false, true
		for x := 0; x < c.w; x++ {
			cl := c.cells[y*c.w+x]
			if first || cl.bold != pbold {
				if cl.bold {
					b.WriteString("\x1b[1m")
				} else {
					b.WriteString("\x1b[22m")
				}
			}
			if first || cl.fg != pf {
				b.WriteString(cl.fg.fg())
			}
			if first || cl.bg != pb {
				b.WriteString(cl.bg.bg())
			}
			pf, pb, pbold, first = cl.fg, cl.bg, cl.bold, false
			b.WriteString(cl.ch)
		}
		b.WriteString("\x1b[0m")
		if y < c.h-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

/* --------------------------------------------------------------- slots */

type slotLook int

const (
	lookNormal slotLook = iota
	lookSelected
	lookFainted
	lookFaintedSelected
)

// slot paints a rounded box with a left-to-right gradient fill. Selected
// borders breathe between two warm theme colours.
func (v *view) slot(x, y, w, h int, look slotLook) {
	t := v.th
	var a, b, border RGB
	breathe := ease(pulse(v.sec / 1.6))
	switch look {
	case lookNormal:
		a, b = mix(t.Bg, t.Blue, .20), mix(t.Bg, t.Cyan, .09)
		border = mix(t.Blue, t.Bg, .45)
	case lookSelected:
		a, b = mix(t.Bg, t.Blue, .36), mix(t.Bg, t.Cyan, .20)
		border = mix(mix(t.HP.Red, t.HP.Orange, .5), t.HP.Yellow, breathe).saturate(1.35)
	case lookFainted:
		a, b = mix(mix(t.Bg, t.Brown, .45), t.HP.Red, .18), mix(mix(t.Bg, t.Brown, .25), t.HP.Red, .06)
		border = mix(mix(t.HP.Red, t.Brown, .45), t.Bg, .25)
	case lookFaintedSelected:
		a, b = mix(mix(t.Bg, t.Brown, .45), t.HP.Red, .32), mix(t.Bg, t.HP.Red, .16)
		border = mix(t.HP.Red, mix(t.HP.Orange, t.HP.Yellow, .5), breathe).saturate(1.35)
	}
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			cl := v.cv.at(x+i, y+j)
			if cl == nil {
				continue
			}
			edge := i == 0 || j == 0 || i == w-1 || j == h-1
			g := float64(i) / float64(max(1, w-1))
			if !edge { // border lines sit on the backdrop; the fill stays inside them
				cl.bg = mix(a, b, g)
			}
			if !edge {
				continue
			}
			ch := "─"
			switch {
			case i == 0 && j == 0:
				ch = "╭"
			case i == w-1 && j == 0:
				ch = "╮"
			case i == 0 && j == h-1:
				ch = "╰"
			case i == w-1 && j == h-1:
				ch = "╯"
			case i == 0 || i == w-1:
				ch = "│"
			}
			cl.ch, cl.fg, cl.bold = ch, border, false
		}
	}
}

/* ---------------------------------------------------------------- bars */

// Shimmer: on every value update a soft vertical band of light sweeps once
// along the bar, left to right. Brightness falls off as a gaussian over
// several cells, so the band reads as a glow rather than as blocks.
const (
	shimmerDur   = 900 * time.Millisecond
	shimmerWidth = .10 // gaussian sigma, as a fraction of the bar
	shimmerPeak  = .16 // at most this far towards white
)

// shimmerAt: brightness boost 0..shimmerPeak at bar position pos (0..1).
func shimmerAt(pos float64, since time.Duration) float64 {
	if since < 0 || since >= shimmerDur {
		return 0
	}
	p := float64(since) / float64(shimmerDur)
	centre := -.25 + 1.5*ease(p)  // enters and leaves fully off the bar
	fade := math.Sin(math.Pi * p) // and fades in and out on the way
	d := (pos - centre) / shimmerWidth
	return shimmerPeak * fade * math.Exp(-d*d/2)
}

type barState struct {
	kind    BarKind
	known   bool
	shown   float64 // tweened value
	shimmer time.Time
	row     int
}

// bar draws n cells at (x, y). Colour depends only on the position along the
// bar: the same in both halves of a cell, and the same for every bar row.
func (v *view) bar(x, y, n int, bs barState) {
	t := v.th
	trackTop, trackBot := t.track(bs.kind)
	track := mix(trackTop, trackBot, .5)
	if !bs.known {
		for i := 0; i < n; i++ {
			if cl := v.cv.at(x+i, y); cl != nil {
				cl.ch, cl.bg = " ", mix(track, t.DarkerBg, .4)
			}
		}
		return
	}
	base := t.level(bs.kind, bs.shown)
	// along its length each bar drifts further into its kind's hue
	var drift RGB
	switch bs.kind {
	case NET:
		drift = base.lean(t.HP.Blue, 35, 1.1)
	case BAT:
		drift = base.lean(t.HP.Yellow, 30, 1.3)
	default:
		drift = base.lean(t.HP.Cyan, 18, 1)
	}
	white := RGB{255, 255, 255}
	since := time.Duration(-1)
	if !bs.shimmer.IsZero() {
		since = v.now.Sub(bs.shimmer)
	}
	for i := 0; i < n; i++ {
		cl := v.cv.at(x+i, y)
		if cl == nil {
			continue
		}
		pos := (float64(i) + .5) / float64(n)
		frac := clamp(bs.shown*float64(n)-float64(i), 0, 1)
		col := mix(base.scale(.84), base, ease(pos))
		col = mix(col, drift, .55*ease(pos))
		// a faint glow drifts along every bar in step, every 3.2 s
		col = mix(col, white, .05*math.Pow(pulse(pos-v.sec/3.2), 8))
		if frac > 0 {
			col = mix(col, white, shimmerAt(pos, since))
		}
		cl.ch, cl.bg, cl.bold = " ", mix(track, col, frac), false
	}
}
