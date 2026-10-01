package main

// herdrmon pick: the Pokémon selector. Devices on the left (party-menu
// slots), every Gen 3 species on the right with a search box and a live
// preview. Enter writes `mon = "<slug>"` into config.toml and offers to
// rebuild the font, since only baked mons have sprites.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

type pickModel struct {
	w, h    int
	th      *Theme
	paths   Paths
	frames  *Frames
	devs    []*Device
	dev     int // selected device
	onList  bool
	search  textinput.Model
	list    []Species
	sel     int // index into list
	top     int // first visible row of the list
	msg     string
	err     bool
	ask     bool // "rebuild the font now?"
	dirty   bool // a mon changed since the last build
	rebuild bool
	start   time.Time
}

func pickMain(args []string) error {
	p := resolvePaths()
	cfg, err := loadConfig(p.Config)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	devs := discover(ctx, cfg, detectMux(ctx, cfg))
	cancel()
	ti := textinput.New()
	ti.CharLimit = 24
	ti.Prompt = ""
	ti.Focus()
	m := pickModel{th: loadTheme(), paths: p, frames: loadFrames(p.Frames), devs: devs,
		onList: true, search: ti, list: searchSpecies(""), start: time.Now()}
	m.jumpTo(devs[0].Mon)
	prog := tea.NewProgram(m, tea.WithAltScreen())
	res, err := prog.Run()
	if err != nil {
		return err
	}
	pm := res.(pickModel)
	cmd := buildCommand(p)
	switch {
	case pm.rebuild && p.BuildPy != "":
		args := []string{"run", p.BuildPy, "--config", p.Config, "--out-font", p.Font, "--out-frames", p.Frames}
		fmt.Println("herdrmon: uv " + strings.Join(args, " "))
		c := exec.Command("uv", args...)
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := c.Run(); err != nil {
			return fmt.Errorf("font build: %w", err)
		}
		if _, err := exec.LookPath("fc-cache"); err == nil {
			exec.Command("fc-cache", "-f").Run()
		}
	case pm.rebuild || pm.dirty:
		fmt.Println("herdrmon: rebuild the font to see the new mons:\n  " + cmd)
	}
	return nil
}

func (m *pickModel) jumpTo(slug string) {
	for i, s := range m.list {
		if s.Slug == slug {
			m.sel = i
			return
		}
	}
}

func (m pickModel) Init() tea.Cmd { return tea.Batch(tick(), textinput.Blink) }

func (m pickModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil
	case tickMsg:
		if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 && (w != m.w || h != m.h) {
			m.w, m.h = w, h
		}
		if m.th.changed() {
			m.th = loadTheme()
		}
		if m.frames.changed(time.Time(msg)) {
			m.frames = loadFrames(m.frames.path)
		}
		return m, tick()
	case tea.KeyMsg:
		return m.key(msg)
	}
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	return m, cmd
}

func (m pickModel) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.ask {
		switch strings.ToLower(k.String()) {
		case "y", "enter":
			m.rebuild = true
			return m, tea.Quit
		case "n", "esc":
			m.ask, m.msg = false, "Rebuild later with herdrmon pick, or: "+buildCommand(m.paths)
		case "ctrl+c":
			return m, tea.Quit
		}
		return m, nil
	}
	page := max(1, m.listRows()-1)
	switch k.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		return m, tea.Quit
	case tea.KeyTab, tea.KeyShiftTab:
		m.onList = !m.onList
		return m, nil
	case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown:
		step := map[tea.KeyType]int{tea.KeyUp: -1, tea.KeyDown: 1, tea.KeyPgUp: -page, tea.KeyPgDown: page}[k.Type]
		if m.onList {
			m.sel = max(0, min(len(m.list)-1, m.sel+step))
		} else {
			m.dev = (m.dev + step%len(m.devs) + len(m.devs)) % len(m.devs)
			m.jumpTo(m.devs[m.dev].Mon)
		}
		return m, nil
	case tea.KeyEnter:
		if !m.onList {
			m.onList = true
			return m, nil
		}
		return m.choose()
	}
	// everything else edits the search
	m.onList = true
	prev := m.search.Value()
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(k)
	if m.search.Value() != prev {
		m.list = searchSpecies(m.search.Value())
		m.sel, m.top = 0, 0
	}
	return m, cmd
}

