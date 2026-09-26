package main

import (
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// timerModel is the original single countdown.
type timerModel struct {
	active, done bool
	cd           countdown
}

func (t *timerModel) begin(d time.Duration) {
	*t = timerModel{active: true, cd: newCountdown(d)}
}

func (t *timerModel) tick(a *app) tea.Cmd {
	if !t.active || t.done || !t.cd.Done() {
		return nil
	}
	t.done = true
	a.ring(screenTimer)
	notify(a.cfg, "Time's up", "Your "+shortDuration(t.cd.total)+" timer finished.", "normal")
	return nil
}

func (t *timerModel) update(a *app, msg tea.KeyMsg) tea.Cmd {
	if t.done {
		if key.Matches(msg, keys.Restart) {
			t.begin(t.cd.total)
			return nil
		}
		if key.Matches(msg, keys.Back, keys.Start, keys.Pause, keys.Stop) {
			t.active = false
			if a.oneShot {
				a.shutdown()
				return tea.Quit
			}
			a.toMenu()
		}
		return nil
	}
	switch {
	case key.Matches(msg, keys.Pause):
		t.cd.Toggle()
	case key.Matches(msg, keys.EditTime):
		note := shortDuration(t.cd.total-t.cd.Remaining()) + " already elapsed"
		a.editLength("timer length", note, a.st.accent, t.cd.total, func(d time.Duration) {
			t.cd.Adjust(d-t.cd.total, time.Second)
		})
	case key.Matches(msg, keys.Restart):
		t.begin(t.cd.total)
	case key.Matches(msg, keys.Stop):
		t.active = false
		a.toMenu()
	case key.Matches(msg, keys.Back):
		a.toMenu()
	}
	return nil
}

func (t *timerModel) view(a *app) string {
	st := a.st
	color := st.accent
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(color)

	if t.done {
		banner := titleStyle.Render("TIME'S UP")
		if blinkOn() {
			banner = lipgloss.NewStyle().Bold(true).Foreground(colorBase).Background(color).Padding(0, 1).Render("TIME'S UP")
		}
		body := lipgloss.JoinVertical(lipgloss.Center,
			banner,
			"",
			a.clock(0, color),
			"",
			st.muted.Render(shortDuration(t.cd.total)+" timer finished"),
		)
		if a.ringing != nil {
			body = lipgloss.JoinVertical(lipgloss.Center, body, "", ringingHint(color))
		}
		return a.frame(body, color, helpKeys{keys.Start, keys.Restart, keys.Back})
	}

	title := titleStyle.Render("TIMER") + st.muted.Render("  ·  "+shortDuration(t.cd.total))
	info := "ends at " + time.Now().Add(t.cd.Remaining()).Format("15:04")
	if t.cd.paused {
		title += "  " + st.badge.Render("PAUSED")
		info = "paused"
	}
	body := lipgloss.JoinVertical(lipgloss.Center,
		title,
		"",
		a.clock(t.cd.Remaining(), color),
		"",
		a.progress(t.cd.Percent(), nil),
		st.muted.Render(info),
	)
	return a.frame(body, color, helpKeys{keys.Pause, keys.EditTime, keys.Restart, keys.Stop, keys.Back, keys.Help})
}
