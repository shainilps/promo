package main

import (
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const usage = `promo - pomodoro, timer and alarm in your terminal

usage:
  promo                 open the menu
  promo <duration>      start a timer right away (e.g. promo 25m)
  promo pomodoro        start a pomodoro right away
  promo alarm <time>    add an alarm right away (e.g. promo alarm 07:30, promo alarm 7:30pm)
  promo stop            stop the background daemon (and everything it runs)
  promo daemon          run the daemon in the foreground (normally started for you)

every promo session attaches to one background daemon, so pomodoro, timer and
alarms keep running after you quit.

config: ~/.config/promo/config.yaml (edit it from Settings in the menu)
`

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func main() {
	path, err := configPath()
	if err != nil {
		fail("Error getting home dir: %v", err)
	}
	cfg, warnings, err := loadConfig(path)
	if err != nil {
		fail("Error loading config: %v", err)
	}

	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			fmt.Print(usage)
			return
		case "daemon":
			if err := runDaemon(path, cfg); err != nil {
				fail("%v", err)
			}
			return
		case "stop":
			if !daemonRunning() {
				fmt.Println("promo daemon is not running")
				return
			}
			if _, err := call(request{Op: "stop"}); err != nil {
				fail("Error stopping daemon: %v", err)
			}
			return
		}
	}

	if err := ensureDaemon(); err != nil {
		fail("%v", err)
	}
	a := newApp(cfg, path)
	a.updates, err = watch()
	if err != nil {
		fail("Error attaching to daemon: %v", err)
	}
	a.send(request{Op: "reload"}) // pick up config edits made outside promo
	if len(warnings) > 0 {
		a.warning = warnings[0]
		if len(warnings) > 1 {
			a.warning += fmt.Sprintf(" (+%d more)", len(warnings)-1)
		}
	}

	switch {
	case len(args) == 0:
	case args[0] == "alarm":
		if len(args) != 2 {
			fail("usage: promo alarm <time>  (e.g. promo alarm 07:30)")
		}
		h, m, err := parseClock(args[1])
		if err != nil {
			fail("%v", err)
		}
		a.alarm.show(a, a.alarm.add(a, nextOccurrence(h, m)))
		a.oneShot = true
	case args[0] == "pomodoro" || args[0] == "pomo":
		a.send(request{Op: "pomo.begin"})
		a.screen = screenPomodoro
	case len(args) == 1:
		d, err := time.ParseDuration(args[0])
		if err != nil || d <= 0 {
			fail("Invalid duration: %s\n\n%s", args[0], usage)
		}
		a.send(request{Op: "timer.begin", Dur: d})
		a.screen, a.oneShot = screenTimer, true
	default:
		fail("%s", usage)
	}
	if a.err != nil {
		fail("%v", a.err)
	}

	var opts []tea.ProgramOption
	if cfg.UI.Fullscreen {
		opts = append(opts, tea.WithAltScreen())
	}
	if _, err := tea.NewProgram(a, opts...).Run(); err != nil {
		fail("Error running program: %v", err)
	}
	if a.err != nil {
		fail("%v", a.err)
	}
}
