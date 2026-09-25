package main

import "github.com/charmbracelet/bubbles/key"

func bind(keys []string, helpKey, desc string) key.Binding {
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(helpKey, desc))
}

var keys = struct {
	ForceQuit, Quit, Help, Back                                       key.Binding
	Up, Down, Top, Bottom, Select                                     key.Binding
	Start, Pause, EditTime, EditFocus, EditBreak, Skip, Stop, Restart key.Binding
	Confirm                                                           key.Binding
	FieldLeft, FieldRight, Inc, Dec, Inc10, Dec10, SetAlarm           key.Binding
	AlarmBack, Cancel, Dismiss, Snooze                                key.Binding
	Edit, Toggle, Save, Undo, Leave, Commit, Discard, Accept          key.Binding
}{
	ForceQuit: bind([]string{"ctrl+c"}, "ctrl+c", "quit"),
	Quit:      bind([]string{"q"}, "q", "quit"),
	Help:      bind([]string{"?"}, "?", "more keys"),
	Back:      bind([]string{"esc", "h", "q"}, "h/esc", "menu"),

	Up:     bind([]string{"k", "up"}, "k/↑", "up"),
	Down:   bind([]string{"j", "down"}, "j/↓", "down"),
	Top:    bind([]string{"g", "home"}, "gg", "top"),
	Bottom: bind([]string{"G", "end"}, "G", "bottom"),
	Select: bind([]string{"l", "enter", "right"}, "l/enter", "select"),

	Start:     bind([]string{"enter"}, "enter/space", "start next"),
	Pause:     bind([]string{" ", "p"}, "space", "pause"),
	EditTime:  bind([]string{"e"}, "e", "edit length"),
	EditFocus: bind([]string{"f"}, "f", "edit focus"),
	EditBreak: bind([]string{"b"}, "b", "edit break"),
	Confirm:   bind([]string{"y", "q"}, "y", "quit"),
	Skip:      bind([]string{"s"}, "s", "skip"),
	Stop:      bind([]string{"x"}, "x", "stop"),
	Restart:   bind([]string{"r"}, "r", "restart"),

	FieldLeft:  bind([]string{"h", "left"}, "h", "hour"),
	FieldRight: bind([]string{"l", "right", "tab"}, "l", "minute"),
	Inc:        bind([]string{"k", "up"}, "k", "+1"),
	Dec:        bind([]string{"j", "down"}, "j", "-1"),
	Inc10:      bind([]string{"K"}, "K", "+10"),
	Dec10:      bind([]string{"J"}, "J", "-10"),
	SetAlarm:   bind([]string{"enter"}, "enter", "set alarm"),

	AlarmBack: bind([]string{"esc", "q"}, "esc", "menu"),
	Cancel:    bind([]string{"x"}, "x", "cancel alarm"),
	Dismiss:   bind([]string{"enter", " ", "esc", "q"}, "enter/space", "stop"),
	Snooze:    bind([]string{"s"}, "s", "snooze"),

	Edit:    bind([]string{"i", "enter", "l", "a"}, "i/enter", "edit"),
	Toggle:  bind([]string{" "}, "space", "toggle"),
	Save:    bind([]string{"w", "ctrl+s"}, "w", "save"),
	Undo:    bind([]string{"u"}, "u", "undo all"),
	Leave:   bind([]string{"esc"}, "esc", "normal mode"),
	Commit:  bind([]string{"enter"}, "enter", "apply"),
	Discard: bind([]string{"n", "d"}, "n", "discard"),
	Accept:  bind([]string{"y", "w"}, "y", "save"),
}

// helpKeys adapts a list of bindings to help.KeyMap.
type helpKeys []key.Binding

func (h helpKeys) ShortHelp() []key.Binding { return h }

func (h helpKeys) FullHelp() [][]key.Binding {
	var cols [][]key.Binding
	for i := 0; i < len(h); i += 3 {
		cols = append(cols, h[i:min(i+3, len(h))])
	}
	return cols
}
