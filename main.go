package main

// herdrmon: pick a device in a Pokémon FireRed party menu and open a new
// Herdr workspace on it. Offline devices are FAINTED; devices without
// Herdr are PARalyzed (Status_Paralysis on their icon). Three HP bars per device (NET, SYS, BAT), Lv = days of
// uptime, and a Harden shine sweeps a bar every time its value updates.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

const refreshEvery = 15 * time.Second

/* ------------------------------------------------------------- sprites */

type monFrames struct {
	Bob       []string `json:"bob"`
	Party     []string `json:"party"`
	Paralysis []string `json:"paralysis"`
	Base      []string `json:"base"`
}

// bigFrames: the 2-row native-size renders (frames.json "big"): each
// frame is [row0, row1], five codepoints each.
type bigFrames struct {
	Base      [][]string `json:"base"`
	Bob       [][]string `json:"bob"`
	Party     [][]string `json:"party"`
	Paralysis [][]string `json:"paralysis"`
}

// Frames: frames.json, keyed by species slug. Big3: the big renders on a
// 3-row canvas (art half a row lower), for centring in an odd number of rows.
type Frames struct {
	Version int                  `json:"version"`
	Cells   int                  `json:"cells"`
	Font    string               `json:"font"`
	Mons    map[string]monFrames `json:"mons"`
	Big     map[string]bigFrames `json:"big"`
	Big3    map[string]bigFrames `json:"big3"`
	path    string
	mtime   time.Time
	checked time.Time
}

func loadFrames(path string) *Frames {
	f := &Frames{path: path}
	if fi, err := os.Stat(path); err == nil {
		f.mtime = fi.ModTime()
	}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, f)
	}
	f.checked = time.Now()
	return f
}

// changed: the file's mtime moved (checked at most once a second).
func (f *Frames) changed(now time.Time) bool {
	if now.Sub(f.checked) < time.Second {
		return false
	}
	f.checked = now
	fi, err := os.Stat(f.path)
	if err != nil {
		return !f.mtime.IsZero()
	}
	return !fi.ModTime().Equal(f.mtime)
}

/* --------------------------------------------------------------- model */

type slotState struct {
	dev    Device
	bars   [3]barState
	target [3]float64
	seen   bool // a probe result has arrived
}

type tickMsg time.Time
type refreshTick struct{}
type createdMsg struct {
	dev  string
	err  error
	text string
}
type quitMsg struct{}

type model struct {
	w, h     int
	th       *Theme
	cfg      Config
	mux      Mux
	frames   *Frames
	slots    []*slotState
	sel      int // 0..len(slots)-1, len(slots) = CANCEL
	scroll   int
	label    textinput.Model
	msg      string
	msgUntil time.Time
	err      string
	busy     bool
	start    time.Time
	loaded   bool
}

type view struct {
	cv  *Canvas
	th  *Theme
	now time.Time
	sec float64
}

var prog *tea.Program

func newModel() model {
	ti := textinput.New()
	ti.CharLimit = 48
	ti.Focus()
	cfg, err := loadConfig(resolvePaths().Config)
	m := model{th: loadTheme(), cfg: cfg, frames: loadFrames(resolvePaths().Frames), label: ti, start: time.Now()}
	if err != nil {
		m.err = err.Error()
	}
	return m
}

