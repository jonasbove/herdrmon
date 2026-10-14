package main

// End to end: the daemon against a fake Herdr server (the socket API it
// uses: pane.list, pane.process_info, pane/workspace.report_metadata),
// driven through a scripted session: a blocked agent, the block answered, a
// head pane moving, a new agent, play/force requests, stop. The tokens it
// leaves at each checkpoint must match what the Node.js daemon left
// (testdata/golden/daemon-e2e.json).
//
// Regenerate the golden from a checkout of the Node.js plugin with
//   HERDRMON_JS_SIDEBAR=/path/to/old/sidebar go test -run TestE2EGenerateJS

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeHerdr struct {
	mu    sync.Mutex
	panes []map[string]any
	procs map[string]map[string]any // pane -> foreground process
	state map[string]map[string]any // "p:<id>" | "w:<id>" -> tokens
	bad   []string                  // protocol complaints
	l     net.Listener
}

func newFakeHerdr(t *testing.T, sock string) *fakeHerdr {
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeHerdr{l: l, procs: map[string]map[string]any{}, state: map[string]map[string]any{}}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	t.Cleanup(func() { l.Close() })
	return f
}

func (f *fakeHerdr) serve(c net.Conn) {
	defer c.Close()
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		return
	}
	var req struct {
		ID     any            `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	if json.Unmarshal(line, &req) != nil {
		return
	}
	f.mu.Lock()
	var result any = map[string]any{}
	switch req.Method {
	case "pane.list":
		var panes []map[string]any
		for _, p := range f.panes {
			q := map[string]any{}
			for k, v := range p {
				q[k] = v
			}
			if tok := f.state["p:"+p["pane_id"].(string)]; len(tok) > 0 {
				q["tokens"] = tok
			}
			panes = append(panes, q)
		}
		result = map[string]any{"panes": panes}
	case "pane.process_info":
		var fg []any
		if p := f.procs[jsString(req.Params["pane_id"])]; p != nil {
			fg = append(fg, p)
		}
		result = map[string]any{"process_info": map[string]any{"foreground_processes": fg}}
	case "pane.report_metadata", "workspace.report_metadata":
		key := "w:" + jsString(req.Params["workspace_id"])
		if req.Method == "pane.report_metadata" {
			key = "p:" + jsString(req.Params["pane_id"])
		}
		if req.Params["source"] != source {
			f.bad = append(f.bad, "source "+jsString(req.Params["source"]))
		}
		tokens, _ := req.Params["tokens"].(map[string]any)
		st := f.state[key]
		if st == nil {
			st = map[string]any{}
			f.state[key] = st
		}
		for k, v := range tokens {
			if v == nil {
				delete(st, k)
			} else {
				st[k] = v
			}
		}
		if len(st) == 0 {
			delete(f.state, key)
		}
	default:
		f.bad = append(f.bad, "method "+req.Method)
	}
	f.mu.Unlock()
	b, _ := json.Marshal(map[string]any{"id": req.ID, "result": result})
	_, _ = c.Write(append(b, '\n'))
}

func (f *fakeHerdr) snapshot() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]any{}
	for k, v := range f.state {
		c := map[string]any{}
		for tk, tv := range v {
			c[tk] = tv
		}
		out[k] = c
	}
	return out
}

func (f *fakeHerdr) set(panes []map[string]any, procs map[string]map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.panes = panes
	f.procs = procs
}

// e2eSession runs the scripted session against a daemon started by
// launch, and returns what it recorded.
func e2eSession(t *testing.T, launch func(env []string) *exec.Cmd) map[string]any {
	dir := t.TempDir()
	proj := filepath.Join(dir, "proj")
	_ = os.MkdirAll(proj, 0o755)
	_ = os.WriteFile(filepath.Join(proj, "package.json"), []byte(`{"dependencies":{"react":"19"}}`), 0o644)
	runtime := filepath.Join(dir, "run")
	_ = os.MkdirAll(runtime, 0o700)
	cfg := filepath.Join(dir, "config.toml")
	_ = os.WriteFile(cfg, []byte("self = \"testbox\"\n[[device]]\nname = \"testbox\"\nmon = \"onix\"\n[sidebar]\nnight_command = \"false\"\n"), 0o644)
	frames := filepath.Join(dir, "frames.json")
	_ = os.WriteFile(frames, []byte(`{"version":2,"mons":{"onix":{"bob":["B"],"poison":["P"],"sleep":["Z"],"sendout":["S1","S2"],"recall":["R1","R2"],"tackle":["T"],"harden":["H"]}}}`), 0o644)
	sock := filepath.Join(dir, "herdr.sock")
	env := map[string]string{
		"HOME": dir, "XDG_RUNTIME_DIR": runtime, "HERDR_SOCKET_PATH": sock, "HERDRMON_CONFIG": cfg,
		"HERDRMON_FRAMES": frames, "HERDRMON_STATE": filepath.Join(dir, "state"), "XDG_CONFIG_HOME": filepath.Join(dir, "cfg"),
		"XDG_STATE_HOME": filepath.Join(dir, "state-home"), "XDG_DATA_HOME": filepath.Join(dir, "data"),
	}
	var envList []string
	for _, k := range []string{"PATH", "TZ", "LANG"} {
		if v, ok := os.LookupEnv(k); ok {
			envList = append(envList, k+"="+v)
		}
	}
	for k, v := range env {
		t.Setenv(k, v) // for request() from this process
		envList = append(envList, k+"="+v)
	}
	herdr := newFakeHerdr(t, sock)

	proc := func(name string, cwd string) map[string]any {
		p := map[string]any{"pid": 0, "name": name}
		if cwd != "" {
			p["cwd"] = cwd
		}
		return p
	}
	pane := func(id, tab, ws, agent, status string) map[string]any {
		p := map[string]any{"pane_id": id, "tab_id": tab, "workspace_id": ws, "agent_status": status, "cwd": dir, "foreground_cwd": dir}
		if agent != "" {
			p["agent"] = agent
		}
		return p
	}
	procs := map[string]map[string]any{
		"w1:p1": proc("claude", ""), "w1:p2": proc("nvim", proj), "w1:p3": proc("zsh", ""), "w1:p4": proc("lazygit", ""),
		"w1:p5": proc("btop", ""), "w1:p6": proc("python3.12", ""), "w1:p7": proc("ssh", ""), "w1:p8": proc("codex", ""),
		"w2:p1": proc("node", ""), "w2:p2": proc("-bash", ""), "w3:p1": proc("yazi", ""), "w3:p2": proc("aider", ""),
	}
	phase1 := []map[string]any{
		pane("w1:p1", "w1:t1", "w1", "claude", "blocked"),
		pane("w1:p2", "w1:t1", "w1", "", "unknown"),
		pane("w1:p3", "w1:t1", "w1", "", "unknown"),
		pane("w1:p4", "w1:t1", "w1", "", "unknown"),
		pane("w1:p5", "w1:t1", "w1", "", "unknown"),
		pane("w1:p6", "w1:t1", "w1", "", "unknown"),
		pane("w1:p7", "w1:t1", "w1", "", "unknown"),
		pane("w1:p8", "w1:t2", "w1", "codex", "idle"),
		pane("w2:p1", "w2:t1", "w2", "gemini", "working"),
		pane("w2:p2", "w2:t1", "w2", "", "unknown"),
		pane("w3:p1", "w3:t1", "w3", "", "unknown"),
	}
	herdr.set(phase1, procs)
	// A $mon left on a non-head row by an earlier daemon: cleared.
	herdr.state["p:w1:p8"] = map[string]any{"mon": "stale"}

	cmd := launch(envList)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
	})

	rec := map[string]any{}
	var up map[string]any
	for i := 0; i < 50 && up == nil; i++ {
		time.Sleep(100 * time.Millisecond)
		up = request(map[string]any{"cmd": "ping"}, 500*time.Millisecond)
	}
	if up == nil {
		t.Fatal("daemon did not answer ping")
	}
	rec["ping"] = map[string]any{"ok": up["ok"], "mon": up["mon"], "self": up["self"], "frames": up["frames"]}

	time.Sleep(3 * time.Second)
	rec["phase1"] = herdr.snapshot()

	var phase2 []map[string]any
	for _, p := range phase1 {
		q := map[string]any{}
		for k, v := range p {
			q[k] = v
		}
		switch q["pane_id"] {
		case "w1:p1":
			q["agent_status"] = "idle" // the block is answered
		case "w1:p8":
			q["tab_id"] = "w1:t0" // moves ahead: the new head
		case "w2:p1":
			continue // the head closes
		}
		phase2 = append(phase2, q)
	}
	phase2 = append(phase2, pane("w3:p2", "w3:t1", "w3", "aider", "done"))
	herdr.set(phase2, procs)
	time.Sleep(3500 * time.Millisecond)
	rec["phase2"] = herdr.snapshot()

	rec["play"] = request(map[string]any{"cmd": "play", "anim": "tackle", "workspace": "w1"}, 15*time.Second)
	rec["force"] = request(map[string]any{"cmd": "force", "status": "blocked", "workspace": "w3", "ms": 700}, 15*time.Second)
	rec["bogus_play"] = request(map[string]any{"cmd": "play", "anim": "bogus", "workspace": "w1"}, 5*time.Second)
	rec["unknown"] = request(map[string]any{"cmd": "nope"}, 5*time.Second)
	time.Sleep(3 * time.Second)
	rec["phase3"] = herdr.snapshot()

	rec["stop"] = request(map[string]any{"cmd": "stop"}, 8*time.Second)
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Error("daemon did not exit after stop")
	}
	rec["stopped"] = herdr.snapshot()
	rec["after_stop_ping"] = request(map[string]any{"cmd": "ping"}, 500*time.Millisecond)
	herdr.mu.Lock()
	rec["protocol"] = herdr.bad
	herdr.mu.Unlock()
	if _, err := os.Stat(controlSocket()); err == nil {
		rec["socket_left"] = true
	}
	return rec
}

func normaliseJSON(t *testing.T, v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var g any
	_ = json.Unmarshal(b, &g)
	out, _ := json.MarshalIndent(g, "", " ") // maps marshal with sorted keys
	return string(out)
}

func TestE2EGenerateJS(t *testing.T) {
	js := os.Getenv("HERDRMON_JS_SIDEBAR")
	if js == "" {
		t.Skip("set HERDRMON_JS_SIDEBAR to the Node.js plugin to regenerate the golden")
	}
	rec := e2eSession(t, func(env []string) *exec.Cmd {
		c := exec.Command("node", filepath.Join(js, "bin", "daemon.js"))
		c.Env, c.Dir = env, js
		return c
	})
	if err := os.WriteFile(filepath.Join("testdata", "golden", "daemon-e2e.json"), []byte(normaliseJSON(t, rec)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestE2EDaemon(t *testing.T) {
	if testing.Short() {
		t.Skip("takes ~12 s")
	}
	want, err := os.ReadFile(filepath.Join("testdata", "golden", "daemon-e2e.json"))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "herdrmon-sidebar")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	rec := e2eSession(t, func(env []string) *exec.Cmd {
		c := exec.Command(bin, "daemon")
		c.Env = env
		return c
	})
	got := normaliseJSON(t, rec)
	if got != strings.TrimRight(string(want), "\n") {
		gl, wl := strings.Split(got, "\n"), strings.Split(strings.TrimRight(string(want), "\n"), "\n")
		for i := 0; i < len(gl) || i < len(wl); i++ {
			var g, w string
			if i < len(gl) {
				g = gl[i]
			}
			if i < len(wl) {
				w = wl[i]
			}
			if g != w {
				t.Errorf("line %d:\n go: %q\n js: %q", i+1, g, w)
			}
		}
	}
}
