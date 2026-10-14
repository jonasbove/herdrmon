package main

// What a pane is running, for $agent and $tools.
//
// Agents and programs come from the table (agents.go). Neovim gets one extra
// step: it's labelled by what it's editing — the project's framework when
// one is detected (FastAPI, React, ...), else the filetype of the file in its
// current window, else plain "neovim". The running nvim is asked over its
// RPC socket.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type icon struct{ Glyph, Colour string }

// icons: Nerd Font devicon + palette slot per language/framework.
var icons = map[string]icon{
	"javascript": {"", "yellow"},
	"typescript": {"", "blue"},
	"html":       {"", "peach"},
	"css":        {"", "blue"},
	"sass":       {"", "mauve"},
	"python":     {"", "blue"},
	"lua":        {"", "blue"},
	"rust":       {"", "peach"},
	"go":         {"", "teal"},
	"c":          {"", "blue"},
	"cpp":        {"", "blue"},
	"java":       {"", "red"},
	"kotlin":     {"", "mauve"},
	"swift":      {"", "peach"},
	"ruby":       {"", "red"},
	"php":        {"", "mauve"},
	"zig":        {"", "peach"},
	"dart":       {"", "blue"},
	"elixir":     {"", "mauve"},
	"haskell":    {"", "mauve"},
	"csharp":     {"", "mauve"},
	"markdown":   {"", "text"},
	"json":       {"", "yellow"},
	"yaml":       {"", "red"},
	"toml":       {"", "peach"},
	"bash":       {"", "green"},
	"nix":        {"", "blue"},
	"sql":        {"", "blue"},
	"docker":     {"", "blue"},
	"fastapi":    {"", "teal"},
	"django":     {"", "green"},
	"flask":      {"", "text"},
	"react":      {"", "blue"},
	"nextjs":     {"", "text"},
	"vue":        {"", "green"},
	"nuxt":       {"", "green"},
	"svelte":     {"", "peach"},
	"astro":      {"", "mauve"},
	"angular":    {"", "red"},
	"vite":       {"", "mauve"},
	"express":    {"", "text"},
	"electron":   {"", "blue"},
	"htmx":       {"", "blue"},
}

var iconLabels = map[string]string{"nextjs": "next.js", "cpp": "c++", "csharp": "c#"}

var filetypes = map[string]string{
	"javascript": "javascript", "javascriptreact": "react", "typescript": "typescript", "typescriptreact": "react",
	"html": "html", "css": "css", "scss": "sass", "sass": "sass", "python": "python", "lua": "lua", "rust": "rust", "go": "go",
	"c": "c", "cpp": "cpp", "java": "java", "kotlin": "kotlin", "swift": "swift", "ruby": "ruby", "php": "php", "zig": "zig",
	"dart": "dart", "elixir": "elixir", "haskell": "haskell", "cs": "csharp", "markdown": "markdown", "json": "json",
	"jsonc": "json", "yaml": "yaml", "toml": "toml", "sh": "bash", "bash": "bash", "zsh": "bash", "nix": "nix", "sql": "sql",
	"dockerfile": "docker", "svelte": "svelte", "vue": "vue", "astro": "astro",
}

// Framework markers, first match wins, most specific first.
var jsFrameworks = [][2]string{
	{"next", "nextjs"}, {"nuxt", "nuxt"}, {"@sveltejs/kit", "svelte"}, {"svelte", "svelte"}, {"astro", "astro"},
	{"@angular/core", "angular"}, {"vue", "vue"}, {"react", "react"}, {"electron", "electron"},
	{"express", "express"}, {"htmx.org", "htmx"}, {"vite", "vite"},
}

var pyFrameworks = [][2]string{{"fastapi", "fastapi"}, {"django", "django"}, {"flask", "flask"}}

var pyFrameworkRe = func() map[string]*regexp.Regexp {
	m := map[string]*regexp.Regexp{}
	for _, f := range pyFrameworks {
		m[f[0]] = regexp.MustCompile(`(?m)^\s*["']?` + regexp.QuoteMeta(f[0]) + `\b`)
	}
	return m
}()

var rePyProjectFile = regexp.MustCompile(`^(requirements.*\.txt|pyproject\.toml|Pipfile|setup\.cfg)$`)

func readText(file string) (string, bool) {
	b, err := os.ReadFile(file)
	if err != nil {
		return "", false
	}
	return string(b), true
}

type cached struct {
	at    time.Time
	value string
	info  *nvimInfo
}

var (
	projectMu    sync.Mutex
	projectCache = map[string]cached{}
)

const projectTTL = 60 * time.Second

// frameworkFor: the project framework in cwd, or "".
func frameworkFor(cwd string) string {
	if cwd == "" {
		return ""
	}
	projectMu.Lock()
	hit, ok := projectCache[cwd]
	projectMu.Unlock()
	if ok && time.Since(hit.at) < projectTTL {
		return hit.value
	}
	key := ""
	if pkg, ok := readText(filepath.Join(cwd, "package.json")); ok {
		var json1 map[string]json.RawMessage
		if json.Unmarshal([]byte(pkg), &json1) == nil {
			deps := map[string]bool{}
			for _, field := range []string{"dependencies", "devDependencies"} {
				var d map[string]json.RawMessage
				if json.Unmarshal(json1[field], &d) == nil {
					for k := range d {
						deps[k] = true
					}
				}
			}
			for _, f := range jsFrameworks {
				if deps[f[0]] {
					key = f[1]
					break
				}
			}
		}
	}
	if key == "" {
		var texts []string
		if entries, err := os.ReadDir(cwd); err == nil {
			for _, e := range entries {
				if rePyProjectFile.MatchString(e.Name()) {
					t, _ := readText(filepath.Join(cwd, e.Name()))
					texts = append(texts, t)
				}
			}
		}
		text := strings.ToLower(strings.Join(texts, "\n"))
		for _, f := range pyFrameworks {
			if pyFrameworkRe[f[0]].MatchString(text) {
				key = f[1]
				break
			}
		}
	}
	projectMu.Lock()
	projectCache[cwd] = cached{at: time.Now(), value: key}
	projectMu.Unlock()
	return key
}

