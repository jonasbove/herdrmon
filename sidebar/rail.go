package main

// $mon_rail: the Pokémon for Herdr's collapsed sidebar, as workspace
// metadata. A patched Herdr ([ui.sidebar.collapsed], token "mon_rail") draws
// it alone in the narrow rail, outlined in the colour of the workspace's
// status icon; a stock Herdr ignores it.
//
// $mon is a fixed run of frames.json cells with blank ones around the
// Pokémon, so it can move about in them (sendout, recall and tackle reach the
// left edge). The rail's outline hugs the Pokémon, so $mon_rail is cropped to
// the cells the species uses while it stays put - the same width in every
// frame, so the outline never jumps - and a frame that reaches outside them
// leaves the rail on the last one that fitted.

// railMoves are the animations that leave the resting cells.
var railMoves = map[string]bool{"sendout": true, "recall": true, "tackle": true}

// railBox: the cells [lo, hi] the species uses at rest, and the blank cell.
type railBox struct {
	lo, hi int
	blank  rune
}

// newRailBox finds the resting cells. The blank cell is whatever the still
// frame starts with; if that's part of the Pokémon (a species that fills the
// first cell), nothing is cropped on the left.
func newRailBox(a Anim) (railBox, bool) {
	still := a["base"]
	if len(still) == 0 {
		still = a["bob"]
	}
	if len(still) == 0 || still[0] == "" {
		return railBox{}, false
	}
	b := railBox{lo: -1, hi: -1, blank: []rune(still[0])[0]}
	for name, frames := range a {
		if railMoves[name] {
			continue
		}
		for _, f := range frames {
			for i, r := range []rune(f) {
				if r == b.blank {
					continue
				}
				if b.lo < 0 || i < b.lo {
					b.lo = i
				}
				if i > b.hi {
					b.hi = i
				}
			}
		}
	}
	return b, b.lo >= 0
}

// crop: the frame's resting cells, or false when it draws outside them.
func (b railBox) crop(frame string) (string, bool) {
	cells := []rune(frame)
	if b.hi >= len(cells) {
		return "", false
	}
	for i, r := range cells {
		if (i < b.lo || i > b.hi) && r != b.blank {
			return "", false
		}
	}
	return string(cells[b.lo : b.hi+1]), true
}

// railValue is $mon_rail for a workspace whose $mon is value: cropped for a
// species, the same as $mon for the fallback glyph or an empty $mon (a
// workspace without agents), nil when $mon is cleared, and prev when the
// frame is mid-move.
func railValue(species Anim, value, prev *string) *string {
	if value == nil {
		return nil
	}
	if species == nil || *value == "" {
		return value
	}
	box, ok := newRailBox(species)
	if !ok {
		return value
	}
	cropped, fits := box.crop(*value)
	if !fits {
		if prev != nil {
			return prev
		}
		// Nothing shown yet (a send-out as the first frame): the still frame.
		if base := species["base"]; len(base) > 0 {
			if still, ok := box.crop(base[0]); ok {
				return &still
			}
		}
		return value
	}
	return &cropped
}