func (m pickModel) choose() (tea.Model, tea.Cmd) {
	if len(m.list) == 0 {
		return m, nil
	}
	d, sp := m.devs[m.dev], m.list[m.sel]
	if err := setDeviceMon(m.paths.Config, d.Node, sp.Slug); err != nil {
		m.msg, m.err = err.Error(), true
		return m, nil
	}
	d.Mon, m.err = sp.Slug, false
	if _, baked := m.frames.Mons[sp.Slug]; baked {
		m.msg = fmt.Sprintf("%s is now %s!", strings.ToUpper(d.Display), strings.ToUpper(sp.Display))
		return m, nil
	}
	m.dirty = true
	m.msg = fmt.Sprintf("%s is now %s! Rebuild the font now? (Y/N)", strings.ToUpper(d.Display), strings.ToUpper(sp.Display))
	m.ask = true
	return m, nil
}

/* -------------------------------------------------------------- render */

const pickLeftW, pickSlotH, previewH = 30, 4, 7

func (m pickModel) listRows() int { return max(1, m.h-previewH-3-6) }

func (m pickModel) View() string {
	if m.w == 0 {
		return ""
	}
	now := time.Now()
	v := &view{th: m.th, now: now, sec: now.Sub(m.start).Seconds()}
	t := m.th
	v.cv = newCanvas(m.w, m.h, func(x, y int) RGB {
		d := float64(x+2*y)/9 - v.sec*.35
		return mix(mix(t.DarkerBg, t.Blue, .05), mix(t.DarkBg, t.Cyan, .09), ease(pulse(d/2))*.7)
	})
	msgH := 5
	// devices
	vis := max(1, (m.h-msgH-1)/pickSlotH)
	top := max(0, min(m.dev-vis+1, len(m.devs)-vis))
	for i := 0; i < vis && top+i < len(m.devs); i++ {
		d := m.devs[top+i]
		y := 1 + i*pickSlotH
		look := lookNormal
		if top+i == m.dev {
			look = lookSelected
		}
		v.slot(1, y, pickLeftW, pickSlotH, look)
		m.drawMon(v, 2, y+1, 9, 2, d.Mon, top+i == m.dev)
		v.cv.text(12, y+1, trunc(strings.ToUpper(d.Display), pickLeftW-13), t.Fg, true)
		name := d.Mon
		if sp, ok := speciesBySlug[d.Mon]; ok {
			name = sp.Display
		}
		v.cv.text(12, y+2, trunc(strings.ToUpper(name), pickLeftW-13), mix(t.Fg, t.Accent, .35), false)
	}
	// preview
	rx, rw := pickLeftW+2, m.w-pickLeftW-3
	v.slot(rx, 1, rw, previewH, lookNormal)
	if len(m.list) > 0 {
		sp := m.list[m.sel]
		m.drawMon(v, rx+1, 2, 11, previewH-2, sp.Slug, true)
		x := v.cv.text(rx+13, 2, fmt.Sprintf("No.%03d", sp.Dex), mix(t.Fg, t.Accent, .35), false)
		v.cv.text(x+2, 2, strings.ToUpper(sp.Display), t.Fg, true)
		note := "not in the font yet: Enter, then rebuild"
		if _, ok := m.frames.Mons[sp.Slug]; ok {
			note = "in the font"
		}
		v.cv.text(rx+13, 3, note, t.DarkFg, false)
		v.cv.text(rx+13, 5, "for "+strings.ToUpper(m.devs[m.dev].Display), t.DarkFg, false)
	}
	// search + list
	ly := previewH + 2
	lh := m.h - ly - msgH
	v.slot(rx, ly, rw, lh, map[bool]slotLook{true: lookSelected, false: lookNormal}[m.onList])
	sx := v.cv.pill(rx+2, ly+1, "SEARCH", t.DarkerBg, t.Accent)
	q := m.search.Value()
	v.cv.text(sx+1, ly+1, q, t.Fg, true)
	if q == "" {
		v.cv.text(sx+1, ly+1, "name or number", t.DarkFg, false)
	}
	if int(v.sec*2)%2 == 0 {
		if c := v.cv.at(sx+1+len([]rune(q)), ly+1); c != nil {
			c.fg, c.bg = t.LighterBg, mix(t.HP.Orange, t.HP.Yellow, .5)
		}
	}
	v.cv.text(rx+rw-12, ly+1, fmt.Sprintf("%3d found", len(m.list)), t.DarkFg, false)
	rows := lh - 3
	ltop := m.top
	if m.sel < ltop {
		ltop = m.sel
	}
	if m.sel >= ltop+rows {
		ltop = m.sel - rows + 1
	}
	cur := m.devs[m.dev].Mon
	for i := 0; i < rows && ltop+i < len(m.list); i++ {
		sp := m.list[ltop+i]
		y := ly + 2 + i
		fg := t.Fg
		if ltop+i == m.sel {
			for x := rx + 1; x < rx+rw-1; x++ {
				if c := v.cv.at(x, y); c != nil {
					c.bg = mix(c.bg, mix(t.HP.Orange, t.HP.Yellow, .5), .35)
				}
			}
		}
		mark := " "
		if sp.Slug == cur {
			mark = "●"
		}
		v.cv.text(rx+2, y, mark, t.Accent, true)
		x := v.cv.text(rx+4, y, fmt.Sprintf("No.%03d", sp.Dex), mix(t.Fg, t.Accent, .35), false)
		x = v.cv.text(x+2, y, strings.ToUpper(sp.Display), fg, ltop+i == m.sel)
		if _, ok := m.frames.Mons[sp.Slug]; ok {
			v.cv.text(x+2, y, string([]rune(m.frames.Mons[sp.Slug].Base[0])), t.Fg, false)
		}
	}
	// message
	my := m.h - msgH
	v.slot(1, my, m.w-2, 4, lookNormal)
	text := m.msg
	if text == "" {
		text = fmt.Sprintf("Which POKéMON should %s be?", strings.ToUpper(m.devs[m.dev].Display))
	}
	col := t.Fg
	if m.err {
		col = t.HP.Red
	}
	v.cv.text(3, my+1, trunc(text, m.w-6), col, true)
	v.cv.text(3, my+2, trunc("config: "+m.paths.Config, m.w-6), t.DarkFg, false)
	keys := "type to search  ↑↓ PgUp/PgDn  tab devices  ⏎ choose  esc done"
	v.cv.text(max(1, m.w-len([]rune(keys))-1), m.h-1, keys, mix(t.DarkFg, t.Fg, .2), false)
	return v.cv.String()
}

