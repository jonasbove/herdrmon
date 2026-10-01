package main

// The multiplexer that opens workspaces: Herdr's CLI by default, or TUIOS,
// which answers Herdr's socket API (0.9.3) on a socket of its own. TUIOS has
// no --machine: remote devices go through its ssh "hosts" instead.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Mux struct {
	Kind   string // "herdr" | "tuios"
	Socket string // TUIOS: its Herdr-API socket
}

func herdrBin() string {
	if p, err := exec.LookPath("herdr"); err == nil {
		return p
	}
	return filepath.Join(home(), ".local/bin/herdr")
}

func haveHerdr() bool {
	_, err := os.Stat(herdrBin())
	return err == nil
}

// tuiosSocket: $XDG_RUNTIME_DIR/tuios/tuios.sock.herdr, or /tmp/tuios-<uid>/.
func tuiosSocket() string {
	if r := os.Getenv("XDG_RUNTIME_DIR"); r != "" {
		return filepath.Join(r, "tuios", "tuios.sock.herdr")
	}
	return filepath.Join(os.TempDir(), "tuios-"+strconv.Itoa(os.Getuid()), "tuios.sock.herdr")
}

// sockCall: one request on Herdr's socket API (newline-delimited JSON).
func sockCall(ctx context.Context, path, method string, params any) (json.RawMessage, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if dl, ok := ctx.Deadline(); ok {
		c.SetDeadline(dl)
	} else {
		c.SetDeadline(time.Now().Add(5 * time.Second))
	}
	if params == nil {
		params = struct{}{}
	}
	req, _ := json.Marshal(map[string]any{"id": "herdrmon:" + method, "method": method, "params": params})
	if _, err := c.Write(append(req, '\n')); err != nil {
		return nil, err
	}
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return nil, err
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s: %s %s", method, resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}

// pingServer: "herdr" or "tuios" (ping answers server: "tuios" and a version
// ending in "+tuios"), or "" when nothing answers.
func pingServer(ctx context.Context, path string) string {
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	r, err := sockCall(ctx, path, "ping", nil)
	if err != nil {
		return ""
	}
	var p struct{ Server, Version string }
	json.Unmarshal(r, &p)
	if strings.EqualFold(p.Server, "tuios") || strings.HasSuffix(p.Version, "+tuios") {
		return "tuios"
	}
	return "herdr"
}

// detectMux: config "herdr"/"tuios" force a backend. "auto": whatever answers
// $HERDR_SOCKET_PATH; else the herdr CLI if installed; else a running TUIOS.
func detectMux(ctx context.Context, cfg Config) Mux {
	switch cfg.Mux {
	case "herdr":
		return Mux{Kind: "herdr"}
	case "tuios":
		s := os.Getenv("HERDR_SOCKET_PATH")
		if s == "" || pingServer(ctx, s) != "tuios" {
			s = tuiosSocket()
		}
		return Mux{Kind: "tuios", Socket: s}
	}
	if s := os.Getenv("HERDR_SOCKET_PATH"); s != "" && pingServer(ctx, s) == "tuios" {
		return Mux{Kind: "tuios", Socket: s}
	}
	if haveHerdr() {
		return Mux{Kind: "herdr"}
	}
	if s := tuiosSocket(); pingServer(ctx, s) == "tuios" {
		return Mux{Kind: "tuios", Socket: s}
	}
	return Mux{Kind: "herdr"}
}

// canOpen: whether a workspace can be opened on d.
func (m Mux) canOpen(d *Device) bool {
	if d.Self {
		return true
	}
	if m.Kind == "tuios" {
		return d.TuiosHost != ""
	}
	return d.Machine != "" && d.Machine != "-"
}

type paneList struct {
	Panes []struct {
		Agent       *string `json:"agent"`
		AgentStatus string  `json:"agent_status"`
	} `json:"panes"`
}

// panes: the agent panes on d, for the BLK/WRK badge.
func (m Mux) panes(ctx context.Context, d *Device) (paneList, error) {
	var pl paneList
	if m.Kind == "tuios" {
		if !d.Self {
			return pl, fmt.Errorf("tuios: remote panes not readable")
		}
		r, err := sockCall(ctx, m.Socket, "pane.list", nil)
		if err != nil {
			return pl, err
		}
		return pl, json.Unmarshal(r, &pl)
	}
	args := []string{"pane", "list"}
	if !d.Self {
		args = append([]string{"--machine", d.Machine}, args...)
	}
	b, err := run(ctx, herdrBin(), args...)
	if err != nil {
		return pl, err
	}
	var r struct{ Result paneList }
	err = json.Unmarshal(b, &r)
	return r.Result, err
}

// create opens a workspace labelled label on d and focuses it. It returns a
// short reason on failure.
func (m Mux) create(ctx context.Context, d *Device, label string) (string, error) {
	var out []byte
	var err error
	switch {
	case m.Kind == "tuios" && d.Self:
		_, err = sockCall(ctx, m.Socket, "workspace.create", map[string]any{"label": label, "focus": true})
		if err != nil {
			return err.Error(), err
		}
		return "", nil
	case m.Kind == "tuios":
		// TUIOS: a session on the host, reached over its own ssh link;
		// it shows in the rail under that machine.
		out, err = exec.CommandContext(ctx, "tuios", "new", "--host", d.TuiosHost, label, "--detach").CombinedOutput()
	default:
		args := []string{"workspace", "create", "--label", label, "--focus"}
		if !d.Self {
			args = append([]string{"--machine", d.Machine}, args...)
		}
		out, err = exec.CommandContext(ctx, herdrBin(), args...).CombinedOutput()
	}
	text := strings.TrimSpace(string(out))
	if i := strings.LastIndex(text, "\n"); i >= 0 {
		text = text[i+1:]
	}
	if err != nil && text == "" {
		text = err.Error()
	}
	return text, err
}
