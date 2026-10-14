package main

// The sidebar snippet and the managed block in Herdr's config.toml.
//
// The block sits between `# >>> herdrmon sidebar block` and
// `# <<< herdrmon sidebar block`. Nothing outside the markers is ever
// changed. TOML forbids defining a table twice, so a [ui.sidebar.agents] or
// [ui.sidebar.spaces] that already exists elsewhere (herdr-radar's managed
// block, or the user's own) is left alone: the block only carries the tables
// nobody else defines, and `configure` prints the token entries to add to the
// existing ones by hand.

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	blockBegin = "# >>> herdrmon sidebar block"
	blockEnd   = "# <<< herdrmon sidebar block"
)

// tomlQuote: a basic string; control characters, DEL and zero-width spaces
// as \uXXXX so the block stays readable.
func tomlQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '\\' || r == '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f || r == 0x200b:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// layoutToken: one coloured token entry: { token, fg?, rules }.
type layoutToken struct {
	Name  string
	Fg    string // "" = none
	Rules []rule
}

func (t layoutToken) toml() string {
	parts := []string{"token = " + tomlQuote("$"+t.Name)}
	if t.Fg != "" {
		parts = append(parts, "fg = "+tomlQuote(t.Fg))
	}
	rules := make([]string, len(t.Rules))
	for i, r := range t.Rules {
		rules[i] = "{ starts_with = " + tomlQuote(r.StartsWith) + ", fg = " + tomlQuote(r.Fg) + " }"
	}
	parts = append(parts, "rules = ["+strings.Join(rules, ", ")+"]")
	return "{ " + strings.Join(parts, ", ") + " }"
}

// A coloured token: default fg plus the palette rules.
func token(name, fg string, resolved map[string]string) layoutToken {
	return layoutToken{Name: name, Fg: resolved[fg], Rules: paletteRules(resolved)}
}

// entryToml: a row entry, a built-in name ("state_icon") or a token.
func entryToml(e any) string {
	switch x := e.(type) {
	case string:
		return tomlQuote(x)
	case layoutToken:
		return x.toml()
	}
	panic(fmt.Sprintf("entryToml: %T", e))
}

func rowsToml(rows [][]any) string {
	lines := make([]string, len(rows))
	for i, r := range rows {
		parts := make([]string, len(r))
		for j, e := range r {
			parts[j] = entryToml(e)
		}
		lines[i] = "  [" + strings.Join(parts, ", ") + "],"
	}
	return "rows = [\n" + strings.Join(lines, "\n") + "\n]"
}

// layouts: the agents and spaces rows for a resolved palette (slot -> hex).
func layouts(resolved map[string]string) map[string][][]any {
	mon := token("mon", "overlay1", resolved)
	agent := token("agent", "text", resolved)
	tools := token("tools", "subtext0", resolved)
	return map[string][][]any{
		// Two lines per agent (more of them fit): the mon leads the status line.
		"agents": {{mon, "state_icon", "machine", "workspace", "tab"}, {agent, tools}},
		"spaces": {{"state_icon", mon, "workspace"}, {"branch", "git_status"}, {tools}},
	}
}

func jsTrim(s string) string { return strings.TrimFunc(s, jsSpace) }

// splitBlock: [before, block, after]; hasBlock false when there is none (then
// before is the whole text).
func splitBlock(text string) (before, block, after string, hasBlock bool) {
	lines := strings.Split(text, "\n")
	b, e := -1, -1
	for i, l := range lines {
		if jsTrim(l) == blockBegin {
			b = i
			break
		}
	}
	if b >= 0 {
		for i := b + 1; i < len(lines); i++ {
			if jsTrim(lines[i]) == blockEnd {
				e = i
				break
			}
		}
	}
	if b < 0 || e < 0 {
		return text, "", "", false
	}
	return strings.Join(lines[:b], "\n"), strings.Join(lines[b:e+1], "\n"), strings.Join(lines[e+1:], "\n"), true
}

func withoutBlock(text string) string {
	before, _, after, ok := splitBlock(text)
	if !ok {
		return before
	}
	joined := strings.TrimRight(before, "\n") + "\n" + strings.TrimLeft(after, "\n")
	return strings.TrimRight(strings.TrimLeft(joined, "\n"), "\n") + "\n"
}

var reTaken = map[string]*regexp.Regexp{
	"agents": regexp.MustCompile(`(?m)^\s*\[\s*ui\.sidebar\.agents\s*[\].]`),
	"spaces": regexp.MustCompile(`(?m)^\s*\[\s*ui\.sidebar\.spaces\s*[\].]`),
}

// taken: which sidebar tables are defined outside our block.
func taken(text string) (agents, spaces bool) {
	rest := withoutBlock(text)
	if cfg, err := ParseToml(rest); err == nil {
		ui, _ := cfg.Get("ui")
		uiT, _ := ui.(*Table)
		sb, ok := uiT.Get("sidebar")
		switch x := sb.(type) {
		case *Table:
			_, a := x.Get("agents")
			_, s := x.Get("spaces")
			return a, s
		case []any:
			return false, false
		}
		if !ok || sb == nil {
			return false, false
		}
		// A scalar sidebar: the parse can't answer, so fall back to the text.
	}
	return reTaken["agents"].MatchString(rest), reTaken["spaces"].MatchString(rest)
}

type built struct {
	Block    string // "" with HasBlock false: nothing to write
	HasBlock bool
	Skipped  []string
	Snippet  string
	Entries  [][2]string // name, entry, in order mon, agent, tools
}

func buildBlock(text string, resolved map[string]string, themeName string) built {
	l := layouts(resolved)
	busyAgents, busySpaces := taken(text)
	busy := map[string]bool{"agents": busyAgents, "spaces": busySpaces}
	var tables []string
	skipped := []string{}
	for _, name := range []string{"agents", "spaces"} {
		if busy[name] {
			skipped = append(skipped, name)
		} else {
			tables = append(tables, "[ui.sidebar."+name+"]\n"+rowsToml(l[name]))
		}
	}
	head := blockBegin + "\n# Written by the herdrmon sidebar plugin (configure). Colours follow the\n# Herdr theme \"" + themeName + "\"; re-run configure after changing theme.\n"
	out := built{Skipped: skipped}
	if len(tables) > 0 {
		out.Block = head + strings.Join(tables, "\n\n") + "\n" + blockEnd
		out.HasBlock = true
	}
	snip := make([]string, 0, 2)
	for _, n := range []string{"agents", "spaces"} {
		snip = append(snip, "[ui.sidebar."+n+"]\n"+rowsToml(l[n]))
	}
	out.Snippet = strings.Join(snip, "\n\n")
	out.Entries = [][2]string{
		{"mon", entryToml(token("mon", "overlay1", resolved))},
		{"agent", entryToml(token("agent", "text", resolved))},
		{"tools", entryToml(token("tools", "subtext0", resolved))},
	}
	return out
}

// applyBlock: the new config text with our block replaced, added or (remove)
// taken out.
func applyBlock(text, block string, remove bool) string {
	before, _, after, ok := splitBlock(text)
	if !ok {
		if remove {
			return text
		}
		if jsTrim(before) != "" {
			return strings.TrimRight(before, "\n") + "\n\n" + block + "\n"
		}
		return block + "\n"
	}
	if remove {
		return withoutBlock(text)
	}
	sep := ""
	if before != "" {
		sep = "\n"
	}
	return before + sep + block + "\n" + strings.TrimPrefix(after, "\n")
}
