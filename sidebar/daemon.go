package main

// The resident daemon: one per Herdr server, for as long as that server runs.
//
// It owns these kinds of pane metadata under its own source:
//   $mon    this machine's animated Pokémon, on the first agent pane of each
//           workspace (the row that heads the workspace's group in the Agents
//           panel), and as workspace metadata for the Spaces panel.
//   $mon_rail the same Pokémon cropped to its own cells, as workspace
//           metadata for Herdr's collapsed sidebar (rail.go).
//   $agent  vendor glyph + name, on every agent pane.
//   $tools  the other programs in the pane's tab (one muted token), plus
//           $tool_1..$tool_4 each in its own colour; workspaces get $tools
//           for everything running in them.
//
// One Pokémon per workspace, not per pane: its state is the workspace's
// rolled-up agent state (blocked beats working beats idle), and git events
// and send-outs from any of its agents play on it.
//
// Single instance: the daemon listens on a control socket (controlSocket,
// one per Herdr socket). A second start finds it answering and exits; a stale
// socket file from a crash is replaced. The same socket takes `stop`, `ping`,
// `play` and `force` requests (the stop and demo commands).

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	source    = "plugin:jonasbove.herdrmon"
	prepMS    = 260 // pane.list (0-100 ms) + pollMS before each boundary
	toolsMS   = 2000
	configMS  = 5000
	goneLimit = 20 // consecutive failed pane.list calls before exiting
	toolSlots = 4
)

/* ---------------------------------------------------------------- logging */

var logMu sync.Mutex

func daemonLog(line string) {
	logMu.Lock()
	defer logMu.Unlock()
	dir := ensureDir(stateDir())
	file := filepath.Join(dir, "daemon.log")
	if st, err := os.Stat(file); err == nil && st.Size() > 256*1024 {
		_ = os.Truncate(file, 0)
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
	if err != nil {
		return // logging never takes the daemon down
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), line)
}

func debugOn() bool {
	_, err := os.Stat(filepath.Join(stateDir(), "debug"))
	return err == nil
}

/* ---------------------------------------------------------- control socket */

// request sends one message to the daemon and returns its reply, or nil.
func request(msg map[string]any, timeout time.Duration) map[string]any {
	deadline := time.Now().Add(timeout)
	conn, err := net.DialTimeout("unix", controlSocket(), timeout)
	if err != nil {
		return nil
	}
	defer conn.Close()
	_ = conn.SetDeadline(deadline)
	if _, err := conn.Write(marshalNoEscape(msg)); err != nil {
		return nil
	}
	line := readLine(conn, deadline)
	if line == nil {
		return nil
	}
	var reply map[string]any
	if json.Unmarshal(line, &reply) != nil || reply == nil {
		return nil
	}
	return reply
}

// lock: a listening control socket, or nil when another daemon owns it.
func lock() (net.Listener, error) {
	sock := controlSocket()
	ensureDir(filepath.Dir(sock))
	l, err := net.Listen("unix", sock)
	if err == nil {
		return l, nil
	}
	if !errors.Is(err, syscall.EADDRINUSE) {
		return nil, err
	}
	if request(map[string]any{"cmd": "ping"}, 1500*time.Millisecond) != nil {
		return nil, nil
	}
	_ = os.Remove(sock) // stale: its daemon is gone
	l, err = net.Listen("unix", sock)
	if err != nil {
		return nil, nil // raced with another starter
	}
	return l, nil
}

/* ------------------------------------------------------------------ order */

var reDigitsRun = regexp.MustCompile(`[0-9]+`)

func idNums(id any) []int64 {
	s := ""
	if id != nil {
		s = jsString(id)
	}
	var out []int64
	for _, m := range reDigitsRun.FindAllString(s, -1) {
		n, _ := strconv.ParseFloat(m, 64)
		out = append(out, int64(n))
	}
	return out
}

func byID(a, b any) int64 {
	x, y := idNums(a), idNums(b)
	n := len(x)
	if len(y) > n {
		n = len(y)
	}
	at := func(l []int64, i int) int64 {
		if i < len(l) {
			return l[i]
		}
		return -1
	}
	for i := 0; i < n; i++ {
		if at(x, i) != at(y, i) {
			return at(x, i) - at(y, i)
		}
	}
	return 0
}

func paneOrder(a, b map[string]any) int64 {
	if d := byID(a["tab_id"], b["tab_id"]); d != 0 {
		return d
	}
	return byID(a["pane_id"], b["pane_id"])
}

