package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// TestAllThemes: in every installed Omarchy theme, at every HP stage, NET
// must stay the bluest bar and BAT the yellowest, and each kind must still
// read good -> caution -> danger (low is the reddest of its three stages).
func TestAllThemes(t *testing.T) {
	home := os.Getenv("HOME")
	paths, _ := filepath.Glob(filepath.Join(home, ".local/share/omarchy/themes/*/colors.toml"))
	more, _ := filepath.Glob(filepath.Join(home, ".config/omarchy/themes/*/colors.toml"))
	paths = append(paths, more...)
	if len(paths) == 0 {
		t.Skip("no Omarchy themes installed")
	}
	blue := func(c RGB) float64 { return c.B - c.R }
	for _, p := range paths {
		th := loadThemeFrom(p)
		name := filepath.Base(filepath.Dir(p))
		for _, v := range []float64{.92, .38, .12} {
			net, sys, bat := th.level(NET, v), th.level(SYS, v), th.level(BAT, v)
			if !(blue(net) > blue(sys) && blue(net) > blue(bat)) {
				t.Errorf("%s v=%.2f: NET not bluest: NET %v SYS %v BAT %v", name, v, net.hex(), sys.hex(), bat.hex())
			}
			// by hue: BAT nearest gold (50°) unless it is the same hue band
			if !(hueDist(bat, 50) < hueDist(sys, 50) || hueDist(bat, 50) < 8) || !(hueDist(bat, 50) <= hueDist(net, 50)) {
				t.Errorf("%s v=%.2f: BAT not yellowest: BAT %v SYS %v NET %v", name, v, bat.hex(), sys.hex(), net.hex())
			}
		}
		// the kinds must look different at every stage, and every fill must
		// stand out from its empty track
		for _, v := range []float64{.92, .38, .12} {
			c := [3]RGB{th.level(NET, v), th.level(SYS, v), th.level(BAT, v)}
			for i := 0; i < 3; i++ {
				for j := i + 1; j < 3; j++ {
					if d := dist(c[i], c[j]); d < 28 {
						t.Errorf("%s v=%.2f: %s %v and %s %v too alike (%.0f)", name, v, barTags[i], c[i].hex(), barTags[j], c[j].hex(), d)
					}
				}
				top, _ := th.track(BarKind(i))
				if d := dist(c[i], top); d < 70 {
					t.Errorf("%s v=%.2f: %s %v hard to see on its track %v (%.0f)", name, v, barTags[i], c[i].hex(), top.hex(), d)
				}
			}
		}
		// the selection outline must stand out from a normal slot's border
		sel := mix(mix(th.HP.Red, th.HP.Orange, .5), th.HP.Yellow, .5).saturate(1.35)
		norm := mix(th.Blue, th.Bg, .45)
		if d := dist(sel, norm); d < 60 {
			t.Errorf("%s: selected outline %v too close to normal border %v (%.0f)", name, sel.hex(), norm.hex(), d)
		}
		for _, k := range []BarKind{NET, SYS, BAT} {
			good, mid, low := th.stages(k)
			// danger reads red: its hue sits closer to red than good's does
			if hueDist(low, 0) >= hueDist(good, 0) || hueDist(low, 0) > hueDist(mid, 0)+1 {
				t.Errorf("%s %s: low %v doesn't read as danger (good %v mid %v)", name, barTags[k], low.hex(), good.hex(), mid.hex())
			}
		}
	}
}

func (c RGB) hex() string {
	return fmt.Sprintf("#%02x%02x%02x", int(c.R+.5), int(c.G+.5), int(c.B+.5))
}

// dist: redmean colour distance, a cheap perceptual approximation.
func dist(a, b RGB) float64 {
	rm := (a.R + b.R) / 2
	dr, dg, db := a.R-b.R, a.G-b.G, a.B-b.B
	return math.Sqrt((2+rm/256)*dr*dr+4*dg*dg+(2+(255-rm)/256)*db*db) / 3
}

func hueDist(c RGB, h float64) float64 {
	ch, _, _ := c.hsl()
	d := ch - h
	for d > 180 {
		d -= 360
	}
	for d < -180 {
		d += 360
	}
	if d < 0 {
		return -d
	}
	return d
}

// go test -run TestThemeSheet: testdata/themes.png, one row per theme on its
// own background: NET, SYS, BAT strips from 100 % HP (left) to 0 % (right),
// each on its empty track.
func TestThemeSheet(t *testing.T) {
	home := os.Getenv("HOME")
	paths, _ := filepath.Glob(filepath.Join(home, ".local/share/omarchy/themes/*/colors.toml"))
	if len(paths) == 0 {
		t.Skip("no themes")
	}
	const W, bh, pad = 300, 8, 6
	rowH := 3*bh + 4*pad/2 + pad
	img := image.NewRGBA(image.Rect(0, 0, W+2*pad, len(paths)*rowH))
	set := func(x, y int, c RGB) { img.Set(x, y, color.RGBA{uint8(c.R), uint8(c.G), uint8(c.B), 255}) }
	for r, p := range paths {
		th := loadThemeFrom(p)
		y0 := r * rowH
		for y := 0; y < rowH; y++ {
			for x := 0; x < W+2*pad; x++ {
				set(x, y0+y, th.Bg)
			}
		}
		for k := 0; k < 3; k++ {
			top, _ := th.track(BarKind(k))
			for x := 0; x < W; x++ {
				c := th.level(BarKind(k), 1-float64(x)/float64(W-1))
				for y := 0; y < bh; y++ {
					col := c
					if y >= bh-2 {
						col = top
					}
					set(pad+x, y0+pad+k*(bh+pad/2)+y, col)
				}
			}
		}
		t.Logf("row %2d %s", r, filepath.Base(filepath.Dir(p)))
	}
	os.MkdirAll("testdata", 0o755)
	f, _ := os.Create("testdata/themes.png")
	defer f.Close()
	png.Encode(f, img)
}
