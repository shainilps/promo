package main

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// gg routines prints every routine against the days of a week, so a missed
// check out stands out.

// monday is the first day of day's week.
func monday(day time.Time) time.Time {
	return dayOf(day).AddDate(0, 0, -(int(day.Weekday())+6)%7)
}

// printRoutines draws the week that holds day: ✓ done, ! missed (or late
// today), ○ still to do, · nothing that day.
func printRoutines(out io.Writer, todos []todo, day, now time.Time) {
	muted := lipgloss.NewStyle().Foreground(colorMuted)
	warn := lipgloss.NewStyle().Foreground(colorWarn).Bold(true)
	blue := lipgloss.NewStyle().Foreground(colorTask)
	green := lipgloss.NewStyle().Foreground(colorBreak)

	var routines []todo
	onDay := map[string]todo{} // routine ID + day -> its task
	slot := func(id int, d time.Time) string { return fmt.Sprint(id, d.Format("2006-01-02")) }
	for _, t := range todos {
		if t.Every != 0 {
			routines = append(routines, t)
		} else if t.Of != 0 {
			onDay[slot(t.Of, dayOf(t.Due))] = t
		}
	}
	if len(routines) == 0 {
		fmt.Fprintln(out, "no routines yet. a routine is a task that comes back on the days you pick:")
		fmt.Fprintln(out, muted.Render(`  gg add work "check out" -t "weekdays 6pm"`))
		fmt.Fprintln(out, muted.Render("  or in gg, under a heading like ## weekdays"))
		return
	}
	slices.SortStableFunc(routines, func(x, y todo) int {
		return cmp.Or(cmp.Compare(clockText(x), clockText(y)), cmp.Compare(x.List, y.List), cmp.Compare(x.Text, y.Text))
	})

	start, today := monday(day), dayOf(now)
	textW, listW := 0, 0
	for _, r := range routines {
		textW = max(textW, min(lipgloss.Width(r.Text), 32))
		listW = max(listW, lipgloss.Width(r.List))
	}
	fmt.Fprintln(out, lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("WEEK OF %s – %s",
		strings.ToUpper(start.Format("Mon 02 Jan")), strings.ToUpper(start.AddDate(0, 0, 6).Format("Mon 02 Jan")))))
	fmt.Fprintln(out)

	lead := strings.Repeat(" ", 2+5+2+textW+2+listW)
	names, dates := lead, lead
	for i := range 7 {
		d := start.AddDate(0, 0, i)
		style := muted
		if d.Equal(today) {
			style = blue.Bold(true)
		}
		names += style.Render(fmt.Sprintf("  %s", d.Format("Mon")[:2]))
		dates += style.Render(fmt.Sprintf("  %s", d.Format("02")))
	}
	fmt.Fprintln(out, names)
	fmt.Fprintln(out, dates)

	for _, r := range routines {
		line := "  " + blue.Render(fit(clockText(r), 5)) + "  " + fit(truncateRight(r.Text, textW), textW) + "  " + muted.Render(fit(r.List, listW))
		done, due := 0, 0
		for i := range 7 {
			d := start.AddDate(0, 0, i)
			t, ok := onDay[slot(r.ID, d)]
			mark := muted.Render("·")
			switch {
			case ok && t.Done:
				mark = green.Render("✓")
			case ok && (d.Before(today) || isOverdue(t, now)):
				mark = warn.Render("!")
			case ok:
				mark = blue.Render("○")
			case r.Every.on(d) && d.After(today):
				mark = muted.Render("○")
			}
			line += "   " + mark
			if ok && !d.After(today) {
				due++
				done += boolInt(t.Done)
			}
		}
		count := ""
		if due > 0 {
			count = fmt.Sprintf("%d/%d", done, due)
		}
		fmt.Fprintln(out, line+"   "+muted.Render(fit(count, 5)+"  "+r.Every.heading()))
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, green.Render("✓")+muted.Render(" done   ")+warn.Render("!")+muted.Render(" missed or late   ")+
		blue.Render("○")+muted.Render(" to do   · nothing that day   ·   gg routines DAY shows that day's week"))
}
