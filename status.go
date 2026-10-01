package main

// Status: one machine's readings, whether they come from /proc here, the
// probe script over ssh, or another machine's `herdrmon serve`.
//
//	GET /status -> {"uptime_s": 86400.5, "mem_free": 0.42, "disk_free": 0.61,
//	  "load1": 0.8, "ncpu": 8, "temp_c": 48, "battery": {"percent": 87,
//	  "status": "discharging"}, "herdr": {"running": true, "blocked": 0, "working": 1}}
//
// mem_free and disk_free are fractions (0..1) of RAM available and of / free.
// temp_c, battery and herdr are null when unknown or absent (no battery =
// on mains).

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Status struct {
	UptimeS  *float64     `json:"uptime_s"`
	MemFree  *float64     `json:"mem_free"`
	DiskFree *float64     `json:"disk_free"`
	Load1    *float64     `json:"load1"`
	NCPU     int          `json:"ncpu"`
	TempC    *float64     `json:"temp_c"`
	Battery  *BatteryInfo `json:"battery"`
	Herdr    *HerdrInfo   `json:"herdr"`
}

type BatteryInfo struct {
	Percent float64 `json:"percent"`
	Status  string  `json:"status"`
}

type HerdrInfo struct {
	Running bool `json:"running"`
	Blocked int  `json:"blocked"`
	Working int  `json:"working"`
}

func fptr(v float64) *float64 { return &v }

// parseSys reads sysScript's key=value lines.
func parseSys(b []byte) *Status {
	kv := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			kv[k] = v
		}
	}
	st := &Status{}
	num := func(s string) (float64, bool) {
		v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		return v, err == nil
	}
	if v, ok := num(kv["up"]); ok {
		st.UptimeS = fptr(v)
	}
	if f := pair(kv["mem"]); f[1] > 0 {
		st.MemFree = fptr(f[0] / f[1])
	}
	if f := pair(kv["disk"]); f[1] > 0 {
		st.DiskFree = fptr(f[0] / f[1])
	}
	if v, ok := num(kv["load"]); ok {
		st.Load1 = fptr(v)
	}
	if v, ok := num(kv["cores"]); ok {
		st.NCPU = int(v)
	}
	if v, ok := num(kv["temp"]); ok {
		st.TempC = fptr(v / 1000)
	}
	if bt := strings.Fields(kv["bat"]); len(bt) > 0 {
		if v, ok := num(bt[0]); ok {
			st.Battery = &BatteryInfo{Percent: v}
			if len(bt) > 1 {
				st.Battery.Status = strings.ToLower(bt[1])
			}
		}
	}
	return st
}

func pair(s string) [2]float64 {
	f := strings.Fields(s)
	var out [2]float64
	for i := 0; i < 2 && i < len(f); i++ {
		out[i], _ = strconv.ParseFloat(f[i], 64)
	}
	return out
}

// health = the worst of: free RAM, free disk, load headroom, temp headroom.
// -1 when nothing was readable.
func (st *Status) health() (float64, string) {
	worst, note, any := 1.0, "", false
	take := func(v float64, n string) {
		any = true
		if v < worst {
			worst, note = v, n
		}
	}
	if st.MemFree != nil {
		take(clamp(*st.MemFree/0.5, 0, 1), fmt.Sprintf("RAM %d%% free", int(*st.MemFree*100)))
	}
	if st.DiskFree != nil {
		take(clamp(*st.DiskFree/0.3, 0, 1), fmt.Sprintf("disk %d%% free", int(*st.DiskFree*100)))
	}
	if st.Load1 != nil && st.NCPU > 0 {
		take(clamp(1-*st.Load1/float64(st.NCPU), 0, 1), fmt.Sprintf("load %.2f/%d", *st.Load1, st.NCPU))
	}
	if st.TempC != nil {
		take(clamp((85-*st.TempC)/35, 0, 1), fmt.Sprintf("%.0f°C", *st.TempC))
	}
	if !any {
		return -1, ""
	}
	if note == "" {
		note = "healthy"
	}
	return worst, note
}

func localStatus(ctx context.Context) *Status {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	b, _ := run(ctx, "sh", "-c", sysScript)
	return parseSys(b)
}

