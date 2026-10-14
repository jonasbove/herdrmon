// herdrmon-sidebar: the Herdr plugin jonasbove.herdrmon. One binary, one
// subcommand per plugin entrypoint (see herdr-plugin.toml):
//
//	herdrmon-sidebar configure [--write|--uninstall] [--reload] [--no-backup] [--herdr-config PATH]
//	herdrmon-sidebar start [--quiet]   start the daemon for this Herdr server
//	herdrmon-sidebar stop              stop it; it clears every token it wrote
//	herdrmon-sidebar demo [--workspace ID]
//	herdrmon-sidebar daemon            the daemon itself (start launches it)
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

const usage = `usage: herdrmon-sidebar <command> [flags]

  configure [--write|--uninstall] [--reload] [--no-backup] [--herdr-config PATH]
            print the sidebar layout, or write/remove it in Herdr's config.toml
  start [--quiet]         start the daemon for this Herdr server
  stop                    stop the daemon and clear its tokens
  demo [--workspace ID]   play every animation on a workspace's Pokémon
  daemon                  run the daemon in the foreground
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	args := os.Args[2:]
	switch os.Args[1] {
	case "configure":
		os.Exit(cmdConfigure(args, os.Stdout, os.Stderr))
	case "start":
		os.Exit(cmdStart(cliArgs(args).flag("--quiet") || os.Getenv("HERDR_PLUGIN_EVENT") != ""))
	case "stop":
		os.Exit(cmdStop())
	case "demo":
		os.Exit(cmdDemo(cliArgs(args)))
	case "daemon":
		started, err := runDaemon()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if !started {
			os.Exit(0)
		}
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

func ping(timeout time.Duration) map[string]any {
	return request(map[string]any{"cmd": "ping"}, timeout)
}

// cmdStart starts the daemon for this Herdr server if it isn't running, and
// returns. Startup hooks and the agent-detected watchdog both run this; it
// must exit quickly, so the daemon is spawned detached.
func cmdStart(quiet bool) int {
	if alive := ping(1500 * time.Millisecond); alive != nil {
		if !quiet {
			fmt.Printf("herdrmon sidebar: already running (pid %s, %s = %s)\n", jsString(alive["pid"]), jsString(alive["self"]), jsString(alive["mon"]))
		}
		return 0
	}
	dir := ensureDir(stateDir())
	errPath := filepath.Join(dir, "daemon.stderr")
	errFile, err := os.OpenFile(errPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
	if err != nil {
		fmt.Fprintf(os.Stderr, "herdrmon sidebar: %v\n", err)
		return 1
	}
	defer errFile.Close()
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "herdrmon sidebar: %v\n", err)
		return 1
	}
	cmd := exec.Command(exe, "daemon")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, errFile
	cmd.Dir = filepath.Dir(filepath.Dir(exe)) // the plugin directory
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "herdrmon sidebar: %v\n", err)
		return 1
	}
	_ = cmd.Process.Release()
	// Wait until it answers, so `start && demo` works.
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		if up := ping(500 * time.Millisecond); up != nil {
			if !quiet {
				extra := ""
				if !truthy(up["frames"]) {
					extra = ", no frames: plain glyph"
				}
				fmt.Printf("herdrmon sidebar: started (pid %s, %s = %s%s)\n", jsString(up["pid"]), jsString(up["self"]), jsString(up["mon"]), extra)
			}
			return 0
		}
	}
	fmt.Fprintf(os.Stderr, "herdrmon sidebar: daemon did not come up; see %s and daemon.log\n", errPath)
	return 1
}

func cmdStop() int {
	if request(map[string]any{"cmd": "stop"}, 8*time.Second) != nil {
		fmt.Println("herdrmon sidebar: stopped")
	} else {
		fmt.Println("herdrmon sidebar: not running")
	}
	return 0
}

// demoSteps: poison last, so when the forced block ends the daemon's own
// "block answered" rule plays recall + send-out, exactly as it does for a
// real agent.
var demoSteps = []map[string]any{
	{"cmd": "play", "anim": "sendout"},
	{"cmd": "play", "anim": "tackle"},
	{"cmd": "play", "anim": "harden"},
	{"cmd": "force", "status": "idle", "night": true, "ms": 2500},
	{"cmd": "force", "status": "blocked", "ms": 2500},
}

const demoSettle = 2500 * time.Millisecond // recall + send-out after the block

// cmdDemo plays every animation on one workspace's Pokémon: the focused
// workspace (HERDR_WORKSPACE_ID when run as a plugin action), or
// --workspace ID.
func cmdDemo(args cliArgs) int {
	workspace, ok := args.opt("--workspace")
	if !ok {
		workspace = os.Getenv("HERDR_WORKSPACE_ID")
	}
	if workspace == "" {
		for _, p := range paneList() {
			if truthy(p["focused"]) {
				workspace = idString(p["workspace_id"])
				break
			}
		}
	}
	if workspace == "" {
		fmt.Fprintln(os.Stderr, "herdrmon demo: no workspace (pass --workspace <id>)")
		return 1
	}
	cmdStart(true)
	code := 0
	for _, step := range demoSteps {
		msg := map[string]any{"workspace": workspace}
		for k, v := range step {
			msg[k] = v
		}
		r := request(msg, 15*time.Second)
		what, _ := step["anim"].(string)
		if what == "" {
			what = "poison"
			if step["night"] == true {
				what = "sleep"
			}
		}
		result := "no answer"
		switch {
		case r != nil && truthy(r["ok"]):
			result = "ok"
		case r != nil && r["error"] != nil:
			result = jsString(r["error"])
		}
		fmt.Printf("%s: %s\n", what, result)
		if r != nil && !truthy(r["ok"]) {
			code = 1
		}
	}
	time.Sleep(demoSettle)
	fmt.Println("recall + sendout: ok")
	return code
}
