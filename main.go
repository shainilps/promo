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
  promo alarm <time>    set an alarm right away (e.g. promo alarm 07:30, promo alarm 7:30pm)

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

	a := newApp(cfg, path)
	if len(warnings) > 0 {
		a.warning = warnings[0]
		if len(warnings) > 1 {
			a.warning += fmt.Sprintf(" (+%d more)", len(warnings)-1)
		}
	}

	args := os.Args[1:]
	switch {
	case len(args) == 0:
	case args[0] == "-h" || args[0] == "--help" || args[0] == "help":
		fmt.Print(usage)
		return
	case args[0] == "alarm":
		if len(args) != 2 {
			fail("usage: promo alarm <time>  (e.g. promo alarm 07:30)")
		}
		h, m, err := parseClock(args[1])
		if err != nil {
			fail("%v", err)
		}
		a.alarm.arm(nextOccurrence(h, m))
		a.screen, a.oneShot = screenAlarm, true
	case args[0] == "pomodoro" || args[0] == "pomo":
		a.pomo.begin(cfg)
		a.screen = screenPomodoro
	case len(args) == 1:
		d, err := time.ParseDuration(args[0])
		if err != nil || d <= 0 {
			fail("Invalid duration: %s\n\n%s", args[0], usage)
		}
		a.timer.begin(d)
		a.screen, a.oneShot = screenTimer, true
	default:
		fail("%s", usage)
	}

	var opts []tea.ProgramOption
	if cfg.UI.Fullscreen {
		opts = append(opts, tea.WithAltScreen())
	}
	if _, err := tea.NewProgram(a, opts...).Run(); err != nil {
		fail("Error running program: %v", err)
	}
	a.shutdown()
}
