package main

// configure: print the sidebar snippet that uses $mon / $agent / $tools, and
// optionally write it into Herdr's config.toml as a managed block.
//
//   herdrmon-sidebar configure              print only
//   herdrmon-sidebar configure --write      write the block (backup first)
//   herdrmon-sidebar configure --uninstall  stop the daemon, remove the block
//   --reload                                then `herdr server reload-config`
//   --no-backup                             no config.toml.bak.<time> (the
//                                           caller keeps its own, e.g.
//                                           bove-theme-sync)
//   --herdr-config PATH                     another config.toml (default: Herdr's)

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type cliArgs []string

func (a cliArgs) flag(f string) bool {
	for _, x := range a {
		if x == f {
			return true
		}
	}
	return false
}

// opt: the value after f, "" when absent.
func (a cliArgs) opt(f string) (string, bool) {
	for i, x := range a {
		if x == f {
			if i+1 < len(a) {
				return a[i+1], true
			}
			return "", false
		}
	}
	return "", false
}

type configurer struct {
	args   cliArgs
	file   string
	stdout io.Writer
	stderr io.Writer
}

func (c *configurer) println(s string)  { fmt.Fprintln(c.stdout, s) }
func (c *configurer) eprintln(s string) { fmt.Fprintln(c.stderr, s) }

func (c *configurer) readConfig() string {
	b, err := os.ReadFile(c.file)
	if err != nil {
		return ""
	}
	return string(b)
}

func copyFile(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(from); err == nil {
		mode = st.Mode().Perm()
	}
	return os.WriteFile(to, b, mode)
}

func (c *configurer) write(next, prev string) bool {
	if next == prev {
		c.println(c.file + ": already up to date")
		return true
	}
	if _, err := ParseToml(next); err != nil {
		c.eprintln("refusing to write " + c.file + ": result would not parse (" + err.Error() + ")")
		return false
	}
	backup := prev != "" && !c.args.flag("--no-backup")
	if backup {
		if err := copyFile(c.file, c.file+".bak."+strconv.FormatInt(time.Now().Unix(), 10)); err != nil {
			c.eprintln("backup of " + c.file + " failed: " + err.Error())
			return false
		}
	}
	ensureDir(filepath.Dir(c.file))
	if err := os.WriteFile(c.file, []byte(next), 0o666); err != nil {
		c.eprintln("writing " + c.file + " failed: " + err.Error())
		return false
	}
	suffix := ""
	if backup {
		suffix = " (backup alongside)"
	}
	c.println(c.file + ": written" + suffix)
	return true
}

func (c *configurer) reload() {
	bin := os.Getenv("HERDR_BIN_PATH")
	if bin == "" {
		bin = "herdr"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "server", "reload-config")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		c.println("herdr: config reloaded")
		return
	}
	why := stderr.String()
	if why == "" {
		why = err.Error()
	}
	c.println("herdr: reload-config failed (" + jsTrim(why) + "); reload from Herdr's settings")
}

// run returns the exit code.
func (c *configurer) run() int {
	text := c.readConfig()

	if c.args.flag("--uninstall") {
		request(map[string]any{"cmd": "stop"}, 8*time.Second)
		if _, _, _, ok := splitBlock(text); !ok {
			c.println(c.file + ": no herdrmon block")
		} else {
			c.write(applyBlock(text, "", true), text)
		}
		if c.args.flag("--reload") {
			c.reload()
		}
		return 0
	}

	theme := readHerdrTheme(c.file)
	resolved := resolvePalette(theme)
	b := buildBlock(text, resolved, theme.NameString())

	if !c.args.flag("--write") {
		c.println("# Sidebar layout for herdrmon (theme: " + theme.NameString() + "). Add to " + c.file + ",")
		c.println("# or run the plugin action \"configure\" to write it as a managed block.\n")
		c.println(b.Snippet)
	}
	if len(b.Skipped) > 0 {
		names := make([]string, len(b.Skipped))
		for i, s := range b.Skipped {
			names[i] = "[ui.sidebar." + s + "]"
		}
		c.println("\n# " + strings.Join(names, " and ") + " already defined in " + c.file)
		c.println("# (by herdr-radar or by you), so herdrmon leaves it alone. Add these token")
		c.println("# entries to its rows where you want them:")
		for _, e := range b.Entries {
			c.println("#   $" + e[0] + ": " + e[1])
		}
	}
	if c.args.flag("--write") {
		next := applyBlock(text, b.Block, !b.HasBlock)
		if !c.write(next, text) {
			return 1
		}
		if c.args.flag("--reload") {
			c.reload()
		}
	}
	return 0
}

func cmdConfigure(args []string, stdout, stderr io.Writer) int {
	a := cliArgs(args)
	file, ok := a.opt("--herdr-config")
	if !ok {
		file = herdrConfigPath()
	}
	return (&configurer{args: a, file: file, stdout: stdout, stderr: stderr}).run()
}
