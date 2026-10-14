package main

// frames.json (DESIGN.md "frames.json"): species slug -> animation -> frames,
// each frame a string of icon-font codepoints. A font rebuild renumbers the
// codepoints, so the file is re-read whenever its mtime changes; a file that
// fails to parse (mid-write) keeps the last good copy.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"time"
)

const framesCheckMS = 2000

// fontInstalled: is a font family visible to fontconfig? Without fc-list
// (macOS) there's no way to ask, so assume yes.
func fontInstalled(family string) bool {
	if family == "" {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "fc-list", ":", "family").Output()
	if err != nil {
		if _, exited := err.(*exec.ExitError); !exited {
			return true
		}
	}
	for _, line := range strings.Split(string(out), "\n") {
		for _, f := range strings.Split(line, ",") {
			if jsTrim(f) == family {
				return true
			}
		}
	}
	return false
}

type Frames struct {
	File      string
	CheckFont func(string) bool
	mons      map[string]map[string]json.RawMessage
	hasData   bool
	mtime     time.Time
	checked   int64
	FontOK    bool
}

func newFrames(file string, checkFont func(string) bool) *Frames {
	if checkFont == nil {
		checkFont = fontInstalled
	}
	return &Frames{File: file, CheckFont: checkFont}
}

func (f *Frames) Load() bool {
	st, err := os.Stat(f.File)
	if err != nil {
		return false
	}
	b, err := os.ReadFile(f.File)
	if err != nil {
		return false
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(b, &top) != nil || top == nil {
		return false
	}
	// mons must be an object (null and arrays pass, and hold nothing).
	monsRaw := strings.TrimSpace(string(top["mons"]))
	if monsRaw == "" || !(monsRaw[0] == '{' || monsRaw[0] == '[' || monsRaw == "null") {
		return false
	}
	mons := map[string]map[string]json.RawMessage{}
	var loose map[string]json.RawMessage
	if json.Unmarshal(top["mons"], &loose) == nil {
		for k, v := range loose {
			var m map[string]json.RawMessage
			if json.Unmarshal(v, &m) == nil && m != nil {
				mons[k] = m
			}
		}
	}
	var font any
	_ = json.Unmarshal(top["font"], &font)
	f.mons, f.hasData, f.mtime = mons, true, st.ModTime()
	name := ""
	if font != nil {
		name = jsString(font)
	}
	f.FontOK = f.CheckFont(name)
	return true
}

// Refresh is cheap enough for every tick: it stats the file at most every
// framesCheckMS.
func (f *Frames) Refresh(now int64) bool {
	if now-f.checked < framesCheckMS {
		return false
	}
	f.checked = now
	st, err := os.Stat(f.File)
	if err != nil {
		return false // file gone: keep what we have
	}
	if !st.ModTime().Equal(f.mtime) {
		return f.Load()
	}
	return false
}

func decodeFrames(raw json.RawMessage) ([]string, bool) {
	var list []any
	if json.Unmarshal(raw, &list) != nil || list == nil {
		return nil, false
	}
	out := make([]string, len(list))
	for i, v := range list {
		out[i] = jsString(v)
	}
	return out, true
}

// Anim: the animations for a species slug. Older manifests key some species
// with hyphens ("ho-oh"); both spellings are accepted.
func (f *Frames) Anim(mon string) Anim {
	if !f.hasData || f.mons == nil || mon == "" || !f.FontOK {
		return nil
	}
	a, ok := f.mons[mon]
	if !ok {
		a, ok = f.mons[strings.ReplaceAll(mon, "_", "-")]
	}
	if !ok {
		a, ok = f.mons[strings.ReplaceAll(mon, "-", "_")]
	}
	if !ok || a == nil {
		return nil
	}
	// bob, else base, must be a list.
	key := "bob"
	if raw, has := a["bob"]; !has || string(raw) == "null" {
		key = "base"
	}
	if _, isList := decodeFrames(a[key]); !isList {
		return nil
	}
	out := Anim{}
	for name, raw := range a {
		if frames, ok := decodeFrames(raw); ok {
			out[name] = frames
		}
	}
	return out
}
