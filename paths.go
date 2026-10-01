package main

// XDG paths (DESIGN.md "Paths"); every one can be overridden by env.

import (
	"os"
	"path/filepath"
	"strings"
)

type Paths struct {
	Config  string `json:"config"`
	Assets  string `json:"assets"`
	Font    string `json:"font"`
	Frames  string `json:"frames"`
	State   string `json:"state"`
	Src     string `json:"src"`      // repo checkout holding font/build.py; "" = not found
	BuildPy string `json:"build_py"` // Src/font/build.py, or ""
}

func xdg(env, fallback string) string {
	if v := os.Getenv(env); v != "" && filepath.IsAbs(v) {
		return v
	}
	return filepath.Join(home(), fallback)
}

func home() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return os.Getenv("HOME")
}

// expand: "~/x" -> $HOME/x.
func expand(p string) string {
	if p == "~" {
		return home()
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home(), p[2:])
	}
	return p
}

func envOr(env, def string) string {
	if v := os.Getenv(env); v != "" {
		return expand(v)
	}
	return def
}

func resolvePaths() Paths {
	cfg := xdg("XDG_CONFIG_HOME", ".config")
	cache := xdg("XDG_CACHE_HOME", ".cache")
	data := xdg("XDG_DATA_HOME", ".local/share")
	state := xdg("XDG_STATE_HOME", ".local/state")
	p := Paths{
		Config: envOr("HERDRMON_CONFIG", filepath.Join(cfg, "herdrmon", "config.toml")),
		Assets: envOr("HERDRMON_ASSETS", filepath.Join(cache, "herdrmon", "assets")),
		Font:   envOr("HERDRMON_FONT", filepath.Join(data, "fonts", "HerdrmonIcons.otf")),
		Frames: envOr("HERDRMON_FRAMES", filepath.Join(data, "herdrmon", "frames.json")),
		State:  filepath.Join(state, "herdrmon"),
	}
	p.Src = findSrc(data)
	if p.Src != "" {
		p.BuildPy = filepath.Join(p.Src, "font", "build.py")
	}
	return p
}

// findSrc: the checkout with font/build.py. HERDRMON_SRC wins; then next to
// the binary (go build in the repo), the working directory, and
// $XDG_DATA_HOME/herdrmon/src (where an installer may keep a checkout).
func findSrc(data string) string {
	var cands []string
	if v := os.Getenv("HERDRMON_SRC"); v != "" {
		cands = append(cands, expand(v))
	}
	if exe, err := os.Executable(); err == nil {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
		cands = append(cands, filepath.Dir(exe))
	}
	if wd, err := os.Getwd(); err == nil {
		cands = append(cands, wd)
	}
	cands = append(cands, filepath.Join(data, "herdrmon", "src"))
	for _, c := range cands {
		if _, err := os.Stat(filepath.Join(c, "font", "build.py")); err == nil {
			return c
		}
	}
	return ""
}