func sortPanes(panes []map[string]any) {
	sort.SliceStable(panes, func(i, j int) bool { return paneOrder(panes[i], panes[j]) < 0 })
}

type wsGroup struct {
	ID     string
	Agents []map[string]any
	Head   map[string]any // nil: no agent
}

func idString(v any) string {
	if v == nil {
		return ""
	}
	return jsString(v)
}

// workspaces: the panes grouped by workspace in order of first appearance,
// each with its agent panes in pane order; the head is the first of them.
func workspaces(panes []map[string]any) []*wsGroup {
	var out []*wsGroup
	idx := map[string]*wsGroup{}
	for _, p := range panes {
		id := idString(p["workspace_id"])
		g := idx[id]
		if g == nil {
			g = &wsGroup{ID: id}
			idx[id] = g
			out = append(out, g)
		}
		if truthy(p["agent"]) {
			g.Agents = append(g.Agents, p)
		}
	}
	for _, g := range out {
		sortPanes(g.Agents)
		if len(g.Agents) > 0 {
			g.Head = g.Agents[0]
		}
	}
	return out
}

/* ----------------------------------------------------------------- daemon */

type force struct {
	status string // "" = none
	night  *bool
	until  int64
}

type wsState struct {
	*AnimState
	force *force
}

type waiter struct {
	entry *ShotEntry
	reply func(map[string]any)
}

type daemon struct {
	mu        sync.Mutex
	cfg       Config
	cfgMtime  time.Time
	cfgCheck  int64
	frames    *Frames
	night     *Night
	ws        map[string]*wsState
	written   map[string]*string // "p:<pane>" | "w:<ws>" -> last $mon (nil = cleared)
	rails     map[string]*string // workspace -> last $mon_rail (rail.go)
	heads     map[string]string  // workspace -> head pane ("" = none)
	waiting   []waiter
	scanned   bool
	stopped   bool
	gone      int
	toolsSent map[string]string
	listener  net.Listener
	stopOnce  sync.Once
	stopDone  chan struct{}
}

func (d *daemon) lead() int64 {
	if v := os.Getenv("HERDRMON_LEAD_MS"); v != "" {
		if n := toNumber(v); n == n {
			return int64(n)
		}
		return 0
	}
	n := toNumber(d.cfg.Sidebar.get("lead_ms"))
	if n != n {
		return 0
	}
	return int64(n)
}

func nowMS() int64 { return time.Now().UnixMilli() }

func (d *daemon) clock() int64 { return nowMS() + d.lead() }

func (d *daemon) stateFor(id string) *wsState {
	st := d.ws[id]
	if st == nil {
		st = &wsState{AnimState: newAnimState()}
		d.ws[id] = st
	}
	return st
}

func (d *daemon) reloadConfig(now int64) {
	if now-d.cfgCheck < configMS {
		return
	}
	d.cfgCheck = now
	var m time.Time
	if st, err := os.Stat(configPath()); err == nil {
		m = st.ModTime()
	}
	if m.Equal(d.cfgMtime) {
		return
	}
	d.cfgMtime = m
	d.cfg = loadConfig(configPath())
	d.night = newNight(d.cfg.Sidebar)
	if d.cfg.Error != nil {
		daemonLog("config: " + d.cfg.Error.Error())
	}
}

type monWrite struct {
	at     int64
	method string
	params map[string]any
}

