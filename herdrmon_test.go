package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSpeciesTable(t *testing.T) {
	if len(gen3) != 386 {
		t.Fatalf("%d species, want 386", len(gen3))
	}
	seen := map[string]bool{}
	for i, s := range gen3 {
		if s.Dex != i+1 || seen[s.Slug] || s.Slug != slugify(s.Slug) {
			t.Errorf("bad entry %d: %+v", i, s)
		}
		seen[s.Slug] = true
	}
	for in, want := range map[string]string{"ho-oh": "ho_oh", "Ho-Oh": "ho_oh", "Mr. Mime": "mr_mime", "farfetch'd": "farfetchd", "25": "pikachu", "386": "deoxys"} {
		if s, ok := lookupSpecies(in); !ok || s.Slug != want {
			t.Errorf("lookupSpecies(%q) = %v, want %s", in, s.Slug, want)
		}
	}
}

func TestDefaultMon(t *testing.T) {
	if got := defaultMon("ho-oh"); got != "ho_oh" {
		t.Errorf("ho-oh -> %s", got)
	}
	if got := defaultMon("Geodude"); got != "geodude" {
		t.Errorf("Geodude -> %s", got)
	}
	a, b := defaultMon("workstation"), defaultMon("workstation")
	if a != b {
		t.Error("not deterministic")
	}
	found := false
	for _, m := range machineMons {
		found = found || m == a
		if _, ok := speciesBySlug[m]; !ok {
			t.Errorf("machine mon %q is not a species", m)
		}
	}
	if !found {
		t.Errorf("workstation -> %s, not a machine mon", a)
	}
}

func TestSearchSpecies(t *testing.T) {
	first := func(q string) string {
		r := searchSpecies(q)
		if len(r) == 0 {
			return ""
		}
		return r[0].Slug
	}
	for q, want := range map[string]string{"25": "pikachu", "ho-oh": "ho_oh", "mime": "mr_mime", "geo": "geodude", "": "bulbasaur"} {
		if got := first(q); got != want {
			t.Errorf("search %q: first %q, want %q", q, got, want)
		}
	}
	if n := len(searchSpecies("zzz")); n != 0 {
		t.Errorf("zzz found %d", n)
	}
}

const sampleConfig = `# my devices
self = "alpha"

[discovery]
tailscale = false   # no tailnet here

[[device]]
name = "geodude"   # the server
mon = "geodude"    # keep this comment
transport = "ssh"

[device.battery]
source = "none"

[[device]]
name = "phone"
transport = "none"

[device.battery]
source = "homeassistant"
url = "http://ha:8123"
`