/* ---------------------------------------------------------------- http */

func getJSON(ctx context.Context, url, bearer string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// probeHTTP: NET = GET time to <address>/status, SYS/BAT/level from its JSON.
func probeHTTP(ctx context.Context, d *Device) {
	base := strings.TrimRight(d.Address, "/")
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	t0 := time.Now()
	var st Status
	if err := getJSON(ctx, base+"/status", "", &st); err != nil {
		d.Online, d.Net, d.NetNote = false, 0, "no /status"
		return
	}
	ms := float64(time.Since(t0).Microseconds()) / 1000
	d.Net, d.NetNote = netScore(ms), fmt.Sprintf("%.0fms http", ms)
	src := d.Battery.Source
	d.apply(&st, src == "" || src == "http" || src == "sysfs")
}

// serveMain: `herdrmon serve [--listen 127.0.0.1:7643]`. No auth beyond the
// bind address; put it behind your tailnet or a reverse proxy.
func serveMain(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:7643", "address to listen on")
	fs.Parse(args)
	cfg, err := loadConfig(resolvePaths().Config)
	if err != nil {
		return err
	}
	http.HandleFunc("/status", statusHandler(cfg))
	fmt.Fprintf(os.Stderr, "herdrmon: serving /status on http://%s\n", *listen)
	return http.ListenAndServe(*listen, nil)
}

func statusHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		st := localStatus(ctx)
		mux := detectMux(ctx, cfg)
		if pl, err := mux.panes(ctx, &Device{Self: true}); err == nil {
			h := &HerdrInfo{Running: true}
			for _, p := range pl.Panes {
				if p.Agent == nil {
					continue
				}
				switch p.AgentStatus {
				case "blocked":
					h.Blocked++
				case "working":
					h.Working++
				}
			}
			st.Herdr = h
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(st)
	}
}

/* ----------------------------------------------------------- tailscale */

var pingRE = regexp.MustCompile(`in ([0-9.]+)ms`)

// probeTailscale: tailscale ping. Direct paths score by latency; DERP relays
// are capped at half.
func probeTailscale(ctx context.Context, d *Device) {
	target := d.IP
	if target == "" {
		target = d.Address
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	b, _ := run(ctx, "tailscale", "ping", "-c", "1", "--timeout", "3s", target)
	s := string(b)
	m := pingRE.FindStringSubmatch(s)
	if m == nil {
		d.Net, d.NetNote = 0.05, "no pong"
		return
	}
	ms, _ := strconv.ParseFloat(m[1], 64)
	score, via := netScore(ms), "direct"
	if strings.Contains(s, "via DERP") {
		via = "relay"
		score = clamp(score, 0.05, 0.5)
	}
	d.Net, d.NetNote = score, fmt.Sprintf("%.0fms %s", ms, via)
}

/* --------------------------------------------------------------- night */

// isNight: the same signal as the sidebar's sleep animation. [night] command
// (exit 0 = night) when set; else hyprsunset below 6000 K when Hyprland and
// hyprsunset answer; else local time between start and end.
func isNight(ctx context.Context, n NightConfig) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if n.Command != "" {
		return exec.CommandContext(ctx, "sh", "-c", n.Command).Run() == nil
	}
	if k, ok := hyprsunset(ctx); ok {
		return k < 6000
	}
	return inHours(time.Now().Hour(), n.Start, n.End)
}

func inHours(h, start, end int) bool {
	if start == end {
		return false
	}
	if start < end {
		return h >= start && h < end
	}
	return h >= start || h < end
}

func hyprsunset(ctx context.Context) (int, bool) {
	if _, err := exec.LookPath("hyprctl"); err != nil {
		return 0, false
	}
	b, err := run(ctx, "sh", "-c", `HYPRLAND_INSTANCE_SIGNATURE=${HYPRLAND_INSTANCE_SIGNATURE:-$(ls -t "${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/hypr" 2>/dev/null | head -1)} hyprctl hyprsunset temperature`)
	if err != nil {
		return 0, false
	}
	k, err := strconv.Atoi(regexp.MustCompile(`\d+`).FindString(string(b)))
	return k, err == nil && k > 0
}