// drawMon: the slug's sprite centred in the box, bobbing; its name if the
// font doesn't have it yet.
func (m pickModel) drawMon(v *view, x, y, w, h int, slug string, anim bool) {
	f := m.frames
	var frames [][]string
	if b, ok := f.Big3[slug]; ok && h%2 == 1 && h >= 3 && len(b.Base) > 0 {
		frames = b.Bob
		if !anim || len(frames) == 0 {
			frames = b.Base
		}
	} else if b, ok := f.Big[slug]; ok && len(b.Base) > 0 {
		frames = b.Bob
		if !anim || len(frames) == 0 {
			frames = b.Base
		}
	} else if s, ok := f.Mons[slug]; ok && len(s.Base) > 0 {
		src := s.Bob
		if !anim || len(src) == 0 {
			src = s.Base
		}
		for _, fr := range src {
			frames = append(frames, []string{fr})
		}
	}
	if len(frames) == 0 {
		name := slug
		if sp, ok := speciesBySlug[slug]; ok {
			name = sp.Display
		}
		name = trunc(name, w)
		v.cv.text(x+max(0, (w-len([]rune(name)))/2), y+(h-1)/2, name, v.th.DarkFg, false)
		return
	}
	rows := frames[int(v.sec*10)%len(frames)]
	x += max(0, (w-len([]rune(rows[0])))/2)
	y += max(0, (h-len(rows))/2)
	for r, str := range rows {
		v.cv.text(x, y+r, str, v.th.Fg, false)
	}
}