func TestSetMonText(t *testing.T) {
	out := setMonText(sampleConfig, "geodude", "onix")
	if !strings.Contains(out, `mon = "onix"    # keep this comment`) || strings.Contains(out, `mon = "geodude"`) {
		t.Errorf("replace lost the comment or didn't replace:\n%s", out)
	}
	out = setMonText(sampleConfig, "phone", "pikachu")
	if !strings.Contains(out, "name = \"phone\"\nmon = \"pikachu\"\ntransport") {
		t.Errorf("insert not after name:\n%s", out)
	}
	out = setMonText(sampleConfig, "laptop", "porygon")
	if !strings.HasSuffix(out, "\n[[device]]\nname = \"laptop\"\nmon = \"porygon\"\n") || !strings.HasPrefix(out, sampleConfig) {
		t.Errorf("append:\n%s", out)
	}
	if out := setMonText("", "x", "ditto"); out != "[[device]]\nname = \"x\"\nmon = \"ditto\"\n" {
		t.Errorf("empty file: %q", out)
	}
	// round trip through the file, with backup and parse check
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	os.WriteFile(p, []byte(sampleConfig), 0o644)
	for dev, mon := range map[string]string{"phone": "pikachu", "geodude": "onix", "laptop": "porygon"} {
		if err := setDeviceMon(p, dev, mon); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := loadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	for dev, mon := range map[string]string{"phone": "pikachu", "geodude": "onix", "laptop": "porygon"} {
		if d := cfg.device(dev); d == nil || d.Mon != mon {
			t.Errorf("%s: %+v", dev, d)
		}
	}
	if d := cfg.device("phone"); d.Battery.Source != "homeassistant" || d.Battery.URL != "http://ha:8123" {
		t.Errorf("phone battery lost: %+v", d.Battery)
	}
	if baks, _ := filepath.Glob(p + ".bak.*"); len(baks) == 0 {
		t.Error("no backup written")
	}
}

func TestLoadConfig(t *testing.T) {
	c, err := loadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil || !c.Discovery.Tailscale || c.Discovery.SSHConfig || !c.Discovery.Herdr || c.Mux != "auto" {
		t.Errorf("defaults: %+v %v", c, err)
	}
	p := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(p, []byte(sampleConfig+"\n[[device]]\nname = \"Bird\"\nmon = \"Ho-Oh\"\n\n[agents.foo]\nlabel = \"x\"\n"), 0o644)
	c, err = loadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.selfName() != "alpha" || c.Discovery.Tailscale || !c.Discovery.Herdr {
		t.Errorf("parsed: %+v", c)
	}
	if d := c.device("bird"); d == nil || d.Mon != "ho_oh" {
		t.Errorf("bird: %+v", d)
	}
}

func TestSSHHosts(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config")
	os.WriteFile(p, []byte("Host *\n  User me\nHost geodude build-box # comment\n  HostName 10.0.0.2\nhost *.lan !x\nHost my-pi\n"), 0o644)
	if got := strings.Join(sshHosts(p), ","); got != "geodude,build-box,my-pi" {
		t.Errorf("got %s", got)
	}
}

func TestHostOf(t *testing.T) {
	for in, want := range map[string]string{"user@geodude.example.ts.net": "geodude", "Geodude": "geodude", "pi:2222": "pi", "u@100.64.0.1": "100.64.0.1"} {
		if got := hostOf(in); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNight(t *testing.T) {
	for _, c := range []struct {
		h, s, e int
		want    bool
	}{{23, 22, 7, true}, {3, 22, 7, true}, {7, 22, 7, false}, {12, 22, 7, false}, {13, 12, 14, true}, {5, 0, 0, false}} {
		if got := inHours(c.h, c.s, c.e); got != c.want {
			t.Errorf("inHours(%d, %d, %d) = %v", c.h, c.s, c.e, got)
		}
	}
	ctx := context.Background()
	if !isNight(ctx, NightConfig{Command: "true"}) || isNight(ctx, NightConfig{Command: "exit 1"}) {
		t.Error("night command not honoured")
	}
}

// fakeHerdr puts a herdr on PATH that answers machine/pane lists and logs
// every call, and points HERDR_SOCKET_PATH away from any live server.
func fakeHerdr(t *testing.T) (logPath string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "herdr.log")
	script := `#!/bin/sh
echo "$@" >> "` + logPath + `"
case "$*" in
  "machine list --json") echo '[{"label":"Testbox","target":"user@testbox.example","enabled":true},{"label":"Off","target":"offbox","enabled":false}]' ;;
  *"pane list"*) echo '{"result":{"panes":[{"agent":"claude","agent_status":"blocked"},{"agent":null,"agent_status":"idle"}]}}' ;;
  *"workspace create"*) echo '{"result":{"type":"workspace_created"}}' ;;
esac
`
	os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0o755)
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	t.Setenv("HERDR_SOCKET_PATH", filepath.Join(dir, "no.sock"))
	t.Setenv("XDG_RUNTIME_DIR", dir)
	return logPath
}

func TestDiscoverMerge(t *testing.T) {
	fakeHerdr(t)
	p := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(p, []byte(`self = "alpha"
[discovery]
tailscale = false
[[device]]
name = "testbox"
transport = "none"
mon = "Onix"
[[device]]
name = "printer"
transport = "none"
[[device]]
name = "nas"
address = "admin@nas.lan"
herdr_machine = "-"
`), 0o644)
	cfg, err := loadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	mux := detectMux(ctx, cfg)
	if mux.Kind != "herdr" {
		t.Fatalf("mux %+v", mux)
	}
	devs := discover(ctx, cfg, mux)
	by := map[string]*Device{}
	var names []string
	for _, d := range devs {
		by[d.Node] = d
		names = append(names, d.Node)
	}
	if got := strings.Join(names, ","); got != "alpha,testbox,nas,printer" {
		t.Errorf("order %s", got)
	}
	if d := by["alpha"]; !d.Self || d.Transport != "local" || !d.CanBattle {
		t.Errorf("self %+v", d)
	}
	if d := by["testbox"]; d.Machine != "Testbox" || !d.CanBattle || d.Mon != "onix" || d.Transport != "none" || d.Address != "user@testbox.example" {
		t.Errorf("testbox %+v", d)
	}
	if d := by["printer"]; d.CanBattle || d.Machine != "-" || !d.Online || d.Mon == "" {
		t.Errorf("printer %+v", d)
	}
	if d := by["nas"]; d.CanBattle || d.Transport != "ssh" || d.Address != "admin@nas.lan" {
		t.Errorf("nas %+v", d)
	}
	if _, ok := by["offbox"]; ok {
		t.Error("disabled machine discovered")
	}
	// probes: none stays unknown, but the BLK badge comes from herdr
	d := *by["testbox"]
	probeDevice(ctx, &d, mux, false)
	if d.Net != -1 || d.Sys != -1 || d.Bat != -1 || d.Status != "BLK" || !d.Online {
		t.Errorf("probed testbox %+v", d)
	}
}

func TestServeAndHTTP(t *testing.T) {
	fakeHerdr(t)
	cfg := defaultConfig()
	srv := httptest.NewServer(statusHandler(cfg))
	defer srv.Close()
	var raw map[string]any
	if err := getJSON(context.Background(), srv.URL+"/status", "", &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"uptime_s", "mem_free", "disk_free", "load1", "ncpu", "temp_c", "battery", "herdr"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("/status lacks %s: %v", k, raw)
		}
	}
	d := &Device{Node: "remote", Transport: "http", Address: srv.URL, Online: true, Net: -1, Sys: -1, Bat: -1}
	probeSys(context.Background(), d)
	if !d.Online || d.Net <= 0 || d.Sys < 0 || !d.LevelKnown || d.Bat < 0 || d.Status != "BLK" {
		t.Errorf("http probe %+v", d)
	}
	down := &Device{Node: "gone", Transport: "http", Address: "127.0.0.1:1", Online: true, Net: -1, Sys: -1, Bat: -1}
	probeSys(context.Background(), down)
	if down.Online {
		t.Error("unreachable http device still online")
	}
}

