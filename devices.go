package main

// Device discovery and the three HP probes (NET, SYS, BAT), level from uptime,
// and the status badge. Everything here is best-effort: a probe that can't
// answer leaves its bar unknown ("--") rather than guessing.
//
// Discovery merges, in order: this machine, tailnet peers (`tailscale status
// --json`), ~/.ssh/config Hosts (opt-in), `herdr machine list`, then the
// [[device]] entries of config.toml, which override what was discovered.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Device struct {
	Node      string // key: lowercase device name
	Display   string // "Onix", "Ho-oh"
	Mon       string // species slug, the frames.json key
	Transport string // local | tailscale | ssh | http | none
	Address   string // ssh target / tailnet name / http base URL
	IP        string
	OS        string
	Online    bool // false only when a probe or the tailnet says so
	Self      bool
	Machine   string // herdr --machine label; "" = none found; "-" = no Herdr
	TuiosHost string // TUIOS host name, from config
	CanBattle bool   // a workspace can be opened here
	LastSeen  time.Time
	Battery   BatteryConfig

	// Bars: 0..1, or -1 when unknown/not applicable.
	Net, Sys, Bat float64
	NetNote       string // "5ms direct", "relay"
	SysNote       string
	BatNote       string
	Level         int // days of uptime
	LevelKnown    bool
	Status        string // "", "WRK", "BLK", "SLP"
}

// display: "geodude" -> "Geodude", "ho-oh" -> "Ho-oh".
func display(node string) string {
	if node == "" {
		return node
	}
	return strings.ToUpper(node[:1]) + node[1:]
}

func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = nil
	return cmd.Output()
}

// herdrMachines: lowercase target (and its host part) -> Herdr machine label.
func herdrMachines(ctx context.Context) map[string]string {
	out := map[string]string{}
	if !haveHerdr() {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b, err := run(ctx, herdrBin(), "machine", "list", "--json")
	if err != nil {
		return out
	}
	var ms []struct {
		Label, Target string
		Enabled       bool
	}
	if json.Unmarshal(b, &ms) != nil {
		return out
	}
	for _, m := range ms {
		if m.Enabled {
			out[strings.ToLower(m.Target)] = m.Label
		}
	}
	return out
}

// hostOf: "user@geodude.example.ts.net:22" -> "geodude".
func hostOf(target string) string {
	t := strings.ToLower(target)
	if i := strings.LastIndex(t, "@"); i >= 0 {
		t = t[i+1:]
	}
	if h, _, err := net.SplitHostPort(t); err == nil {
		t = h
	}
	if net.ParseIP(t) != nil {
		return t
	}
	t, _, _ = strings.Cut(t, ".")
	return t
}

type peer struct {
	DNSName      string
	HostName     string
	OS           string
	Online       bool
	TailscaleIPs []string
	LastSeen     string
}

func (p *peer) name() string {
	if n := strings.SplitN(strings.TrimSuffix(p.DNSName, "."), ".", 2)[0]; n != "" {
		return strings.ToLower(n)
	}
	return strings.ToLower(p.HostName)
}

// tailnet: nil, nil when tailscale isn't installed or isn't running.
func tailnet(ctx context.Context) (self *peer, peers []*peer) {
	if _, err := exec.LookPath("tailscale"); err != nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b, err := run(ctx, "tailscale", "status", "--json")
	if err != nil {
		return nil, nil
	}
	var st struct {
		Self *peer
		Peer map[string]*peer
	}
	if json.Unmarshal(b, &st) != nil {
		return nil, nil
	}
	for _, p := range st.Peer {
		peers = append(peers, p)
	}
	return st.Self, peers
}

// sshHosts: literal Host aliases from ~/.ssh/config (no patterns).
func sshHosts(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) < 2 || !strings.EqualFold(fs[0], "host") {
			continue
		}
		for _, h := range fs[1:] {
			if strings.HasPrefix(h, "#") {
				break
			}
			if !strings.ContainsAny(h, "*?!") {
				out = append(out, h)
			}
		}
	}
	return out
}

