package main

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
}

type taskForm struct {
	inputs   [3]textinput.Model // task, list, when
	focus    int
	err      string
	showList bool // list box shown (A, the all view, editing)
	edit     bool // editing orig instead of adding
	orig     todo
}

// listEditedMsg arrives when the editor opened with E exits.
type listEditedMsg struct{}

var formLabels = [3]string{"task", "list", "when"}

// lists is the tab row: all, then every list file.
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

// focusTask moves the cursor to a task, wherever sorting put it.
func (u *tasksUI) focusTask(a *app, id int) {
	for i, t := range u.visible(a) {
		if t.ID == id {
			u.cursor = i
		}
	}
}

func (u *tasksUI) update(a *app, msg tea.KeyMsg) tea.Cmd {
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
		a.screen = screenTaskAdd
		return u.form.openEdit(items[u.cursor])
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
	case key.Matches(msg, keys.DelTask) && n > 0:
		a.send(request{Op: "todo.remove", ID: items[u.cursor].ID})
	case key.Matches(msg, keys.ClearDone):
		a.send(request{Op: "todo.clear", List: u.list})
	case key.Matches(msg, keys.AlarmBack):
		a.toMenu()
	}
	return nil
}

// tabs renders the list names, scrolled so the selected one always fits.
func (u *tasksUI) tabs(a *app, width int) string {
	st := a.st
	lists := u.lists(a)
	tabs := make([]string, len(lists))
	sel := 0
	for i, l := range lists {
		name := cmp.Or(l, "all")
		tabs[i] = st.muted.Render(" " + name + " ")
		if l == u.list {
			tabs[i], sel = st.badge.Background(colorTask).Render(name), i
		}
	}
	lo, hi := sel, sel+1
	used := lipgloss.Width(tabs[sel]) + 4 // room for the ‹ › markers
	for grew := true; grew; {
		grew = false
		if hi < len(tabs) && used+lipgloss.Width(tabs[hi]) <= width {
			used += lipgloss.Width(tabs[hi])
			hi, grew = hi+1, true
		}
		if lo > 0 && used+lipgloss.Width(tabs[lo-1]) <= width {
			used += lipgloss.Width(tabs[lo-1])
			lo, grew = lo-1, true
		}
	}
	out := strings.Join(tabs[lo:hi], "")
	if lo > 0 {
		out = st.muted.Render("‹ ") + out
	}
	if hi < len(tabs) {
		out += st.muted.Render(" ›")
	}
	return out
}

func (u *tasksUI) row(a *app, t todo, selected, overdue bool) string {
	st := a.st
	now := time.Now()
	marker, box := "  ", lipgloss.NewStyle().Foreground(colorTask).Render("○")
	if selected {
		marker = st.selected.Render("▌ ")
	}
	if t.Done {
		box = st.success.Render("✓")
	}

	when := ""
	if t.Remind {
		when = t.Due.Format("15:04")
	}
	whenStyle := lipgloss.NewStyle().Foreground(colorTask)
	if t.Done {
		whenStyle = st.muted
	}
	if overdue {
		when = overdueWhen(t, now)
		whenStyle = lipgloss.NewStyle().Foreground(colorAlarm)
		box = whenStyle.Render("!")
	}

	suffix := ""
	if u.list == "" {
		suffix = "  " + t.List
	}
	room := taskWidth - 4 - lipgloss.Width(suffix)
	if when != "" {
		room -= lipgloss.Width(when) + 2
	}
	text := truncateRight(t.Text, max(room, 8))
	textStyle := st.text
	switch {
	case t.Done:
		textStyle = st.muted.Strikethrough(true)
	case overdue:
		textStyle = lipgloss.NewStyle().Foreground(colorAlarm).Bold(selected)
	case selected:
		textStyle = st.selected
	}

	out := marker + box + " "
	if when != "" {
		out += whenStyle.Render(when) + "  "
	}
	return out + textStyle.Render(text) + st.muted.Render(suffix)
}

