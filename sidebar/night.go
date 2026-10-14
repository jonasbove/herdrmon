package main

// Is it night? (The Pokémon sleeps unless an agent is working.)
//
//   [sidebar] night_command = "..."  run with sh -c; exit 0 = night, else day
//   night_command = ""               clock only
//   unset (auto)                     Hyprland with hyprsunset on PATH: night
//                                    while `hyprctl hyprsunset temperature`
//                                    is below night_below_k (the usual
//                                    night-light test); otherwise the clock
//   clock                            night_start:00 to night_end:00 local
//
// Checked at most every 30 s, off the frame path.

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const nightCheckMS = 30000

func onPath(bin string) bool {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, bin)); err == nil {
			return true
		}
	}
	return false
}

func byClock(t time.Time, start, end float64) bool {
	h := float64(t.Hour())
	if start > end {
		return h >= start || h < end
	}
	return h >= start && h < end
}

func hyprlandRunning() bool {
	if os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "" {
		return true
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = "/run/user/" + strconv.Itoa(os.Getuid())
	}
	entries, err := os.ReadDir(filepath.Join(dir, "hypr"))
	return err == nil && len(entries) > 0
}

type NightPlan struct {
	Mode    string // "command" | "hyprsunset" | "clock"
	Command string
}

func nightPlan(sb Sidebar) NightPlan {
	if cmd, ok := sb.get("night_command").(string); ok {
		if jsTrim(cmd) != "" {
			return NightPlan{Mode: "command", Command: cmd}
		}
		return NightPlan{Mode: "clock"}
	}
	if onPath("hyprctl") && onPath("hyprsunset") && hyprlandRunning() {
		// The daemon may have been started without Hyprland's env: find the
		// newest instance.
		return NightPlan{
			Mode:    "hyprsunset",
			Command: `HYPRLAND_INSTANCE_SIGNATURE=${HYPRLAND_INSTANCE_SIGNATURE:-$(ls -t "${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/hypr" | head -1)} hyprctl hyprsunset temperature`,
		}
	}
	return NightPlan{Mode: "clock"}
}

type Night struct {
	sb      Sidebar
	Plan    NightPlan
	mu      sync.Mutex
	on      bool
	checked int64
	pending bool
}

func newNight(sb Sidebar) *Night { return &Night{sb: sb, Plan: nightPlan(sb)} }

var reDigits = regexp.MustCompile(`\d+`)

func numOr(v any, def float64) float64 {
	if v == nil {
		return def
	}
	return toNumber(v)
}

// Check answers at once with the last known state and refreshes it in the
// background at most every nightCheckMS.
func (n *Night) Check(now int64) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.pending || now-n.checked < nightCheckMS {
		return n.on
	}
	n.checked = now
	if n.Plan.Mode == "clock" {
		n.on = byClock(time.UnixMilli(now), numOr(n.sb.get("night_start"), 22), numOr(n.sb.get("night_end"), 7))
		return n.on
	}
	n.pending = true
	below := n.sb.num("night_below_k")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "sh", "-c", n.Plan.Command).Output()
		n.mu.Lock()
		defer n.mu.Unlock()
		n.pending = false
		if n.Plan.Mode == "command" {
			n.on = err == nil
			return
		}
		// hyprsunset not running answers with an error: that's daytime.
		k := math.NaN()
		if err == nil {
			if m := reDigits.FindString(strings.TrimSpace(string(out))); m != "" {
				k, _ = strconv.ParseFloat(m, 64)
			}
		}
		n.on = !math.IsNaN(k) && k < below
	}()
	return n.on
}