func discover(ctx context.Context, cfg Config, mux Mux) []*Device {
	byName := map[string]*Device{}
	var order []*Device
	add := func(name, transport, address string) *Device {
		if d := byName[name]; d != nil {
			return d
		}
		d := &Device{Node: name, Transport: transport, Address: address, Online: true, Net: -1, Sys: -1, Bat: -1}
		byName[name] = d
		order = append(order, d)
		return d
	}
	me := add(cfg.selfName(), "local", "")
	me.Self, me.OS = true, runtime.GOOS

	if cfg.Discovery.Tailscale {
		self, peers := tailnet(ctx)
		if self != nil && len(self.TailscaleIPs) > 0 {
			me.IP = self.TailscaleIPs[0]
		}
		for _, p := range peers {
			n := p.name()
			if n == "" || n == me.Node {
				continue
			}
			d := add(n, "tailscale", n)
			d.Online, d.OS = p.Online, p.OS
			if len(p.TailscaleIPs) > 0 {
				d.IP = p.TailscaleIPs[0]
			}
			if t, err := time.Parse(time.RFC3339Nano, p.LastSeen); err == nil && t.Year() > 2000 {
				d.LastSeen = t
			}
		}
	}
	if cfg.Discovery.SSHConfig {
		for _, h := range sshHosts(filepath.Join(home(), ".ssh", "config")) {
			if n := strings.ToLower(h); n != me.Node {
				add(n, "ssh", h)
			}
		}
	}
	var machines map[string]string
	if cfg.Discovery.Herdr && mux.Kind == "herdr" {
		machines = herdrMachines(ctx)
		for target := range machines {
			if n := hostOf(target); n != "" && n != me.Node {
				add(n, "ssh", target)
			}
		}
	}
	for _, c := range cfg.Devices {
		if c.Name == "" {
			continue
		}
		d := byName[c.Name]
		if d == nil {
			d = add(c.Name, "ssh", c.Name)
		}
		if c.Transport != "" && !d.Self {
			d.Transport = c.Transport
		}
		if c.Address != "" {
			d.Address = c.Address
		}
		if d.Transport == "none" || d.Transport == "http" {
			d.Online = true // learnt from the probe, if at all
		}
		d.Display, d.Mon, d.Machine, d.TuiosHost, d.Battery = c.Display, c.Mon, c.HerdrMachine, c.TuiosHost, c.Battery
	}
	for _, d := range order {
		if d.Display == "" {
			d.Display = display(d.Node)
		}
		if d.Mon == "" {
			d.Mon = defaultMon(d.Node)
		}
		if d.Machine == "" && !d.Self {
			d.Machine = matchMachine(machines, d)
		}
		d.CanBattle = mux.canOpen(d)
	}
	sortDevices(order)
	return order
}

// matchMachine: the Herdr machine whose target is this device, by name or
// by address. "-" when there is none.
func matchMachine(machines map[string]string, d *Device) string {
	for target, label := range machines {
		t := strings.ToLower(target)
		if hostOf(t) == d.Node || (d.Address != "" && (t == strings.ToLower(d.Address) || hostOf(t) == hostOf(d.Address))) {
			return label
		}
	}
	return "-"
}

// Party order: this machine first (the "lead" slot), then Herdr machines,
// then the rest; online before offline inside each group; then by name.
func sortDevices(d []*Device) {
	rank := func(x *Device) int {
		switch {
		case x.Self:
			return 0
		case x.CanBattle && x.Online:
			return 1
		case x.CanBattle:
			return 2
		case x.Online:
			return 3
		}
		return 4
	}
	for i := 1; i < len(d); i++ {
		for j := i; j > 0; j-- {
			a, b := d[j-1], d[j]
			if rank(a) < rank(b) || (rank(a) == rank(b) && a.Node <= b.Node) {
				break
			}
			d[j-1], d[j] = b, a
		}
	}
}

/* ------------------------------------------------------------------ NET */

// netScore: <=20ms full, 400ms+ nearly empty.
func netScore(ms float64) float64 { return clamp(1-(ms-20)/380, 0.05, 1) }

// probeNet per transport. ssh: TCP connect time to port 22; http: done by
// probeHTTP together with SYS; none: unknown.
func probeNet(ctx context.Context, d *Device) {
	switch {
	case d.Self:
		d.Net, d.NetNote = 1, "this machine"
	case !d.Online:
		d.Net = 0
	case d.Transport == "tailscale":
		probeTailscale(ctx, d)
	case d.Transport == "ssh":
		host := d.Address
		if i := strings.LastIndex(host, "@"); i >= 0 {
			host = host[i+1:]
		}
		if _, _, err := net.SplitHostPort(host); err != nil {
			host = net.JoinHostPort(host, "22")
		}
		t0 := time.Now()
		c, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", host)
		if err != nil {
			d.Online, d.Net, d.NetNote = false, 0, "unreachable"
			return
		}
		c.Close()
		ms := float64(time.Since(t0).Microseconds()) / 1000
		d.Net, d.NetNote = netScore(ms), fmt.Sprintf("%.0fms ssh", ms)
	}
}

/* ------------------------------------------------------------- SYS + Lv */

// One shell line gathers uptime, memory, disk, load, cores, temperature and
// battery, locally or over ssh.
const sysScript = `printf 'up=%s\n' "$(cut -d' ' -f1 /proc/uptime)"; ` +
	`awk '/^MemTotal/{t=$2}/^MemAvailable/{a=$2}END{printf "mem=%d %d\n",a,t}' /proc/meminfo; ` +
	`df -P / | awk 'NR==2{printf "disk=%s %s\n",$4,$2}'; ` +
	`printf 'load=%s\n' "$(cut -d' ' -f1 /proc/loadavg)"; ` +
	`printf 'cores=%s\n' "$(nproc)"; ` +
	`t=$(cat /sys/class/thermal/thermal_zone0/temp 2>/dev/null); [ -n "$t" ] && printf 'temp=%s\n' "$t"; ` +
	`for b in /sys/class/power_supply/BAT*; do [ -r "$b/capacity" ] && printf 'bat=%s %s\n' "$(cat $b/capacity)" "$(cat $b/status 2>/dev/null)"; break; done; true`

