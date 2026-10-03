package main

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// gg routines lays every routine against the days of a week, so a missed
// check out stands out.

type routinesUI struct {
	cursor int
	week   int    // 0 = this week, -1 = last week ...
	ask    string // y/n question in the status bar
	onYes  func()
	flash  string
}

// items is every routine, by time of day.
func (u *routinesUI) items(a *app) []todo {
	var rs []todo
	for _, t := range a.state.Todos {
		if t.Every != 0 {
			rs = append(rs, t)
		}
	}
	slices.SortStableFunc(rs, func(x, y todo) int {
		return cmp.Or(cmp.Compare(clockText(x), clockText(y)), cmp.Compare(x.List, y.List), cmp.Compare(x.Text, y.Text))
	})
	return rs
}

// focus moves the cursor to a routine, found by list and text.
func (u *routinesUI) focus(a *app, list, text string) {
	for i, r := range u.items(a) {
		if r.List == list && r.Text == text {
			u.cursor = i
		}
	}
}

// taskOn is a routine's task on one day, if it has one.
func taskOn(a *app, r todo, day time.Time) (todo, bool) {
	for _, t := range a.state.Todos {
		if t.Of == r.ID && dayOf(t.Due).Equal(day) {
			return t, true
		}
	}
	return todo{}, false
}

// monday is the first day of the week week weeks from now's.
func monday(now time.Time, week int) time.Time {
	d := dayOf(now)
	return d.AddDate(0, 0, -(int(d.Weekday())+6)%7+7*week)
}

func (u *routinesUI) update(a *app, msg tea.KeyMsg) tea.Cmd {
	if yes := u.onYes; yes != nil {
		u.ask, u.onYes = "", nil
		if msg.String() == "y" {
			yes()
		}
		return nil
	}
	u.flash = ""
	items := u.items(a)
	n := len(items)
	u.cursor = max(min(u.cursor, n-1), 0)
	f := &a.tasks.form
	switch {
	case key.Matches(msg, keys.Up):
		u.cursor = max(u.cursor-1, 0)
	case key.Matches(msg, keys.Down):
		u.cursor = max(min(u.cursor+1, n-1), 0)
	case key.Matches(msg, keys.Top):
		u.cursor = 0
	case key.Matches(msg, keys.Bottom):
		u.cursor = max(n-1, 0)
	case key.Matches(msg, keys.WeekPrev):
		u.week--
	case key.Matches(msg, keys.WeekNext):
		u.week++
	case key.Matches(msg, keys.ThisWeek):
		u.week = 0
	case key.Matches(msg, keys.Check) && n > 0:
		r := items[u.cursor]
		if t, ok := taskOn(a, r, dayOf(time.Now())); ok {
			a.send(request{Op: "todo.toggle", ID: t.ID})
		} else if r.Every.on(time.Now()) {
			u.flash = "added after its time today · it starts tomorrow"
		} else {
			u.flash = r.Text + " isn't on today"
		}
	case key.Matches(msg, keys.AddTask, keys.AddToList):
		list := defaultList
		if n > 0 {
			list = items[u.cursor].List
		}
		cmd := f.open(list, true)
		f.days, f.back = weekdays, screenRoutines
		a.screen = screenRoutineForm
		return cmd
	case key.Matches(msg, keys.EditTask) && n > 0:
		cmd := f.openEdit(items[u.cursor])
		f.back = screenRoutines
		a.screen = screenRoutineForm
		return cmd
	case key.Matches(msg, keys.DelTask, keys.DelTaskNow) && n > 0:
		r := items[u.cursor]
		u.ask = fmt.Sprintf("delete %q? it repeats %s", truncateRight(r.Text, 30), describeWhen(r, time.Now()))
		u.onYes = func() { a.send(request{Op: "todo.remove", ID: r.ID}) }
	case key.Matches(msg, keys.Back):
		return tea.Quit
	}
	return nil
}