func tick() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func startRefresh(cfg Config) tea.Cmd {
	return func() tea.Msg {
		go refresh(context.Background(), cfg, func(m any) { prog.Send(m) })
		return nil
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(tick(), startRefresh(m.cfg), textinput.Blink,
		tea.Tick(refreshEvery, func(time.Time) tea.Msg { return refreshTick{} }))
}

func (m *model) slot(node string) *slotState {
	for _, s := range m.slots {
		if s.dev.Node == node {
			return s
		}
	}
	return nil
}

// setBar updates a bar's target and starts its shimmer. kind staggers the
// three bars of one device so the shine cascades NET -> SYS -> BAT.
func (s *slotState) setBar(k BarKind, v float64, now time.Time) {
	b := &s.bars[k]
	b.kind, b.row = k, int(k)
	if v < 0 {
		b.known = false
		return
	}
	if !b.known {
		b.shown = 0 // first value fills up from empty, like a party entering
	}
	b.known = true
	s.target[k] = v
	b.shimmer = now
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil

	case tickMsg:
		now := time.Time(msg)
		// A window rule can resize the terminal before Bubble Tea's SIGWINCH
		// handler is up, and that resize is never reported. Re-check the
		// real size every tick.
		// Send it as a WindowSizeMsg so the renderer repaints at the new size too.
		var resize tea.Cmd
		if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 && (w != m.w || h != m.h) {
			resize = func() tea.Msg { return tea.WindowSizeMsg{Width: w, Height: h} }
		}
		for _, s := range m.slots {
			for k := range s.bars {
				b := &s.bars[k]
				if b.known {
					// FRLG drains HP a pixel a frame; here: ease towards it
					b.shown += (s.target[k] - b.shown) * 0.16
					if math.Abs(s.target[k]-b.shown) < 0.002 {
						b.shown = s.target[k]
					}
				}
			}
		}
		if m.th.changed() {
			m.th = loadTheme()
		}
		if m.frames.changed(now) {
			m.frames = loadFrames(m.frames.path)
		}
		if !m.msgUntil.IsZero() && now.After(m.msgUntil) {
			m.msg, m.msgUntil = "", time.Time{}
		}
		if resize != nil {
			return m, tea.Batch(tick(), resize)
		}
		return m, tick()

	case refreshTick:
		return m, tea.Batch(startRefresh(m.cfg),
			tea.Tick(refreshEvery, func(time.Time) tea.Msg { return refreshTick{} }))

	case discoverMsg:
		var next []*slotState
		for _, d := range msg.devs {
			if s := m.slot(d.Node); s != nil {
				// keep bars and probe results; refresh identity/online state
				keep := s.dev
				s.dev = *d
				s.dev.Net, s.dev.Sys, s.dev.Bat = keep.Net, keep.Sys, keep.Bat
				s.dev.NetNote, s.dev.SysNote, s.dev.BatNote = keep.NetNote, keep.SysNote, keep.BatNote
				s.dev.Level, s.dev.LevelKnown, s.dev.Status = keep.Level, keep.LevelKnown, keep.Status
				next = append(next, s)
				continue
			}
			next = append(next, &slotState{dev: *d})
		}
		selNode := ""
		if m.sel < len(m.slots) {
			selNode = m.slots[m.sel].dev.Node
		}
		m.slots, m.loaded = next, true
		m.sel = len(m.slots)
		for i, s := range m.slots {
			if s.dev.Node == selNode || (selNode == "" && i == 0) {
				m.sel = i
				break
			}
		}
		if selNode == "" && len(m.slots) > 0 {
			m.sel = 0
		}
		return m, nil

	case probeMsg:
		s := m.slot(msg.dev.Node)
		if s == nil {
			return m, nil
		}
		now := time.Now()
		s.dev = msg.dev
		if !s.dev.Online {
			s.dev.Net, s.dev.Sys, s.dev.Bat = 0, 0, 0
		}
		s.setBar(NET, s.dev.Net, now)
		s.setBar(SYS, s.dev.Sys, now)
		s.setBar(BAT, s.dev.Bat, now)
		s.seen = true
		return m, nil

	case createdMsg:
		m.busy = false
		if msg.err != nil {
			m.flash(fmt.Sprintf("%s couldn't be sent out: %s", strings.ToUpper(msg.dev), msg.text), 6*time.Second)
			return m, nil
		}
		m.msg, m.msgUntil = fmt.Sprintf("Go! %s!", strings.ToUpper(msg.dev)), time.Time{}
		return m, tea.Tick(900*time.Millisecond, func(time.Time) tea.Msg { return quitMsg{} })

	case quitMsg:
		return m, tea.Quit

	case tea.KeyMsg:
		if m.busy {
			return m, nil
		}
		n := len(m.slots)
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyCtrlR:
			return m, startRefresh(m.cfg)
		case tea.KeyUp, tea.KeyShiftTab:
			m.sel = (m.sel + n) % (n + 1)
			return m, nil
		case tea.KeyDown, tea.KeyTab:
			m.sel = (m.sel + 1) % (n + 1)
			return m, nil
		case tea.KeyLeft:
			if m.label.Value() == "" || m.label.Position() == 0 {
				m.sel = 0
				return m, nil
			}
		case tea.KeyRight:
			if m.label.Value() == "" && m.sel == 0 && n > 1 {
				m.sel = 1
				return m, nil
			}
		case tea.KeyEnter:
			return m.choose()
		}
		var cmd tea.Cmd
		m.label, cmd = m.label.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.label, cmd = m.label.Update(msg)
	return m, cmd
}

