package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// gg task add [list] with no text opens $EDITOR on a draft in the same
// markdown format as the list files, so several tasks can be written at once.

const draftHelp = `<!--
  Write your tasks, then save and quit. Leave it empty to add nothing.

  # <list>            the list the tasks below go to; a new name makes a new list
  ## <day>            the day for the tasks below: today, tomorrow, 9/10/2029
                      (day/month/year). tasks before any ## in a list are for today.
  - [ ] some task     a task for that day
  - [ ] 13:00 task    a reminder: you get a notification at 13:00 that day
                      (9am and 7:30pm work too)

  Example:

  # work
  ## tomorrow
  - [ ] 09:00 standup
  - [ ] review the pr
  ## 9/10/2029
  - [ ] 13:00 dentist
-->
`

func draftTemplate(list, day string) string {
	if day == "" {
		day = "today"
	}
	return fmt.Sprintf("# %s\n\n## %s\n\n- [ ] \n\n%s", list, day, draftHelp)
}

var (
	htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	draftLine   = regexp.MustCompile(`^\s*[-*]\s+(?:\[[ xX]?\]\s*)?(.*)$`)
)

// parseDraft reads the tasks written in the editor. Errors name the line.
func parseDraft(text, list string, now time.Time) ([]todo, error) {
	text = htmlComment.ReplaceAllStringFunc(text, func(c string) string {
		return strings.Repeat("\n", strings.Count(c, "\n")) // keep line numbers right
	})
	today := dayOf(now)
	day := today
	var todos []todo
	sc := bufio.NewScanner(strings.NewReader(text))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
		case strings.HasPrefix(line, "## "):
			head := strings.ToLower(strings.TrimSpace(line[3:]))
			d, ok := parseDay(head, today)
			if !ok {
				d, ok = parseDay(strings.Fields(head + " x")[0], today) // "2026-10-02 Fri"
			}
			if !ok {
				return nil, fmt.Errorf("line %d: can't read day %q (try today, tomorrow or 9/10/2029)", n, head)
			}
			if d.Before(today) {
				return nil, fmt.Errorf("line %d: %s has already passed", n, d.Format("Mon 02 Jan 2006"))
			}
			day = d
		case strings.HasPrefix(line, "# "):
			list, day = cleanList(line[2:]), today
		default:
			m := draftLine.FindStringSubmatch(line)
			if m == nil {
				return nil, fmt.Errorf("line %d: tasks start with - [ ] (got %q)", n, line)
			}
			t := todo{List: list, Text: strings.TrimSpace(m[1]), Due: day}
			if t.Text == "" {
				continue
			}
			if tm := timePrefix.FindStringSubmatch(t.Text); tm != nil {
				h, mm, err := parseClock(tm[1])
				if err != nil {
					return nil, fmt.Errorf("line %d: %v", n, err)
				}
				t.Due = time.Date(day.Year(), day.Month(), day.Day(), h, mm, 0, 0, time.Local)
				t.Remind, t.Text = true, tm[2]
				if !t.Due.After(now) {
					return nil, fmt.Errorf("line %d: %s has already passed", n, t.Due.Format("Mon 02 Jan 15:04"))
				}
			}
			todos = append(todos, t)
		}
	}
	return todos, nil
}

// editTasks opens the draft in $VISUAL/$EDITOR, adds what was written and
// offers to reopen the draft when something in it can't be read.
func editTasks(list, when string, in io.Reader, out io.Writer) error {
	if when != "" {
		if _, remind, err := parseWhen(when, time.Now()); err != nil {
			return err
		} else if remind {
			return fmt.Errorf("--time with the editor takes a day, not a time (put times on the tasks)")
		}
	}
	f, err := os.CreateTemp("", "gg-tasks-*.md")
	if err != nil {
		return err
	}
	path := f.Name()
	defer os.Remove(path)
	f.WriteString(draftTemplate(list, strings.TrimSpace(when)))
	f.Close()

	reader := bufio.NewReader(in)
	for {
		cmd := editorCmd(path)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("editor: %w", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		todos, err := parseDraft(string(data), list, time.Now())
		if err == nil {
			return addDrafted(todos, out)
		}
		fmt.Fprintf(out, "%v\nedit again? [Y/n] ", err)
		answer, _ := reader.ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a == "n" || a == "no" {
			return fmt.Errorf("nothing added")
		}
		// Put the problem at the top of the draft so it's visible in the editor.
		text := regexp.MustCompile(`(?m)^<!-- gg: .*-->\n`).ReplaceAllString(string(data), "")
		os.WriteFile(path, []byte("<!-- gg: "+err.Error()+" -->\n"+text), 0o600)
	}
}

// editorCmd opens path in $VISUAL, $EDITOR or vi. The variable may carry
// arguments (code -w), so it goes through the shell.
func editorCmd(path string) *exec.Cmd {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	return exec.Command("sh", "-c", editor+` "$1"`, "sh", path)
}

func addDrafted(todos []todo, out io.Writer) error {
	if len(todos) == 0 {
		fmt.Fprintln(out, "nothing added")
		return nil
	}
	now := time.Now()
	for _, t := range todos {
		if _, err := call(request{Op: "todo.add", Text: t.Text, List: t.List, At: t.Due, Remind: t.Remind}); err != nil {
			return err
		}
		kind := "task"
		if t.Remind {
			kind = "reminder"
		}
		fmt.Fprintf(out, "added %s to %s · %s · %s\n", kind, t.List, describeWhen(t, now), t.Text)
	}
	return nil
}
