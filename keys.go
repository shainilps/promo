package main

import "github.com/charmbracelet/bubbles/key"

func bind(keys []string, helpKey, desc string) key.Binding {
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(helpKey, desc))
}

var keys = struct {
	ForceQuit, Help, Back                                             key.Binding
	Up, Down, Top, Bottom, Select                                     key.Binding
	Start, Pause, EditTime, EditFocus, EditBreak, Skip, Stop, Restart key.Binding
	FieldLeft, FieldRight, Inc, Dec, Inc10, Dec10                     key.Binding
	Edit, Toggle, Save, Undo, Leave, Commit, Discard, Accept          key.Binding
	ListNext, ListPrev, Check, AddTask, DelTask, ClearDone, NextField key.Binding
	PrevField, AddToList, EditTask, OpenFile, DelList, DelTaskNow     key.Binding
	WeekPrev, WeekNext, ThisWeek                                      key.Binding
}{
	ForceQuit: bind([]string{"ctrl+c"}, "ctrl+c", "quit"),
	Help:      bind([]string{"?"}, "?", "more keys"),
	Back:      bind([]string{"esc", "q"}, "q/esc", "quit"),

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
	Skip:      bind([]string{"s"}, "s", "skip"),
	Stop:      bind([]string{"x"}, "x", "stop & quit"),
	Restart:   bind([]string{"r"}, "r", "restart"),

	FieldLeft:  bind([]string{"h", "left"}, "h", "hour"),
	FieldRight: bind([]string{"l", "right", "tab"}, "l", "minute"),
	Inc:        bind([]string{"k", "up"}, "k", "+1"),
	Dec:        bind([]string{"j", "down"}, "j", "-1"),
	Inc10:      bind([]string{"K"}, "K", "+10"),
	Dec10:      bind([]string{"J"}, "J", "-10"),

	Edit:    bind([]string{"i", "enter", "l", "a"}, "i/enter", "edit"),
	Toggle:  bind([]string{" "}, "space", "toggle"),
	Save:    bind([]string{"w", "ctrl+s"}, "w", "save"),
	Undo:    bind([]string{"u"}, "u", "undo all"),
	Leave:   bind([]string{"esc"}, "esc", "normal mode"),
	Commit:  bind([]string{"enter"}, "enter", "apply"),
	Discard: bind([]string{"n", "d"}, "n", "discard"),
	Accept:  bind([]string{"y", "w"}, "y", "save"),

	ListNext:   bind([]string{"l", "tab", "right"}, "l/tab", "next list"),
	ListPrev:   bind([]string{"h", "shift+tab", "left"}, "h", "prev list"),
	Check:      bind([]string{" ", "enter"}, "space", "done"),
	AddTask:    bind([]string{"a", "n"}, "a", "add"),
	AddToList:  bind([]string{"A"}, "A", "add to other list"),
	EditTask:   bind([]string{"e"}, "e", "edit"),
	OpenFile:   bind([]string{"E"}, "E", "edit list file"),
	DelList:    bind([]string{"D"}, "D", "delete list"),
	DelTask:    bind([]string{"x", "d"}, "x", "delete"),
	DelTaskNow: bind([]string{"X"}, "X", "delete, no asking"),
	ClearDone:  bind([]string{"c"}, "c", "clear done"),
	NextField:  bind([]string{"tab", "down"}, "tab", "next field"),
	PrevField:  bind([]string{"shift+tab", "up"}, "shift+tab", "prev field"),

	WeekPrev: bind([]string{"h", "left"}, "h", "last week"),
	WeekNext: bind([]string{"l", "right"}, "l", "next week"),
	ThisWeek: bind([]string{"t"}, "t", "this week"),
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