func nullable(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// step computes and sends the $mon frames for the boundary target, and
// returns when to run next.
func (d *daemon) step(target int64) (period, after int64, ok bool) {
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		return 0, 0, false
	}
	now := nowMS()
	d.reloadConfig(now)
	d.frames.Refresh(now)
	isNight := d.night.Check(now)
	species := d.frames.Anim(d.cfg.Mon)
	d.mu.Unlock()

	panes := paneList()

	d.mu.Lock()
	defer d.mu.Unlock()
	if panes == nil {
		d.gone++
		if d.gone >= goneLimit {
			daemonLog("herdr socket unreachable; exiting")
			_ = os.Remove(controlSocket())
			os.Exit(0)
		}
		return bobMS, target, true
	}
	d.gone = 0

	var writes []monWrite
	put := func(key string, at int64, value *string) {
		if old, seen := d.written[key]; seen && ((old == nil && value == nil) || (old != nil && value != nil && *old == *value)) {
			return
		}
		d.written[key] = value
		kind, id := key[:1], key[2:]
		tokens := map[string]any{"mon": nullable(value)}
		if kind == "w" {
			rail := railValue(species, value, d.rails[id])
			d.rails[id] = rail
			tokens["mon_rail"] = nullable(rail)
		}
		if kind == "p" {
			writes = append(writes, monWrite{at, "pane.report_metadata", map[string]any{"pane_id": id, "source": source, "tokens": tokens}})
		} else {
			writes = append(writes, monWrite{at, "workspace.report_metadata", map[string]any{"workspace_id": id, "source": source, "tokens": tokens}})
		}
	}
	str := func(s string) *string { return &s }

	busy := false
	groups := workspaces(panes)
	present := map[string]bool{}
	paneExists := map[string]bool{}
	for _, p := range panes {
		paneExists[idString(p["pane_id"])] = true
	}
	for _, w := range groups {
		present[w.ID] = true
		st := d.stateFor(w.ID)
		statuses := make([]string, len(w.Agents))
		for i, p := range w.Agents {
			statuses[i] = idString(p["agent_status"])
		}
		status := rollup(statuses)
		if st.force != nil && now > st.force.until {
			st.force = nil
		}
		if st.force != nil && st.force.status != "" {
			status = st.force.status
		}
		observe(st.AnimState, status, len(w.Agents) > 0, d.scanned)

		for _, p := range w.Agents {
			cwd := ""
			for _, k := range []string{"foreground_cwd", "cwd"} {
				if cwd == "" && truthy(p[k]) {
					cwd = jsString(p[k])
				}
			}
			if cwd == "" {
				continue
			}
			go func(st *wsState, cwd string) {
				ev := gitEvents(cwd, now)
				if !ev.Commit && !ev.Push {
					return
				}
				d.mu.Lock()
				defer d.mu.Unlock()
				if ev.Commit {
					enqueue(st.AnimState, "tackle", "")
				}
				if ev.Push {
					enqueue(st.AnimState, "harden", "")
				}
			}(st, cwd)
		}

		// The head moved (closed, reordered): take the Pokémon off the old row.
		head := ""
		if w.Head != nil {
			head = idString(w.Head["pane_id"])
		}
		if old := d.heads[w.ID]; old != "" && old != head && paneExists[old] {
			put("p:"+old, target, nil)
		}
		d.heads[w.ID] = head
		keys := []string{"w:" + w.ID}
		if head != "" {
			keys = append(keys, "p:"+head)
		}

		if species == nil {
			value := coloured("overlay1", d.cfg.Sidebar.str("fallback_glyph"))
			for _, k := range keys {
				put(k, target, str(value))
			}
			continue
		}
		nightNow := isNight
		if st.force != nil && st.force.night != nil {
			nightNow = *st.force.night
		}
		r := pick(st.AnimState, status, species, target, nightNow)
		busy = busy || r.Busy || len(st.Queue) > 0
		if r.Entry != nil {
			entry := r.Entry
			wait := r.End - d.clock()
			if wait < 0 {
				wait = 0
			}
			time.AfterFunc(time.Duration(wait)*time.Millisecond, func() { d.finished(entry) })
		}
		if r.Hold {
			continue
		}
		timed := r.Shot
		if timed == nil {
			timed = []Timed{{At: target, Frame: r.Frame}}
		}
		for _, t := range timed {
			for _, k := range keys {
				put(k, t.At, str(t.Frame))
			}
		}
	}

	// Rows that carry a $mon from us but no longer head anything.
	headSet := map[string]bool{}
	for _, h := range d.heads {
		if h != "" {
			headSet[h] = true
		}
	}
	for _, p := range panes {
		id := idString(p["pane_id"])
		k := "p:" + id
		tokens, _ := p["tokens"].(map[string]any)
		_, hasMon := tokens["mon"]
		old, seen := d.written[k]
		if !headSet[id] && hasMon && !(seen && old == nil) {
			put(k, target, nil)
		}
	}
	for id := range d.ws {
		if !present[id] {
			delete(d.ws, id)
		}
	}
	for id := range d.heads {
		if !present[id] {
			delete(d.heads, id)
		}
	}
	for id := range d.rails {
		if !present[id] {
			delete(d.rails, id)
		}
	}
	d.scanned = true

	lead := d.lead()
	for _, w := range writes {
		real := w.at - lead
		go func(w monWrite) {
			reply := ipcCallAt(w.method, w.params, float64(real))
			if debugOn() {
				extra := ""
				if reply != nil && len(reply.Error) > 0 && string(reply.Error) != "null" {
					extra = string(reply.Error)
				}
				tok, _ := json.Marshal(w.params["tokens"])
				daemonLog(fmt.Sprintf("%s %s late %dms %s", w.method, tok, nowMS()-real, extra))
			}
		}(w)
	}
	if busy {
		return tickMS, target, true
	}
	return bobMS, target, true
}

