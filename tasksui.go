package main

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const taskWidth = 60

// tasksUI is a session's view of the task lists.
type tasksUI struct {
	list   string // selected list, "" = all of them
	cursor int    // index into visible()
	form   taskForm
	ask    string // y/n question in the status bar
	onYes  func() // what y does for ask
	flash  string // one-off note in the status bar
}

type taskForm struct {
	inputs   [3]textinput.Model // task, list, when
	focus    int                // an input, or fieldRepeat
	days     repeat             // the repeat row; none = a one-off task
	day      int                // cursor in the repeat row, 0 = Monday
	err      string
	showList bool // list box shown (A, the all view, editing)
	edit     bool // editing orig instead of adding
	orig     todo
	back     screen // where the form returns to
}

const fieldRepeat = 3

// listEditedMsg arrives when the editor opened with E exits.
type listEditedMsg struct{}

var formLabels = [4]string{"task", "list", "when", "repeat"}

// lists is the sidebar: all, then every list file.
func (u *tasksUI) lists(a *app) []string {
	return append([]string{""}, a.state.Lists...)
}

func (u *tasksUI) visible(a *app) []todo {
	if u.list != "" && !contains(a.state.Lists, u.list) {
		u.list = ""
	}
	var items []todo
	for _, g := range groupTasks(a.state.Todos, u.list, time.Now()) {
		items = append(items, g.items...)
	}
	return items
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

// routine is the routine behind t: t itself, or the one today's task
// came from. Editing or deleting either acts on the routine.
func routine(a *app, t todo) (todo, bool) {
	if t.Every != 0 {
		return t, true
	}
	for _, r := range a.state.Todos {
		if t.Of != 0 && r.ID == t.Of {
			return r, true
		}
	}
	return t, false
}

// focusTask moves the cursor to a task, wherever sorting put it.
func (u *tasksUI) focusTask(a *app, id int) {
	for i, t := range u.visible(a) {
		if t.ID == id {
			u.cursor = i
		}
	}
}

func (u *tasksUI) update(a *app, msg tea.KeyMsg) tea.Cmd {
	if yes := u.onYes; yes != nil {
		u.ask, u.onYes = "", nil
		if msg.String() == "y" {
			yes()
		}
		return nil
	}
	u.flash = ""
	items := u.visible(a)
	n := len(items)
	u.cursor = max(min(u.cursor, n-1), 0)
	switch {
	case key.Matches(msg, keys.Up):
		u.cursor = max(u.cursor-1, 0)
	case key.Matches(msg, keys.Down):
		u.cursor = max(min(u.cursor+1, n-1), 0)
	case key.Matches(msg, keys.Top):
		u.cursor = 0
	case key.Matches(msg, keys.Bottom):
		u.cursor = max(n-1, 0)
	case key.Matches(msg, keys.ListNext, keys.ListPrev):
		lists := u.lists(a)
		step := 1
		if key.Matches(msg, keys.ListPrev) {
			step = -1
		}
		i := 0
		for j, l := range lists {
			if l == u.list {
				i = j
			}
		}
		u.list, u.cursor = lists[(i+step+len(lists))%len(lists)], 0
	case key.Matches(msg, keys.Check) && n > 0 && items[u.cursor].Every != 0:
		// On a routine, space checks off its task for today.
		r := items[u.cursor]
		if t, ok := taskOn(a, r, dayOf(time.Now())); ok {
			a.send(request{Op: "todo.toggle", ID: t.ID})
		} else {
			u.flash = r.Text + " has nothing to check today"
		}
	case key.Matches(msg, keys.Check) && n > 0:
		id := items[u.cursor].ID
		a.send(request{Op: "todo.toggle", ID: id})
		u.focusTask(a, id)
	case key.Matches(msg, keys.AddTask):
		a.screen = screenTaskAdd
		return u.form.open(cmp.Or(u.list, defaultList), u.list == "")
	case key.Matches(msg, keys.AddToList):
		a.screen = screenTaskAdd
		return u.form.open("", true)
	case key.Matches(msg, keys.EditTask) && n > 0:
		t, _ := routine(a, items[u.cursor])
		a.screen = screenTaskAdd
		return u.form.openEdit(t)
	case key.Matches(msg, keys.OpenFile):
		list := u.list
		if list == "" && n > 0 {
			list = items[u.cursor].List
		}
		if list == "" {
			return nil
		}
		path := filepath.Join(a.state.TasksDir, list+".md")
		return tea.ExecProcess(editorCmd(path), func(error) tea.Msg { return listEditedMsg{} })
	case key.Matches(msg, keys.DelTaskNow) && n > 0 && items[u.cursor].Every == 0 && items[u.cursor].Of == 0:
		a.send(request{Op: "todo.remove", ID: items[u.cursor].ID})
	case key.Matches(msg, keys.DelTask, keys.DelTaskNow) && n > 0:
		// A routine always asks: X on today's check out shouldn't quietly
		// drop every check out after it.
		t, isRoutine := routine(a, items[u.cursor])
		u.ask = fmt.Sprintf("delete %q?", truncateRight(t.Text, 40))
		if isRoutine {
			u.ask = fmt.Sprintf("delete %q? it repeats %s", truncateRight(t.Text, 30), describeWhen(t, time.Now()))
		}
		u.onYes = func() { a.send(request{Op: "todo.remove", ID: t.ID}) }
	case key.Matches(msg, keys.DelList):
		list := u.list
		if list == "" {
			u.flash = "select a list first (h/l), then D deletes it"
			break
		}
		n := 0
		for _, t := range a.state.Todos {
			n += boolInt(t.List == list)
		}
		what := fmt.Sprintf("its %d tasks", n)
		if n == 1 {
			what = "its 1 task"
		}
		u.ask = fmt.Sprintf("delete list %s and %s?", list, what)
		u.onYes = func() {
			a.send(request{Op: "list.remove", List: list})
			u.list, u.cursor = "", 0
			u.flash = "deleted list " + list
		}
	case key.Matches(msg, keys.ClearDone):
		a.send(request{Op: "todo.clear", List: u.list})
	case key.Matches(msg, keys.Back):
		return tea.Quit
	}
	return nil
}

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

func (u *tasksUI) whenText(t todo, overdue bool, now time.Time) string {
	if overdue {
		return overdueWhen(t, now)
	}
	if t.Remind {
		return t.Due.Format("15:04")
	}
	return ""
}

// sidebar lists every list with its open and overdue counts.
func (u *tasksUI) sidebar(a *app, now time.Time, w int) []string {
	st := a.st
	lines := []string{st.muted.Bold(true).Render(" LISTS"), ""}
	for _, l := range u.lists(a) {
		open, late := 0, 0
		for _, t := range a.state.Todos {
			if l != "" && t.List != l {
				continue
			}
			if !t.Done && t.Every == 0 {
				open++
			}
			if isOverdue(t, now) {
				late++
			}
		}
		counts := st.muted.Render(fmt.Sprint(open))
		if late > 0 {
			counts = lipgloss.NewStyle().Foreground(colorWarn).Render(fmt.Sprintf("%d!", late)) + " " + counts
		}
		marker, name := "  ", cmp.Or(l, "all")
		name = truncateRight(name, max(w-4-lipgloss.Width(counts), 4))
		if l == u.list {
			marker, name = lipgloss.NewStyle().Foreground(colorTask).Render("▌ "), lipgloss.NewStyle().Bold(true).Foreground(colorTask).Render(name)
		} else {
			name = st.text.Render(name)
		}
		lines = append(lines, spread(marker+name, counts+" ", w))
	}
	return lines
}

// mainLines draws the tasks by day, scrolled to keep the cursor in a
// height-line window (0 = no limit).
func (u *tasksUI) mainLines(a *app, now time.Time, w, height int) []string {
	st := a.st
	groups := groupTasks(a.state.Todos, u.list, now)

	// Column widths, so times and the right-hand notes line up.
	timeW, listW, relW := 0, 0, 0
	for _, g := range groups {
		for _, t := range g.items {
			timeW = max(timeW, lipgloss.Width(u.whenText(t, g.overdue, now)))
			rel, _ := relative(t, g.overdue, now)
			relW = max(relW, lipgloss.Width(rel))
			if u.list == "" {
				listW = max(listW, lipgloss.Width(t.List))
			}
		}
	}
	// The task text gets at least 24 cells; on a narrow terminal the
	// "in 2h" column goes first, then the list column.
	timeCol := 0
	if timeW > 0 {
		timeCol = timeW + 2
	}
	rightW := func() int {
		r := relW
		if listW > 0 {
			r += listW + 3
		}
		return r
	}
	if w-4-timeCol-2-rightW() < 24 {
		relW = 0
	}
	if w-4-timeCol-2-rightW() < 24 {
		listW = 0
	}
	textW := max(w-4-timeCol-2-rightW(), 10)

	var lines []string
	cursorLine, i := 0, 0
	for gi, g := range groups {
		if gi > 0 {
			lines = append(lines, "")
		}
		title := st.muted.Bold(true).Render(g.title)
		if g.overdue {
			title = st.badge.Background(colorWarn).Render(g.title)
		}
		lines = append(lines, title)
		for _, t := range g.items {
			if i == u.cursor {
				cursorLine = len(lines)
			}
			lines = append(lines, u.row(a, t, i == u.cursor, g.overdue, now, timeW, textW, listW, relW))
			i++
		}
	}
	if i == 0 {
		lines = append(lines, st.muted.Render("nothing here · a to add a task"))
	}

	return scrollLines(st, lines, cursorLine, height)
}

// scrollLines keeps cursorLine in view when lines are taller than height
// (0 = no limit), marking what is cut off above and below.
func scrollLines(st styles, lines []string, cursorLine, height int) []string {
	if height <= 0 || len(lines) <= height {
		return lines
	}
	height = max(height, 3)
	start := min(max(cursorLine-height/2, 0), len(lines)-height)
	window := slices.Clone(lines[start : start+height])
	if start > 0 {
		window[0] = st.muted.Render(fmt.Sprintf("↑ %d more", start+1))
	}
	if rest := len(lines) - start - height; rest > 0 {
		window[len(window)-1] = st.muted.Render(fmt.Sprintf("↓ %d more", rest+1))
	}
	return window
}

func (u *tasksUI) row(a *app, t todo, selected, overdue bool, now time.Time, timeW, textW, listW, relW int) string {
	st := a.st
	late := lipgloss.NewStyle().Foreground(colorWarn)
	marker, box := "  ", lipgloss.NewStyle().Foreground(colorTask).Render("○")
	if selected {
		marker = lipgloss.NewStyle().Foreground(colorTask).Render("▌ ")
	}
	whenStyle, textStyle := lipgloss.NewStyle().Foreground(colorTask), st.text
	switch {
	case t.Every != 0:
		box = lipgloss.NewStyle().Foreground(colorTask).Render("↻")
	case t.Done:
		box, whenStyle, textStyle = st.success.Render("✓"), st.muted, st.muted.Strikethrough(true)
	case overdue:
		box, whenStyle, textStyle = late.Render("!"), late, late
	}
	if selected && !t.Done {
		textStyle = textStyle.Bold(true)
		if !overdue {
			textStyle = textStyle.Foreground(colorTask)
		}
	}

	out := marker + box + " "
	if timeW > 0 {
		out += whenStyle.Render(fit(u.whenText(t, overdue, now), timeW)) + "  "
	}
	out += textStyle.Render(fit(truncateRight(t.Text, textW), textW)) + "  "
	if listW > 0 {
		out += st.muted.Render(fit(t.List, listW)) + "   "
	}
	rel, isLate := relative(t, overdue, now)
	relStyle := st.muted
	if isLate {
		relStyle = late
	}
	return out + relStyle.Render(fit(rel, relW))
}

// view draws the task screen across the whole terminal: lists on the left,
// tasks by day on the right, and the add/edit form as a panel at the bottom.
func (u *tasksUI) view(a *app) string {
	st := a.st
	now := time.Now()
	w := cmp.Or(a.width, 100)
	items := u.visible(a)
	u.cursor = max(min(u.cursor, len(items)-1), 0)
	rule := st.muted.Render(strings.Repeat("─", w))

	var panel []string
	hk := helpKeys{keys.Down, keys.Up, keys.Check, keys.AddTask, keys.AddToList, keys.EditTask, keys.OpenFile,
		keys.DelTask, keys.DelTaskNow, keys.ListNext, keys.ListPrev, keys.ClearDone, keys.DelList, keys.Back, keys.Help}
	if a.screen == screenTaskAdd {
		panel, hk = u.formPanel(a, w)
	}
	help := ""
	if a.cfg.UI.ShowHelp {
		a.help.Width = w - 2
		help = lipgloss.NewStyle().PaddingLeft(1).Render(a.help.View(hk))
	}

	// Status bar: a pending question, a note, an error, or where the files are.
	left := st.muted.Render(" " + shortHome(a.state.TasksDir))
	switch {
	case u.ask != "":
		left = lipgloss.NewStyle().Bold(true).Foreground(colorWarn).Render(" " + u.ask + "  y / n")
	case u.flash != "":
		left = lipgloss.NewStyle().Foreground(colorWarn).Render(" " + u.flash)
	case a.state.TaskErr != "":
		left = st.errorMsg.Render(" ✗ " + a.state.TaskErr)
	}
	open, late := 0, 0
	for _, t := range a.state.Todos {
		if u.list == "" || t.List == u.list {
			open += boolInt(!t.Done && t.Every == 0)
			late += boolInt(isOverdue(t, now))
		}
	}
	right := st.muted.Render(fmt.Sprintf("%d open", open))
	if late > 0 {
		right += st.muted.Render(" · ") + lipgloss.NewStyle().Foreground(colorWarn).Render(fmt.Sprintf("%d overdue", late))
	}
	status := spread(left, right+" ", w)

	bodyH := 0
	if a.height > 0 {
		bodyH = max(a.height-4-len(panel)-lipgloss.Height(help)*boolInt(help != ""), 3)
	}
	sideW := 16
	for _, l := range a.state.Lists {
		sideW = max(sideW, lipgloss.Width(l)+10)
	}
	sideW = min(sideW, 28, w/3)
	if w < 100 {
		sideW = min(sideW, max(w/4, 14))
	}
	side := u.sidebar(a, now, sideW)
	main := u.mainLines(a, now, w-sideW-3, bodyH)
	n := max(len(side), len(main))
	if bodyH > 0 {
		n = bodyH
	}
	sep := st.muted.Render(" │ ")
	rows := a.header(colorTask)
	for i := range n {
		l, r := "", ""
		if i < len(side) {
			l = side[i]
		}
		if i < len(main) {
			r = main[i]
		}
		rows = append(rows, fit(l, sideW)+sep+r)
	}
	rows = append(rows, rule, status)
	rows = append(rows, panel...)
	if help != "" {
		rows = append(rows, help)
	}
	return strings.Join(rows, "\n")
}

func shortHome(p string) string {
	if home := expandHome("~/"); strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// open readies the form. With showList the list box is shown so the task
// can go to another (or a new) list; otherwise it goes to list.
func (f *taskForm) open(list string, showList bool) tea.Cmd {
	*f = taskForm{showList: showList, back: screenTasks}
	for i := range f.inputs {
		in := textinput.New()
		in.Prompt = ""
		in.Width = taskWidth - 10
		in.CharLimit = 300
		in.Cursor.SetMode(cursor.CursorStatic)
		f.inputs[i] = in
	}
	f.inputs[0].Placeholder = "what needs doing"
	f.inputs[1].Placeholder = "new or existing list"
	f.inputs[1].SetValue(list)
	return f.inputs[0].Focus()
}

// openEdit fills the form with an existing task. A routine's days go in
// the repeat row and its time in the when box.
func (f *taskForm) openEdit(t todo) tea.Cmd {
	cmd := f.open(t.List, true)
	f.edit, f.orig = true, t
	f.inputs[0].SetValue(t.Text)
	f.inputs[2].SetValue(whenString(t, time.Now()))
	if t.Every != 0 {
		f.days = t.Every
		f.inputs[2].SetValue(clockText(t))
	}
	f.inputs[0].CursorEnd()
	return cmd
}

// whenString writes a task's time back in the form parseWhen reads.
func whenString(t todo, now time.Time) string {
	if t.Every != 0 {
		return strings.TrimSuffix(t.Every.flag()+"-"+clockText(t), "-")
	}
	day, today := dayOf(t.Due), dayOf(now)
	s := day.Format("2/1/2006")
	switch {
	case day.Equal(today):
		s = "today"
	case day.Equal(today.AddDate(0, 0, 1)):
		s = "tomorrow"
	}
	if t.Remind {
		s += "-" + t.Due.Format("15:04")
	}
	return s
}

// when reads the time box and the repeat row into a task's Due, Remind
// and Every. With days picked, the box takes just a time (or nothing). An
// unchanged time on an edited task is kept as is, even when it has passed,
// so overdue tasks can be reworded.
func (f *taskForm) when() (todo, error) {
	v := strings.TrimSpace(f.inputs[2].Value())
	now := time.Now()
	if f.days != 0 {
		if v == "" {
			return todo{Due: clockOf(0, 0), Every: f.days}, nil
		}
		h, m, err := parseClock(v)
		if err != nil {
			return todo{}, errors.New("with repeat days picked, when is just a time, like 18:00 or 6pm (or empty)")
		}
		return todo{Due: clockOf(h, m), Remind: true, Every: f.days}, nil
	}
	if f.edit && f.orig.Every == 0 && v == whenString(f.orig, now) {
		return todo{Due: f.orig.Due, Remind: f.orig.Remind}, nil
	}
	return parseWhen(v, now)
}

func (f *taskForm) setFocus(i int) tea.Cmd {
	if f.focus < len(f.inputs) {
		f.inputs[f.focus].Blur()
	}
	step := 1
	if i < f.focus {
		step = -1
	}
	n := len(formLabels)
	f.focus = (i + n) % n
	if f.focus == 1 && !f.showList {
		f.focus = (f.focus + step + n) % n
	}
	if f.focus == fieldRepeat {
		// "weekdays 6pm" typed in the when box moves into the row.
		if t, err := parseWhen(f.inputs[2].Value(), time.Now()); f.days == 0 && err == nil && t.Every != 0 {
			f.days = t.Every
			f.inputs[2].SetValue(clockText(t))
		}
		return nil
	}
	return f.inputs[f.focus].Focus()
}

// updateRepeat handles keys on the repeat row.
func (f *taskForm) updateRepeat(msg tea.KeyMsg) {
	switch msg.String() {
	case "h", "left":
		f.day = (f.day + 6) % 7
	case "l", "right":
		f.day = (f.day + 1) % 7
	case " ", "x":
		f.days ^= 1 << monFirst(f.day).Weekday()
	case "d":
		f.days = everyDay
	case "w":
		f.days = weekdays
	case "e":
		f.days = weekends
	case "0", "backspace", "delete":
		f.days = 0
	}
}

func (u *tasksUI) updateForm(a *app, msg tea.KeyMsg) tea.Cmd {
	f := &u.form
	switch {
	case key.Matches(msg, keys.Leave):
		a.screen = f.back
	case key.Matches(msg, keys.NextField):
		return f.setFocus(f.focus + 1)
	case key.Matches(msg, keys.PrevField):
		return f.setFocus(f.focus - 1)
	case key.Matches(msg, keys.Commit):
		text := strings.TrimSpace(f.inputs[0].Value())
		if text == "" {
			f.err = "the task needs some text"
			return f.setFocus(0)
		}
		if strings.TrimSpace(f.inputs[1].Value()) == "" {
			f.err = "which list? type a name (a new one is created)"
			return f.setFocus(1)
		}
		w, err := f.when()
		if err != nil {
			f.err = err.Error()
			return f.setFocus(2)
		}
		list := cleanList(f.inputs[1].Value())
		req := request{Op: "todo.add", Text: text, List: list, At: w.Due, Remind: w.Remind, Every: w.Every}
		if f.edit {
			req.Op, req.ID = "todo.edit", f.orig.ID
		}
		a.send(req)
		a.screen = f.back
		if f.back == screenRoutines {
			a.routines.focus(a, list, strings.Join(strings.Fields(text), " "))
			return nil
		}
		if u.list != "" {
			u.list = list
		}
		id := req.ID
		if !f.edit {
			for _, t := range a.state.Todos {
				id = max(id, t.ID)
			}
		}
		u.focusTask(a, id)
	case f.focus == fieldRepeat:
		f.err = ""
		f.updateRepeat(msg)
	default:
		f.err = ""
		var cmd tea.Cmd
		f.inputs[f.focus], cmd = f.inputs[f.focus].Update(msg)
		return cmd
	}
	return nil
}

// repeatRow draws the days as toggles: picked days filled in, the cursor
// underlined while the row has focus.
func (f *taskForm) repeatRow(st styles) string {
	focused := f.focus == fieldRepeat
	var cells []string
	for i := range 7 {
		wd := monFirst(i).Weekday()
		style := st.muted
		if f.days&(1<<wd) != 0 {
			style = lipgloss.NewStyle().Foreground(colorBase).Background(colorTask).Bold(true)
		}
		name := " " + wd.String()[:2] + " "
		if focused && i == f.day {
			name = " " + lipgloss.NewStyle().Underline(true).Render(wd.String()[:2]) + " "
			if f.days&(1<<wd) == 0 {
				style = lipgloss.NewStyle().Foreground(colorTask).Bold(true)
			}
		}
		cells = append(cells, style.Render(name))
	}
	row := strings.Join(cells, " ")
	switch {
	case focused:
		row += st.muted.Render("   h/l move · space pick · w weekdays · d daily · e weekends · 0 none")
	case f.days == 0:
		row += st.muted.Render("   not repeating · tab here to pick days")
	}
	return row
}

// formPanel draws the add/edit form as lines for the bottom of the task screen.
func (u *tasksUI) formPanel(a *app, w int) ([]string, helpKeys) {
	st := a.st
	f := &u.form
	on := lipgloss.NewStyle().Bold(true).Foreground(colorTask)
	title, action := "NEW TASK", "add"
	if f.edit {
		title, action = "EDIT TASK", "save"
		if f.orig.Every != 0 {
			title = "EDIT ROUTINE"
		}
	}
	list := cleanList(f.inputs[1].Value())
	if !f.showList {
		title += st.muted.Render("  ·  " + list)
	}
	f.inputs[2].Placeholder = "today · 13:00 · tomorrow 9am · 9/10/2029 · weekdays 6pm"
	if f.days != 0 {
		f.inputs[2].Placeholder = "a time, like 18:00 or 6pm (empty = no reminder)"
	}
	rows := []string{st.muted.Render(strings.Repeat("─", w)), " " + on.Render(title)}
	for i := range formLabels {
		if i == 1 && !f.showList {
			continue
		}
		label := st.muted.Render(fmt.Sprintf("%-6s", formLabels[i]))
		if i == f.focus {
			label = on.Render(fmt.Sprintf("%-6s", formLabels[i]))
		}
		if i == fieldRepeat {
			rows = append(rows, fit(" "+label+"  "+f.repeatRow(st), w))
			continue
		}
		f.inputs[i].Width = max(w-12, 10)
		rows = append(rows, " "+label+"  "+f.inputs[i].View())
	}

	var status string
	if w, err := f.when(); err != nil {
		status = st.errorMsg.Render("✗ " + err.Error())
	} else {
		where := list
		if strings.TrimSpace(f.inputs[1].Value()) == "" {
			where = "?"
		} else if !contains(a.state.Lists, list) {
			where += " (new list)"
		}
		status = st.muted.Render(fmt.Sprintf("→ %s in %s · %s", w.kind(), where, describeWhen(w, time.Now())))
	}
	if f.err != "" {
		status = st.errorMsg.Render("✗ " + f.err)
	}
	rows = append(rows, " "+status)
	hk := helpKeys{bind([]string{"enter"}, "enter", action), keys.NextField, keys.PrevField, bind([]string{"esc"}, "esc", "cancel")}
	return rows, hk
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