func (m *model) flash(s string, d time.Duration) {
	m.msg, m.msgUntil = s, time.Now().Add(d)
}

// choose: FRLG's answers for slots that can't be sent out, else create.
func (m model) choose() (tea.Model, tea.Cmd) {
	if m.sel >= len(m.slots) {
		return m, tea.Quit
	}
	d := m.slots[m.sel].dev
	name := strings.ToUpper(d.Display)
	switch {
	case !d.Online:
		m.flash(name+" has no energy left to battle!", 3*time.Second)
		return m, nil
	case !d.CanBattle:
		m.flash(name+" is paralyzed! It can't move!", 3*time.Second)
		return m, nil
	}
	label := strings.TrimSpace(m.label.Value())
	if label == "" {
		m.flash("Give the new project a LABEL first.", 3*time.Second)
		return m, nil
	}
	m.busy = true
	m.msg, m.msgUntil = fmt.Sprintf("Sending out %s…", name), time.Time{}
	cfg := m.cfg
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		text, err := detectMux(ctx, cfg).create(ctx, &d, label)
		return createdMsg{dev: d.Display, err: err, text: text}
	}
}

/* -------------------------------------------------------------- render */

// Slot sizes. The sprite is 7 cells x 2 rows, so text starts at x+10. A
// wide slot is 4 rows (bars side by side) or, when every device fits at that
// height, 6 rows with the bars stacked like the lead slot's (tallH).
const leadW, leadH, slotH, tallH, textX = 28, 11, 4, 6, 10

func (m model) View() string {
	if m.w == 0 {
		return ""
	}
	now := time.Now()
	v := &view{th: m.th, now: now, sec: now.Sub(m.start).Seconds()}
	t := m.th
	// backdrop: FRLG's diagonal stripes, slowly drifting, in theme blues
	v.cv = newCanvas(m.w, m.h, func(x, y int) RGB {
		d := float64(x+2*y)/9 - v.sec*.35
		s := ease(pulse(d / 2))
		return mix(mix(t.DarkerBg, t.Blue, .05), mix(t.DarkBg, t.Cyan, .09), s*.7)
	})
	if !m.loaded {
		v.cv.text(2, 1, "Looking for devices…", t.Fg, false)
		if m.err != "" {
			v.cv.text(2, 2, m.err, t.HP.Red, false)
		}
		return v.cv.String()
	}
	n := len(m.slots)
	msgH := 5
	bodyH := m.h - msgH
	// lead slot: this machine
	if n > 0 {
		m.drawLead(v, 1, 1, m.slots[0], m.sel == 0)
	}
	// right column, scrolled so the selection stays visible
	rx, rw := leadW+2, m.w-leadW-3
	rest := n - 1
	sh := slotH
	if rest*tallH <= bodyH-1 {
		sh = tallH
	}
	vis := max(1, (bodyH-1)/sh)
	top := m.scroll
	selR := m.sel - 1
	if selR >= 0 && selR < rest {
		if selR < top {
			top = selR
		}
		if selR >= top+vis {
			top = selR - vis + 1
		}
	}
	top = max(0, min(top, rest-vis))
	for i := 0; i < vis && top+i < rest; i++ {
		m.drawWide(v, rx, 1+i*sh, rw, sh, m.slots[1+top+i], m.sel == 1+top+i)
	}
	if top > 0 {
		v.cv.text(rx+rw-4, 0, "▲", t.Accent, true)
	}
	if top+vis < rest {
		v.cv.text(rx+rw-4, 1+vis*sh, "▼", t.Accent, true)
	}
	m.drawMessage(v, m.h-msgH, n)
	return lipgloss.NewStyle().Render(v.cv.String())
}

func (m model) look(s *slotState, sel bool) slotLook {
	switch {
	case !s.dev.Online && sel:
		return lookFaintedSelected
	case !s.dev.Online:
		return lookFainted
	case sel:
		return lookSelected
	}
	return lookNormal
}