// sshTarget: where SYS goes over ssh for a tailnet or ssh device.
func (d *Device) sshTarget() string {
	if d.Address != "" {
		return d.Address
	}
	return d.Node
}

func probeSys(ctx context.Context, d *Device) {
	if !d.Online {
		return
	}
	var st *Status
	switch d.Transport {
	case "local":
		st = localStatus(ctx)
	case "tailscale", "ssh":
		if d.Transport == "tailscale" && d.OS != "" && d.OS != "linux" {
			return
		}
		ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
		defer cancel()
		b, err := run(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=4", d.sshTarget(), sysScript)
		if err != nil && len(b) == 0 {
			d.SysNote = "no ssh"
			return
		}
		st = parseSys(b)
	case "http":
		probeHTTP(ctx, d)
		return
	default:
		return
	}
	d.apply(st, d.Battery.Source == "" || d.Battery.Source == "sysfs")
}

// apply copies a Status onto the bars. withBat: the battery comes from it.
func (d *Device) apply(st *Status, withBat bool) {
	if st == nil {
		return
	}
	if st.UptimeS != nil {
		d.Level, d.LevelKnown = int(*st.UptimeS/86400), true
	}
	d.Sys, d.SysNote = st.health()
	if !withBat {
		return
	}
	if st.Battery != nil {
		d.Bat = st.Battery.Percent / 100
		d.BatNote = fmt.Sprintf("%.0f%%", st.Battery.Percent)
		if st.Battery.Status != "" {
			d.BatNote += " " + strings.ToLower(st.Battery.Status)
		}
	} else if d.Sys >= 0 {
		d.Bat, d.BatNote = 1, "on mains" // the probe ran and found no battery
	}
	if st.Herdr != nil && d.Status == "" {
		switch {
		case st.Herdr.Blocked > 0:
			d.Status = "BLK"
		case st.Herdr.Working > 0:
			d.Status = "WRK"
		}
	}
}

/* ------------------------------------------------------------------ BAT */

// probeHA: a battery sensor from Home Assistant ([device.battery] source =
// "homeassistant"). The token is read from token_file and never printed.
func probeHA(ctx context.Context, d *Device) {
	b := d.Battery
	if b.URL == "" || b.Entity == "" || b.TokenFile == "" {
		d.BatNote = "HA not configured"
		return
	}
	tok, err := os.ReadFile(expand(b.TokenFile))
	if err != nil {
		d.BatNote = "no HA token"
		return
	}
	var s struct{ State string }
	err = getJSON(ctx, strings.TrimRight(b.URL, "/")+"/api/states/"+b.Entity, strings.TrimSpace(string(tok)), &s)
	if err != nil {
		return
	}
	var v float64
	if _, err := fmt.Sscanf(s.State, "%g", &v); err == nil {
		d.Bat, d.BatNote = v/100, fmt.Sprintf("%.0f%%", v)
	}
}

/* --------------------------------------------------------------- status */

// probeStatus: BLK beats WRK (an agent waiting on you matters most), then
// SLP at night.
func probeStatus(ctx context.Context, d *Device, mux Mux, night bool) {
	if !d.Online {
		return
	}
	if d.CanBattle {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		pl, err := mux.panes(ctx, d)
		cancel()
		if err == nil {
			for _, p := range pl.Panes {
				if p.Agent == nil {
					continue
				}
				switch p.AgentStatus {
				case "blocked":
					d.Status = "BLK"
				case "working":
					if d.Status == "" {
						d.Status = "WRK"
					}
				}
			}
		}
	}
	if d.Status == "" && night {
		d.Status = "SLP"
	}
}

// Messages from a refresh round to the UI. Probes work on copies, so the UI
// never shares a Device with a running goroutine.
type discoverMsg struct{ devs []*Device }
type probeMsg struct{ dev Device }

// probeDevice runs every probe for one device.
func probeDevice(ctx context.Context, d *Device, mux Mux, night bool) {
	if d.Transport == "ssh" || d.Transport == "http" {
		probeNet(ctx, d) // may mark it offline: SYS then skips (http does both in probeSys)
		probeSys(ctx, d)
	} else {
		// NET and SYS touch different fields here, and ping is slow
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); probeNet(ctx, d) }()
		go func() { defer wg.Done(); probeSys(ctx, d) }()
		wg.Wait()
	}
	if d.Online && d.Battery.Source == "homeassistant" {
		probeHA(ctx, d)
	}
	probeStatus(ctx, d, mux, night)
}

// refresh: discover, then probe every device in parallel and send each
// result as soon as it lands, so the menu fills in (and shimmers) live.
func refresh(ctx context.Context, cfg Config, send func(any)) {
	mux := detectMux(ctx, cfg)
	devs := discover(ctx, cfg, mux)
	send(discoverMsg{devs})
	night := isNight(ctx, cfg.Night)
	var wg sync.WaitGroup
	for _, d := range devs {
		c := *d
		wg.Add(1)
		go func(d *Device) {
			defer wg.Done()
			probeDevice(ctx, d, mux, night)
			send(probeMsg{*d})
		}(&c)
	}
	wg.Wait()
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
