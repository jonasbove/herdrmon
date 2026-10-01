package main

// herdrmon doctor: what's installed, what's missing, and what to run.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

func doctorMain() bool {
	ok := true
	line := func(mark, what, detail string) {
		fmt.Printf("  %s %-14s %s\n", mark, what, detail)
	}
	good := func(what, detail string) { line("✓", what, detail) }
	warn := func(what, detail string) { line("!", what, detail) }
	bad := func(what, detail string) { line("✗", what, detail); ok = false }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	p := resolvePaths()
	fmt.Println("herdrmon doctor")

	cfg, err := loadConfig(p.Config)
	switch {
	case err != nil:
		bad("config", err.Error())
	case fileExists(p.Config):
		good("config", fmt.Sprintf("%s (%d devices)", p.Config, len(cfg.Devices)))
	default:
		warn("config", p.Config+" missing; using defaults")
	}
	for _, d := range cfg.Devices {
		if d.Mon != "" {
			if _, found := lookupSpecies(d.Mon); !found {
				bad("config", fmt.Sprintf("device %q: unknown mon %q", d.Name, d.Mon))
			}
		}
		switch d.Transport {
		case "", "local", "tailscale", "ssh", "http", "none":
		default:
			bad("config", fmt.Sprintf("device %q: unknown transport %q", d.Name, d.Transport))
		}
	}
	good("self", cfg.selfName())

	// multiplexer
	mux := detectMux(ctx, cfg)
	if mux.Kind == "tuios" {
		if pingServer(ctx, mux.Socket) == "tuios" {
			good("tuios", "answers on "+mux.Socket)
		} else {
			bad("tuios", "no answer on "+mux.Socket)
		}
		if _, err := exec.LookPath("tuios"); err != nil {
			warn("tuios cli", "not on PATH: remote devices can't be opened")
		}
	} else if haveHerdr() {
		v, _ := run(ctx, herdrBin(), "--version")
		good("herdr", strings.TrimSpace(string(v)))
	} else {
		bad("herdr", "not installed (https://herdr.dev), and no TUIOS running")
	}

	// discovery sources
	if cfg.Discovery.Tailscale {
		if _, err := exec.LookPath("tailscale"); err != nil {
			warn("tailscale", "not installed; tailnet discovery skipped")
		} else if self, _ := tailnet(ctx); self == nil {
			warn("tailscale", "installed but not answering (logged out?)")
		} else {
			good("tailscale", "up")
		}
	}

	// font and frames
	for _, c := range []string{"uv", "node", "fc-list"} {
		if path, err := exec.LookPath(c); err == nil {
			good(c, path)
		} else {
			warn(c, "not on PATH")
		}
	}
	if fileExists(p.Font) {
		good("font", p.Font)
	} else {
		bad("font", p.Font+" missing")
	}
	if b, err := exec.CommandContext(ctx, "fc-list", ":family=Herdrmon Icons", "family").Output(); err == nil {
		if strings.TrimSpace(string(b)) != "" {
			good("fontconfig", "sees \"Herdrmon Icons\"")
		} else {
			bad("fontconfig", "doesn't see \"Herdrmon Icons\" (fc-cache -f?)")
		}
	}
	f := loadFrames(p.Frames)
	if f.mtime.IsZero() {
		bad("frames", p.Frames+" missing")
	} else {
		good("frames", fmt.Sprintf("%s (v%d, %d mons)", p.Frames, f.Version, len(f.Mons)))
		var missing []string
		for _, d := range discover(ctx, cfg, mux) {
			if _, found := f.Mons[d.Mon]; !found {
				missing = append(missing, d.Node+"="+d.Mon)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			warn("frames", "not baked yet: "+strings.Join(missing, ", "))
		}
	}
	if !ok || !fileExists(p.Font) {
		fmt.Println()
		fmt.Println("  rebuild the font: " + buildCommand(p))
	}
	return ok
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// buildCommand: how to (re)build the font and frames.
func buildCommand(p Paths) string {
	if p.BuildPy != "" {
		return "uv run " + p.BuildPy
	}
	return "uv run <herdrmon checkout>/font/build.py   (set HERDRMON_SRC to find it)"
}
