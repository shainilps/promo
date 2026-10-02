package main

import (
	"errors"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type screen int

const (
	screenMenu screen = iota
	screenPomodoro
	screenTimer
	screenAlarmSet
	screenAlarms
	screenAlarm
	screenTasks
	screenTaskAdd
	screenSettings
)

type (
	tickMsg  time.Time
	stateMsg State
	lostMsg  struct{}
)

// The daemon runs the countdowns; the tick only redraws them.
func tick() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func waitState(ch <-chan State) tea.Cmd {
	return func() tea.Msg {
		s, ok := <-ch
		if !ok {
			return lostMsg{}
		}
		return stateMsg(s)
	}
}

type app struct {
	cfg           Config
	cfgPath       string
	st            styles
	help          help.Model
	width, height int
	screen        screen
	oneShot       bool // launched via a CLI shortcut: quit when that mode finishes
	warning       string

	state   State        // latest copy from the daemon
	updates <-chan State // pushed by the daemon on every change
	err     error        // lost the daemon; quit and report it

	editor   lengthEditor
	menu     menuModel
	alarm    alarmUI
	tasks    tasksUI
	settings settingsModel
}

func newApp(cfg Config, path string) *app {
	a := &app{cfgPath: path}
	a.applyConfig(cfg)
	return a
}

func (a *app) applyConfig(cfg Config) {
	a.cfg = cfg
	a.st = newStyles(cfg.UI)
	showAll := a.help.ShowAll
	a.help = newHelp(a.st)
	a.help.ShowAll = showAll
}

func (a *app) toMenu() {
	a.screen = screenMenu
	a.oneShot = false
}

// send runs a request on the daemon and takes the state it returns.
func (a *app) send(r request) {
	s, err := call(r)
	if err != nil {
		a.err = errors.New("lost connection to the gg daemon")
		return
	}
	a.setState(s)
}

// setState takes a newer state and brings whatever just started ringing to
// the front.
func (a *app) setState(s State) {
	if s.Seq < a.state.Seq {
		return
	}
	old := a.state
	a.state = s
	if s.Ring != "" && s.Ring != old.Ring {
		a.editor.active = false
		a.screen = screenTimer
		if s.Ring == ringPomodoro {
			a.screen = screenPomodoro
		}
	}
	for _, al := range s.Alarms {
		if prev := old.alarm(al.ID); al.Ringing && (prev == nil || !prev.Ringing) {
			a.editor.active = false
			a.alarm.show(a, al.ID)
		}
	}
	if a.screen == screenAlarm && s.alarm(a.alarm.sel) == nil {
		a.alarm.leave(a)
	}
}

func (a *app) Init() tea.Cmd { return tea.Batch(tick(), waitState(a.updates)) }

func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		return a, nil

	case tickMsg:
		return a, tick()

	case stateMsg:
		a.setState(State(msg))
		return a, waitState(a.updates)

	case listEditedMsg:
		a.send(request{Op: "tasks.reload"})
		if a.err != nil {
			return a, tea.Quit
		}
		return a, nil

	case lostMsg:
		a.err = errors.New("the gg daemon stopped")
		return a, tea.Quit

	case tea.KeyMsg:
		cmd := a.handleKey(msg)
		if a.err != nil {
			return a, tea.Quit
		}
		return a, cmd
	}
	return a, nil
}

func (a *app) handleKey(msg tea.KeyMsg) tea.Cmd {
	if key.Matches(msg, keys.ForceQuit) {
		return tea.Quit
	}
	// Any key silences a ringing pomodoro/timer, then still does its job
	// (enter/space starts the next phase).
	if a.state.Ring != "" {
		a.send(request{Op: "silence"})
	}
	if a.editor.active {
		return a.editor.update(msg)
	}
	typing := a.screen == screenTaskAdd || (a.screen == screenSettings && a.settings.mode == modeInsert)
	if !typing && key.Matches(msg, keys.Help) {
		a.help.ShowAll = !a.help.ShowAll
		return nil
	}
	switch a.screen {
	case screenMenu:
		return a.menu.update(a, msg)
	case screenPomodoro:
		return a.state.Pomo.update(a, msg)
	case screenTimer:
		return a.state.Timer.update(a, msg)
	case screenAlarmSet:
		return a.alarm.updateSetter(a, msg)
	case screenAlarms:
		return a.alarm.updateList(a, msg)
	case screenAlarm:
		return a.alarm.update(a, msg)
	case screenTasks:
		return a.tasks.update(a, msg)
	case screenTaskAdd:
		return a.tasks.updateForm(a, msg)
	case screenSettings:
		return a.settings.update(a, msg)
	}
	return nil
}

func (a *app) View() string {
	if a.editor.active {
		return a.editor.view(a)
	}
	switch a.screen {
	case screenPomodoro:
		return a.state.Pomo.view(a)
	case screenTimer:
		return a.state.Timer.view(a)
	case screenAlarmSet:
		return a.alarm.viewSetter(a)
	case screenAlarms:
		return a.alarm.viewList(a)
	case screenAlarm:
		return a.alarm.view(a)
	case screenTasks:
		return a.tasks.view(a)
	case screenTaskAdd:
		return a.tasks.viewForm(a)
	case screenSettings:
		return a.settings.view(a)
	default:
		return a.menu.view(a)
	}
}

// frame wraps a screen body in a bordered card with the help footer and
// centers it in the window.
func (a *app) frame(body string, border lipgloss.TerminalColor, hk helpKeys) string {
	parts := []string{a.st.card.BorderForeground(border).Render(body)}
	if a.cfg.UI.ShowHelp {
		a.help.Width = a.width
		parts = append(parts, "", a.help.View(hk))
	}
	out := lipgloss.JoinVertical(lipgloss.Center, parts...)
	if a.width == 0 {
		return out
	}
	if !a.cfg.UI.Fullscreen {
		return lipgloss.PlaceHorizontal(a.width, lipgloss.Center, out)
	}
	return lipgloss.Place(a.width, a.height, lipgloss.Center, lipgloss.Center, out)
}

// clock renders a duration in big block digits, or plain text when disabled
// or when the terminal is too narrow.
func (a *app) clock(d time.Duration, color lipgloss.TerminalColor) string {
	return a.bigOrPlain(formatDuration(d), color)
}

func (a *app) bigOrPlain(s string, color lipgloss.TerminalColor) string {
	style := lipgloss.NewStyle().Foreground(color).Bold(true)
	if a.cfg.UI.BigClock && (a.width == 0 || bigTextWidth(s)+14 <= a.width) {
		return style.Render(bigText(s))
	}
	return style.Render(s)
}

// progress draws a bar in the given gradient; nil uses the configured one.
func (a *app) progress(percent float64, grad *[2]string) string {
	from, to := a.cfg.UI.BarStartColor, a.cfg.UI.BarEndColor
	if grad != nil {
		from, to = grad[0], grad[1]
	}
	w := a.cfg.UI.BarWidth
	if a.width > 0 {
		w = min(w, a.width-14)
	}
	return newBar(from, to, max(w, 10)).ViewAs(percent)
}

func blinkOn() bool {
	return time.Now().UnixMilli()/500%2 == 0
}

func ringingHint(color lipgloss.TerminalColor) string {
	style := lipgloss.NewStyle().Bold(true).Foreground(color)
	if !blinkOn() {
		style = style.Faint(true)
	}
	return style.Render("ringing · any key to silence")
}
