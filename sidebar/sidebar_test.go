package main

// The Node.js plugin's unit tests (test/*.test.js), ported one for one.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"herdrmon/internal/pokedex"
)

func mustToml(t *testing.T, text string) *Table {
	t.Helper()
	v, err := ParseToml(text)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

/* ------------------------------------------------------- agents.test.js */

func TestShipsEveryAgentDesignLists(t *testing.T) {
	table := agentTable(NewTable())
	for _, n := range []string{"claude", "codex", "opencode", "gemini", "hermes", "aider", "goose", "amp", "cursor-agent", "crush", "qwen", "copilot", "pi", "kiro", "droid", "cline"} {
		if hit := classify(table, map[string]any{}, n); hit == nil || hit.Kind != "agent" {
			t.Errorf("%s: %+v", n, hit)
		}
	}
	for _, n := range []string{"nvim", "vim", "hx"} {
		if hit := classify(table, map[string]any{}, n); hit == nil || hit.Kind != "program" {
			t.Errorf("%s: %+v", n, hit)
		}
	}
}

func TestEveryColourIsASlot(t *testing.T) {
	for _, group := range [][]*AgentEntry{builtinAgents.Agents, builtinAgents.Programs} {
		for _, e := range group {
			if _, ok := slotIndex[jsString(e.field("colour"))]; !ok {
				t.Errorf("%s: %v", e.ID, e.field("colour"))
			}
		}
	}
}

func TestHerdrAgentIDWinsOverProcess(t *testing.T) {
	table := agentTable(NewTable())
	// hermes runs as python underneath
	if id := classify(table, map[string]any{"agent": "hermes"}, "python3").ID; id != "hermes" {
		t.Error(id)
	}
	if id := classify(table, map[string]any{"agent": "cursor"}, "node").ID; id != "cursor" {
		t.Error(id)
	}
}

func TestProcessNames(t *testing.T) {
	table := agentTable(NewTable())
	for in, want := range map[string]string{"-zsh": "shell", "/usr/bin/python3.12": "python", "cursor-agent": "cursor"} {
		if hit := classify(table, map[string]any{}, in); hit == nil || hit.ID != want {
			t.Errorf("%s: %+v", in, hit)
		}
	}
	if hit := classify(table, map[string]any{}, "unknownthing"); hit != nil {
		t.Errorf("unknownthing: %+v", hit)
	}
}

func TestTitleRegex(t *testing.T) {
	table := agentTable(NewTable())
	if id := classify(table, map[string]any{"title": "aider v0.80"}, "python3").ID; id != "python" {
		t.Error(id)
	}
	if id := classify(table, map[string]any{"title": "aider v0.80"}, "").ID; id != "aider" {
		t.Error(id)
	}
}

func TestUnknownHerdrAgentShowsAsReported(t *testing.T) {
	hit := classify(agentTable(NewTable()), map[string]any{"agent": "newbot"}, "")
	if l := look(hit.Entry, "nerd").Label; l != "newbot" {
		t.Error(l)
	}
}

func TestGlyphModes(t *testing.T) {
	nvim, _ := findEntry(builtinAgents.Programs, "neovim")
	claude, _ := findEntry(builtinAgents.Agents, "claude")
	if g := look(nvim, "nerd").Glyph; g != "\ue6ae" {
		t.Errorf("%q", g)
	}
	if g := look(nvim, "text").Glyph; g != "ν" {
		t.Errorf("%q", g)
	}
	if g := look(claude, "text").Glyph; g != "✻" {
		t.Errorf("%q", g)
	}
}

/* --------------------------------------------------------- anim.test.js */

func mk(name string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = name + string(rune('0'+i))
	}
	return out
}

var testAnim = Anim{"base": {"b"}, "bob": mk("bob", 2), "poison": mk("poison", 4), "sleep": mk("sleep", 4), "sendout": mk("out", 3), "recall": mk("rec", 3), "tackle": mk("tk", 2), "harden": mk("hd", 2)}

func TestBobFollowsTheBeat(t *testing.T) {
	st := newAnimState()
	for at, want := range map[int64]string{0: "bob0", 375: "bob0", 500: "bob1", 1000: "bob0"} {
		if f := pick(st, "idle", testAnim, at, false).Frame; f != want {
			t.Errorf("%d: %s", at, f)
		}
	}
	// two independent states agree at the same instant
	if a, b := pick(newAnimState(), "idle", testAnim, 1500, false).Frame, pick(newAnimState(), "idle", testAnim, 1500, false).Frame; a != b {
		t.Error(a, b)
	}
}

