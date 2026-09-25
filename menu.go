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
			p := a.pomo
			switch {
			case !p.active:
				return fmt.Sprintf("focus %s · break %s", a.cfg.Pomodoro.Work, a.cfg.Pomodoro.Break)
			case p.ready:
				return "● ready · space to start focus"
			case p.waiting:
				return "● waiting for you to start the next phase"
			default:
				return fmt.Sprintf("● %s · %s left", strings.ToLower(p.phaseName()), formatDuration(p.cd.Remaining()))
			}
		},
		action: func(a *app) tea.Cmd {
			if !a.pomo.active {
				a.pomo.begin(a.cfg)
			}
			a.screen = screenPomodoro
			return nil
		},
	},
	{
		icon: "◔", title: "Timer",
		desc: func(a *app) string {
			t := a.timer
			switch {
			case !t.active:
				return "simple countdown from " + a.cfg.DefaultTime
			case t.done:
				return "● time's up"
			default:
				return fmt.Sprintf("● running · %s left", formatDuration(t.cd.Remaining()))
			}
		},
		action: func(a *app) tea.Cmd {
			if !a.timer.active {
				a.timer.begin(dur(a.cfg.DefaultTime))
			}
			a.screen = screenTimer
			return nil
		},
	},
	{
		icon: "◆", color: colorAlarm, title: "Alarm",
		desc: func(a *app) string {
			if a.alarm.armed {
				return fmt.Sprintf("● set for %s · %s left", a.alarm.target.Format("15:04"), formatDuration(time.Until(a.alarm.target)))
			}
			return "ring at a time of day"
		},
		action: func(a *app) tea.Cmd {
			if a.alarm.armed {
				a.screen = screenAlarm
			} else {
				a.alarm.prepareSetter()
				a.screen = screenAlarmSet
			}
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
		desc:   func(a *app) string { return "" },
		action: func(a *app) tea.Cmd { return a.menu.quit(a) },
	},
}

type menuModel struct {
	cursor      int
	pendingG    bool
	confirmQuit bool
}

// busy reports whether quitting would stop something that is running.
func (a *app) busy() bool {
	return a.pomo.active || (a.timer.active && !a.timer.done) || a.alarm.armed
}

func (m *menuModel) quit(a *app) tea.Cmd {
	if a.busy() {
		m.confirmQuit = true
		return nil
	}
	a.shutdown()
	return tea.Quit
}

func (m *menuModel) update(a *app, msg tea.KeyMsg) tea.Cmd {
	n := len(menuItems)
	if m.confirmQuit {
		m.confirmQuit = false
		if key.Matches(msg, keys.Confirm) {
			a.shutdown()
			return tea.Quit
		}
		return nil
	}
	wasG := m.pendingG
	m.pendingG = false
	switch {
	case key.Matches(msg, keys.Quit):
		return m.quit(a)
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
	if m.confirmQuit {
		rows = append(rows, lipgloss.NewStyle().Foreground(colorAlarm).Bold(true).Render("quit? running timers will stop  y / n"))
		hk = helpKeys{keys.Confirm, bind([]string{"n"}, "n", "stay")}
	}
	body := lipgloss.NewStyle().Width(width).Render(strings.TrimRight(strings.Join(rows, "\n"), "\n"))

	return a.frame(body, st.accent, hk)
}
