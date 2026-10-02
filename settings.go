package main

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type fieldKind int

const (
	kindText fieldKind = iota
	kindBool
	kindColor
)

type field struct {
	section, label, hint string
	kind                 fieldKind
	get                  func(*Config) string
	set                  func(*Config, string)
	validate             func(string) error
}

func textField(section, label, hint string, kind fieldKind, ptr func(*Config) *string, validate func(string) error) field {
	return field{
		section: section, label: label, hint: hint, kind: kind, validate: validate,
		get: func(c *Config) string { return *ptr(c) },
		set: func(c *Config, v string) { *ptr(c) = v },
	}
}

func boolField(section, label, hint string, ptr func(*Config) *bool) field {
	return field{
		section: section, label: label, hint: hint, kind: kindBool,
		get: func(c *Config) string {
			if *ptr(c) {
				return "on"
			}
			return "off"
		},
		set: func(c *Config, v string) { *ptr(c) = v == "on" },
	}
}

var settingsFields = []field{
	textField("General", "Sound file", "played when a timer or pomodoro phase ends (empty = system sound)", kindText,
		func(c *Config) *string { return &c.SoundPath }, validateSoundPath),
	textField("General", "Timer length", "length of Timer mode, e.g. 30m", kindText,
		func(c *Config) *string { return &c.DefaultTime }, validateDuration),
	boolField("General", "Notifications", "desktop notifications via notify-send",
		func(c *Config) *bool { return &c.Notifications }),

	textField("Pomodoro", "Focus length", "length of each focus session, e.g. 25m", kindText,
		func(c *Config) *string { return &c.Pomodoro.Work }, validateDuration),
	textField("Pomodoro", "Break length", "default break after each session, e.g. 10m", kindText,
		func(c *Config) *string { return &c.Pomodoro.Break }, validateDuration),

	textField("Alarm", "Alarm sound", "looped while the alarm rings (empty = sound file)", kindText,
		func(c *Config) *string { return &c.Alarm.SoundPath }, validateSoundPath),
	textField("Alarm", "Snooze", "how long s snoozes the alarm, e.g. 5m", kindText,
		func(c *Config) *string { return &c.Alarm.Snooze }, validateDuration),

	textField("Appearance", "Accent color", "titles, borders and selection (hex)", kindColor,
		func(c *Config) *string { return &c.UI.AccentColor }, validateColor),
	textField("Appearance", "Bar start", "timer progress bar gradient start (hex)", kindColor,
		func(c *Config) *string { return &c.UI.BarStartColor }, validateColor),
	textField("Appearance", "Bar end", "timer progress bar gradient end (hex)", kindColor,
		func(c *Config) *string { return &c.UI.BarEndColor }, validateColor),
	{
		section: "Appearance", label: "Bar width", hint: "maximum progress bar width (10-200)", kind: kindText,
		get:      func(c *Config) string { return strconv.Itoa(c.UI.BarWidth) },
		set:      func(c *Config, v string) { c.UI.BarWidth, _ = strconv.Atoi(v) },
		validate: validateBarWidth,
	},
	boolField("Appearance", "Big clock", "draw the time in big block digits",
		func(c *Config) *bool { return &c.UI.BigClock }),
	boolField("Appearance", "Fullscreen", "use the whole terminal (alt screen)",
		func(c *Config) *bool { return &c.UI.Fullscreen }),
	boolField("Appearance", "Help footer", "show the keybinding hints",
		func(c *Config) *bool { return &c.UI.ShowHelp }),
}

type settingsMode int

const (
	modeNormal settingsMode = iota
	modeInsert
	modeConfirm
)

// settingsModel edits a draft copy of the config with vim-style modes.
type settingsModel struct {
	draft    Config
	cursor   int
	mode     settingsMode
	input    textinput.Model
	pendingG bool
	err      string
	flash    string
	flashAt  time.Time
}

func (s *settingsModel) open(a *app) {
	s.draft = a.cfg
	s.mode = modeNormal
	s.err, s.flash = "", ""
	s.input = textinput.New()
	s.input.Prompt = ""
	s.input.Width = 34
	s.input.Cursor.SetMode(cursor.CursorStatic)
}

func (s *settingsModel) dirty(a *app) bool { return s.draft != a.cfg }

func (s *settingsModel) setFlash(msg string) {
	s.flash, s.flashAt = msg, time.Now()
}

func (s *settingsModel) update(a *app, msg tea.KeyMsg) tea.Cmd {
	switch s.mode {
	case modeInsert:
		return s.updateInsert(msg)
	case modeConfirm:
		switch {
		case key.Matches(msg, keys.Accept):
			if cmd, ok := s.save(a); ok {
				a.toMenu()
				return cmd
			}
			s.mode = modeNormal
		case key.Matches(msg, keys.Discard):
			s.draft = a.cfg
			a.toMenu()
		default:
			s.mode = modeNormal
		}
		return nil
	}

	n := len(settingsFields)
	f := settingsFields[s.cursor]
	wasG := s.pendingG
	s.pendingG = false
	s.err = ""
	switch {
	case key.Matches(msg, keys.Up):
		s.cursor = max(s.cursor-1, 0)
	case key.Matches(msg, keys.Down):
		s.cursor = min(s.cursor+1, n-1)
	case key.Matches(msg, keys.Top):
		if wasG || msg.String() == "home" {
			s.cursor = 0
		} else {
			s.pendingG = true
		}
	case key.Matches(msg, keys.Bottom):
		s.cursor = n - 1
	case msg.String() == "ctrl+d":
		s.cursor = min(s.cursor+n/2, n-1)
	case msg.String() == "ctrl+u":
		s.cursor = max(s.cursor-n/2, 0)
	case key.Matches(msg, keys.Toggle, keys.Edit) && f.kind == kindBool:
		if f.get(&s.draft) == "on" {
			f.set(&s.draft, "off")
		} else {
			f.set(&s.draft, "on")
		}
	case key.Matches(msg, keys.Edit):
		s.mode = modeInsert
		s.input.SetValue(f.get(&s.draft))
		s.input.CursorEnd()
		return s.input.Focus()
	case key.Matches(msg, keys.Save):
		cmd, _ := s.save(a)
		return cmd
	case key.Matches(msg, keys.Undo):
		if s.dirty(a) {
			s.draft = a.cfg
			s.setFlash("reverted unsaved changes")
		}
	case key.Matches(msg, keys.Back):
		if s.dirty(a) {
			s.mode = modeConfirm
		} else {
			a.toMenu()
		}
	}
	return nil
}

