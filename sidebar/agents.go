package main

// Table-driven agent and program matching (agents.json + config overrides).
// No vendor gets a code branch; Neovim's extra detail lives in procs.go.

import (
	_ "embed"
	"regexp"
	"strings"
	"sync"
)

//go:embed agents.json
var agentsJSON []byte

// AgentEntry: one row of agents.json. Fields hold whatever the config gave
// (normally strings); Match is normalised.
type AgentEntry struct {
	ID     string
	Fields *Table // label, glyph, text, colour, hide, ... (not match)
	Match  Match
}

type Match struct {
	Agent, Process []string // nil = no such matcher
	Title          *string
	HasAgent       bool
	HasProcess     bool
}

func (m Match) empty() bool { return !m.HasAgent && !m.HasProcess && m.Title == nil }

func (e *AgentEntry) field(k string) any { v, _ := e.Fields.Get(k); return v }

// AgentTable: agents first, then programs, each in table order.
type AgentTable struct {
	Agents, Programs []*AgentEntry
}

func findEntry(group []*AgentEntry, id string) (*AgentEntry, int) {
	for i, e := range group {
		if e.ID == id {
			return e, i
		}
	}
	return nil, -1
}

func (e *AgentEntry) clone() *AgentEntry {
	f := NewTable()
	for _, k := range e.Fields.Keys() {
		v, _ := e.Fields.Get(k)
		f.Set(k, v)
	}
	m := e.Match
	m.Agent = append([]string(nil), e.Match.Agent...)
	m.Process = append([]string(nil), e.Match.Process...)
	return &AgentEntry{ID: e.ID, Fields: f, Match: m}
}

func asList(v any) []string {
	switch x := v.(type) {
	case nil:
		return []string{}
	case []any:
		out := make([]string, len(x))
		for i, e := range x {
			out[i] = jsString(e)
		}
		return out
	}
	return []string{jsString(v)}
}

func entryFromTable(id string, t *Table) *AgentEntry {
	e := &AgentEntry{ID: id, Fields: NewTable()}
	for _, k := range t.Keys() {
		v, _ := t.Get(k)
		if k == "match" {
			m, _ := v.(*Table)
			if a, ok := m.Get("agent"); ok {
				e.Match.Agent, e.Match.HasAgent = asList(a), true
			}
			if p, ok := m.Get("process"); ok {
				e.Match.Process, e.Match.HasProcess = asList(p), true
			}
			if ti, ok := m.Get("title"); ok {
				s := jsString(ti)
				e.Match.Title = &s
			}
			continue
		}
		e.Fields.Set(k, v)
	}
	return e
}

var builtinAgents = func() AgentTable {
	v, err := decodeOrdered(agentsJSON)
	if err != nil {
		panic(err)
	}
	root := v.(*Table)
	var t AgentTable
	for _, g := range []struct {
		name string
		dst  *[]*AgentEntry
	}{{"agents", &t.Agents}, {"programs", &t.Programs}} {
		sub := root.Sub(g.name)
		for _, id := range sub.Keys() {
			*g.dst = append(*g.dst, entryFromTable(id, sub.Sub(id)))
		}
	}
	return t
}()

// agentTable: agents.json merged with config.toml [agents.<id>]. An id that
// names a built-in agent or program is merged into it; a new id becomes an
// agent.
func agentTable(raw *Table) AgentTable {
	t := AgentTable{}
	for _, e := range builtinAgents.Agents {
		t.Agents = append(t.Agents, e.clone())
	}
	for _, e := range builtinAgents.Programs {
		t.Programs = append(t.Programs, e.clone())
	}
	users := raw.Sub("agents")
	for _, id := range users.Keys() {
		u, _ := users.Get(id)
		user, ok := u.(*Table)
		if !ok {
			continue
		}
		group := &t.Agents
		if e, _ := findEntry(t.Programs, id); e != nil {
			group = &t.Programs
		}
		base, at := findEntry(*group, id)
		if base == nil {
			base = &AgentEntry{ID: id, Fields: NewTable()}
			base.Fields.Set("label", id)
			base.Fields.Set("glyph", "•")
			base.Fields.Set("colour", "text")
		}
		match := base.Match
		um := NewTable()
		if m, ok := user.Get("match"); ok {
			if mt, ok := m.(*Table); ok {
				for _, k := range mt.Keys() {
					v, _ := mt.Get(k)
					um.Set(k, v)
				}
			}
		}
		for _, k := range []string{"agent", "process", "title"} {
			if v, ok := user.Get(k); ok {
				um.Set(k, v)
			}
		}
		if v, ok := um.Get("agent"); ok {
			match.Agent, match.HasAgent = asList(v), true
		}
		if v, ok := um.Get("process"); ok {
			match.Process, match.HasProcess = asList(v), true
		}
		if v, ok := um.Get("title"); ok {
			s := jsString(v)
			match.Title = &s
		}
		if match.empty() {
			match.Agent, match.HasAgent = []string{id}, true
		}
		next := base.clone()
		next.Match = match
		for _, k := range user.Keys() {
			switch k {
			case "match", "agent", "process", "title":
				continue
			}
			v, _ := user.Get(k)
			next.Fields.Set(k, v)
		}
		if at >= 0 {
			(*group)[at] = next
		} else {
			*group = append(*group, next)
		}
	}
	return t
}

