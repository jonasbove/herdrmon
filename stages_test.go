package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
)

// go test -run TestStageSheet -v: testdata/stages.png, three strips (NET, SYS,
// BAT) of the bar colour from 100 % HP (left) to 0 % (right), and the hue
// of every stage colour.
func TestStageSheet(t *testing.T) {
	th := loadTheme()
	const W, H, gap = 600, 40, 10
	img := image.NewRGBA(image.Rect(0, 0, W, 3*(H+gap)))
	for k := 0; k < 3; k++ {
		for x := 0; x < W; x++ {
			c := th.level(BarKind(k), 1-float64(x)/float64(W-1))
			for y := 0; y < H; y++ {
				img.Set(x, k*(H+gap)+y, color.RGBA{uint8(c.R), uint8(c.G), uint8(c.B), 255})
			}
		}
		g, m, l := th.stages(BarKind(k))
		hs := func(c RGB) string { h, s, li := c.hsl(); return fmt.Sprintf("h%3.0f s%.2f l%.2f", h, s, li) }
		t.Logf("%s  good %s | mid %s | low %s", barTags[k], hs(g), hs(m), hs(l))
	}
	f, _ := os.Create("testdata/stages.png")
	defer f.Close()
	png.Encode(f, img)
}
