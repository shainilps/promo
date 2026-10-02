package main

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// helpText is what gg help prints: every command, the time formats, the
// draft format and the task keys, with this machine's real paths.
func helpText(cfgPath, tasksDir string) string {
	return fmt.Sprintf(`gg - a task manager with reminders (and a pomodoro) in your terminal

COMMANDS
  gg                       open your tasks
  gg LIST                  open your tasks on one list, e.g. gg work
  gg add [LIST]            write tasks in $EDITOR (list defaults to inbox)
  gg add LIST "TEXT" [-t WHEN]
                           add one task, e.g. gg add work "ship it" -t tomorrow-9am
  gg ls [LIST]             print what's coming up, by day
  gg ls DAY... [LIST]      print whole days, open tasks first, then done
                           e.g. gg ls today · gg ls yesterday today work
  gg pomodoro              open the pomodoro (gg pomo for short)
  gg settings              open the settings
  gg help                  show this help
  gg stop                  stop the background daemon (and the pomodoro it runs)

WHEN  (-t or --time; dates are day/month/year, past times are refused)
  (none)                   a task for today
  tomorrow, 9/10/2029      a task for that day
  13:00, 1pm               a reminder today at that time
  tomorrow-9am, 9/10/2029-13:00
                           a reminder on that day at that time

DAY   (for ls)
  today, yesterday, tomorrow, 9/10/2029, 9/10

EDITOR DRAFT  (gg add)
  # work                   the list for the tasks below (a new name makes a new list)
  ## tomorrow              the day for the tasks below (today if there is none)
  - [ ] 09:00 standup      a reminder at 09:00
  - [ ] review the pr      a plain task

TASK KEYS  (press ? in gg for all of them)
  j/k move · space done · a add · A add to another list · e edit · E edit the list file
  x delete (asks) · X delete now · D delete the list (asks) · h/l switch list · c clear done
  q quit

REMINDERS
  a task with a time notifies you when it's due. unchecked tasks whose time has passed
  move to OVERDUE at the top, and every overdue_nag (default 1h) you get a nudge.

FILES
  config   %s
  tasks    %s/<list>.md  (plain markdown; edit by hand if you like)
`, shortHome(cfgPath), shortHome(tasksDir))
}

const seeHelp = "run gg help to see every command"

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// warnRinging tells a plain CLI command that the pomodoro is ringing, since
// it has no screen to show it on.
func warnRinging() {
	if s, err := call(request{Op: "get"}); err == nil && s.Ring != "" {
		fmt.Fprintln(os.Stderr, "the pomodoro is ringing · run gg and press any key")
	}
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
		case "-h", "--help", "help", "h":
			fmt.Print(helpText(path, expandHome(cfg.TasksDir)))
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
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	if cmd == "add" || cmd == "ls" || cmd == "list" {
		warnRinging()
		if err := taskCommand(args, os.Stdout); err != nil {
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
		a.tasks.flash = "config: " + warnings[0]
		if len(warnings) > 1 {
			a.tasks.flash += fmt.Sprintf(" (+%d more)", len(warnings)-1)
		}
	}

	// Each view stands alone: tasks (the default), the pomodoro, settings.
	switch {
	case cmd == "":
	case cmd == "pomodoro" || cmd == "pomo":
		a.send(request{Op: "pomo.begin"}) // no-op while one is running
		a.screen = screenPomodoro
	case cmd == "settings" || cmd == "config":
		a.settings.open(a)
		a.screen = screenSettings
	case len(args) == 1 && contains(a.state.Lists, cleanList(cmd)):
		a.tasks.list = cleanList(cmd)
	case len(args) == 1:
		fail("no command or list called %q · %s", cmd, seeHelp)
	default:
		fail("unknown command %q · %s", strings.Join(args, " "), seeHelp)
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
