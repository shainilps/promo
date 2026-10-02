package main

import (
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const usage = `gg - pomodoro, timer, alarms and tasks in your terminal

usage:
  gg                 open the menu
  gg <duration>      start a timer right away (e.g. gg 25m)
  gg pomodoro        start a pomodoro right away
  gg alarm <time>    add an alarm right away (e.g. gg alarm 07:30, gg alarm 7:30pm)
  gg task add [list]
                     write tasks in $EDITOR (list defaults to inbox; # headings in
                     the draft pick other lists, a new name makes a new list)
  gg task add list "text" [--time WHEN]
                     add one task to a list. no --time means today;
                     a date makes it a task for that day; a time makes it a reminder.
                     WHEN: 13:00, 1pm, tomorrow, tomorrow-9am, 9/10/2029, 9/10/2029-13:00
                     (dates are day/month/year)
  gg task [list]     open the task lists (check tasks off here)
  gg task ls [list]  print tasks, by date
  gg stop            stop the background daemon (and everything it runs)
  gg daemon          run the daemon in the foreground (normally started for you)

every gg session attaches to one background daemon, so pomodoro, timer, alarms
and reminders keep running after you quit.

config: ~/.config/gg/config.yaml (edit it from Settings in the menu)
tasks:  one markdown file per list in ~/.local/share/gg/tasks (tasks_dir in config)
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
				fmt.Println("gg daemon is not running")
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
	isTask := len(args) > 0 && (args[0] == "task" || args[0] == "tasks")
	if isTask && len(args) > 1 && (args[1] == "add" || args[1] == "ls" || args[1] == "list") {
		if err := taskCommand(args[1:], os.Stdout); err != nil {
			fail("%v", err)
		}
		return
	}
	a := newApp(cfg, path)
	a.updates, err = watch()
	if err != nil {
		fail("Error attaching to daemon: %v", err)
	}
	a.send(request{Op: "reload"}) // pick up config edits made outside gg
	if len(warnings) > 0 {
		a.warning = warnings[0]
		if len(warnings) > 1 {
			a.warning += fmt.Sprintf(" (+%d more)", len(warnings)-1)
		}
	}

	switch {
	case len(args) == 0:
	case isTask:
		if len(args) > 2 {
			fail("%s", taskUsage)
		}
		if len(args) == 2 {
			a.tasks.list = cleanList(args[1])
		}
		a.screen = screenTasks
	case args[0] == "alarm":
		if len(args) != 2 {
			fail("usage: gg alarm <time>  (e.g. gg alarm 07:30)")
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