func truncateRight(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	return string(r[:w-1]) + "…"
}

func (u *tasksUI) view(a *app) string {
	st := a.st
	now := time.Now()
	items := u.visible(a)
	u.cursor = max(min(u.cursor, len(items)-1), 0)

	header := lipgloss.NewStyle().Bold(true).Foreground(colorTask).Render("TASKS") + "  " + u.tabs(a, taskWidth-7)

	var lines []string
	cursorLine, i := 0, 0
	for gi, g := range groupTasks(a.state.Todos, u.list, now) {
		if gi > 0 {
			lines = append(lines, "")
		}
		title := st.muted.Bold(true).Render(g.title)
		if g.overdue {
			title = st.badge.Background(colorAlarm).Render(g.title)
		}
		lines = append(lines, title)
		for _, t := range g.items {
			if i == u.cursor {
				cursorLine = len(lines)
			}
			lines = append(lines, u.row(a, t, i == u.cursor, g.overdue))
			i++
		}
	}
	if i == 0 {
		lines = append(lines, st.muted.Render("nothing here · a to add a task"))
	}

	// Keep the cursor in view when the list is taller than the window.
	if limit := a.height - 16; a.height > 0 && len(lines) > max(limit, 6) {
		limit = max(limit, 6)
		start := min(max(cursorLine-limit/2, 0), len(lines)-limit)
		more := func(n int, arrow string) string {
			return st.muted.Render(fmt.Sprintf("%s %d more lines", arrow, n))
		}
		window := []string{more(start, "↑")}
		if start == 0 {
			window[0] = ""
		}
		window = append(window, lines[start:start+limit]...)
		if rest := len(lines) - start - limit; rest > 0 {
			window = append(window, more(rest, "↓"))
		}
		lines = window
	}

	footer := st.muted.Render(shortHome(a.state.TasksDir))
	if a.state.TaskErr != "" {
		footer = st.errorMsg.Render("✗ " + a.state.TaskErr)
	}
	rows := append([]string{header, ""}, lines...)
	rows = append(rows, "", footer)
	body := lipgloss.NewStyle().Width(taskWidth).Render(strings.Join(rows, "\n"))
	hk := helpKeys{keys.Down, keys.Up, keys.Check, keys.AddTask, keys.AddToList, keys.EditTask, keys.OpenFile, keys.DelTask, keys.ListNext, keys.ListPrev, keys.ClearDone, keys.AlarmBack, keys.Help}
	return a.frame(body, colorTask, hk)
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
	*f = taskForm{showList: showList}
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
	f.inputs[2].Placeholder = "today · 13:00 · tomorrow-9am · 9/10/2029-13:00"
	return f.inputs[0].Focus()
}

// openEdit fills the form with an existing task.
func (f *taskForm) openEdit(t todo) tea.Cmd {
	cmd := f.open(t.List, true)
	f.edit, f.orig = true, t
	f.inputs[0].SetValue(t.Text)
	f.inputs[2].SetValue(whenString(t, time.Now()))
	f.inputs[0].CursorEnd()
	return cmd
}