func (s *settingsModel) updateInsert(msg tea.KeyMsg) tea.Cmd {
	f := settingsFields[s.cursor]
	switch {
	case key.Matches(msg, keys.Commit, keys.Leave):
		v := strings.TrimSpace(s.input.Value())
		if f.validate != nil {
			if err := f.validate(v); err != nil {
				if key.Matches(msg, keys.Leave) {
					// esc always leaves insert mode; the bad edit is dropped.
					s.err = f.label + ": " + err.Error() + " (change discarded)"
					s.mode = modeNormal
					s.input.Blur()
				} else {
					s.err = f.label + ": " + err.Error()
				}
				return nil
			}
		}
		f.set(&s.draft, v)
		s.err = ""
		s.mode = modeNormal
		s.input.Blur()
		return nil
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	return cmd
}

func (s *settingsModel) save(a *app) (tea.Cmd, bool) {
	if !s.dirty(a) {
		s.setFlash("nothing to save")
		return nil, true
	}
	if err := saveConfig(a.cfgPath, s.draft); err != nil {
		s.err = "save failed: " + err.Error()
		return nil, false
	}
	old := a.cfg
	a.applyConfig(s.draft)
	a.send(request{Op: "reload"})
	a.warning = ""
	s.setFlash("saved ✓")
	if old.UI.Fullscreen != s.draft.UI.Fullscreen {
		if s.draft.UI.Fullscreen {
			return tea.EnterAltScreen, true
		}
		return tea.ExitAltScreen, true
	}
	return nil, true
}

const (
	labelWidth = 16
	valueWidth = 36
)

func truncateLeft(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	return "…" + string(r[len(r)-w+1:])
}

func (s *settingsModel) renderValue(a *app, f field, selected bool) string {
	st := a.st
	if selected && s.mode == modeInsert {
		return s.input.View()
	}
	v := f.get(&s.draft)
	switch f.kind {
	case kindBool:
		if v == "on" {
			return st.success.Render("● on")
		}
		return st.muted.Render("○ off")
	case kindColor:
		return lipgloss.NewStyle().Foreground(lipgloss.Color(v)).Render("██") + " " + st.text.Render(v)
	}
	if v == "" {
		return st.muted.Render("(default)")
	}
	return st.text.Render(truncateLeft(v, valueWidth))
}

func (s *settingsModel) view(a *app) string {
	st := a.st
	saved := a.cfg
	path := a.cfgPath
	if home := expandHome("~/"); strings.HasPrefix(path, home) {
		path = "~" + strings.TrimPrefix(path, home)
	}

	rows := []string{
		st.title.Render("SETTINGS") + "  " + st.muted.Render(path),
	}
	section := ""
	for i, f := range settingsFields {
		if f.section != section {
			section = f.section
			rows = append(rows, st.section.Render(strings.ToUpper(section)))
		}
		selected := i == s.cursor
		marker, label := "  ", st.text.Render(f.label)
		if selected {
			marker, label = st.selected.Render("▌ "), st.selected.Render(f.label)
		}
		changed := " "
		if f.get(&s.draft) != f.get(&saved) {
			changed = st.selected.Render("●")
		}
		rows = append(rows, marker+
			lipgloss.NewStyle().Width(labelWidth).Render(label)+
			lipgloss.NewStyle().Width(valueWidth+2).Render(s.renderValue(a, f, selected))+
			changed)
	}

	rows = append(rows, "", st.muted.Render(settingsFields[s.cursor].hint), "")

	var status string
	var hk helpKeys
	switch s.mode {
	case modeInsert:
		status = st.mode.Background(colorBreak).Render("INSERT")
		hk = helpKeys{keys.Commit, keys.Leave}
	case modeConfirm:
		status = st.mode.Background(colorAlarm).Render("UNSAVED") + " " +
			lipgloss.NewStyle().Foreground(colorAlarm).Render("save changes?  y save · n discard · esc stay")
		hk = helpKeys{keys.Accept, keys.Discard}
	default:
		status = st.mode.Render("NORMAL")
		hk = helpKeys{keys.Down, keys.Up, keys.Edit, keys.Toggle, keys.Save, keys.Undo, keys.Top, keys.Bottom, keys.Back, keys.Help}
	}
	switch {
	case s.err != "":
		status += " " + st.errorMsg.Render("✗ "+s.err)
	case s.flash != "" && time.Since(s.flashAt) < 3*time.Second:
		status += " " + st.success.Render(s.flash)
	case s.mode == modeNormal && s.dirty(a):
		status += " " + st.muted.Render("unsaved changes · w to save")
	}
	rows = append(rows, status)

	body := lipgloss.NewStyle().Width(2 + labelWidth + valueWidth + 3).Render(strings.Join(rows, "\n"))
	return a.frame(body, st.accent, hk)
}
