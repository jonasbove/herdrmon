package main

// setDeviceMon writes `mon = "<slug>"` into config.toml by editing the text,
// so comments and layout survive: the device's [[device]] block gets its mon
// line replaced (or one added after `name`), or a new block is appended.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	nameLine = regexp.MustCompile(`^(\s*)name\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	monLine  = regexp.MustCompile(`^(\s*mon\s*=\s*)("[^"]*"|'[^']*')(.*)$`)
)

func setMonText(text, device, slug string) string {
	lines := strings.Split(text, "\n")
	header := func(l string) bool { return strings.HasPrefix(strings.TrimSpace(l), "[") }
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "[[device]]" {
			continue
		}
		// the block's own keys end at the next header (sub-tables like
		// [device.battery] come after them)
		end := i + 1
		for end < len(lines) && !header(lines[end]) {
			end++
		}
		nameAt := -1
		for j := i + 1; j < end; j++ {
			if m := nameLine.FindStringSubmatch(lines[j]); m != nil && strings.EqualFold(m[2]+m[3], device) {
				nameAt = j
			}
		}
		if nameAt < 0 {
			continue
		}
		for j := i + 1; j < end; j++ {
			if m := monLine.FindStringSubmatch(lines[j]); m != nil {
				lines[j] = m[1] + `"` + slug + `"` + m[3]
				return strings.Join(lines, "\n")
			}
		}
		indent := nameLine.FindStringSubmatch(lines[nameAt])[1]
		out := append([]string{}, lines[:nameAt+1]...)
		out = append(out, indent+`mon = "`+slug+`"`)
		return strings.Join(append(out, lines[nameAt+1:]...), "\n")
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if text != "" {
		text += "\n"
	}
	return text + fmt.Sprintf("[[device]]\nname = %q\nmon = %q\n", device, slug)
}

// setDeviceMon edits the file at path, backing it up first (path.bak.<unix>)
// and putting it back if the result doesn't parse to the wanted mon.
func setDeviceMon(path, device, slug string) error {
	old, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if len(old) > 0 {
		bak := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
		if err := os.WriteFile(bak, old, 0o644); err != nil {
			return err
		}
	}
	next := setMonText(string(old), device, slug)
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		return err
	}
	cfg, err := loadConfig(path)
	if err == nil {
		if d := cfg.device(strings.ToLower(device)); d == nil || d.Mon != slug {
			err = fmt.Errorf("edit didn't take")
		}
	}
	if err != nil {
		if existed {
			os.WriteFile(path, old, 0o644)
		} else {
			os.Remove(path)
		}
		return fmt.Errorf("couldn't write mon to %s: %w", path, err)
	}
	return nil
}
