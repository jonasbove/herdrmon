package main

import "testing"

// A species in five cells, "." blank: it rests in cells 1-4 and leaves them
// only to be sent out, recalled or to tackle.
var railSpecies = Anim{
	"base":    {".ABCD"},
	"bob":     {".ABCD", ".EFGH"},
	"poison":  {".IJKL"},
	"sendout": {"oo..."},
	"recall":  {"oABCD"},
	"tackle":  {"ABCD."},
}

func ptr(s string) *string { return &s }

func TestRailBoxIsTheRestingCells(t *testing.T) {
	box, ok := newRailBox(railSpecies)
	if !ok || box.lo != 1 || box.hi != 4 || box.blank != '.' {
		t.Fatalf("box = %+v, %v", box, ok)
	}
	// Pikachu-like: two blank cells in front at rest, one in a poison frame.
	box, _ = newRailBox(Anim{"base": {"..ABC"}, "poison": {".XABC"}})
	if box.lo != 1 || box.hi != 4 {
		t.Fatalf("box = %+v", box)
	}
}

func TestRailValueCropsAndHoldsThroughMoves(t *testing.T) {
	if got := railValue(railSpecies, ptr(".EFGH"), nil); got == nil || *got != "EFGH" {
		t.Fatalf("bob frame: %v", got)
	}
	prev := ptr("IJKL")
	for _, frame := range []string{"oo...", "oABCD", "ABCD."} {
		if got := railValue(railSpecies, ptr(frame), prev); got != prev {
			t.Errorf("%q: %v, want the previous rail", frame, got)
		}
	}
	// A send-out before anything was shown: the still frame.
	if got := railValue(railSpecies, ptr("oo..."), nil); got == nil || *got != "ABCD" {
		t.Errorf("first frame mid-move: %v", got)
	}
}

func TestRailValuePassesThroughWhatIsNotASprite(t *testing.T) {
	if railValue(railSpecies, nil, ptr("ABCD")) != nil {
		t.Error("a cleared $mon clears the rail")
	}
	if got := railValue(railSpecies, ptr(""), ptr("ABCD")); got == nil || *got != "" {
		t.Errorf("no agents: %v", got)
	}
	glyph := coloured("overlay1", "S")
	if got := railValue(nil, &glyph, nil); got == nil || *got != glyph {
		t.Errorf("fallback glyph: %v", got)
	}
}

// Every species in a real frames.json: the rail is the same width in every
// frame, so the collapsed sidebar's outline never jumps.
func TestRailWidthIsStableForEveryFrame(t *testing.T) {
	frames := newFrames(framesPath(), func(string) bool { return true })
	if !frames.Load() {
		t.Skip("no frames.json here")
	}
	for mon := range frames.mons {
		species := frames.Anim(mon)
		box, ok := newRailBox(species)
		if !ok {
			t.Fatalf("%s: no resting cells", mon)
		}
		var prev *string
		for name, list := range species {
			for _, f := range list {
				prev = railValue(species, ptr(f), prev)
				if n := len([]rune(*prev)); n != box.hi-box.lo+1 {
					t.Errorf("%s %s %q: rail %d cells, want %d", mon, name, f, n, box.hi-box.lo+1)
				}
			}
		}
	}
}
