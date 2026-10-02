package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	minLen = time.Minute
	maxLen = 4 * time.Hour
)

type pomoPhase int

const (
	phaseFocus pomoPhase = iota
	phaseBreak
)

// pomodoro loops focus → break → focus. Nothing starts on its own: the first
// focus waits for space, and each finished phase waits for space/enter.
// It runs in the daemon; sessions only render it and send requests.
type pomodoro struct {
	Active       bool          `json:"active"`
	Phase        pomoPhase     `json:"phase"`
	CD           countdown     `json:"cd"`
	Ready        bool          `json:"ready"`   // opened but the first focus hasn't been started
	Waiting      bool          `json:"waiting"` // current phase finished, next one not started yet
	WaitingSince time.Time     `json:"waiting_since"`
	Sessions     int           `json:"sessions"`
	FocusLen     time.Duration `json:"focus_len"` // lengths for this run; start from the config
	BreakLen     time.Duration `json:"break_len"`
}

func (p *pomodoro) begin(cfg Config) {
	*p = pomodoro{Active: true, Ready: true, FocusLen: dur(cfg.Pomodoro.Work), BreakLen: dur(cfg.Pomodoro.Break)}
	p.startFocus()
	p.CD.Toggle() // hold at full length until space
}

func (p *pomodoro) startFocus() {
	p.Phase, p.Waiting = phaseFocus, false
	p.CD = newCountdown(p.FocusLen)
}

func (p *pomodoro) startBreak() {
	p.Phase, p.Waiting = phaseBreak, false
	p.CD = newCountdown(p.BreakLen)
}

// next starts the first focus, or the phase after the one that finished.
func (p *pomodoro) next() {
	switch {
	case p.Ready:
		p.Ready = false
		p.startFocus()
	case p.Waiting && p.Phase == phaseFocus:
		p.startBreak()
	case p.Waiting:
		p.startFocus()
	}
}

func (p *pomodoro) phaseName() string { return p.phaseNameOf(p.Phase) }

func (p *pomodoro) phaseNameOf(ph pomoPhase) string {
	if ph == phaseBreak {
		return "Break"
	}
	return "Focus"
}

func (p *pomodoro) finish(d *daemon, silent bool) {
	p.Waiting, p.WaitingSince = true, time.Now()
	if p.Phase == phaseFocus {
		p.Sessions++
	}
	if silent {
		return
	}
	d.startRing(ringPomodoro)
	if p.Phase == phaseFocus {
		notify(d.cfg, "Focus session done",
			fmt.Sprintf("Session %d complete. Start your %s break when ready.", p.Sessions, shortDuration(p.BreakLen)), "normal")
	} else {
		notify(d.cfg, "Break is over", "Start your next focus session when you're ready.", "normal")
	}
}

// check finishes the running phase once its time is up.
func (p *pomodoro) check(d *daemon) bool {
	if !p.Active || p.Waiting || !p.CD.Done() {
		return false
	}
	p.finish(d, false)
	return true
}

// editTarget is the phase e changes: the running phase, or the next one
// while waiting.
func (p *pomodoro) editTarget() pomoPhase {
	if p.Waiting {
		return 1 - p.Phase
	}
	return p.Phase
}

// setLen changes a phase length for the rest of this run, and the running
// countdown too when that phase is the one running.
func (p *pomodoro) setLen(ph pomoPhase, d time.Duration) {
	l := &p.FocusLen
	if ph == phaseBreak {
		l = &p.BreakLen
	}
	next := min(max(d, minLen), maxLen)
	delta := next - *l
	*l = next
	if !p.Waiting && p.Phase == ph {
		p.CD.Adjust(delta, minLen)
	}
}

func (p *pomodoro) edit(a *app, ph pomoPhase) {
	l, color := p.FocusLen, colorFocus
	if ph == phaseBreak {
		l, color = p.BreakLen, colorBreak
	}
	note := "applies to this pomodoro run"
	if !p.Waiting && p.Phase == ph {
		note = shortDuration(p.CD.Total-p.CD.Remaining()) + " of this " + strings.ToLower(p.phaseNameOf(ph)) + " already elapsed"
	}
	a.editLength(p.phaseNameOf(ph)+" length", note, color, l, func(d time.Duration) {
		a.send(request{Op: "pomo.len", Phase: ph, Dur: d})
	})
}