// cell is one routine on one day: ✓ done, ! missed (or late today),
// ○ still to do, · nothing that day.
func cell(a *app, r todo, day, now time.Time) string {
	today := dayOf(now)
	t, ok := taskOn(a, r, day)
	switch {
	case ok && t.Done:
		return a.st.success.Render("✓")
	case ok && (day.Before(today) || isOverdue(t, now)):
		return lipgloss.NewStyle().Foreground(colorWarn).Bold(true).Render("!")
	case ok:
		return lipgloss.NewStyle().Foreground(colorTask).Render("○")
	case r.Every.on(day) && day.After(today):
		return a.st.muted.Render("○")
	}
	return a.st.muted.Render("·")
}

// weekLabel names the week shown: this week, last week, 3 weeks ago ...
func weekLabel(week int) string {
	switch {
	case week == 0:
		return "this week"
	case week == -1:
		return "last week"
	case week == 1:
		return "next week"
	case week < 0:
		return fmt.Sprintf("%d weeks ago", -week)
	}
	return fmt.Sprintf("in %d weeks", week)
}

// grid draws the routines against the week's days.
func (u *routinesUI) grid(a *app, now time.Time, w int) []string {
	st := a.st
	items := u.items(a)
	start := monday(now, u.week)
	today := dayOf(now)
	end := start.AddDate(0, 0, 6)
	title := st.muted.Bold(true).Render(fmt.Sprintf("WEEK OF %s – %s", strings.ToUpper(start.Format("Mon 02 Jan")), strings.ToUpper(end.Format("Mon 02 Jan"))))
	lines := []string{spread(title, st.muted.Render(weekLabel(u.week)), w), ""}
	if len(items) == 0 {
		return append(lines,
			st.text.Render("no routines yet"),
			"",
			st.muted.Render("a routine is a task that comes back on the days you pick, like"),
			st.muted.Render("check in at 09:30 and check out at 18:00 on weekdays."),
			"",
			st.muted.Render("press a to add one, or from the shell:  gg add work \"check out\" -t \"weekdays 6pm\""))
	}

	// Columns: marker, time, text, list, the seven days, done count, schedule.
	textW, listW, everyW := 4, 4, 0
	for _, r := range items {
		textW = max(textW, lipgloss.Width(r.Text))
		listW = max(listW, lipgloss.Width(r.List))
		everyW = max(everyW, lipgloss.Width(r.Every.String())+6)
	}
	const timeW, dayW, countW = 5, 4, 5
	fixed := 2 + timeW + 2 + 2 + 7*dayW + 2 + countW
	textW = min(textW, 32)
	if fixed+textW+listW+2+everyW+2 > w {
		everyW = 0
	}
	if fixed+textW+listW+2 > w {
		listW = 0
	}
	textW = max(min(textW, w-fixed-boolInt(listW > 0)*(listW+2)-boolInt(everyW > 0)*(everyW+2)), 8)

	lead := strings.Repeat(" ", 2+timeW+2+textW+2)
	if listW > 0 {
		lead += strings.Repeat(" ", listW+2)
	}
	names, dates := lead, lead
	for i := range 7 {
		d := start.AddDate(0, 0, i)
		style := st.muted
		if d.Equal(today) {
			style = lipgloss.NewStyle().Foreground(colorTask).Bold(true)
		}
		names += style.Render(fit(" "+d.Format("Mon")[:2], dayW))
		dates += style.Render(fit(" "+d.Format("02"), dayW))
	}
	lines = append(lines, names, dates)

	for i, r := range items {
		selected := i == u.cursor
		marker, textStyle := "  ", st.text
		if selected {
			marker, textStyle = lipgloss.NewStyle().Foreground(colorTask).Render("▌ "), lipgloss.NewStyle().Bold(true).Foreground(colorTask)
		}
		row := marker + lipgloss.NewStyle().Foreground(colorTask).Render(fit(clockText(r), timeW)) + "  " +
			textStyle.Render(fit(truncateRight(r.Text, textW), textW)) + "  "
		if listW > 0 {
			row += st.muted.Render(fit(truncateRight(r.List, listW), listW)) + "  "
		}
		done, due := 0, 0
		for j := range 7 {
			d := start.AddDate(0, 0, j)
			row += "  " + cell(a, r, d, now) + " "
			if t, ok := taskOn(a, r, d); ok && !d.After(today) {
				due++
				done += boolInt(t.Done)
			}
		}
		count := ""
		if due > 0 {
			count = fmt.Sprintf("%d/%d", done, due)
		}
		row += "  " + st.muted.Render(fit(count, countW))
		if everyW > 0 {
			row += "  " + st.muted.Render("every "+r.Every.String())
		}
		lines = append(lines, row)
	}
	legend := a.st.success.Render("✓") + st.muted.Render(" done   ") +
		lipgloss.NewStyle().Foreground(colorWarn).Bold(true).Render("!") + st.muted.Render(" missed or late   ") +
		lipgloss.NewStyle().Foreground(colorTask).Render("○") + st.muted.Render(" to do   · nothing that day")
	return append(lines, "", legend)
}