// finished answers the play requests waiting for entry.
func (d *daemon) finished(entry *ShotEntry) {
	d.mu.Lock()
	var replies []func(map[string]any)
	for i := len(d.waiting) - 1; i >= 0; i-- {
		if d.waiting[i].entry == entry {
			replies = append(replies, d.waiting[i].reply)
			d.waiting = append(d.waiting[:i], d.waiting[i+1:]...)
		}
	}
	d.mu.Unlock()
	for _, r := range replies {
		r(map[string]any{"ok": true, "done": entry.Name})
	}
}

func (d *daemon) frameLoop() {
	period, after := int64(tickMS), int64(0)
	for {
		d.mu.Lock()
		stopped := d.stopped
		d.mu.Unlock()
		if stopped {
			return
		}
		next := nextBoundary(d.clock(), period, prepMS, after)
		if wait := next - prepMS - d.clock(); wait > 0 {
			time.Sleep(time.Duration(wait) * time.Millisecond)
		}
		var ok bool
		func() {
			defer func() {
				if r := recover(); r != nil {
					daemonLog(fmt.Sprintf("step: %v\n%s", r, debug.Stack()))
					period, after, ok = bobMS, next, true
				}
			}()
			period, after, ok = d.step(next)
		}()
		if !ok {
			return
		}
	}
}

/* ------------------------------------------------- $agent and $tools */

type toolTokenSet = map[string]any

func toolTokens(list []*Desc, indent string) toolTokenSet {
	t := toolTokenSet{"tools": nil}
	for i := 0; i < toolSlots; i++ {
		t["tool_"+strconv.Itoa(i+1)] = nil
	}
	if len(list) == 0 {
		return t
	}
	parts := make([]string, len(list))
	for i, d := range list {
		parts[i] = d.Glyph + " " + d.Label
	}
	t["tools"] = coloured("subtext0", strings.Join(parts, "  "))
	for i, d := range list {
		if i >= toolSlots {
			break
		}
		pre := ""
		if i == 0 {
			pre = indent
		}
		t["tool_"+strconv.Itoa(i+1)] = coloured(d.Colour, pre+d.Glyph+" "+d.Label)
	}
	if len(list) > toolSlots {
		k := "tool_" + strconv.Itoa(toolSlots)
		t[k] = t[k].(string) + " +" + strconv.Itoa(len(list)-toolSlots+1)
	}
	return t
}

// uniqueDescs: shown entries, the first of each label (a hidden entry still
// claims its label).
func uniqueDescs(list []*Desc) []*Desc {
	var out []*Desc
	for i, d := range list {
		if d == nil || d.Hide {
			continue
		}
		first := -1
		for j, e := range list {
			if e != nil && e.Label == d.Label {
				first = j
				break
			}
		}
		if first == i {
			out = append(out, d)
		}
	}
	return out
}

func foregroundProc(paneID any) *Proc {
	r := ipcCall("pane.process_info", map[string]any{"pane_id": paneID})
	if r == nil {
		return nil
	}
	var res struct {
		ProcessInfo struct {
			Foreground []map[string]any `json:"foreground_processes"`
		} `json:"process_info"`
	}
	if json.Unmarshal(r.Result, &res) != nil || len(res.ProcessInfo.Foreground) == 0 || res.ProcessInfo.Foreground[0] == nil {
		return nil
	}
	f := res.ProcessInfo.Foreground[0]
	p := &Proc{Name: f["name"], Pid: int(toNumber(f["pid"]))}
	if truthy(f["cwd"]) {
		p.Cwd = jsString(f["cwd"])
	}
	return p
}

type paneDesc struct {
	p map[string]any
	d *Desc
}

