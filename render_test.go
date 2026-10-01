package main

// go test -run TestRenderBars -v: writes testdata/bars.png, a sheet of every
// bar kind at full / mid / low HP across the Harden shine, for eyeballing.

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
	"time"
)

func TestRenderBars(t *testing.T) {
	th := loadTheme()
	const n, px = 40, 6 // cells per bar, pixels per half-cell
	vals := []float64{.92, .38, .12}
	steps := []int{-1, 3, 6, 9, 12, 15} // x 50 ms into the shimmer
	rowsPerStep := len(vals) * 3        // 3 kinds per value
	img := image.NewRGBA(image.Rect(0, 0, len(steps)*(n*px+px*2), rowsPerStep*(2*px+px)))
	t0 := time.Unix(1000, 0)
	for si, st := range steps {
		v := &view{th: th, now: t0, sec: 0}
		v.cv = newCanvas(n, rowsPerStep, func(x, y int) RGB { return th.Bg })
		r := 0
		for _, val := range vals {
			for k := 0; k < 3; k++ {
				bs := barState{kind: BarKind(k), row: k, known: true, shown: val}
				if st >= 0 {
					bs.shimmer = t0.Add(-time.Duration(st) * 50 * time.Millisecond)
				}
				v.bar(0, r, n, bs)
				r++
			}
		}
		ox := si * (n*px + px*2)
		for y := 0; y < rowsPerStep; y++ {
			for x := 0; x < n; x++ {
				c := v.cv.at(x, y)
				for dy := 0; dy < 2*px; dy++ {
					col := c.fg
					if dy >= px {
						col = c.bg
					}
					for dx := 0; dx < px; dx++ {
						img.Set(ox+x*px+dx, y*(3*px)+dy, color.RGBA{uint8(col.R), uint8(col.G), uint8(col.B), 255})
					}
				}
			}
		}
	}
	os.MkdirAll("testdata", 0o755)
	f, err := os.Create("testdata/bars.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	// every stage must keep the kinds apart: NET bluest, BAT yellowest
	for _, val := range vals {
		net, sys, bat := th.level(NET, val), th.level(SYS, val), th.level(BAT, val)
		if !(net.B-net.R > sys.B-sys.R && net.B-net.R > bat.B-bat.R) {
			t.Errorf("v=%.2f: NET %v is not the bluest (SYS %v, BAT %v)", val, net, sys, bat)
		}
		yl := func(c RGB) float64 { return (c.R+c.G)/2 - c.B }
		if !(yl(bat) > yl(sys) && yl(bat) > yl(net)) {
			t.Errorf("v=%.2f: BAT %v is not the yellowest (SYS %v, NET %v)", val, bat, sys, net)
		}
	}
}
