package main

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// helpText is what gg help prints: every command, the time formats, the
// draft format and the task keys, with this machine's real paths.
func helpText(cfgPath, tasksDir string) string {
	return fmt.Sprintf(`gg - a task manager with reminders (and a pomodoro) in your terminal

COMMANDS
  gg                       open your tasks in $EDITOR, as one markdown document
  gg LIST                  open just one list, e.g. gg work
  gg add [LIST]            write new tasks in $EDITOR (list defaults to inbox)
  gg add LIST "TEXT" [-t WHEN]
                           add one task, e.g. gg add work "ship it" -t "tomorrow 9am"
  gg ls [LIST]             print what's coming up, by day
  gg ls DAY... [LIST]      print whole days, open tasks first, then done
                           e.g. gg ls today · gg ls yesterday today work
  gg routines [DAY]        print your routines on a week grid: done, missed, to do (gg r)
  gg pomodoro              open the pomodoro (gg pomo for short)
  gg settings              open the config in $EDITOR; it's checked when you quit
  gg help                  show this help
  gg stop                  stop the background daemon (and the pomodoro it runs)

THE DOCUMENT  (gg, gg LIST, gg add)
  # work                   a list (a new name makes a new list)
  ## today                 the day for the tasks below: today, tomorrow, friday, 9/10, 9/10/2029
  ## weekdays              the tasks below repeat: daily, weekdays, weekends, mon-fri, every mon, thu
  - [ ] review the pr      a task · - [x] is done · delete the line to delete the task
  - [ ] 09:30 standup      a reminder at 09:30 (9am and 7:30pm work too)

  save and quit to apply. if a line can't be read, nothing is saved: you see why, and
  the editor reopens on that line. finished tasks from past days aren't shown; they stay
  in the files.

WHEN  (-t or --time; day/month/year; a space or - between the parts)
  (none)                   a task for today
  tomorrow, friday, 9/10/2029
                           a task for that day
  13:00, 1pm               a reminder today at that time
  "tomorrow 9am", "9/10/2029 13:00"
                           a reminder on that day at that time
  "weekdays 6pm", "daily 9am", "mon-fri 9:30", "every mon, thu 7pm", weekends
                           a routine: it repeats on those days, with or without a time

DAY   (for ls and routines)
  today, yesterday, tomorrow, friday, 9/10/2029, 9/10

REMINDERS
  a task with a time notifies you when it's due. unchecked tasks whose time has passed
  are overdue, and every overdue_nag (default 1h) you get a nudge.
  a routine gets a task on each of its days; check that off, and it's back next time.
  left unchecked, it's overdue (and nags) until the day ends, then a missed day.

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

// textCommand runs everything but the pomodoro.
func textCommand(args []string, cfgPath string) error {
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	switch {
	case cmd == "":
		return editDoc("", os.Stdin, os.Stdout)
	case cmd == "add" || cmd == "ls" || cmd == "list":
		return taskCommand(args, os.Stdout)
	case cmd == "settings" || cmd == "config":
		return editConfig(cfgPath, os.Stdin, os.Stdout)
	case (cmd == "routines" || cmd == "routine" || cmd == "r") && len(args) <= 2:
		day := time.Now()
		if len(args) == 2 {
			d, ok := parseDay(args[1], dayOf(day))
			if !ok {
				return fmt.Errorf("can't read day %q (try last week's date like 28/9, or monday)", args[1])
			}
			day = d
		}
		s, err := call(request{Op: "get"})
		if err != nil {
			return err
		}
		printRoutines(os.Stdout, s.Todos, day, time.Now())
		return nil
	case len(args) == 1:
		s, err := call(request{Op: "get"})
		if err != nil {
			return err
		}
		if !slices.Contains(s.Lists, cleanList(cmd)) {
			return fmt.Errorf("no command or list called %q · %s (to start a list, add a # %s section in gg)", cmd, seeHelp, cleanList(cmd))
		}
		return editDoc(cleanList(cmd), os.Stdin, os.Stdout)
	}
	return fmt.Errorf("unknown command %q · %s", strings.Join(args, " "), seeHelp)
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

	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "config: "+w+" · gg settings to fix it")
	}
	if err := ensureDaemon(); err != nil {
		fail("%v", err)
	}
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	// Everything but the pomodoro is text: an editor, or printed output.
	if cmd != "pomodoro" && cmd != "pomo" {
		warnRinging()
		if err := textCommand(args, path); err != nil {
			fail("%v", err)
		}
		return
	}

	a := newApp(cfg, path)
	a.updates, err = watch()
	if err != nil {
		fail("Error attaching to daemon: %v", err)
	}
	a.send(request{Op: "reload"})     // pick up config edits made outside gg
	a.send(request{Op: "pomo.begin"}) // no-op while one is running
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