func (p *pomodoro) update(a *app, msg tea.KeyMsg) tea.Cmd {
	running := !p.Waiting && !p.Ready
	switch {
	case key.Matches(msg, keys.Start, keys.Pause) && (p.Ready || p.Waiting):
		a.send(request{Op: "pomo.next"})
	case key.Matches(msg, keys.Pause) && running:
		a.send(request{Op: "pomo.pause"})
	case key.Matches(msg, keys.EditTime):
		p.edit(a, p.editTarget())
	case key.Matches(msg, keys.EditFocus):
		p.edit(a, phaseFocus)
	case key.Matches(msg, keys.EditBreak):
		p.edit(a, phaseBreak)
	case key.Matches(msg, keys.Skip) && running:
		a.send(request{Op: "pomo.skip"})
	case key.Matches(msg, keys.Restart) && running:
		a.send(request{Op: "pomo.restart"})
	case key.Matches(msg, keys.Stop):
		a.send(request{Op: "pomo.stop"})
		a.toMenu()
	case key.Matches(msg, keys.Back):
		a.toMenu()
	}
	return nil
}

func (p *pomodoro) view(a *app) string {
	st := a.st
	color, grad := colorFocus, &gradFocus
	if p.Phase == phaseBreak {
		color, grad = colorBreak, &gradBreak
	}
	phaseStyle := lipgloss.NewStyle().Bold(true).Foreground(color)

	tomatoes := phaseStyle.Render(strings.TrimSpace(strings.Repeat("● ", min(p.Sessions, 8))))
	if p.Sessions > 8 {
		tomatoes += fmt.Sprintf(" ×%d", p.Sessions)
	}
	if p.Sessions == 0 {
		tomatoes = st.muted.Render("no sessions yet")
	}
	// Both lengths, with the one e would change highlighted.
	lenPart := func(ph pomoPhase, name string, d time.Duration) string {
		if ph == p.editTarget() {
			c := colorFocus
			if ph == phaseBreak {
				c = colorBreak
			}
			return st.muted.Render(name+" ") + lipgloss.NewStyle().Bold(true).Foreground(c).Render("◂ "+shortDuration(d)+" ▸")
		}
		return st.muted.Render(name + " " + shortDuration(d))
	}
	lengths := lenPart(phaseFocus, "focus", p.FocusLen) + st.muted.Render("   ·   ") + lenPart(phaseBreak, "break", p.BreakLen)
	adjustHint := st.muted.Render("e edit this · f focus · b break")

	var rows []string
	var hk helpKeys
	if p.Waiting {
		if p.Phase == phaseFocus {
			rows = []string{
				phaseStyle.Render("FOCUS DONE"),
				"",
				st.bold.Render(fmt.Sprintf("Nice work! Session %d complete.", p.Sessions)),
				"",
				lengths,
				adjustHint,
				"",
				phaseStyle.Render("press space to start your break"),
			}
		} else {
			rows = []string{
				phaseStyle.Render("BREAK IS OVER"),
				"",
				st.bold.Render(fmt.Sprintf("Ready for session %d?", p.Sessions+1)),
				"",
				lengths,
				adjustHint,
				"",
				lipgloss.NewStyle().Bold(true).Foreground(colorFocus).Render("press space to start focusing"),
			}
		}
		status := st.muted.Render("waiting " + shortDuration(time.Since(p.WaitingSince).Truncate(time.Second)))
		if a.state.Ring == ringPomodoro {
			status = ringingHint(color)
		}
		rows = append(rows, "", status, "", tomatoes)
		hk = helpKeys{keys.Start, keys.EditTime, keys.EditFocus, keys.EditBreak, keys.Stop, keys.Back, keys.Help}
	} else {
		title := phaseStyle.Render(strings.ToUpper(p.phaseName())) +
			st.muted.Render(fmt.Sprintf("  ·  session %d", p.Sessions+boolInt(p.Phase == phaseFocus)))
		info := "ends at " + time.Now().Add(p.CD.Remaining()).Format("15:04")
		switch {
		case p.Ready:
			title += "  " + st.badge.Background(color).Render("READY")
			info = lipgloss.NewStyle().Bold(true).Foreground(color).Render("press space to start")
		case p.CD.Paused:
			title += "  " + st.badge.Render("PAUSED")
			info = st.muted.Render("paused")
		default:
			info = st.muted.Render(info)
		}
		rows = []string{
			title,
			"",
			a.clock(p.CD.Remaining(), color),
			"",
			a.progress(p.CD.Percent(), grad),
			info,
			"",
			lengths,
			adjustHint,
			"",
			tomatoes,
		}
		hk = helpKeys{keys.Pause, keys.EditTime, keys.EditFocus, keys.EditBreak, keys.Skip, keys.Restart, keys.Stop, keys.Back, keys.Help}
	}
	return a.frame(lipgloss.JoinVertical(lipgloss.Center, rows...), color, hk)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