// sprite: FRLG icon timing from HP (pokemon_icon.c sMonIconAnim_*): full 6,
// green 8, yellow 14, red 22 game frames a frame; fainted holds still. The
// selected slot bounces (party frames).
// sprite draws the icon centred in the box x, y, w x h (cells). The art is
// centred on its 7x2 canvas by the font build; here the canvas is centred in
// the box. An odd box height uses the 3-row renders (art shifted half a row
// down in the glyphs), since cells can't be offset by half a row.
func (m model) sprite(v *view, x, y, w, h int, s *slotState, sel bool) {
	var rows []string
	f := m.frames
	b3, ok3 := f.Big3[s.dev.Mon]
	if ok3 && len(b3.Base) > 0 && h%2 == 1 && h >= 3 {
		rows = pickFrame(v, s, sel, b3.Base, b3.Bob, b3.Party, b3.Paralysis)
	} else if b, ok := f.Big[s.dev.Mon]; ok && len(b.Base) > 0 {
		rows = pickFrame(v, s, sel, b.Base, b.Bob, b.Party, b.Paralysis)
	} else if f, ok := f.Mons[s.dev.Mon]; ok && len(f.Base) > 0 {
		wrap := func(fr []string) [][]string {
			out := make([][]string, len(fr))
			for i, x := range fr {
				out[i] = []string{x}
			}
			return out
		}
		rows = pickFrame(v, s, sel, wrap(f.Base), wrap(f.Bob), wrap(f.Party), wrap(f.Paralysis))
	}
	if len(rows) == 0 {
		return
	}
	x += max(0, (w-len([]rune(rows[0])))/2)
	y += max(0, (h-len(rows))/2)
	for r, str := range rows {
		cx := x
		for _, ch := range str {
			if cl := v.cv.at(cx, y+r); cl != nil {
				cl.ch, cl.fg = string(ch), v.th.Fg
			}
			cx++
		}
	}
}

// pickFrame: FRLG icon timing from HP (pokemon_icon.c sMonIconAnim_*): full
// 6, green 8, yellow 14, red 22 game frames a frame; fainted holds still; the
// selected slot bounces; devices without Herdr play Status_Paralysis.
func pickFrame(v *view, s *slotState, sel bool, base, bob, party, par [][]string) []string {
	switch {
	case !s.dev.Online:
		return base[0]
	case !s.dev.CanBattle && len(par) > 0 && parFrame(v.now) >= 0:
		return par[min(parFrame(v.now), len(par)-1)]
	}
	frames := bob
	if sel && len(party) == 2 {
		frames = party
	}
	if len(frames) == 0 {
		return base[0]
	}
	worst := 1.0
	for _, b := range s.bars {
		if b.known {
			worst = math.Min(worst, b.shown)
		}
	}
	gf := 6.0
	switch {
	case worst <= .2:
		gf = 22
	case worst <= .5:
		gf = 14
	case worst < 1:
		gf = 8
	}
	return frames[int(v.sec*60/gf)%len(frames)]
}

func parFrame(now time.Time) int {
	ms := now.UnixMilli() % 2000
	if ms >= 12*50 {
		return -1
	}
	return int(ms / 50)
}

func (m model) statusPill(v *view, x, y int, s *slotState) int {
	t := v.th
	var tag string
	var bg RGB
	switch {
	case !s.dev.Online:
		tag, bg = "FNT", mix(t.HP.Red, t.Brown, .30)
	case !s.dev.CanBattle:
		tag, bg = "PAR", t.HP.Yellow.lean(t.HP.Orange, 12, 1.2) // FRLG's PAR is yellow
	case s.dev.Status == "BLK":
		tag, bg = "BLK", mix(t.Magenta, t.Blue, .25) // radar plays Poison for blocked
	case s.dev.Status == "WRK":
		tag, bg = "WRK", mix(t.HP.Green, t.HP.Cyan, .4)
	case s.dev.Status == "SLP":
		tag, bg = "SLP", t.Muted
	default:
		return x
	}
	return v.cv.pill(x, y, tag, t.DarkerBg, bg)
}

func (m model) nameAndLevel(v *view, x, y int, s *slotState, look slotLook) int {
	t := v.th
	name := strings.ToUpper(s.dev.Display)
	nameCol := t.Fg
	if look == lookFainted || look == lookFaintedSelected {
		nameCol = mix(t.Fg, t.HP.Red, .25)
	}
	x = v.cv.text(x, y, name, nameCol, true)
	lv := "Lv--"
	if s.dev.LevelKnown {
		lv = fmt.Sprintf("Lv%d", s.dev.Level)
	}
	return v.cv.text(x+2, y, lv, mix(t.Fg, t.Accent, .35), false)
}

