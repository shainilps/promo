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
type pomodoro struct {
	active       bool
	phase        pomoPhase
	cd           countdown
	ready        bool // opened but the first focus hasn't been started
	waiting      bool // current phase finished, next one not started yet
	waitingSince time.Time
	sessions     int
	focusLen     time.Duration // lengths for this run; start from the config
	breakLen     time.Duration
}

func (p *pomodoro) begin(cfg Config) {
	*p = pomodoro{active: true, ready: true, focusLen: dur(cfg.Pomodoro.Work), breakLen: dur(cfg.Pomodoro.Break)}
	p.startFocus()
	p.cd.Toggle() // hold at full length until space
}

func (p *pomodoro) startFocus() {
	p.phase, p.waiting = phaseFocus, false
	p.cd = newCountdown(p.focusLen)
}

func (p *pomodoro) startBreak() {
	p.phase, p.waiting = phaseBreak, false
	p.cd = newCountdown(p.breakLen)
}

func (p *pomodoro) phaseName() string { return p.phaseNameOf(p.phase) }

func (p *pomodoro) phaseNameOf(ph pomoPhase) string {
	if ph == phaseBreak {
		return "Break"
	}
	return "Focus"
}

func (p *pomodoro) finish(a *app, silent bool) {
	p.waiting, p.waitingSince = true, time.Now()
	if p.phase == phaseFocus {
		p.sessions++
	}
	if silent {
		return
	}
	playOnce(a.cfg.soundFor(false))
	if p.phase == phaseFocus {
		notify(a.cfg, "Focus session done",
			fmt.Sprintf("Session %d complete. Start your %s break when ready.", p.sessions, shortDuration(p.breakLen)), "normal")
	} else {
		notify(a.cfg, "Break is over", "Start your next focus session when you're ready.", "normal")
	}
}

func (p *pomodoro) tick(a *app) tea.Cmd {
	if p.active && !p.waiting && p.cd.Done() {
		p.finish(a, false)
	}
	return nil
}

// editTarget is the phase e changes: the running phase, or the next one
// while waiting.
func (p *pomodoro) editTarget() pomoPhase {
	if p.waiting {
		return 1 - p.phase
	}
	return p.phase
}

// setLen changes a phase length for the rest of this run, and the running
// countdown too when that phase is the one running.
func (p *pomodoro) setLen(ph pomoPhase, d time.Duration) {
	l := &p.focusLen
	if ph == phaseBreak {
		l = &p.breakLen
	}
	next := min(max(d, minLen), maxLen)
	delta := next - *l
	*l = next
	if !p.waiting && p.phase == ph {
		p.cd.Adjust(delta, minLen)
	}
}

func (p *pomodoro) edit(a *app, ph pomoPhase) {
	l, color := p.focusLen, colorFocus
	if ph == phaseBreak {
		l, color = p.breakLen, colorBreak
	}
	note := "applies to this pomodoro run"
	if !p.waiting && p.phase == ph {
		note = shortDuration(p.cd.total-p.cd.Remaining()) + " of this " + strings.ToLower(p.phaseNameOf(ph)) + " already elapsed"
	}
	a.editLength(p.phaseNameOf(ph)+" length", note, color, l, func(d time.Duration) { p.setLen(ph, d) })
}

func (p *pomodoro) update(a *app, msg tea.KeyMsg) tea.Cmd {
	switch {
	case key.Matches(msg, keys.Start, keys.Pause) && p.ready:
		p.ready = false
		p.startFocus()
	case key.Matches(msg, keys.Start, keys.Pause) && p.waiting:
		if p.phase == phaseFocus {
			p.startBreak()
		} else {
			p.startFocus()
		}
	case key.Matches(msg, keys.Pause) && !p.waiting:
		p.cd.Toggle()
	case key.Matches(msg, keys.EditTime):
		p.edit(a, p.editTarget())
	case key.Matches(msg, keys.EditFocus):
		p.edit(a, phaseFocus)
	case key.Matches(msg, keys.EditBreak):
		p.edit(a, phaseBreak)
	case key.Matches(msg, keys.Skip) && !p.waiting && !p.ready:
		p.finish(a, true)
	case key.Matches(msg, keys.Restart) && !p.waiting && !p.ready:
		p.cd = newCountdown(p.cd.total)
	case key.Matches(msg, keys.Stop):
		p.active = false
		a.toMenu()
	case key.Matches(msg, keys.Back):
		a.toMenu()
	}
	return nil
}

func (p *pomodoro) view(a *app) string {
	st := a.st
	color, grad := colorFocus, &gradFocus
	if p.phase == phaseBreak {
		color, grad = colorBreak, &gradBreak
	}
	phaseStyle := lipgloss.NewStyle().Bold(true).Foreground(color)

	tomatoes := phaseStyle.Render(strings.TrimSpace(strings.Repeat("● ", min(p.sessions, 8))))
	if p.sessions > 8 {
		tomatoes += fmt.Sprintf(" ×%d", p.sessions)
	}
	if p.sessions == 0 {
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
	lengths := lenPart(phaseFocus, "focus", p.focusLen) + st.muted.Render("   ·   ") + lenPart(phaseBreak, "break", p.breakLen)
	adjustHint := st.muted.Render("e edit this · f focus · b break")

	var rows []string
	var hk helpKeys
	if p.waiting {
		if p.phase == phaseFocus {
			rows = []string{
				phaseStyle.Render("FOCUS DONE"),
				"",
				st.bold.Render(fmt.Sprintf("Nice work! Session %d complete.", p.sessions)),
				"",
				lengths,
				adjustHint,
				"",
				phaseStyle.Render("press enter to start your break"),
			}
		} else {
			rows = []string{
				phaseStyle.Render("BREAK IS OVER"),
				"",
				st.bold.Render(fmt.Sprintf("Ready for session %d?", p.sessions+1)),
				"",
				lengths,
				adjustHint,
				"",
				lipgloss.NewStyle().Bold(true).Foreground(colorFocus).Render("press enter to start focusing"),
			}
		}
		rows = append(rows, "", st.muted.Render("waiting "+shortDuration(time.Since(p.waitingSince).Truncate(time.Second))), "", tomatoes)
		hk = helpKeys{keys.Start, keys.EditTime, keys.EditFocus, keys.EditBreak, keys.Stop, keys.Back, keys.Help}
	} else {
		title := phaseStyle.Render(strings.ToUpper(p.phaseName())) +
			st.muted.Render(fmt.Sprintf("  ·  session %d", p.sessions+boolInt(p.phase == phaseFocus)))
		info := "ends at " + time.Now().Add(p.cd.Remaining()).Format("15:04")
		switch {
		case p.ready:
			title += "  " + st.badge.Background(color).Render("READY")
			info = lipgloss.NewStyle().Bold(true).Foreground(color).Render("press space to start")
		case p.cd.paused:
			title += "  " + st.badge.Render("PAUSED")
			info = st.muted.Render("paused")
		default:
			info = st.muted.Render(info)
		}
		rows = []string{
			title,
			"",
			a.clock(p.cd.Remaining(), color),
			"",
			a.progress(p.cd.Percent(), grad),
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