// fakeTUIOS answers Herdr's socket API like TUIOS and records requests.
func fakeTUIOS(t *testing.T, path string) chan map[string]any {
	reqs := make(chan map[string]any, 16)
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				line, err := bufio.NewReader(c).ReadBytes('\n')
				if err != nil {
					return
				}
				var req map[string]any
				json.Unmarshal(line, &req)
				reqs <- req
				var result any = map[string]any{}
				switch req["method"] {
				case "ping":
					result = map[string]any{"type": "pong", "version": "0.9.3+tuios", "server": "tuios"}
				case "pane.list":
					result = map[string]any{"panes": []any{map[string]any{"agent": "codex", "agent_status": "working"}}}
				}
				b, _ := json.Marshal(map[string]any{"id": req["id"], "result": result})
				c.Write(append(b, '\n'))
			}(c)
		}
	}()
	return reqs
}

func TestTUIOS(t *testing.T) {
	fakeHerdr(t)
	sock := filepath.Join(t.TempDir(), "tuios.sock.herdr")
	reqs := fakeTUIOS(t, sock)
	t.Setenv("HERDR_SOCKET_PATH", sock)
	ctx := context.Background()
	cfg := defaultConfig()
	cfg.Self = "alpha"
	cfg.Discovery.Tailscale = false
	cfg.Devices = []DeviceConfig{{Name: "build", Transport: "none", TuiosHost: "build"}, {Name: "testbox", Transport: "none"}}
	mux := detectMux(ctx, cfg)
	if mux.Kind != "tuios" || mux.Socket != sock {
		t.Fatalf("mux %+v", mux)
	}
	<-reqs // ping
	devs := discover(ctx, cfg, mux)
	can := map[string]bool{}
	for _, d := range devs {
		can[d.Node] = d.CanBattle
	}
	if !can["alpha"] || !can["build"] || can["testbox"] {
		t.Errorf("canOpen in TUIOS: %v", can)
	}
	if _, err := mux.create(ctx, devs[0], "proj"); err != nil {
		t.Fatal(err)
	}
	r := <-reqs
	p, _ := r["params"].(map[string]any)
	if r["method"] != "workspace.create" || p["label"] != "proj" || p["focus"] != true {
		t.Errorf("create sent %v", r)
	}
	d := *devs[0]
	probeStatus(ctx, &d, mux, false)
	if d.Status != "WRK" {
		t.Errorf("status over TUIOS socket: %q", d.Status)
	}
	// "auto" with nothing on HERDR_SOCKET_PATH and no herdr: a running TUIOS
	t.Setenv("HERDR_SOCKET_PATH", "")
	t.Setenv("PATH", "/nonexistent")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	os.MkdirAll(filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "tuios"), 0o755)
	fakeTUIOS(t, tuiosSocket())
	if m := detectMux(ctx, cfg); m.Kind != "tuios" || m.Socket != tuiosSocket() {
		t.Errorf("tuios socket not found: %+v", m)
	}
}

// TestEndToEnd: the real binary in a pty, with a fake herdr on PATH. Picks
// the second slot, types a label, Enter; the fake records the create.
func TestEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3")
	}
	bin := filepath.Join(t.TempDir(), "herdrmon")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	py, _ := exec.LookPath("python3")
	log := fakeHerdr(t)
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(cfgPath, []byte(`self = "alpha"
[discovery]
tailscale = false
[night]
command = "false"
[[device]]
name = "testbox"
transport = "none"
`), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, py, "testdata/pty_drive.py", "1", "e2e-proj")
	cmd.Env = append(os.Environ(), "HERDRMON_BIN="+bin, "HERDRMON_CONFIG="+cfgPath,
		"HERDRMON_FRAMES="+filepath.Join(t.TempDir(), "none.json"))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("pty_drive: %v\n%s", err, out)
	}
	t.Logf("%s", out)
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "--machine Testbox workspace create --label e2e-proj --focus") {
		t.Errorf("no create on Testbox; herdr calls:\n%s\npty:\n%s", calls, out)
	}
	if !strings.Contains(string(out), "'Go! ': True") {
		t.Errorf("no Go! message:\n%s", out)
	}
}
