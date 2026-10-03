package main

import (
	"cmp"
	"errors"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type screen int

const (
	screenTasks screen = iota
	screenPomodoro
	screenTaskAdd
	screenSettings
	screenRoutines
	screenRoutineForm
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
	warning       string

	state   State        // latest copy from the daemon
	updates <-chan State // pushed by the daemon on every change
	err     error        // lost the daemon; quit and report it

	editor   lengthEditor
	tasks    tasksUI
	routines routinesUI
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

// send runs a request on the daemon and takes the state it returns.
func (a *app) send(r request) {
	s, err := call(r)
	if err != nil {
		a.err = errors.New("lost connection to the gg daemon")
		return
	}
	a.setState(s)
}

// setState takes a newer state from the daemon, dropping stale ones.
func (a *app) setState(s State) {
	if s.Seq >= a.state.Seq {
		a.state = s
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
		a.err = errors.New("the gg daemon stopped (gg stop, or a newer gg replaced it; just run gg again)")
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
	// Any key silences a ringing pomodoro, then still does its job
	// (enter/space starts the next phase).
	if a.state.Ring != "" {
		a.send(request{Op: "silence"})
	}
	if a.editor.active {
		return a.editor.update(msg)
	}
	typing := a.screen == screenTaskAdd || a.screen == screenRoutineForm || (a.screen == screenSettings && a.settings.mode == modeInsert)
	if !typing && key.Matches(msg, keys.Help) {
		a.help.ShowAll = !a.help.ShowAll
		return nil
	}
	switch a.screen {
	case screenPomodoro:
		return a.state.Pomo.update(a, msg)
	case screenTasks:
		return a.tasks.update(a, msg)
	case screenTaskAdd, screenRoutineForm:
		return a.tasks.updateForm(a, msg)
	case screenRoutines:
		return a.routines.update(a, msg)
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
	case screenSettings:
		return a.settings.view(a)
	case screenRoutines, screenRoutineForm:
		return a.routines.view(a)
	default: // the tasks and the add/edit panel
		return a.tasks.view(a)
	}
}

var screenNames = map[screen]string{
	screenPomodoro:    "pomodoro",
	screenTasks:       "tasks",
	screenTaskAdd:     "tasks",
	screenSettings:    "settings",
	screenRoutines:    "routines",
	screenRoutineForm: "routines",
}

// header is the bar across the top of every screen: where you are on the
// left, the date on the right, and a rule under it.
func (a *app) header(color lipgloss.TerminalColor) []string {
	w := cmp.Or(a.width, 80)
	left := " " + lipgloss.NewStyle().Bold(true).Foreground(color).Render("gg")
	if name := screenNames[a.screen]; name != "" {
		left += a.st.muted.Render(" · " + name)
	}
	right := a.st.muted.Render(time.Now().Format("Mon 02 Jan · 15:04")) + " "
	if ring := a.ringingNote(); ring != "" {
		badge := a.st.badge.Background(colorWarn)
		if !blinkOn() {
			badge = badge.Faint(true)
		}
		right = badge.Render(ring) + "  " + right
	}
	return []string{spread(left, right, w), a.st.muted.Render(strings.Repeat("─", w))}
}

// ringingNote flags a ringing pomodoro in the header of other screens.
func (a *app) ringingNote() string {
	if a.state.Ring != "" && a.screen != screenPomodoro {
		return "POMODORO RINGING · any key silences"
	}
	return ""
}

// frame lays a screen out over the whole terminal: the header bar, the body
// against the left edge, and the key help pinned to the bottom.
func (a *app) frame(body string, color lipgloss.TerminalColor, hk helpKeys) string {
	w := cmp.Or(a.width, 80)
	top := a.header(color)
	body = lipgloss.NewStyle().Padding(1, 2, 0).Render(body)
	var bottom []string
	if a.cfg.UI.ShowHelp {
		a.help.Width = w - 2
		bottom = []string{a.st.muted.Render(strings.Repeat("─", w)), lipgloss.NewStyle().PaddingLeft(1).Render(a.help.View(hk))}
	}
	if a.height > 0 {
		// Fill down to the help, or cut the body so header and help stay visible.
		room := a.height - len(top) - lipgloss.Height(strings.Join(bottom, "\n"))
		if lines := strings.Split(body, "\n"); len(lines) > room {
			body = strings.Join(lines[:max(room, 1)], "\n")
		} else if a.cfg.UI.Fullscreen {
			body += strings.Repeat("\n", room-len(lines))
		}
	}
	return strings.Join(append(append(top, body), bottom...), "\n")
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

// progress draws a bar in the given gradient.
func (a *app) progress(percent float64, grad *[2]string) string {
	from, to := grad[0], grad[1]
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