func TestBlockedLoopsPoison(t *testing.T) {
	r := pick(newAnimState(), "blocked", testAnim, 250, false)
	if r.Frame != "poison2" || !r.Busy {
		t.Errorf("%+v", r)
	}
}

func TestNightSleepsUnlessWorking(t *testing.T) {
	if f := pick(newAnimState(), "idle", testAnim, 125, true).Frame; f != "sleep1" {
		t.Error(f)
	}
	if f := pick(newAnimState(), "working", testAnim, 0, true).Frame; f != "bob0" {
		t.Error(f)
	}
}

func TestOneShotStartsOnNextBeat(t *testing.T) {
	st := newAnimState()
	enqueue(st, "tackle", "")
	r := pick(st, "idle", testAnim, 625, false)
	if !reflect.DeepEqual(r.Shot, []Timed{{1000, "tk0"}, {1050, "tk1"}}) || r.End != 1100 {
		t.Errorf("%+v", r)
	}
	if !pick(st, "idle", testAnim, 1000, false).Hold {
		t.Error("should hold")
	}
	if f := pick(st, "idle", testAnim, 1500, false).Frame; f != "bob1" {
		t.Error(f)
	}
}

func queueNames(st *AnimState) []string {
	var out []string
	for _, e := range st.Queue {
		out = append(out, e.Name)
	}
	return out
}

func TestAnsweringABlock(t *testing.T) {
	st := newAnimState()
	observe(st, "blocked", true, false)
	if f := pick(st, "blocked", testAnim, 0, false).Frame; f != "poison0" {
		t.Error(f)
	}
	observe(st, "idle", true, true)
	if q := queueNames(st); !reflect.DeepEqual(q, []string{"recall", "sendout"}) {
		t.Error(q)
	}
	r1 := pick(st, "idle", testAnim, 100, false)
	if r1.Entry.Name != "recall" || st.Parked == nil || *st.Parked != "rec2" {
		t.Errorf("%+v", r1)
	}
	r2 := pick(st, "idle", testAnim, r1.End, false)
	if r2.Entry.Name != "sendout" || st.Parked != nil {
		t.Errorf("%+v", r2)
	}
}

func TestParkedStaysInTheBall(t *testing.T) {
	st := newAnimState()
	enqueue(st, "recall", "")
	r := pick(st, "idle", testAnim, 0, false)
	if f := pick(st, "idle", testAnim, r.End+500, false).Frame; f != "rec2" {
		t.Error(f)
	}
}

func TestNewAgentIsSentOut(t *testing.T) {
	st := newAnimState()
	observe(st, "none", false, false)
	observe(st, "idle", true, true)
	if q := queueNames(st); !reflect.DeepEqual(q, []string{"sendout"}) {
		t.Error(q)
	}
	old := newAnimState()
	observe(old, "idle", true, false)
	if len(old.Queue) != 0 {
		t.Error(queueNames(old))
	}
}

func TestMissingAnimationsFallBack(t *testing.T) {
	thin := Anim{"bob": {"x", "y"}}
	if f := pick(newAnimState(), "blocked", thin, 0, false).Frame; f != "x" {
		t.Error(f)
	}
	st := newAnimState()
	enqueue(st, "harden", "")
	if f := pick(st, "idle", thin, 500, true).Frame; f != "y" {
		t.Error(f)
	}
}

func TestRollupAndGrid(t *testing.T) {
	if r := rollup([]string{"idle", "blocked", "working"}); r != "blocked" {
		t.Error(r)
	}
	if r := rollup([]string{"done", "working"}); r != "working" {
		t.Error(r)
	}
	if r := rollup(nil); r != "none" {
		t.Error(r)
	}
	if b := nextBoundary(1000, 125, 260, 0); b != 1375 {
		t.Error(b)
	}
	if b := nextBoundary(1000, 500, 260, 1500); b != 2000 {
		t.Error(b)
	}
}

