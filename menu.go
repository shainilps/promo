package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type menuItem struct {
	icon   string
	color  lipgloss.TerminalColor // nil = accent
	title  string
	desc   func(a *app) string
	action func(a *app) tea.Cmd
}

var menuItems = []menuItem{
	{
		icon: "●", color: colorFocus, title: "Pomodoro",
		desc: func(a *app) string {
			p := a.state.Pomo
			switch {
			case !p.Active:
				return fmt.Sprintf("focus %s · break %s", a.cfg.Pomodoro.Work, a.cfg.Pomodoro.Break)
			case p.Ready:
				return "● ready · space to start focus"
			case p.Waiting:
				return "● waiting for you to start the next phase"
			default:
				return fmt.Sprintf("● %s · %s left", strings.ToLower(p.phaseName()), formatDuration(p.CD.Remaining()))
			}
		},
		action: func(a *app) tea.Cmd {
			a.send(request{Op: "pomo.begin"}) // no-op while one is running
			a.screen = screenPomodoro
			return nil
		},
	},
	{
		icon: "◔", title: "Timer",
		desc: func(a *app) string {
			t := a.state.Timer
			switch {
			case !t.Active:
				return "simple countdown from " + a.cfg.DefaultTime
			case t.Done:
				return "● time's up"
			default:
				return fmt.Sprintf("● running · %s left", formatDuration(t.CD.Remaining()))
			}
		},
		action: func(a *app) tea.Cmd {
			if !a.state.Timer.Active {
				a.send(request{Op: "timer.begin", Dur: dur(a.cfg.DefaultTime)})
			}
			a.screen = screenTimer
			return nil
		},
	},
	{
		icon: "◆", color: colorAlarm, title: "Alarm",
		desc: func(a *app) string {
			alarms := a.state.Alarms
			switch {
			case a.state.ringingAlarm() != nil:
				return "● ringing"
			case len(alarms) == 0:
				return "ring at a time of day"
			}
			next := alarms[0].Target
			return fmt.Sprintf("● %d set · next %s in %s", len(alarms), next.Format("15:04"), formatDuration(time.Until(next)))
		},
		action: func(a *app) tea.Cmd {
			a.alarm.open(a)
			return nil
		},
	},
	{
		icon: "≡", color: colorMuted, title: "Settings",
		desc: func(a *app) string { return "durations, sounds, colors" },
		action: func(a *app) tea.Cmd {
			a.settings.open(a)
			a.screen = screenSettings
			return nil
		},
	},
	{
		icon: "×", color: colorMuted, title: "Quit",
		desc: func(a *app) string {
			if a.busy() {
				return "timers keep running in the background"
			}
			return ""
		},
		action: func(a *app) tea.Cmd { return tea.Quit },
	},
}

type menuModel struct {
	cursor   int
	pendingG bool
}

// busy reports whether the daemon is running something.
func (a *app) busy() bool {
	s := a.state
	return s.Pomo.Active || (s.Timer.Active && !s.Timer.Done) || len(s.Alarms) > 0
}

func (m *menuModel) update(a *app, msg tea.KeyMsg) tea.Cmd {
	n := len(menuItems)
	wasG := m.pendingG
	m.pendingG = false
	switch {
	case key.Matches(msg, keys.Quit):
		return tea.Quit
	case key.Matches(msg, keys.Up):
		m.cursor = (m.cursor - 1 + n) % n
	case key.Matches(msg, keys.Down):
		m.cursor = (m.cursor + 1) % n
	case key.Matches(msg, keys.Top):
		if wasG || msg.String() == "home" {
			m.cursor = 0
		} else {
			m.pendingG = true
		}
	case key.Matches(msg, keys.Bottom):
		m.cursor = n - 1
	case key.Matches(msg, keys.Select):
		return menuItems[m.cursor].action(a)
	default:
		// 1-5 jump straight to an item.
		if s := msg.String(); len(s) == 1 && s[0] >= '1' && int(s[0]-'1') < n {
			m.cursor = int(s[0] - '1')
			return menuItems[m.cursor].action(a)
		}
	}
	return nil
}

func (m *menuModel) view(a *app) string {
	st := a.st
	const width = 46
	now := time.Now()
	header := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(width/2).Render(st.title.Render("promo")),
		lipgloss.NewStyle().Width(width-width/2).Align(lipgloss.Right).Render(st.muted.Render(now.Format("Mon 02 Jan · 15:04"))),
	)

	rows := []string{header, ""}
	for i, item := range menuItems {
		title, desc := st.text.Render(item.title), st.muted.Render(item.desc(a))
		marker := "  "
		if i == m.cursor {
			marker = st.selected.Render("▌ ")
			title = st.selected.Render(item.title)
		}
		color := item.color
		if color == nil {
			color = st.accent
		}
		icon := lipgloss.NewStyle().Foreground(color).Render(item.icon)
		rows = append(rows, fmt.Sprintf("%s%s  %s", marker, icon, title))
		if d := item.desc(a); d != "" {
			rows = append(rows, "     "+desc)
		}
		rows = append(rows, "")
	}
	if a.warning != "" {
		rows = append(rows, st.errorMsg.Render("config: "+a.warning))
	}
	hk := helpKeys{keys.Down, keys.Up, keys.Select, keys.Top, keys.Bottom, keys.Quit, keys.Help}
	body := lipgloss.NewStyle().Width(width).Render(strings.TrimRight(strings.Join(rows, "\n"), "\n"))

	return a.frame(body, st.accent, hk)
}