func (m model) drawLead(v *view, x, y int, s *slotState, sel bool) {
	t := v.th
	look := m.look(s, sel)
	v.slot(x, y, leadW, leadH, look)
	m.sprite(v, x+1, y+1, textX-1, 3, s, sel) // the panel left of the name, above the bars
	m.nameAndLevel(v, x+textX, y+1, s, look)
	m.statusPill(v, x+textX, y+2, s)
	if !s.dev.CanBattle {
		v.cv.text(x+textX+6, y+2, "no Herdr", t.DarkFg, false) // after the PAR pill
	}
	bw := leadW - 9
	notes := [3]string{s.dev.NetNote, s.dev.SysNote, s.dev.BatNote}
	for k := 0; k < 3; k++ {
		ry := y + 4 + 2*k
		v.cv.pill(x+1, ry, barTags[k], t.DarkerBg, t.tag(BarKind(k)))
		m.drawBar(v, x+7, ry, bw, s, BarKind(k))
		note := notes[k]
		if !s.bars[k].known && s.dev.Online && note == "" {
			note = "…"
		}
		v.cv.text(x+7, ry+1, trunc(note, bw), t.DarkFg, false)
	}
}

func (m model) drawWide(v *view, x, y, w, h int, s *slotState, sel bool) {
	t := v.th
	look := m.look(s, sel)
	v.slot(x, y, w, h, look)
	m.sprite(v, x+1, y+1, textX-1, h-2, s, sel) // the panel left of the text
	end := m.nameAndLevel(v, x+textX, y+1, s, look)
	end = m.statusPill(v, end+2, y+1, s)
	// right side of row 1: what the bars mean right now
	var info string
	switch {
	case !s.dev.Online && !s.dev.LastSeen.IsZero():
		info = "last seen " + ago(v.now.Sub(s.dev.LastSeen))
	case h >= tallH && !s.dev.CanBattle:
		info = "no Herdr" // the readings are next to the bars
	case h >= tallH:
	case !s.dev.CanBattle:
		info = join("no Herdr", s.dev.NetNote, s.dev.BatNote)
	default:
		info = join(s.dev.NetNote, s.dev.SysNote, s.dev.BatNote)
	}
	room := x + w - 2 - (end + 2)
	if room > 4 {
		info = trunc(info, room)
		v.cv.text(x+w-2-len([]rune(info)), y+1, info, t.DarkFg, false)
	}
	if h >= tallH {
		// tall: the bars stacked, each with its reading after it
		notes := [3]string{s.dev.NetNote, s.dev.SysNote, s.dev.BatNote}
		bw := max(8, (w-textX-2-6)*3/5)
		for k := 0; k < 3; k++ {
			ry := y + 2 + k
			bx := v.cv.pill(x+textX, ry, barTags[k], t.DarkerBg, t.tag(BarKind(k)))
			m.drawBar(v, bx+1, ry, bw, s, BarKind(k))
			note := notes[k]
			if !s.bars[k].known && s.dev.Online && note == "" {
				note = "…"
			}
			if room := x + w - 2 - (bx + bw + 3); room > 3 {
				v.cv.text(bx+bw+3, ry, trunc(note, room), t.DarkFg, false)
			}
		}
		return
	}
	// short: the three bars side by side on row 2
	// three segments of pill(5) + gap(1) + bar(bw), 2 apart
	bw := max(4, (w-textX-2-3*6-4)/3)
	bx := x + textX
	for k := 0; k < 3; k++ {
		bx = v.cv.pill(bx, y+2, barTags[k], t.DarkerBg, t.tag(BarKind(k)))
		m.drawBar(v, bx+1, y+2, bw, s, BarKind(k))
		bx += bw + 3
	}
}

func (m model) drawBar(v *view, x, y, n int, s *slotState, k BarKind) {
	bs := s.bars[k]
	bs.kind, bs.row = k, int(k)
	if !s.dev.Online {
		bs.known, bs.shown = true, 0 // FAINTED: empty, not unknown
	}
	v.bar(x, y, n, bs)
}

