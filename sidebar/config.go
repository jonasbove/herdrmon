package main

// herdrmon's config.toml, as the sidebar needs it: which device this is,
// which Pokémon it is, the agent table and the [sidebar] knobs.
//
//   self = "onix"                       # default: hostname, lowercased, no domain
//   [[device]] name = "onix"  mon = "onix"
//   [agents.myagent]                    # add or override an agents.json entry
//   label = "My Agent"  glyph = "◎"  colour = "teal"
//   match = { agent = ["myagent"], process = ["myagent"], title = "^my" }
//   [sidebar]
//   night_command = "test -e ~/.night"  # exit 0 = night; "" = clock only
//   night_start = 22  night_end = 7      # hours for the clock fallback
//   glyphs = "nerd"                     # or "text" for plain Unicode marks
//   fallback_glyph = "◓"                # $mon when frames/font are missing
//   lead_ms = 0                         # shift this machine's frames in time

import (
	"os"
	"strings"
)

// sidebarDefaults; night_command nil = auto (hyprsunset on Hyprland, else
// the clock).
func sidebarDefaults() *Table {
	t := NewTable()
	t.Set("night_command", nil)
	t.Set("night_below_k", 6000.0)
	t.Set("night_start", 22.0)
	t.Set("night_end", 7.0)
	t.Set("glyphs", "nerd")
	t.Set("fallback_glyph", "◓")
	t.Set("lead_ms", 0.0)
	return t
}

// Sidebar: the [sidebar] table over its defaults.
type Sidebar struct{ *Table }

func (s Sidebar) get(k string) any { v, _ := s.Get(k); return v }

func (s Sidebar) str(k string) string { return jsString(s.get(k)) }

func (s Sidebar) num(k string) float64 { return toNumber(s.get(k)) }

type Config struct {
	Self, Mon, Form string
	Agents          AgentTable
	Sidebar         Sidebar
	Error           error
	Missing         bool
}

func hostname() string {
	h, _ := os.Hostname()
	h = strings.ToLower(h)
	before, _, _ := strings.Cut(h, ".")
	return before
}

func readRawConfig(file string) (raw *Table, err error, missing bool) {
	b, rerr := os.ReadFile(file)
	if rerr != nil {
		return NewTable(), nil, true
	}
	t, perr := ParseToml(string(b))
	if perr != nil {
		return NewTable(), perr, false
	}
	return t, nil, false
}

func resolveConfig(raw *Table) Config {
	get := func(t *Table, k string) any { v, _ := t.Get(k); return v }
	selfName := get(raw, "self")
	src := hostname()
	if truthy(selfName) {
		src = jsString(selfName)
	}
	self := slugify(src)
	if self == "" {
		self = "localhost"
	}
	var me *Table
	if devices, ok := get(raw, "device").([]any); ok {
		for _, d := range devices {
			dt, ok := d.(*Table)
			if !ok {
				continue
			}
			name := ""
			if n := get(dt, "name"); n != nil {
				name = jsString(n)
			}
			if slugify(name) == self {
				me = dt
				break
			}
		}
	}
	base := defaultMon(self)
	if m := get(me, "mon"); truthy(m) {
		base = slugify(jsString(m))
	}
	formRaw := ""
	if f := get(me, "form"); truthy(f) {
		formRaw = jsString(f)
	}
	// frames.json keys carry the form: "deoxys-attack", "unown-b".
	form := slugify(formRaw)
	mon := base
	if form != "" && !strings.Contains(base, "-") {
		mon = base + "-" + form
	}
	sb := sidebarDefaults()
	if user := raw.Sub("sidebar"); user != nil {
		for _, k := range user.Keys() {
			v, _ := user.Get(k)
			sb.Set(k, v)
		}
	}
	return Config{Self: self, Mon: mon, Form: formRaw, Agents: agentTable(raw), Sidebar: Sidebar{sb}}
}

func loadConfig(file string) Config {
	raw, err, missing := readRawConfig(file)
	c := resolveConfig(raw)
	c.Error, c.Missing = err, missing
	return c
}