// view draws the routines screen across the whole terminal, with the
// add/edit form as a panel at the bottom.
func (u *routinesUI) view(a *app) string {
	st := a.st
	now := time.Now()
	w := cmp.Or(a.width, 100)
	u.cursor = max(min(u.cursor, len(u.items(a))-1), 0)

	var panel []string
	hk := helpKeys{keys.Down, keys.Up, keys.Check, keys.WeekPrev, keys.WeekNext, keys.ThisWeek,
		keys.AddTask, keys.EditTask, keys.DelTask, keys.Back, keys.Help}
	if a.screen == screenRoutineForm {
		panel, hk = a.tasks.formPanel(a, w)
	}
	help := ""
	if a.cfg.UI.ShowHelp {
		a.help.Width = w - 2
		help = lipgloss.NewStyle().PaddingLeft(1).Render(a.help.View(hk))
	}

	left := st.muted.Render(" " + shortHome(a.state.TasksDir))
	switch {
	case u.ask != "":
		left = lipgloss.NewStyle().Bold(true).Foreground(colorWarn).Render(" " + u.ask + "  y / n")
	case u.flash != "":
		left = lipgloss.NewStyle().Foreground(colorWarn).Render(" " + u.flash)
	case a.state.TaskErr != "":
		left = st.errorMsg.Render(" ✗ " + a.state.TaskErr)
	}
	open, done := 0, 0
	for _, t := range a.state.Todos {
		if t.Of != 0 && dayOf(t.Due).Equal(dayOf(now)) {
			open += boolInt(!t.Done)
			done += boolInt(t.Done)
		}
	}
	right := st.muted.Render(fmt.Sprintf("today %d of %d done", done, open+done))
	status := spread(left, right+" ", w)

	body := u.grid(a, now, w-4)
	bodyH := 0
	if a.height > 0 {
		bodyH = max(a.height-5-len(panel)-lipgloss.Height(help)*boolInt(help != ""), 3)
	}
	const head = 4 // title, gap, day names, dates stay put while rows scroll
	if bodyH > 0 && len(body) > bodyH && len(body) > head {
		body = append(body[:head:head], scrollLines(st, body[head:], u.cursor, bodyH-head)...)
	}
	rows := a.header(colorTask)
	rows = append(rows, "")
	for i := range max(len(body), bodyH) {
		line := ""
		if i < len(body) {
			line = "  " + body[i]
		}
		rows = append(rows, line)
	}
	rows = append(rows, st.muted.Render(strings.Repeat("─", w)), status)
	rows = append(rows, panel...)
	if help != "" {
		rows = append(rows, help)
	}
	return strings.Join(rows, "\n")
}
