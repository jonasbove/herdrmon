package main

// config.toml (DESIGN.md "config.toml"): one file read by the TUI, the font
// build and the sidebar. Unknown keys (e.g. [agents.*]) belong to the other
// parts and are ignored here.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Self string `toml:"self"`
	// Mux: "auto" (herdr CLI, or TUIOS when that is what answers), "herdr", "tuios".
	Mux       string          `toml:"mux"`
	Font      FontConfig      `toml:"font"`
	Discovery DiscoveryConfig `toml:"discovery"`
	Night     NightConfig     `toml:"night"`
	Devices   []DeviceConfig  `toml:"device"`

	path string
}

type FontConfig struct {
	Extra       []string `toml:"extra"`
	PretEmerald string   `toml:"pret_emerald"`
	PretFirered string   `toml:"pret_firered"`
}

type DiscoveryConfig struct {
	Tailscale bool `toml:"tailscale"`
	SSHConfig bool `toml:"ssh_config"`
	Herdr     bool `toml:"herdr"`
}

// NightConfig: the SLP signal, shared with the sidebar. Command, when set,
// runs through sh; exit 0 means night. Empty: hyprsunset (below 6000 K) when
// Hyprland is running, else local time Start..End.
type NightConfig struct {
	Command string `toml:"command"`
	Start   int    `toml:"start"`
	End     int    `toml:"end"`
}

type DeviceConfig struct {
	Name         string        `toml:"name"`
	Display      string        `toml:"display"`
	Mon          string        `toml:"mon"`
	Transport    string        `toml:"transport"`
	Address      string        `toml:"address"`
	HerdrMachine string        `toml:"herdr_machine"`
	TuiosHost    string        `toml:"tuios_host"`
	Form         string        `toml:"form"`
	Battery      BatteryConfig `toml:"battery"`
}

type BatteryConfig struct {
	Source    string `toml:"source"` // sysfs | http | homeassistant | none
	URL       string `toml:"url"`
	Entity    string `toml:"entity"`
	TokenFile string `toml:"token_file"`
}

func defaultConfig() Config {
	return Config{
		Mux:       "auto",
		Discovery: DiscoveryConfig{Tailscale: true, Herdr: true},
		Night:     NightConfig{Start: 22, End: 7},
	}
}

// loadConfig: a missing file is the default config, not an error.
func loadConfig(path string) (Config, error) {
	c := defaultConfig()
	c.path = path
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if _, err := toml.Decode(string(b), &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	c.path = path
	for i := range c.Devices {
		d := &c.Devices[i]
		d.Name = strings.ToLower(strings.TrimSpace(d.Name))
		d.Transport = strings.ToLower(d.Transport)
		if d.Mon != "" {
			if sp, ok := lookupSpecies(d.Mon); ok {
				d.Mon = sp.Slug
			}
		}
		// frames.json keys carry the form: "deoxys-attack", "unown-b".
		if f := slugify(d.Form); f != "" {
			if d.Mon == "" {
				d.Mon = defaultMon(d.Name) // name = "deoxys", form = "attack"
			}
			if !strings.Contains(d.Mon, "-") {
				d.Mon += "-" + f
			}
		}
	}
	return c, nil
}

// selfName: config, else the hostname, lowercased, without the domain.
func (c Config) selfName() string {
	if c.Self != "" {
		return strings.ToLower(c.Self)
	}
	h, _ := os.Hostname()
	h, _, _ = strings.Cut(strings.ToLower(h), ".")
	return h
}

func (c Config) device(name string) *DeviceConfig {
	for i := range c.Devices {
		if c.Devices[i].Name == name {
			return &c.Devices[i]
		}
	}
	return nil
}