/* ------------------------------------------------------- config.test.js */

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{"Ho-Oh": "ho_oh", "Mr. Mime": "mr_mime", "  ONIX.local ": "onix_local"} {
		if got := slugify(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

func TestSpeciesListIsGen3(t *testing.T) {
	if len(pokedex.Gen3) != 386 {
		t.Fatal(len(pokedex.Gen3))
	}
	for _, s := range []string{"bulbasaur", "ho_oh", "mr_mime", "nidoran_f", "deoxys", "unown", "farfetchd"} {
		if !isSpecies(s) {
			t.Error(s)
		}
	}
	for _, m := range pokedex.MachineMons {
		if !isSpecies(m) {
			t.Error(m)
		}
	}
}

func TestDefaultMon(t *testing.T) {
	if m := defaultMon("Onix"); m != "onix" {
		t.Error(m)
	}
	if m := defaultMon("ho-oh"); m != "ho_oh" {
		t.Error(m)
	}
	a := defaultMon("workstation")
	if !contains(pokedex.MachineMons, a) || defaultMon("workstation") != a || defaultMon("WorkStation") != a {
		t.Error(a)
	}
	// fnv1a32("workstation") pinned so other implementations can check theirs
	if pokedex.MachineMons[fnv1a("workstation")%uint32(len(pokedex.MachineMons))] != a {
		t.Error("fnv")
	}
}

func TestSelfAndMonFromConfig(t *testing.T) {
	c := resolveConfig(mustToml(t, "self = \"geodude\"\n[[device]]\nname = \"geodude\"\nmon = \"Tangela\"\n"))
	if c.Self != "geodude" || c.Mon != "tangela" {
		t.Errorf("%+v", c)
	}
	if d := resolveConfig(mustToml(t, "self = \"ho-oh\"\n")); d.Mon != "ho_oh" {
		t.Error(d.Mon)
	}
	if e := resolveConfig(NewTable()); e.Self != slugify(hostname()) {
		t.Error(e.Self)
	}
}

func TestSidebarDefaultsAndOverrides(t *testing.T) {
	c := resolveConfig(mustToml(t, "[sidebar]\nnight_command = \"\"\nglyphs = \"text\"\n"))
	if c.Sidebar.get("night_command") != "" || c.Sidebar.str("glyphs") != "text" || c.Sidebar.num("night_start") != 22 {
		t.Errorf("%v", c.Sidebar.Table)
	}
}

func TestAgentsExtendAndOverride(t *testing.T) {
	raw := mustToml(t, `
[agents.claude]
colour = "mauve"
[agents.myagent]
label = "Mine"
glyph = "◎"
process = "my-agent"
[agents.neovim]
label = "nv"
`)
	tb := agentTable(raw)
	claude, _ := findEntry(tb.Agents, "claude")
	if claude.field("colour") != "mauve" || claude.field("label") != "Claude Code" || !reflect.DeepEqual(claude.Match.Agent, []string{"claude"}) {
		t.Errorf("%+v", claude)
	}
	mine, _ := findEntry(tb.Agents, "myagent")
	if !reflect.DeepEqual(mine.Match.Process, []string{"my-agent"}) {
		t.Errorf("%+v", mine.Match)
	}
	nv, _ := findEntry(tb.Programs, "neovim")
	if nv.field("label") != "nv" {
		t.Error(nv.field("label"))
	}
	// the shipped table is not mutated
	fresh, _ := findEntry(agentTable(NewTable()).Agents, "claude")
	if fresh.field("colour") != "peach" {
		t.Error(fresh.field("colour"))
	}
}

/* ------------------------------------------------------- daemon.test.js */

func TestHeadIsFirstAgentPaneByTabThenPane(t *testing.T) {
	panes := []map[string]any{
		{"pane_id": "w1:p10", "tab_id": "w1:t1", "workspace_id": "w1", "agent": "codex"},
		{"pane_id": "w1:p2", "tab_id": "w1:t2", "workspace_id": "w1", "agent": "claude"},
		{"pane_id": "w1:p9", "tab_id": "w1:t1", "workspace_id": "w1", "agent": "claude"},
		{"pane_id": "w1:p1", "tab_id": "w1:t1", "workspace_id": "w1"},
		{"pane_id": "w2:p1", "tab_id": "w2:t1", "workspace_id": "w2"},
	}
	w := workspaces(panes)
	if w[0].Head["pane_id"] != "w1:p9" || w[1].Head != nil {
		t.Errorf("%v %v", w[0].Head, w[1].Head)
	}
}

/* ------------------------------------------------------- frames.test.js */

func TestFramesLoadReloadKeepLastGood(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "frames.json")
	write := func(s string, mtime time.Time) {
		if err := os.WriteFile(file, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(file, time.Now(), mtime); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"version":2,"cells":5,"font":"X","mons":{"ho-oh":{"bob":["a","b"]}}}`, time.Now())
	fontOK := true
	f := newFrames(file, func(string) bool { return fontOK })
	if !f.Load() {
		t.Fatal("load")
	}
	if a := f.Anim("ho_oh"); !reflect.DeepEqual(a["bob"], []string{"a", "b"}) {
		t.Error(a)
	}
	if f.Anim("onix") != nil {
		t.Error("onix")
	}
	now := time.Now().UnixMilli()
	write(`{ "mons": { broken`, time.Now().Add(5*time.Second))
	f.Refresh(now + 10000)
	if a := f.Anim("ho_oh"); !reflect.DeepEqual(a["bob"], []string{"a", "b"}) {
		t.Error(a)
	}
	write(`{"mons":{"onix":{"bob":["c"]}}}`, time.Now().Add(10*time.Second))
	f.Refresh(now + 20000)
	if a := f.Anim("onix"); !reflect.DeepEqual(a["bob"], []string{"c"}) {
		t.Error(a)
	}
	fontOK = false
	f.Load()
	if f.Anim("onix") != nil {
		t.Error("font missing -> no frames -> plain glyph")
	}
}

/* -------------------------------------------------------- night.test.js */

func TestClockFallbackSpansMidnight(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 10, 1, h, 30, 0, 0, time.Local) }
	for h, want := range map[int]bool{23: true, 3: true, 7: false, 12: false} {
		if byClock(at(h), 22, 7) != want {
			t.Error(h)
		}
	}
	if !byClock(at(13), 12, 14) {
		t.Error("13 in 12-14")
	}
}

func TestExplicitAndEmptyNightCommand(t *testing.T) {
	sb := sidebarDefaults()
	sb.Set("night_command", "true")
	if p := nightPlan(Sidebar{sb}); p != (NightPlan{Mode: "command", Command: "true"}) {
		t.Errorf("%+v", p)
	}
	sb.Set("night_command", "")
	if p := nightPlan(Sidebar{sb}); p != (NightPlan{Mode: "clock"}) {
		t.Errorf("%+v", p)
	}
}

/* ------------------------------------------------------ palette.test.js */

func TestEveryBuiltinThemeResolves(t *testing.T) {
	if len(palettes) != 18 {
		t.Error(len(palettes))
	}
	for name := range palettes {
		r := resolvePalette(HerdrTheme{Name: name, Custom: NewTable()})
		if r["green"] == "" || r["red"] == "" || r["blue"] == "" {
			t.Error(name)
		}
	}
	if c := canonicalTheme("Catppuccin Mocha"); c != "catppuccin" {
		t.Error(c)
	}
	if c := canonicalTheme("dawn"); c != "rose-pine-dawn" {
		t.Error(c)
	}
	if c := canonicalTheme("nope"); c != "" {
		t.Error(c)
	}
}

func TestThemeFromHerdrConfig(t *testing.T) {
	th := themeFromHerdrConfig("[theme]\nname = \"nord\"\n[theme.custom]\npeach = \"#123456\"\nred = \"red\"\n")
	r := resolvePalette(th)
	if r["peach"] != "#123456" || r["red"] != palettes["nord"]["red"] { // non-hex ignored
		t.Errorf("%v", r)
	}
	auto := themeFromHerdrConfig("[theme]\nauto_switch = true\nname = \"x\"\ndark_name = \"gruvbox\"\n[theme.custom.dark]\nteal = \"#000\"\n")
	if teal, _ := auto.Custom.Get("teal"); auto.Name != "gruvbox" || teal != "#000" {
		t.Errorf("%+v", auto)
	}
	if n := themeFromHerdrConfig("[theme]\nauto_switch = true\nname = \"latte\"\n").Name; n != "catppuccin" {
		t.Error(n)
	}
}

func TestColourPrefixesAndRulesLongestFirst(t *testing.T) {
	v := coloured("peach", "x")
	if v != strings.Repeat("\u200b", 12)+"x" || uncoloured(v) != "x" {
		t.Errorf("%q", v)
	}
	if c := coloured("nonsense", "x"); c != "\u200bx" {
		t.Errorf("%q", c)
	}
	def := resolvePalette(HerdrTheme{Name: "catppuccin", Custom: NewTable()})
	rules := paletteRules(def)
	if len(rules) != 12 {
		t.Fatal(len(rules))
	}
	for i := 1; i < len(rules); i++ {
		if len(rules[i].StartsWith) >= len(rules[i-1].StartsWith) {
			t.Error("order")
		}
	}
	// the first rule matching a value is its own slot
	for _, r := range rules {
		if strings.HasPrefix(v, r.StartsWith) {
			if r.Fg != def["peach"] {
				t.Error(r.Fg)
			}
			break
		}
	}
}

func TestManagedBlockWriteRewriteCoexistRemove(t *testing.T) {
	r := resolvePalette(HerdrTheme{Name: "catppuccin", Custom: NewTable()})
	base := "[theme]\nname = \"catppuccin\"\n"
	b := buildBlock(base, r, "catppuccin")
	one := applyBlock(base, b.Block, false)
	parsed := mustToml(t, one)
	agents := field(parsed, "ui", "sidebar", "agents", "rows")
	spaces := field(parsed, "ui", "sidebar", "spaces", "rows")
	if field(list(list(agents)[0])[0], "token") != "$mon" || field(list(list(spaces)[0])[1], "token") != "$mon" {
		t.Error("tokens")
	}
	if again := applyBlock(one, buildBlock(one, r, "catppuccin").Block, false); again != one {
		t.Error("rewrite changed it")
	}
	if removed := applyBlock(one, "", true); removed != base {
		t.Errorf("%q", removed)
	}
	// radar (or the user) already owns [ui.sidebar.agents]
	radar := base + "\n# >>> herdr-radar sidebar block\n[ui.sidebar.agents]\nrows = [[\"agent\"]]\n# <<< herdr-radar sidebar block\n"
	rb := buildBlock(radar, r, "catppuccin")
	if !reflect.DeepEqual(rb.Skipped, []string{"agents"}) {
		t.Error(rb.Skipped)
	}
	merged := applyBlock(radar, rb.Block, false)
	m := mustToml(t, merged)
	if js(generic(t, field(m, "ui", "sidebar", "agents", "rows"))) != `[["agent"]]` || field(m, "ui", "sidebar", "spaces") == nil {
		t.Error(merged)
	}
	if !strings.Contains(merged, "# >>> herdr-radar sidebar block") {
		t.Error("radar block lost")
	}
	// both owned: nothing to write
	both := radar + "[ui.sidebar.spaces]\nrows = [[\"workspace\"]]\n"
	if buildBlock(both, r, "catppuccin").HasBlock {
		t.Error("block for a fully owned sidebar")
	}
}

/* --------------------------------------------------------- toml.test.js */

func TestTomlTablesArraysDottedKeys(t *testing.T) {
	v := mustToml(t, `
# comment
self = "onix" # trailing
[font]
extra = ["porygon", 'tangela']
[[device]]
name = "geodude"
mon = "geodude"
[device.battery]
source = "sysfs"
[[device]]
name = "ho-oh"
display = "Ho-oh"
a.b.c = 1
`)
	devices := list(field(v, "device"))
	if field(v, "self") != "onix" || js(field(v, "font", "extra")) != `["porygon","tangela"]` || len(devices) != 2 ||
		field(devices[0], "battery", "source") != "sysfs" || field(devices[1], "display") != "Ho-oh" || field(devices[1], "a", "b", "c") != 1.0 {
		t.Errorf("%s", js(v))
	}
}

func TestTomlValues(t *testing.T) {
	v := mustToml(t, `
n = 1_000
f = -2.5e3
yes = true
x = 0x1F
s = "tab\tq\"\u200B"
lit = 'C:\path'
ml = """
line1
line2"""
rows = [
  ["state_icon", { token = "$mon", fg = "#fff", rules = [{ starts_with = "\u200B", fg = "#000" }] }],
  ["agent"], # comment
]
when = 2026-10-01T10:00:00Z
`)
	rows := list(field(v, "rows"))
	switch {
	case field(v, "n") != 1000.0, field(v, "f") != -2500.0, field(v, "yes") != true, field(v, "x") != 31.0,
		field(v, "s") != "tab\tq\"\u200b", field(v, "lit") != `C:\path`, field(v, "ml") != "line1\nline2",
		field(list(field(list(rows[0])[1], "rules"))[0], "starts_with") != "\u200b", list(rows[1])[0] != "agent",
		field(v, "when") != "2026-10-01T10:00:00Z":
		t.Errorf("%s", js(generic(t, v)))
	}
}

func TestTomlErrorsCarryALine(t *testing.T) {
	_, err := ParseToml("a = 1\na = 2\n")
	if e, ok := err.(*TomlError); !ok || e.Line != 2 {
		t.Errorf("%v", err)
	}
	for _, in := range []string{"[x]\n[x.y\n", "s = \"open\n"} {
		if _, err := ParseToml(in); err == nil {
			t.Errorf("%q parsed", in)
		} else if _, ok := err.(*TomlError); !ok {
			t.Errorf("%q: %T", in, err)
		}
	}
}
