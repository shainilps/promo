package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type alarmModel struct {
	armed, ringing bool
	setAt, target  time.Time
	loop           *soundLoop

	picker clockPicker
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

func (m *alarmModel) prepareSetter() {
	t := time.Now().Add(time.Hour)
	m.picker = newPicker(t.Hour(), t.Minute())
}

func (m *alarmModel) arm(target time.Time) {
	m.armed, m.ringing = true, false
	m.setAt, m.target = time.Now(), target
}

func (m *alarmModel) tick(a *app) tea.Cmd {
	if !m.armed || m.ringing || time.Now().Before(m.target) {
		return nil
	}
	m.ringing = true
	m.loop = startSoundLoop(a.cfg.soundFor(true))
	notify(a.cfg, "Alarm", "It's "+m.target.Format("15:04")+".", "critical")
	a.screen = screenAlarm
	return nil
}

func (m *alarmModel) stopRinging() {
	m.loop.Stop()
	m.loop = nil
	m.ringing = false
}

func (m *alarmModel) update(a *app, msg tea.KeyMsg) tea.Cmd {
	if m.ringing {
		switch {
		case key.Matches(msg, keys.Snooze):
			m.stopRinging()
			m.arm(time.Now().Add(dur(a.cfg.Alarm.Snooze)))
		case key.Matches(msg, keys.Dismiss):
			m.stopRinging()
			m.armed = false
			if a.oneShot {
				return tea.Quit
			}
			a.toMenu()
		}
		return nil
	}
	switch {
	case key.Matches(msg, keys.Cancel):
		m.armed = false
		a.toMenu()
	case key.Matches(msg, keys.AlarmBack):
		a.toMenu()
	}
	return nil
}

func (m *alarmModel) updateSetter(a *app, msg tea.KeyMsg) tea.Cmd {
	switch {
	case m.picker.handle(msg):
	case key.Matches(msg, keys.SetAlarm):
		m.arm(nextOccurrence(m.picker.hours(), m.picker.minutes()))
		a.screen = screenAlarm
	case key.Matches(msg, keys.AlarmBack):
		a.toMenu()
	}
	return nil
}

func (m *alarmModel) viewSetter(a *app) string {
	st := a.st
	on := lipgloss.NewStyle().Bold(true).Foreground(colorAlarm)
	picker := m.picker.view(a, colorAlarm, [2]string{"hour", "min"})

	target := nextOccurrence(m.picker.hours(), m.picker.minutes())
	when := "today"
	if target.Day() != time.Now().Day() {
		when = "tomorrow"
	}
	body := lipgloss.JoinVertical(lipgloss.Center,
		on.Render("SET ALARM"),
		"",
		picker,
		"",
		st.muted.Render(fmt.Sprintf("rings %s · in %s", when, shortDuration(time.Until(target).Truncate(time.Minute)+time.Minute))),
		"",
		st.muted.Render("type digits or use h/l and j/k"),
	)
	hk := append(helpKeys{keys.SetAlarm, keys.AlarmBack}, pickerHelp...)
	return a.frame(body, colorAlarm, hk)
}

func (m *alarmModel) view(a *app) string {
	st := a.st
	title := lipgloss.NewStyle().Bold(true).Foreground(colorAlarm)

	if m.ringing {
		banner := title.Padding(0, 1).Render("  ALARM  ")
		if blinkOn() {
			banner = lipgloss.NewStyle().Bold(true).Padding(0, 1).
				Foreground(colorBase).Background(colorAlarm).Render("  ALARM  ")
		}
		body := lipgloss.JoinVertical(lipgloss.Center,
			banner,
			"",
			a.bigOrPlain(time.Now().Format("15:04"), colorAlarm),
			"",
			st.bold.Render("enter/space to stop · s to snooze "+a.cfg.Alarm.Snooze),
		)
		return a.frame(body, colorAlarm, helpKeys{keys.Dismiss, keys.Snooze})
	}

	day := "today"
	if m.target.Day() != time.Now().Day() {
		day = "tomorrow"
	}
	total := m.target.Sub(m.setAt)
	percent := 1.0
	if total > 0 {
		percent = min(max(time.Since(m.setAt).Seconds()/total.Seconds(), 0), 1)
	}
	body := lipgloss.JoinVertical(lipgloss.Center,
		title.Render("ALARM")+st.muted.Render("  ·  "+m.target.Format("15:04")+" "+day),
		"",
		a.clock(time.Until(m.target), colorAlarm),
		"",
		a.progress(percent, &gradAlarm),
		st.muted.Render("set at "+m.setAt.Format("15:04")+" · rings at "+m.target.Format("15:04")),
	)
	return a.frame(body, colorAlarm, helpKeys{keys.Cancel, keys.AlarmBack, keys.Help})
}
