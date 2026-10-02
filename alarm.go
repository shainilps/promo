package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// alarm is one entry in the daemon's alarm list.
type alarm struct {
	ID      int       `json:"id"`
	SetAt   time.Time `json:"set_at"`
	Target  time.Time `json:"target"`
	Ringing bool      `json:"ringing"`
}

// alarmUI is a session's view of the alarm list.
type alarmUI struct {
	picker clockPicker
	cursor int // position in the list screen
	sel    int // id of the alarm shown on screenAlarm
}

// parseClock accepts 07:30, 7:30, 19:05, 7pm, 7:30pm and 7:30 PM.
func parseClock(s string) (hour, minute int, err error) {
	s = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	for _, layout := range []string{"15:04", "3:04pm", "3pm", "15"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Hour(), t.Minute(), nil
		}
	}
	return 0, 0, fmt.Errorf("invalid time %q (try 07:30 or 7:30pm)", s)
}

// nextOccurrence returns the next time the clock shows hour:minute.
func nextOccurrence(hour, minute int) time.Time {
	now := time.Now()
	t := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

func dayWord(t time.Time) string {
	if t.Day() != time.Now().Day() {
		return "tomorrow"
	}
	return "today"
}

func (u *alarmUI) prepareSetter() {
	t := time.Now().Add(time.Hour)
	u.picker = newPicker(t.Hour(), t.Minute())
}

// open is the menu entry: a ringing alarm first, then the list, or the
// setter when there are no alarms yet.
func (u *alarmUI) open(a *app) {
	switch {
	case a.state.ringingAlarm() != nil:
		u.show(a, a.state.ringingAlarm().ID)
	case len(a.state.Alarms) == 0:
		u.prepareSetter()
		a.screen = screenAlarmSet
	default:
		a.screen = screenAlarms
	}
}

func (u *alarmUI) show(a *app, id int) {
	u.sel = id
	a.screen = screenAlarm
}

// leave goes back to the alarm list, or the menu once it is empty.
func (u *alarmUI) leave(a *app) {
	if len(a.state.Alarms) == 0 {
		a.toMenu()
		return
	}
	u.cursor = min(u.cursor, len(a.state.Alarms)-1)
	a.screen = screenAlarms
}

// add sets a new alarm and returns its id.
func (u *alarmUI) add(a *app, at time.Time) int {
	a.send(request{Op: "alarm.add", At: at})
	id := 0
	for i, al := range a.state.Alarms {
		if al.ID > id {
			id, u.cursor = al.ID, i
		}
	}
	return id
}

func (u *alarmUI) update(a *app, msg tea.KeyMsg) tea.Cmd {
	m := a.state.alarm(u.sel)
	if m == nil {
		u.leave(a)
		return nil
	}
	if m.Ringing {
		switch {
		case key.Matches(msg, keys.Snooze):
			a.send(request{Op: "alarm.snooze", ID: m.ID})
		case key.Matches(msg, keys.Dismiss):
			a.send(request{Op: "alarm.remove", ID: m.ID})
			if r := a.state.ringingAlarm(); r != nil {
				u.show(a, r.ID)
				return nil
			}
			if a.oneShot {
				return tea.Quit
			}
			u.leave(a)
			return nil
		default:
			return nil
		}
		// After a snooze, move on to any other alarm that is still ringing.
		if r := a.state.ringingAlarm(); r != nil {
			u.show(a, r.ID)
		}
		return nil
	}
	switch {
	case key.Matches(msg, keys.Cancel):
		a.send(request{Op: "alarm.remove", ID: m.ID})
		u.leave(a)
	case key.Matches(msg, keys.AlarmBack):
		u.leave(a)
	}
	return nil
}

func (u *alarmUI) updateList(a *app, msg tea.KeyMsg) tea.Cmd {
	n := len(a.state.Alarms)
	u.cursor = max(min(u.cursor, n-1), 0)
	switch {
	case key.Matches(msg, keys.Up):
		u.cursor = max(u.cursor-1, 0)
	case key.Matches(msg, keys.Down):
		u.cursor = max(min(u.cursor+1, n-1), 0)
	case key.Matches(msg, keys.NewAlarm):
		u.prepareSetter()
		a.screen = screenAlarmSet
	case key.Matches(msg, keys.Select) && n > 0:
		u.show(a, a.state.Alarms[u.cursor].ID)
	case key.Matches(msg, keys.Cancel) && n > 0:
		a.send(request{Op: "alarm.remove", ID: a.state.Alarms[u.cursor].ID})
		u.leave(a)
	case key.Matches(msg, keys.Back):
		a.toMenu()
	}
	return nil
}

func (u *alarmUI) updateSetter(a *app, msg tea.KeyMsg) tea.Cmd {
	switch {
	case u.picker.handle(msg):
	case key.Matches(msg, keys.SetAlarm):
		u.add(a, nextOccurrence(u.picker.hours(), u.picker.minutes()))
		a.screen = screenAlarms
	case key.Matches(msg, keys.AlarmBack):
		u.leave(a)
	}
	return nil
}

func (u *alarmUI) viewSetter(a *app) string {
	st := a.st
	on := lipgloss.NewStyle().Bold(true).Foreground(colorAlarm)
	picker := u.picker.view(a, colorAlarm, [2]string{"hour", "min"})

	target := nextOccurrence(u.picker.hours(), u.picker.minutes())
	body := lipgloss.JoinVertical(lipgloss.Center,
		on.Render("NEW ALARM"),
		"",
		picker,
		"",
		st.muted.Render(fmt.Sprintf("rings %s · in %s", dayWord(target), shortDuration(time.Until(target).Truncate(time.Minute)+time.Minute))),
		"",
		st.muted.Render("type digits or use h/l and j/k"),
	)
	hk := append(helpKeys{keys.SetAlarm, keys.AlarmBack}, pickerHelp...)
	return a.frame(body, colorAlarm, hk)
}

func (u *alarmUI) viewList(a *app) string {
	st := a.st
	alarms := a.state.Alarms
	on := lipgloss.NewStyle().Bold(true).Foreground(colorAlarm)
	rows := []string{on.Render("ALARMS") + st.muted.Render(fmt.Sprintf("  ·  %d set", len(alarms))), ""}
	if len(alarms) == 0 {
		rows = append(rows, st.muted.Render("no alarms · a to add one"))
	}
	for i, al := range alarms {
		marker, clock := "  ", st.text.Render(al.Target.Format("15:04"))
		if i == u.cursor {
			marker, clock = st.selected.Render("▌ "), st.selected.Render(al.Target.Format("15:04"))
		}
		left := st.muted.Render("in " + formatDuration(time.Until(al.Target)))
		if al.Ringing {
			left = on.Render("ringing")
		}
		rows = append(rows, marker+on.Render("◆")+"  "+clock+"  "+
			lipgloss.NewStyle().Width(10).Render(st.muted.Render(dayWord(al.Target)))+left)
	}
	body := lipgloss.NewStyle().Width(40).Render(strings.Join(rows, "\n"))
	hk := helpKeys{keys.Down, keys.Up, bind([]string{"enter"}, "enter", "open"), keys.NewAlarm, keys.Cancel, keys.Back, keys.Help}
	return a.frame(body, colorAlarm, hk)
}

func (u *alarmUI) view(a *app) string {
	st := a.st
	title := lipgloss.NewStyle().Bold(true).Foreground(colorAlarm)
	m := a.state.alarm(u.sel)
	if m == nil {
		return u.viewList(a)
	}

	if m.Ringing {
		banner := title.Padding(0, 1).Render("  ALARM  ")
		if blinkOn() {
			banner = lipgloss.NewStyle().Bold(true).Padding(0, 1).
				Foreground(colorBase).Background(colorAlarm).Render("  ALARM  ")
		}
		rows := []string{
			banner,
			"",
			a.bigOrPlain(time.Now().Format("15:04"), colorAlarm),
			"",
			st.bold.Render("enter/space to stop · s to snooze " + a.cfg.Alarm.Snooze),
		}
		ringing := 0
		for _, al := range a.state.Alarms {
			if al.Ringing {
				ringing++
			}
		}
		if ringing > 1 {
			rows = append(rows, st.muted.Render(fmt.Sprintf("%d more ringing", ringing-1)))
		}
		return a.frame(lipgloss.JoinVertical(lipgloss.Center, rows...), colorAlarm, helpKeys{keys.Dismiss, keys.Snooze})
	}

	total := m.Target.Sub(m.SetAt)
	percent := 1.0
	if total > 0 {
		percent = min(max(time.Since(m.SetAt).Seconds()/total.Seconds(), 0), 1)
	}
	body := lipgloss.JoinVertical(lipgloss.Center,
		title.Render("ALARM")+st.muted.Render("  ·  "+m.Target.Format("15:04")+" "+dayWord(m.Target)),
		"",
		a.clock(time.Until(m.Target), colorAlarm),
		"",
		a.progress(percent, &gradAlarm),
		st.muted.Render("set at "+m.SetAt.Format("15:04")+" · rings at "+m.Target.Format("15:04")),
	)
	return a.frame(body, colorAlarm, helpKeys{keys.Cancel, keys.AlarmBack, keys.Help})
}
