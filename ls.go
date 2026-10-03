package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// gg add and gg ls: the task commands that print instead of opening an editor.

func truncateRight(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w <= 1 {
		return "…"
	}
	return string(r[:w-1]) + "…"
}

// fit pads or cuts a styled line to exactly w cells.
func fit(s string, w int) string {
	if lipgloss.Width(s) > w {
		return lipgloss.NewStyle().MaxWidth(w).Render(s)
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

// spread puts left and right at the two ends of a w-wide line.
func spread(left, right string, w int) string {
	return left + strings.Repeat(" ", max(w-lipgloss.Width(left)-lipgloss.Width(right), 1)) + right
}

// shortSpan renders a gap like 45m, 3h 20m, 2d.
func shortSpan(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 10*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// relative says how far off a task is: "in 2h", "3h late", "1d late",
// or for a routine, when it repeats.
func relative(t todo, overdue bool, now time.Time) (string, bool) {
	switch {
	case t.Every != 0:
		return "every " + t.Every.String(), false
	case t.Done:
		return "", false
	case t.Remind && t.Due.After(now):
		return "in " + shortSpan(t.Due.Sub(now)), false
	case t.Remind:
		return shortSpan(now.Sub(t.Due)) + " late", true
	case overdue:
		return shortSpan(dayOf(now).Sub(dayOf(t.Due))) + " late", true
	}
	return "", false
}

func shortHome(p string) string {
	if home := expandHome("~/"); strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

var taskUsage = "usage: gg add [LIST] · gg add LIST \"TEXT\" [-t WHEN] · gg ls [DAY...] [LIST] · " + seeHelp

// taskCommand runs gg add / gg ls from the shell.
func taskCommand(args []string, out io.Writer) error {
	switch args[0] {
	case "add":
		var when string
		var words []string
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			switch arg := rest[i]; {
			case arg == "--time" || arg == "-t":
				if i+1 >= len(rest) {
					return errors.New("--time needs a value (" + whenHelp + ")")
				}
				i++
				when = rest[i]
			case strings.HasPrefix(arg, "--time="):
				when = strings.TrimPrefix(arg, "--time=")
			default:
				words = append(words, arg)
			}
		}
		if len(words) <= 1 {
			list := defaultList
			if len(words) == 1 {
				list = cleanList(words[0])
			}
			return editTasks(list, when, os.Stdin, out)
		}
		list := words[0]
		text := strings.TrimSpace(strings.Join(words[1:], " "))
		if text == "" {
			return errors.New(taskUsage)
		}
		t, err := parseWhen(when, time.Now())
		if err != nil {
			return err
		}
		list = cleanList(list)
		if _, err := call(request{Op: "todo.add", Text: text, List: list, At: t.Due, Remind: t.Remind, Every: t.Every}); err != nil {
			return err
		}
		fmt.Fprintf(out, "added %s to %s · %s · %s\n", t.kind(), list, describeWhen(t, time.Now()), text)
		return nil

	case "ls", "list":
		// Arguments that read as a day pick days; the other one is a list.
		var list string
		var days []time.Time
		today := dayOf(time.Now())
		for _, arg := range args[1:] {
			if d, ok := parseDay(strings.ToLower(arg), today); ok {
				days = append(days, d)
			} else if list == "" {
				list = cleanList(arg)
			} else {
				return errors.New(taskUsage)
			}
		}
		s, err := call(request{Op: "get"})
		if err != nil {
			return err
		}
		if len(days) > 0 {
			slices.SortFunc(days, time.Time.Compare)
			for i, d := range slices.CompactFunc(days, time.Time.Equal) {
				if i > 0 {
					fmt.Fprintln(out)
				}
				printDay(out, s.Todos, list, d, time.Now())
			}
			return nil
		}
		groups := groupTasks(s.Todos, list, time.Now())
		if len(groups) == 0 {
			fmt.Fprintln(out, "nothing here")
		}
		for i, g := range groups {
			if i > 0 {
				fmt.Fprintln(out)
			}
			fmt.Fprintln(out, g.title)
			for _, t := range g.items {
				box := "[ ]"
				if t.Done {
					box = "[x]"
				}
				if t.Every != 0 {
					box = "↻"
				}
				line := "  " + box + " "
				switch {
				case g.overdue:
					line += overdueWhen(t, time.Now()) + "  "
				case t.Remind:
					line += t.Due.Format("15:04") + " "
				}
				line += t.Text
				var notes []string
				if list == "" {
					notes = append(notes, t.List)
				}
				if t.Every != 0 {
					notes = append(notes, "every "+t.Every.String())
				}
				if len(notes) > 0 {
					line += "  (" + strings.Join(notes, " · ") + ")"
				}
				fmt.Fprintln(out, line)
			}
		}
		return nil
	}
	return errors.New(taskUsage)
}

// printDay prints every task on one day, open ones first (reminders by
// time), then the finished ones, with list and how late or soon each is.
// A day still to come shows the routines that fall on it.
func printDay(out io.Writer, todos []todo, list string, day, now time.Time) {
	var mine []todo
	for _, t := range todos {
		switch {
		case list != "" && t.List != list:
		case t.Every != 0 && day.After(dayOf(now)) && t.Every.on(day):
			mine = append(mine, todo{ID: t.ID, List: t.List, Text: t.Text, Due: on(day, t.Due), Remind: t.Remind})
		case t.Every == 0 && dayOf(t.Due).Equal(day):
			mine = append(mine, t)
		}
	}
	slices.SortFunc(mine, compareTodos)
	done := 0
	textW, listW := 0, 0
	for _, t := range mine {
		done += boolInt(t.Done)
		textW = max(textW, min(lipgloss.Width(t.Text), 50))
		listW = max(listW, lipgloss.Width(t.List))
	}

	muted := lipgloss.NewStyle().Foreground(colorMuted)
	late := lipgloss.NewStyle().Foreground(colorWarn)
	title := lipgloss.NewStyle().Bold(true).Render(dayTitle(day, dayOf(now)))
	where := ""
	if list != "" {
		where = " · " + list
	}
	if len(mine) == 0 {
		fmt.Fprintln(out, title+muted.Render("  ·  nothing"+where))
		return
	}
	fmt.Fprintln(out, title+muted.Render(fmt.Sprintf("  ·  %d open · %d done%s", len(mine)-done, done, where)))
	for _, t := range mine {
		box, when := "[ ]", "     "
		if t.Done {
			box = "[x]"
		}
		if t.Remind {
			when = t.Due.Format("15:04")
		}
		status, isLate := relative(t, isOverdue(t, now), now)
		if t.Done {
			status = "done"
		}
		line := fmt.Sprintf("  %s %s  %s", box, when, fit(truncateRight(t.Text, 50), textW))
		if list == "" {
			line += "  " + fit(t.List, listW)
		}
		switch {
		case t.Done:
			line = muted.Render(line + "  " + status)
		case isLate:
			line = late.Render(line + "  " + status)
		default:
			line += "  " + muted.Render(status)
		}
		fmt.Fprintln(out, strings.TrimRight(line, " "))
	}
}
