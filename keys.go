package main

import "github.com/charmbracelet/bubbles/key"

func bind(keys []string, helpKey, desc string) key.Binding {
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(helpKey, desc))
}

var keys = struct {
	ForceQuit, Help, Back                                             key.Binding
	Start, Pause, EditTime, EditFocus, EditBreak, Skip, Stop, Restart key.Binding
	FieldLeft, FieldRight, Inc, Dec, Inc10, Dec10                     key.Binding
	Leave, Commit                                                     key.Binding
}{
	ForceQuit: bind([]string{"ctrl+c"}, "ctrl+c", "quit"),
	Help:      bind([]string{"?"}, "?", "more keys"),
	Back:      bind([]string{"esc", "q"}, "q/esc", "quit"),

	Start:     bind([]string{"enter"}, "enter/space", "start next"),
	Pause:     bind([]string{" ", "p"}, "space", "pause"),
	EditTime:  bind([]string{"e"}, "e", "edit length"),
	EditFocus: bind([]string{"f"}, "f", "edit focus"),
	EditBreak: bind([]string{"b"}, "b", "edit break"),
	Skip:      bind([]string{"s"}, "s", "skip"),
	Stop:      bind([]string{"x"}, "x", "stop & quit"),
	Restart:   bind([]string{"r"}, "r", "restart"),

	FieldLeft:  bind([]string{"h", "left"}, "h", "hour"),
	FieldRight: bind([]string{"l", "right", "tab"}, "l", "minute"),
	Inc:        bind([]string{"k", "up"}, "k", "+1"),
	Dec:        bind([]string{"j", "down"}, "j", "-1"),
	Inc10:      bind([]string{"K"}, "K", "+10"),
	Dec10:      bind([]string{"J"}, "J", "-10"),

	Leave:  bind([]string{"esc"}, "esc", "normal mode"),
	Commit: bind([]string{"enter"}, "enter", "apply"),
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