// toolsPass publishes $agent and $tools once.
func (d *daemon) toolsPass() {
	panes := paneList()
	if panes == nil {
		return
	}
	d.mu.Lock()
	table, glyphs := d.cfg.Agents, d.cfg.Sidebar.str("glyphs")
	d.mu.Unlock()
	descs := make([]*Desc, len(panes))
	var wg sync.WaitGroup
	for i, p := range panes {
		wg.Add(1)
		go func(i int, p map[string]any) {
			defer wg.Done()
			defer func() { _ = recover() }()
			descs[i] = describe(table, p, foregroundProc(p["pane_id"]), glyphs)
		}(i, p)
	}
	wg.Wait()

	var sends []func()
	send := func(kind, id string, tokens map[string]any) {
		key := kind + ":" + id
		js := string(marshalNoEscape(tokens))
		d.mu.Lock()
		same := d.toolsSent[key] == js
		if !same {
			d.toolsSent[key] = js
		}
		d.mu.Unlock()
		if same {
			return
		}
		if kind == "p" {
			sends = append(sends, func() {
				ipcCall("pane.report_metadata", map[string]any{"pane_id": id, "source": source, "tokens": tokens})
			})
		} else {
			sends = append(sends, func() {
				ipcCall("workspace.report_metadata", map[string]any{"workspace_id": id, "source": source, "tokens": tokens})
			})
		}
	}
	order := make([]paneDesc, len(panes))
	for i, p := range panes {
		order[i] = paneDesc{p, descs[i]}
	}
	sort.SliceStable(order, func(i, j int) bool { return paneOrder(order[i].p, order[j].p) < 0 })
	for _, o := range order {
		if !truthy(o.p["agent"]) {
			continue
		}
		var agent any
		if o.d != nil {
			agent = coloured(o.d.Colour, o.d.Glyph+" "+o.d.Label)
		}
		var others []*Desc
		for _, x := range order {
			if sameID(x.p["tab_id"], o.p["tab_id"]) && !sameID(x.p["pane_id"], o.p["pane_id"]) {
				others = append(others, x.d)
			}
		}
		tokens := toolTokens(uniqueDescs(others), "")
		tokens["agent"] = agent
		send("p", idString(o.p["pane_id"]), tokens)
	}
	var spaceIDs []string
	spaces := map[string][]*Desc{}
	for _, o := range order {
		id := idString(o.p["workspace_id"])
		if _, ok := spaces[id]; !ok {
			spaceIDs = append(spaceIDs, id)
		}
		spaces[id] = append(spaces[id], o.d)
	}
	for _, id := range spaceIDs {
		send("w", id, toolTokens(uniqueDescs(spaces[id]), ""))
	}
	alive := map[string]bool{}
	for _, p := range panes {
		alive["p:"+idString(p["pane_id"])] = true
	}
	for _, id := range spaceIDs {
		alive["w:"+id] = true
	}
	d.mu.Lock()
	for k := range d.toolsSent {
		if !alive[k] {
			delete(d.toolsSent, k)
		}
	}
	d.mu.Unlock()
	var sw sync.WaitGroup
	for _, s := range sends {
		sw.Add(1)
		go func(s func()) { defer sw.Done(); s() }(s)
	}
	sw.Wait()
}

func (d *daemon) toolsLoop() {
	for {
		d.mu.Lock()
		stopped := d.stopped
		d.mu.Unlock()
		if stopped {
			return
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					daemonLog(fmt.Sprintf("tools: %v\n%s", r, debug.Stack()))
				}
			}()
			d.toolsPass()
		}()
		time.Sleep(toolsMS * time.Millisecond)
	}
}

/* -------------------------------------------------------- control */

func emptyTokens() map[string]any {
	t := map[string]any{"mon": nil, "mon_rail": nil, "agent": nil, "tools": nil}
	for i := 0; i < toolSlots; i++ {
		t["tool_"+strconv.Itoa(i+1)] = nil
	}
	return t
}

func clearAll() {
	panes := paneList()
	var wg sync.WaitGroup
	seen := map[string]bool{}
	var wsIDs []string
	for _, p := range panes {
		wg.Add(1)
		go func(id any) {
			defer wg.Done()
			ipcCall("pane.report_metadata", map[string]any{"pane_id": id, "source": source, "tokens": emptyTokens()})
		}(p["pane_id"])
		id := idString(p["workspace_id"])
		if !seen[id] {
			seen[id] = true
			wsIDs = append(wsIDs, id)
		}
	}
	for _, id := range wsIDs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			ipcCall("workspace.report_metadata", map[string]any{"workspace_id": id, "source": source, "tokens": emptyTokens()})
		}(id)
	}
	wg.Wait()
}

// shutdown stops the loops, clears every token and closes the socket.
func (d *daemon) shutdown() {
	d.stopOnce.Do(func() {
		d.mu.Lock()
		d.stopped = true
		d.mu.Unlock()
		// Frames are sent up to prepMS ahead of their boundary; let those
		// land first, or they'd repaint a token right after it was cleared.
		time.Sleep(time.Duration((prepMS + pollMS + 150) * float64(time.Millisecond)))
		clearAll()
		_ = d.listener.Close()
		_ = os.Remove(controlSocket())
		close(d.stopDone)
	})
	<-d.stopDone
}