// nvimLua: the filetype of the file in the current window (or the first
// real file in the current tab: the focused window may be an explorer),
// plus nvim's cwd.
const nvimLua = `local function ft(w)
  local b = vim.api.nvim_win_get_buf(w)
  if vim.bo[b].buftype == '' and vim.api.nvim_buf_get_name(b) ~= '' then return vim.bo[b].filetype end
end
local r = ft(0)
if not r then
  for _, w in ipairs(vim.api.nvim_tabpage_list_wins(0)) do r = ft(w); if r then break end end
end
return vim.fn.getcwd() .. '\t' .. (r or '')`

var nvimExpr = "luaeval('(function() " + strings.ReplaceAll(nvimLua, "'", "''") + " end)()')"

type nvimInfo struct{ Cwd, Filetype string }

var (
	nvimMu    sync.Mutex
	nvimCache = map[int]cached{}
)

const nvimTTL = 4 * time.Second

func runtimeDir() string {
	if v := os.Getenv("XDG_RUNTIME_DIR"); v != "" {
		return v
	}
	return "/run/user/" + strconv.Itoa(os.Getuid())
}

func parentPid(pid int) int {
	stat, ok := readText("/proc/" + strconv.Itoa(pid) + "/stat")
	if !ok {
		return -1
	}
	i := strings.LastIndex(stat, ")")
	if i < 0 || i+2 > len(stat) {
		return -1
	}
	fields := strings.Split(stat[i+2:], " ")
	if len(fields) < 2 {
		return -1
	}
	n, err := strconv.Atoi(fields[1])
	if err != nil {
		return -1
	}
	return n
}

var reNvimSocket = regexp.MustCompile(`^nvim\.\d+\.\d+$`)

// nvimSocketFor: nvim's TUI process (the pane's foreground) talks to an
// embedded child that owns the socket, named after that child's pid:
// $XDG_RUNTIME_DIR/nvim.<pid>.0
func nvimSocketFor(tuiPid int) string {
	entries, err := os.ReadDir(runtimeDir())
	if err != nil {
		return ""
	}
	for _, e := range entries {
		name := e.Name()
		if !reNvimSocket.MatchString(name) {
			continue
		}
		pid, _ := strconv.Atoi(strings.Split(name, ".")[1])
		if pid == tuiPid || parentPid(pid) == tuiPid {
			return filepath.Join(runtimeDir(), name)
		}
	}
	return ""
}

func queryNvim(tuiPid int) *nvimInfo {
	nvimMu.Lock()
	hit, ok := nvimCache[tuiPid]
	nvimMu.Unlock()
	if ok && time.Since(hit.at) < nvimTTL {
		return hit.info
	}
	if tuiPid == 0 {
		return nil
	}
	sock := nvimSocketFor(tuiPid)
	if sock == "" {
		return nil
	}
	bin := "nvim"
	if exe, err := os.Readlink("/proc/" + strconv.Itoa(tuiPid) + "/exe"); err == nil {
		bin = exe
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--server", sock, "--remote-expr", nvimExpr).Output()
	var info *nvimInfo
	if err == nil {
		cwd, ft, _ := strings.Cut(jsTrim(string(out)), "\t")
		info = &nvimInfo{Cwd: cwd, Filetype: ft}
	}
	nvimMu.Lock()
	nvimCache[tuiPid] = cached{at: time.Now(), info: info}
	nvimMu.Unlock()
	return info
}

// neovimLabel: framework beats language, "fastapi" says more about a repo
// than "python".
func neovimLabel(info *nvimInfo, fallbackCwd string) string {
	cwd := fallbackCwd
	if info != nil && info.Cwd != "" {
		cwd = info.Cwd
	}
	if key := frameworkFor(cwd); key != "" {
		return key
	}
	if info != nil {
		return filetypes[info.Filetype]
	}
	return ""
}

// Proc: a pane's foreground process (pane.process_info).
type Proc struct {
	Pid  int
	Name any
	Cwd  string
}

// Desc: what to draw for a pane.
type Desc struct {
	ID, Kind             string
	Glyph, Label, Colour string
	Hide                 bool
}

// describe: what a pane runs. pane holds the pane.list fields.
func describe(table AgentTable, pane map[string]any, proc *Proc, glyphs string) *Desc {
	var name any
	if proc != nil {
		name = proc.Name
	}
	hit := classify(table, pane, name)
	if hit == nil {
		return nil
	}
	l := look(hit.Entry, glyphs)
	out := &Desc{ID: hit.ID, Kind: hit.Kind, Glyph: l.Glyph, Label: l.Label, Colour: l.Colour, Hide: truthy(hit.Entry.field("hide"))}
	if hit.ID == "neovim" && hit.Kind == "program" {
		var info *nvimInfo
		fallback := ""
		if proc != nil {
			info = queryNvim(proc.Pid)
			fallback = proc.Cwd
		}
		for _, k := range []string{"foreground_cwd", "cwd"} {
			if fallback == "" && truthy(pane[k]) {
				fallback = jsString(pane[k])
			}
		}
		key := neovimLabel(info, fallback)
		if ic, ok := icons[key]; key != "" && ok {
			if glyphs != "text" {
				out.Glyph = ic.Glyph
			}
			out.Colour = ic.Colour
			out.Label = key
			if l, ok := iconLabels[key]; ok {
				out.Label = l
			}
		}
	}
	return out
}