// whenString writes a task's time back in the form parseWhen reads.
func whenString(t todo, now time.Time) string {
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

// when reads the time box. An unchanged time on an edited task is kept as
// is, even when it has passed, so overdue tasks can be reworded.
func (f *taskForm) when() (time.Time, bool, error) {
	v := f.inputs[2].Value()
	if f.edit && strings.TrimSpace(v) == whenString(f.orig, time.Now()) {
		return f.orig.Due, f.orig.Remind, nil
	}
	return parseWhen(v, time.Now())
}

func (f *taskForm) setFocus(i int) tea.Cmd {
	f.inputs[f.focus].Blur()
	step := 1
	if i < f.focus {
		step = -1
	}
	f.focus = (i + len(f.inputs)) % len(f.inputs)
	if f.focus == 1 && !f.showList {
		f.focus = (f.focus + step + len(f.inputs)) % len(f.inputs)
	}
	return f.inputs[f.focus].Focus()
}

func (u *tasksUI) updateForm(a *app, msg tea.KeyMsg) tea.Cmd {
	f := &u.form
	switch {
	case key.Matches(msg, keys.Leave):
		a.screen = screenTasks
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
		due, remind, err := f.when()
		if err != nil {
			f.err = err.Error()
			return f.setFocus(2)
		}
		list := cleanList(f.inputs[1].Value())
		req := request{Op: "todo.add", Text: text, List: list, At: due, Remind: remind}
		if f.edit {
			req.Op, req.ID = "todo.edit", f.orig.ID
		}
		a.send(req)
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
		a.screen = screenTasks
	default:
		f.err = ""
		var cmd tea.Cmd
		f.inputs[f.focus], cmd = f.inputs[f.focus].Update(msg)
		return cmd
	}
	return nil
}

func (u *tasksUI) viewForm(a *app) string {
	st := a.st
	f := &u.form
	on := lipgloss.NewStyle().Bold(true).Foreground(colorTask)
	title, action := "NEW TASK", "add"
	if f.edit {
		title, action = "EDIT TASK", "save"
	}
	list := cleanList(f.inputs[1].Value())
	if !f.showList {
		title += st.muted.Render("  ·  " + list)
	}
	rows := []string{on.Render(title), ""}
	for i, in := range f.inputs {
		if i == 1 && !f.showList {
			continue
		}
		label := st.muted.Render(fmt.Sprintf("%-6s", formLabels[i]))
		if i == f.focus {
			label = on.Render(fmt.Sprintf("%-6s", formLabels[i]))
		}
		rows = append(rows, label+"  "+in.View())
	}

	var status string
	if due, remind, err := f.when(); err != nil {
		status = st.errorMsg.Render("✗ " + err.Error())
	} else {
		kind := "task"
		if remind {
			kind = "reminder"
		}
		where := list
		if strings.TrimSpace(f.inputs[1].Value()) == "" {
			where = "?"
		} else if !contains(a.state.Lists, list) {
			where += " (new list)"
		}
		status = st.muted.Render(fmt.Sprintf("→ %s in %s · %s", kind, where, describeWhen(todo{Due: due, Remind: remind}, time.Now())))
	}
	if f.err != "" {
		status = st.errorMsg.Render("✗ " + f.err)
	}
	rows = append(rows, "", status)
	body := lipgloss.NewStyle().Width(taskWidth).Render(strings.Join(rows, "\n"))
	hk := helpKeys{bind([]string{"enter"}, "enter", action), keys.NextField, keys.PrevField, bind([]string{"esc"}, "esc", "cancel")}
	return a.frame(body, colorTask, hk)
}

const taskUsage = `usage:
  gg task [list]                          open the task lists
  gg task add [list]                      write tasks in $EDITOR
  gg task add list "text" [--time WHEN]   add one task
  gg task ls [list]`

// taskCommand runs gg task ... from the shell.
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
		due, remind, err := parseWhen(when, time.Now())
		if err != nil {
			return err
		}
		list = cleanList(list)
		if _, err := call(request{Op: "todo.add", Text: text, List: list, At: due, Remind: remind}); err != nil {
			return err
		}
		kind := "task"
		if remind {
			kind = "reminder"
		}
		fmt.Fprintf(out, "added %s to %s · %s · %s\n", kind, list, describeWhen(todo{Due: due, Remind: remind}, time.Now()), text)
		return nil

	case "ls", "list":
		list := ""
		if len(args) > 1 {
			list = cleanList(args[1])
		}
		s, err := call(request{Op: "get"})
		if err != nil {
			return err
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
				line := "  " + box + " "
				switch {
				case g.overdue:
					line += overdueWhen(t, time.Now()) + "  "
				case t.Remind:
					line += t.Due.Format("15:04") + " "
				}
				line += t.Text
				if list == "" {
					line += "  (" + t.List + ")"
				}
				fmt.Fprintln(out, line)
			}
		}
		return nil
	}
	return errors.New(taskUsage)
}
