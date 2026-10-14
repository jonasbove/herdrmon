package main

// Where things live. Every path follows XDG and can be overridden by env,
// per DESIGN.md's path table.

import (
	"os"
	"path/filepath"
	"strconv"

	"herdrmon/internal/pokedex"
)

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return os.Getenv("HOME")
}

func xdgDir(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return filepath.Join(homeDir(), fallback)
}

// configPath: herdrmon's own config.toml (shared with the party TUI and the
// font build).
func configPath() string {
	if v := os.Getenv("HERDRMON_CONFIG"); v != "" {
		return v
	}
	return filepath.Join(xdgDir("XDG_CONFIG_HOME", ".config"), "herdrmon", "config.toml")
}

// framesPath: the frames manifest written by the font build.
func framesPath() string {
	if v := os.Getenv("HERDRMON_FRAMES"); v != "" {
		return v
	}
	return filepath.Join(xdgDir("XDG_DATA_HOME", ".local/share"), "herdrmon", "frames.json")
}

// stateDir: durable state, the one Herdr gives the plugin, else herdrmon's
// XDG state dir.
func stateDir() string {
	for _, env := range []string{"HERDRMON_STATE", "HERDR_PLUGIN_STATE_DIR"} {
		if v := os.Getenv(env); v != "" {
			return v
		}
	}
	return filepath.Join(xdgDir("XDG_STATE_HOME", ".local/state"), "herdrmon", "sidebar")
}

// herdrConfigPath: Herdr's own config.toml. HERDR_CONFIG_PATH is Herdr's
// override.
func herdrConfigPath() string {
	if v := os.Getenv("HERDR_CONFIG_PATH"); v != "" {
		return v
	}
	return filepath.Join(xdgDir("XDG_CONFIG_HOME", ".config"), "herdr", "config.toml")
}

// herdrSocket: the socket of the Herdr server to talk to. Herdr injects
// HERDR_SOCKET_PATH into plugin commands and panes; a bare shell gets the
// default session's.
func herdrSocket() string {
	if v := os.Getenv("HERDR_SOCKET_PATH"); v != "" {
		return v
	}
	return filepath.Join(xdgDir("XDG_CONFIG_HOME", ".config"), "herdr", "herdr.sock")
}

// fnv1a: 32-bit FNV-1a, for the lock name and default mons.
func fnv1a(text string) uint32 { return pokedex.FNV1a(text) }

// controlSocket: the daemon's lock and control socket, one per Herdr server
// so two named sessions each get their own daemon. Kept short (sun_path is
// ~108 bytes), hence the runtime dir and a hash rather than the state dir.
func controlSocket() string {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "herdrmon", "sidebar-"+strconv.FormatUint(uint64(fnv1a(herdrSocket())), 16)+".sock")
}

// ensureDir: callers degrade gracefully when it fails.
func ensureDir(dir string) string {
	_ = os.MkdirAll(dir, 0o777)
	return dir
}
