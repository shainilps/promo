package main

import (
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// timerModel is the original single countdown. It runs in the daemon.
type timerModel struct {
	Active bool      `json:"active"`
	Done   bool      `json:"done"`
	CD     countdown `json:"cd"`
}

func (t *timerModel) begin(d time.Duration) {
	*t = timerModel{Active: true, CD: newCountdown(d)}
}

func (t *timerModel) check(d *daemon) bool {
	if !t.Active || t.Done || !t.CD.Done() {
		return false
	}
	t.Done = true
	d.startRing(ringTimer)
	notify(d.cfg, "Time's up", "Your "+shortDuration(t.CD.Total)+" timer finished.", "normal")
	return true
}

func (t *timerModel) update(a *app, msg tea.KeyMsg) tea.Cmd {
	if t.Done {
		if key.Matches(msg, keys.Restart) {
			a.send(request{Op: "timer.begin", Dur: t.CD.Total})
			return nil
		}
		if key.Matches(msg, keys.Back, keys.Start, keys.Pause, keys.Stop) {
			a.send(request{Op: "timer.stop"})
			if a.oneShot {
				return tea.Quit
			}
			a.toMenu()
		}
		return nil
	}
	switch {
	case key.Matches(msg, keys.Pause):
		a.send(request{Op: "timer.pause"})
	case key.Matches(msg, keys.EditTime):
		note := shortDuration(t.CD.Total-t.CD.Remaining()) + " already elapsed"
		a.editLength("timer length", note, a.st.accent, t.CD.Total, func(d time.Duration) {
			a.send(request{Op: "timer.len", Dur: d})
		})
	case key.Matches(msg, keys.Restart):
		a.send(request{Op: "timer.begin", Dur: t.CD.Total})
	case key.Matches(msg, keys.Stop):
		a.send(request{Op: "timer.stop"})
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

	if t.Done {
		banner := titleStyle.Render("TIME'S UP")
		if blinkOn() {
			banner = lipgloss.NewStyle().Bold(true).Foreground(colorBase).Background(color).Padding(0, 1).Render("TIME'S UP")
		}
		body := lipgloss.JoinVertical(lipgloss.Center,
			banner,
			"",
			a.clock(0, color),
			"",
			st.muted.Render(shortDuration(t.CD.Total)+" timer finished"),
		)
		if a.state.Ring == ringTimer {
			body = lipgloss.JoinVertical(lipgloss.Center, body, "", ringingHint(color))
		}
		return a.frame(body, color, helpKeys{keys.Start, keys.Restart, keys.Back})
	}

	title := titleStyle.Render("TIMER") + st.muted.Render("  ·  "+shortDuration(t.CD.Total))
	info := "ends at " + time.Now().Add(t.CD.Remaining()).Format("15:04")
	if t.CD.Paused {
		title += "  " + st.badge.Render("PAUSED")
		info = "paused"
	}
	body := lipgloss.JoinVertical(lipgloss.Center,
		title,
		"",
		a.clock(t.CD.Remaining(), color),
		"",
		a.progress(t.CD.Percent(), nil),
		st.muted.Render(info),
	)
	return a.frame(body, color, helpKeys{keys.Pause, keys.EditTime, keys.Restart, keys.Stop, keys.Back, keys.Help})
}
