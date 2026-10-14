package main

// Herdr's socket API: newline-delimited JSON, one request per connection.
// Cheaper than spawning the CLI per write, and parallel by construction.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"time"
)

const (
	ipcTimeout  = 4000 * time.Millisecond
	maxInFlight = 16
	// pollMS: Herdr accepts a connection at once but polls it for its
	// request line every ~100 ms. A request written a little after
	// connecting always misses the first poll, so Herdr reads it exactly
	// pollMS after the connect: connecting pollMS before `at` lands it on
	// `at` (measured spread ~1 ms, herdr-radar).
	pollMS     = 100.6
	writeGapMS = 40
)

var inFlight = make(chan struct{}, maxInFlight)

// Reply: Herdr's answer; Result/Error stay raw for the caller to decode.
type Reply struct {
	ID     any             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

func marshalNoEscape(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v) // appends the newline
	return b.Bytes()
}

// readLine: one newline-terminated JSON value from conn, or nil.
func readLine(conn net.Conn, deadline time.Time) []byte {
	_ = conn.SetDeadline(deadline)
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return nil
	}
	return line
}

func exchange(method string, params any, writeDelay time.Duration) *Reply {
	deadline := time.Now().Add(ipcTimeout)
	conn, err := net.DialTimeout("unix", herdrSocket(), ipcTimeout)
	if err != nil {
		return nil
	}
	defer conn.Close()
	if writeDelay > 0 {
		time.Sleep(writeDelay)
	}
	_ = conn.SetDeadline(deadline)
	req := map[string]any{"id": "herdrmon", "method": method, "params": params}
	if _, err := conn.Write(marshalNoEscape(req)); err != nil {
		return nil
	}
	line := readLine(conn, deadline)
	if line == nil {
		return nil
	}
	var r Reply
	if json.Unmarshal(line, &r) != nil {
		return nil
	}
	return &r
}

// ipcCall: the parsed reply (which may carry an error), or nil if the
// transport failed.
func ipcCall(method string, params any) *Reply {
	inFlight <- struct{}{}
	defer func() { <-inFlight }()
	return exchange(method, params, 0)
}

// ipcCallAt: a call Herdr reads at a chosen moment (ms since the epoch),
// for animation frames.
func ipcCallAt(method string, params any, at float64) *Reply {
	wait := at - pollMS - float64(time.Now().UnixMilli())
	if wait > 0 {
		time.Sleep(time.Duration(wait * float64(time.Millisecond)))
	}
	return exchange(method, params, writeGapMS*time.Millisecond)
}

// paneList: the panes of the Herdr server, or nil when it can't be asked.
func paneList() []map[string]any {
	r := ipcCall("pane.list", map[string]any{})
	if r == nil {
		return nil
	}
	var res struct {
		Panes []map[string]any `json:"panes"`
	}
	if json.Unmarshal(r.Result, &res) != nil || res.Panes == nil {
		return nil
	}
	return res.Panes
}