var (
	titleMu    sync.Mutex
	titleCache = map[string]*regexp.Regexp{}
)

// titleRe: a case-insensitive title matcher; a bad user regex matches
// nothing.
func titleRe(src string) *regexp.Regexp {
	titleMu.Lock()
	defer titleMu.Unlock()
	if re, ok := titleCache[src]; ok {
		return re
	}
	re, err := regexp.Compile("(?i)" + src)
	if err != nil {
		re = nil
	}
	titleCache[src] = re
	return re
}

// procName: "-zsh" -> "zsh", "/usr/bin/python3.12" -> "python3.12".
func procName(name string) string {
	name = strings.TrimPrefix(name, "-")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return strings.ToLower(name)
}

var reVersionTail = regexp.MustCompile(`[\d.]+$`)

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func processMatches(list []string, name string) bool {
	if name == "" || len(list) == 0 {
		return false
	}
	if contains(list, name) {
		return true
	}
	// python3.12 -> python3 -> python
	bare := reVersionTail.ReplaceAllString(name, "")
	return bare != name && contains(list, bare)
}

// Hit: what classify found.
type Hit struct {
	ID    string
	Kind  string // "agent" | "program"
	Entry *AgentEntry
}

// classify: the first entry whose match fits, checked by Herdr's agent id,
// then the foreground process name, then a title regex. pane holds the
// pane.list fields (agent, title, terminal_title); proc is the foreground
// process name.
func classify(table AgentTable, pane map[string]any, proc any) *Hit {
	groups := []struct {
		kind    string
		entries []*AgentEntry
	}{{"agent", table.Agents}, {"program", table.Programs}}
	agent := ""
	if truthy(pane["agent"]) {
		agent = strings.ToLower(jsString(pane["agent"]))
	}
	if agent != "" {
		for _, g := range groups {
			for _, e := range g.entries {
				if contains(e.Match.Agent, agent) {
					return &Hit{e.ID, g.kind, e}
				}
			}
		}
	}
	name := ""
	if proc != nil {
		name = procName(jsString(proc))
	}
	for _, g := range groups {
		for _, e := range g.entries {
			if processMatches(e.Match.Process, name) {
				return &Hit{e.ID, g.kind, e}
			}
		}
	}
	var titles []string
	for _, k := range []string{"title", "terminal_title"} {
		if truthy(pane[k]) {
			titles = append(titles, jsString(pane[k]))
		}
	}
	for _, g := range groups {
		for _, e := range g.entries {
			if e.Match.Title == nil || *e.Match.Title == "" {
				continue
			}
			re := titleRe(*e.Match.Title)
			if re == nil {
				continue
			}
			for _, t := range titles {
				if re.MatchString(t) {
					return &Hit{e.ID, g.kind, e}
				}
			}
		}
	}
	// Herdr detected an agent we have no row for: show it as Herdr names it.
	if agent != "" {
		f := NewTable()
		f.Set("label", pane["agent"])
		f.Set("glyph", "•")
		f.Set("colour", "text")
		return &Hit{agent, "agent", &AgentEntry{ID: agent, Fields: f}}
	}
	return nil
}

// Look: what to draw for an entry under the configured glyph mode.
type Look struct {
	Glyph, Label, Colour string
}

func look(e *AgentEntry, glyphs string) Look {
	glyph := e.field("glyph")
	if glyphs == "text" {
		if t := e.field("text"); t != nil {
			glyph = t
		}
	}
	l := Look{Glyph: "•", Label: "", Colour: "text"}
	if glyph != nil {
		l.Glyph = jsString(glyph)
	}
	if v := e.field("label"); v != nil {
		l.Label = jsString(v)
	}
	if v := e.field("colour"); v != nil {
		l.Colour = jsString(v)
	}
	return l
}