func (m model) drawMessage(v *view, y, n int) {
	t := v.th
	w := m.w
	cw := 12
	mw := w - cw - 3
	// message box (FRLG's white box, here in the theme's light background)
	box := func(x, bw int, sel bool) {
		for j := 0; j < 4; j++ {
			for i := 0; i < bw; i++ {
				cl := v.cv.at(x+i, y+j)
				if cl == nil {
					continue
				}
				g := float64(i) / float64(max(1, bw-1))
				cl.bg = mix(mix(t.LighterBg, t.Fg, .06), t.LighterBg, g)
				edge := i == 0 || j == 0 || i == bw-1 || j == 3
				if !edge {
					continue
				}
				bc := mix(t.Accent, t.LighterBg, .35)
				if sel {
					bc = mix(mix(t.HP.Red, t.HP.Orange, .5), t.HP.Yellow, ease(pulse(v.sec/1.6))).saturate(1.35)
				}
				ch := "─"
				switch {
				case i == 0 && j == 0:
					ch = "╭"
				case i == bw-1 && j == 0:
					ch = "╮"
				case i == 0 && j == 3:
					ch = "╰"
				case i == bw-1 && j == 3:
					ch = "╯"
				case i == 0 || i == bw-1:
					ch = "│"
				}
				cl.ch, cl.fg = ch, bc
			}
		}
	}
	box(1, mw, false)
	text := m.msg
	if text == "" {
		text = "Choose your device."
		if m.err != "" {
			text = m.err
		}
		if m.sel < n {
			d := m.slots[m.sel].dev
			if !d.Online {
				text = strings.ToUpper(d.Display) + " has fainted. Choose your device."
			}
		}
	}
	v.cv.text(3, y+1, trunc(text, mw-4), t.Fg, true)
	// label input
	lx := v.cv.pill(3, y+2, "LABEL", t.DarkerBg, t.Accent)
	val := []rune(m.label.Value())
	pos := m.label.Position()
	room := mw - (lx - 1) - 4
	off := 0
	if pos > room {
		off = pos - room
	}
	for i := 0; i < room; i++ {
		c := v.cv.at(lx+1+i, y+2)
		if c == nil {
			break
		}
		if off+i < len(val) {
			c.ch, c.fg = string(val[off+i]), t.Fg
		} else {
			c.ch, c.fg = "·", mix(t.LighterBg, t.DarkFg, .35)
		}
		if off+i == pos && int(v.sec*2)%2 == 0 {
			c.fg, c.bg = t.LighterBg, mix(t.HP.Orange, t.HP.Yellow, .5)
		}
	}
	if len(val) == 0 {
		v.cv.text(lx+1, y+2, "name the new project", t.DarkFg, false)
		if int(v.sec*2)%2 == 0 {
			if c := v.cv.at(lx+1, y+2); c != nil {
				c.fg, c.bg = t.LighterBg, mix(t.HP.Orange, t.HP.Yellow, .5)
			}
		}
	}
	// CANCEL button
	cx := w - cw - 1
	box(cx, cw, m.sel >= n)
	cc := t.Fg
	if m.sel >= n {
		cc = mix(t.HP.Orange, t.HP.Yellow, .5)
	}
	v.cv.text(cx+3, y+1, "CANCEL", cc, true)
	v.cv.text(cx+3, y+2, "esc", t.DarkFg, false)
	keys := "↑↓ choose  ⏎ send out  ^R refresh"
	v.cv.text(max(1, w-len([]rune(keys))-1), m.h-1, keys, mix(t.DarkFg, t.Fg, .2), false)
}

func trunc(s string, n int) string {
	r := []rune(s)
	if n <= 0 {
		return ""
	}
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func join(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

func ago(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

const usage = `herdrmon: a FireRed party menu of your devices; Enter opens a
Herdr workspace on the one you pick.

usage:
  herdrmon            the party menu
  herdrmon pick       choose each device's Pokémon
  herdrmon serve [--listen 127.0.0.1:7643]
                      serve this machine's /status as JSON
  herdrmon doctor     check deps, font, frames and config
  herdrmon paths      print the resolved paths as JSON
`

func main() {
	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "":
		prog = tea.NewProgram(newModel(), tea.WithAltScreen())
		_, err = prog.Run()
	case "pick":
		err = pickMain(os.Args[2:])
	case "serve":
		err = serveMain(os.Args[2:])
	case "doctor":
		if !doctorMain() {
			os.Exit(1)
		}
	case "paths":
		b, _ := json.MarshalIndent(resolvePaths(), "", "  ")
		fmt.Println(string(b))
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "herdrmon:", err)
		os.Exit(1)
	}
}