func (d *daemon) handle(conn net.Conn) {
	line := readLine(conn, time.Now().Add(24*time.Hour))
	if line == nil {
		conn.Close()
		return
	}
	var msg map[string]any
	_ = json.Unmarshal(line, &msg) // anything else is an unknown command
	reply := func(v map[string]any) {
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = conn.Write(marshalNoEscape(v))
		conn.Close()
	}
	cmd, _ := msg["cmd"].(string)
	workspace := ""
	if truthy(msg["workspace"]) {
		workspace = jsString(msg["workspace"])
	}
	switch {
	case cmd == "ping":
		d.mu.Lock()
		r := map[string]any{"ok": true, "pid": os.Getpid(), "mon": d.cfg.Mon, "self": d.cfg.Self, "frames": d.frames.Anim(d.cfg.Mon) != nil}
		d.mu.Unlock()
		reply(r)
	case cmd == "stop":
		d.shutdown()
		reply(map[string]any{"ok": true})
		time.AfterFunc(50*time.Millisecond, func() { os.Exit(0) })
	case cmd == "play" && workspace != "" && contains(oneShots, jsString(msg["anim"])) && msg["anim"] != nil:
		d.mu.Lock()
		if d.frames.Anim(d.cfg.Mon) == nil {
			mon := d.cfg.Mon
			d.mu.Unlock()
			reply(map[string]any{"ok": false, "error": "no frames for " + mon + " (or font missing)"})
			return
		}
		entry := &ShotEntry{Name: jsString(msg["anim"]), Done: "request"}
		st := d.stateFor(workspace)
		st.Queue = append(st.Queue, entry)
		d.waiting = append(d.waiting, waiter{entry, reply})
		d.mu.Unlock()
	case cmd == "force" && workspace != "":
		ms := toNumber(msg["ms"])
		if ms != ms || ms == 0 {
			ms = 2000
		}
		f := &force{until: nowMS() + int64(ms)}
		if truthy(msg["status"]) {
			f.status = jsString(msg["status"])
		}
		if n, ok := msg["night"].(bool); ok {
			f.night = &n
		}
		d.mu.Lock()
		d.stateFor(workspace).force = f
		d.mu.Unlock()
		time.AfterFunc(time.Duration(ms)*time.Millisecond, func() { reply(map[string]any{"ok": true}) })
	default:
		reply(map[string]any{"ok": false, "error": "unknown command"})
	}
}

// runDaemon serves until stopped; false when another daemon already serves
// this Herdr socket.
func runDaemon() (bool, error) {
	cfg := loadConfig(configPath())
	d := &daemon{
		cfg:       cfg,
		frames:    newFrames(framesPath(), nil),
		night:     newNight(cfg.Sidebar),
		ws:        map[string]*wsState{},
		written:   map[string]*string{},
		rails:     map[string]*string{},
		heads:     map[string]string{},
		toolsSent: map[string]string{},
		stopDone:  make(chan struct{}),
	}
	d.frames.Load()
	// The first config check (after configMS) only reloads on a change.
	if st, err := os.Stat(configPath()); err == nil {
		d.cfgMtime = st.ModTime()
	}
	d.cfgCheck = nowMS()

	l, err := lock()
	if err != nil {
		return false, err
	}
	if l == nil {
		return false, nil
	}
	d.listener = l
	daemonLog(fmt.Sprintf("started pid %d self=%s mon=%s frames=%s font=%t night=%s", os.Getpid(), cfg.Self, cfg.Mon, d.frames.File, d.frames.FontOK, d.night.Plan.Mode))

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		<-sigs
		d.shutdown()
		os.Exit(0)
	}()
	go d.frameLoop()
	go d.toolsLoop()
	for {
		conn, err := l.Accept()
		if err != nil {
			d.mu.Lock()
			stopped := d.stopped
			d.mu.Unlock()
			if stopped {
				select {} // shutdown is finishing; the stop handler exits
			}
			daemonLog("accept: " + err.Error())
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go d.handle(conn)
	}
}

// sameID: JavaScript's === for the scalar ids Herdr sends.
func sameID(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case string:
		y, ok := b.(string)
		return ok && x == y
	case float64:
		y, ok := b.(float64)
		return ok && x == y
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	}
	return false
}
