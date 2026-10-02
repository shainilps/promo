package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// clockPicker edits two numbers (hours and minutes) vim-style: h/l pick the
// field, j/k step it, or digits are typed straight in ("0730").
type clockPicker struct {
	vals  [2]int
	field int // 0 = hours, 1 = minutes
	typed int // digits typed into the current field
}

var pickerLimits = [2]int{24, 60}

func newPicker(hours, minutes int) clockPicker {
	return clockPicker{vals: [2]int{hours, minutes}}
}

func (p clockPicker) hours() int   { return p.vals[0] }
func (p clockPicker) minutes() int { return p.vals[1] }

func (p clockPicker) duration() time.Duration {
	return time.Duration(p.vals[0])*time.Hour + time.Duration(p.vals[1])*time.Minute
}

// handle applies a picker key and reports whether it was one.
func (p *clockPicker) handle(msg tea.KeyMsg) bool {
	// Fast typing or a paste can deliver several digits in one message.
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 && strings.Trim(string(msg.Runes), "0123456789") == "" {
		for _, r := range msg.Runes {
			p.typeDigit(int(r - '0'))
		}
		return true
	}
	s := msg.String()
	p.typed = 0
	switch {
	case s == "tab":
		p.field ^= 1
	case key.Matches(msg, keys.FieldLeft):
		p.field = 0
	case key.Matches(msg, keys.FieldRight):
		p.field = 1
	case key.Matches(msg, keys.Inc):
		p.step(1)
	case key.Matches(msg, keys.Dec):
		p.step(-1)
	case key.Matches(msg, keys.Inc10):
		p.step(10)
	case key.Matches(msg, keys.Dec10):
		p.step(-10)
	default:
		return false
	}
	return true
}

func (p *clockPicker) step(n int) {
	l := pickerLimits[p.field]
	p.vals[p.field] = ((p.vals[p.field]+n)%l + l) % l
}

func (p *clockPicker) typeDigit(d int) {
	l, cur := pickerLimits[p.field], &p.vals[p.field]
	if p.typed == 0 {
		*cur = d
		p.typed = 1
		// A first digit that can't start a two-digit value completes the field.
		if d*10 >= l {
			p.typed, p.field = 0, 1
		}
		return
	}
	if v := *cur*10 + d; v < l {
		*cur = v
	}
	p.typed, p.field = 0, 1
}

// view renders HH:MM with the active field highlighted and underlined.
func (p clockPicker) view(a *app, color lipgloss.TerminalColor, labels [2]string) string {
	on := lipgloss.NewStyle().Bold(true).Foreground(color)
	off := lipgloss.NewStyle().Foreground(colorMuted)
	styles := [2]lipgloss.Style{off, off}
	styles[p.field] = on
	hh, mm := fmt.Sprintf("%02d", p.vals[0]), fmt.Sprintf("%02d", p.vals[1])

	if !a.cfg.UI.BigClock || (a.width > 0 && bigTextWidth("00:00")+14 > a.width) {
		return styles[0].Underline(p.field == 0).Render(hh) + off.Render(":") + styles[1].Underline(p.field == 1).Render(mm) +
			"\n" + off.Render(labels[0]+" : "+labels[1])
	}
	w := bigTextWidth("00")
	gap := strings.Repeat(" ", 4+bigTextWidth(":"))
	under := func(i int) string {
		if i == p.field {
			return on.Render(strings.Repeat("▔", w))
		}
		return strings.Repeat(" ", w)
	}
	label := func(i int) string {
		return styles[i].Width(w).Align(lipgloss.Center).Render(labels[i])
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.JoinHorizontal(lipgloss.Top,
			styles[0].Render(bigText(hh)), "  ", off.Render(bigText(":")), "  ", styles[1].Render(bigText(mm))),
		under(0)+gap+under(1),
		label(0)+gap+label(1),
	)
}

var pickerHelp = helpKeys{keys.FieldLeft, keys.FieldRight, keys.Inc, keys.Dec, keys.Inc10, keys.Dec10}

// lengthEditor is a full-screen picker for changing a timer or phase length.
type lengthEditor struct {
	active bool
	title  string
	note   string
	color  lipgloss.TerminalColor
	picker clockPicker
	err    string
	apply  func(time.Duration)
}

func (a *app) editLength(title, note string, color lipgloss.TerminalColor, current time.Duration, apply func(time.Duration)) {
	mins := int(current.Round(time.Minute) / time.Minute)
	a.editor = lengthEditor{
		active: true, title: title, note: note, color: color, apply: apply,
		picker: newPicker(min(mins/60, 23), mins%60),
	}
	a.editor.picker.field = 1 // most lengths are minutes
	if mins >= 60 {
		a.editor.picker.field = 0
	}
}

func (e *lengthEditor) update(msg tea.KeyMsg) tea.Cmd {
	switch {
	case key.Matches(msg, keys.Leave, keys.AlarmBack):
		e.active = false
	case key.Matches(msg, keys.Commit):
		d := e.picker.duration()
		if d < time.Minute {
			e.err = "must be at least 1 minute"
			return nil
		}
		e.active = false
		e.apply(d)
	default:
		if e.picker.handle(msg) {
			e.err = ""
		}
	}
	return nil
}

func (e *lengthEditor) view(a *app) string {
	st := a.st
	status := st.muted.Render("type digits · h/l hours/min · j/k adjust")
	if e.err != "" {
		status = st.errorMsg.Render(e.err)
	}
	rows := []string{
		lipgloss.NewStyle().Bold(true).Foreground(e.color).Render(strings.ToUpper(e.title)),
		"",
		e.picker.view(a, e.color, [2]string{"hours", "min"}),
		"",
		st.bold.Render("= " + shortDuration(e.picker.duration())),
	}
	if e.note != "" {
		rows = append(rows, st.muted.Render(e.note))
	}
	rows = append(rows, "", status)
	hk := append(helpKeys{keys.Commit, bind([]string{"esc"}, "esc", "cancel")}, pickerHelp...)
	return a.frame(lipgloss.JoinVertical(lipgloss.Center, rows...), e.color, hk)
}
