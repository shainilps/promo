package main

import (
	"bufio"
	"cmp"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// gg opens your tasks as one markdown document in $EDITOR, a # section per
// list. Save and quit to apply it. When a line can't be read nothing is
// saved: the problem goes in a note at the top and the editor reopens on
// that line.

const docHelp = `
<!--
  save and quit to apply · a line gg can't read reopens the editor on it

  # work                  a list (a new name makes a new list)
  ## today                the day for the tasks below: today, tomorrow, friday, 9/10, 9/10/2029
  ## weekdays             the tasks below repeat: daily, weekdays, weekends, mon-fri, every mon, thu
  - [ ] some task         a task · - [x] done · delete the line to delete it
  - [ ] 09:30 standup     a reminder at 09:30 (9am and 7:30pm work too)

  finished tasks from past days aren't shown here; they stay in the files.
-->
`

// lineError is a problem with one line of a document.
type lineError struct {
	line int
	msg  string
}

func (e *lineError) Error() string { return fmt.Sprintf("line %d: %s", e.line, e.msg) }

// docHidden reports a task the document leaves out: finished ones from past
// days, and the days a routine was missed. They stay in the files as is.
func docHidden(t todo, today time.Time) bool {
	return t.Every == 0 && dayOf(t.Due).Before(today) && (t.Done || t.Of != 0)
}

// taskMarkdown is a task's line in a list file or the document.
func taskMarkdown(t todo) string {
	box := " "
	if t.Done {
		box = "x"
	}
	return strings.TrimSpace(fmt.Sprintf("- [%s] %s %s", box, clockText(t), t.Text))
}

// docHeading names the group a task sits under in the document.
func docHeading(t todo, today time.Time) string {
	if t.Every != 0 {
		return t.Every.heading()
	}
	day := dayOf(t.Due)
	date := day.Format("Mon 02 Jan")
	if day.Year() != today.Year() {
		date = day.Format("Mon 02 Jan 2006")
	}
	switch {
	case day.Equal(today):
		return "today · " + date
	case day.Equal(today.AddDate(0, 0, 1)):
		return "tomorrow · " + date
	case day.Before(today):
		return date + " · overdue"
	}
	return date
}

// docLists puts inbox first, then the rest by name.
func docLists(lists []string) []string {
	out := slices.Clone(lists)
	slices.SortStableFunc(out, func(a, b string) int { return boolInt(b == defaultList) - boolInt(a == defaultList) })
	return out
}

// renderDoc writes the given lists as one document: routines first, then
// what's overdue, today (always there, so there's somewhere to write), and
// the days after.
func renderDoc(todos []todo, lists []string, now time.Time) string {
	today := dayOf(now)
	todayHeading := docHeading(todo{Due: today}, today)
	var b strings.Builder
	for i, list := range lists {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "# %s\n", list)
		var mine []todo
		for _, t := range todos {
			if t.List == list && !docHidden(t, today) {
				mine = append(mine, t)
			}
		}
		slices.SortFunc(mine, compareTodos)
		heading, wroteToday := "", false
		for _, t := range mine {
			if !wroteToday && t.Every == 0 && dayOf(t.Due).After(today) {
				fmt.Fprintf(&b, "\n## %s\n", todayHeading)
				wroteToday = true
			}
			if h := docHeading(t, today); h != heading {
				heading = h
				wroteToday = wroteToday || h == todayHeading
				fmt.Fprintf(&b, "\n## %s\n\n", h)
			}
			b.WriteString(taskMarkdown(t) + "\n")
		}
		if !wroteToday {
			fmt.Fprintf(&b, "\n## %s\n\n", todayHeading)
		}
	}
	b.WriteString(docHelp)
	return b.String()
}

var (
	htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	docTaskLine = regexp.MustCompile(`^[-*+]\s+(?:\[([ xX]?)\]\s*)?(.*)$`)
)

// parseDoc reads a document (or a gg add draft) back into tasks. Tasks
// before any # heading go to list. Anything it can't read is an error that
// names the line, so nothing is quietly dropped.
func parseDoc(text, list string, now time.Time) ([]todo, error) {
	text = htmlComment.ReplaceAllStringFunc(text, func(c string) string {
		return strings.Repeat("\n", strings.Count(c, "\n")) // keep line numbers right
	})
	today := dayOf(now)
	day, every := today, repeat(0)
	var todos []todo
	sc := bufio.NewScanner(strings.NewReader(text))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		bad := func(format string, args ...any) error { return &lineError{n, fmt.Sprintf(format, args...)} }
		switch {
		case line == "":
		case strings.HasPrefix(line, "###"):
			return nil, bad("use # for a list and ## for a day or a repeat (got %q)", line)
		case strings.HasPrefix(line, "##"):
			head := strings.TrimSpace(strings.TrimPrefix(line, "##"))
			head = strings.ToLower(strings.TrimSpace(strings.Split(head, "·")[0])) // "today · Sat 03 Oct"
			if r, ok := parseRepeat(head); ok {
				every = r
			} else if d, ok := parseDay(head, today); ok {
				day, every = d, 0
			} else {
				return nil, bad("can't read heading %q: a day (today, tomorrow, friday, 9/10) or a repeat (weekdays, daily, mon-fri, every mon, thu)", head)
			}
		case strings.HasPrefix(line, "#"):
			name := strings.TrimSpace(strings.TrimPrefix(line, "#"))
			if name == "" {
				return nil, bad("a # heading needs a list name")
			}
			list, day, every = cleanList(name), today, 0
		default:
			m := docTaskLine.FindStringSubmatch(line)
			if m == nil {
				return nil, bad("tasks start with - [ ] (got %q)", truncateRight(line, 40))
			}
			t := todo{List: list, Text: strings.Join(strings.Fields(m[2]), " "), Due: day, Done: m[1] == "x" || m[1] == "X", Every: every, line: n}
			if t.Text == "" {
				continue
			}
			if every != 0 {
				if t.Done {
					return nil, bad("a repeating task can't be checked off itself; check off its task under ## today")
				}
				t.Due = clockOf(0, 0)
			}
			if tm := timePrefix.FindStringSubmatch(t.Text); tm != nil {
				h, mm, err := parseClock(tm[1])
				if err != nil {
					return nil, bad("%v", err)
				}
				t.Due = on(t.Due, clockOf(h, mm))
				t.Remind, t.Text = true, tm[2]
			} else if wrong := badTime.FindString(t.Text); wrong != "" {
				return nil, bad("can't read time %q (try 09:30 or 9:30am)", wrong)
			}
			todos = append(todos, t)
		}
	}
	return todos, nil
}

// badTime catches a time gg would otherwise read as part of the text, like 25:00.
var badTime = regexp.MustCompile(`^\d{1,2}:\d{2}\S*`)

// contentHash fingerprints the given lists, to notice a change made while
// the document was open.
func contentHash(todos []todo, lists []string) string {
	var rows []string
	for _, t := range todos {
		if slices.Contains(lists, t.List) {
			rows = append(rows, fmt.Sprintf("%s|%s|%s|%v|%v|%d", t.List, t.Text, t.Due.Format(time.RFC3339), t.Remind, t.Done, t.Every))
		}
	}
	slices.Sort(rows)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(rows, "\n"))))
}

// editDoc opens the lists (only one, or all of them) as a document, and
// applies it once it reads.
func editDoc(only string, in io.Reader, out io.Writer) error {
	s, err := call(request{Op: "get"})
	if err != nil {
		return err
	}
	lists := docLists(s.Lists)
	if !slices.Contains(lists, defaultList) {
		lists = append([]string{defaultList}, lists...)
	}
	if only != "" {
		lists = []string{only}
	}
	now := time.Now()
	orig := renderDoc(s.Todos, lists, now)
	base := contentHash(s.Todos, lists)
	shown := 0
	for _, t := range s.Todos {
		shown += boolInt(slices.Contains(lists, t.List) && !docHidden(t, dayOf(now)))
	}

	f, err := os.CreateTemp("", "gg-*.md")
	if err != nil {
		return err
	}
	path := f.Name()
	defer os.Remove(path)
	f.WriteString(orig)
	f.Close()

	confirmedEmpty := false
	return editUntilRead(path, "<!-- gg: %s -->", in, out, func(text string) error {
		if text == orig {
			fmt.Fprintln(out, "no changes")
			return nil
		}
		todos, err := parseDoc(text, cmp.Or(only, defaultList), now)
		if err != nil {
			return err
		}
		if len(todos) == 0 && shown > 0 && !confirmedEmpty {
			confirmedEmpty = true
			return fmt.Errorf("this deletes all %d tasks; save and quit again if you mean it", shown)
		}
		cur, err := call(request{Op: "get"})
		if err != nil {
			return err
		}
		if h := contentHash(cur.Todos, lists); h != base {
			base = h // saving again keeps this version
			return errors.New("your tasks changed while this was open (gg add, a hand edit, or midnight); save and quit again to keep this version, or n to drop it and run gg again")
		}
		if _, err := call(request{Op: "lists.set", Lists: lists, Todos: todos}); err != nil {
			return err
		}
		fmt.Fprintln(out, summarize(s.Todos, todos, lists, now))
		return nil
	})
}

// summarize says what a saved document changed: "saved · 2 done, 1 added".
func summarize(before, after []todo, lists []string, now time.Time) string {
	id := func(t todo) string {
		return t.List + "\x00" + t.Text + "\x00" + t.Due.Format(time.RFC3339) + fmt.Sprint(t.Every)
	}
	was := map[string]todo{}
	for _, t := range before {
		if slices.Contains(lists, t.List) && !docHidden(t, dayOf(now)) {
			was[id(t)] = t
		}
	}
	var done, undone, added int
	for _, t := range after {
		old, ok := was[id(t)]
		switch {
		case !ok:
			added++
		case t.Done && !old.Done:
			done++
		case !t.Done && old.Done:
			undone++
		}
		delete(was, id(t))
	}
	var parts []string
	for _, p := range []struct {
		n    int
		what string
	}{{done, "done"}, {undone, "reopened"}, {added, "added or changed"}, {len(was), "removed"}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.what))
		}
	}
	if len(parts) == 0 {
		return "saved"
	}
	return "saved · " + strings.Join(parts, ", ")
}

// editUntilRead opens path in the editor until apply accepts what was
// written. When it doesn't, the problem goes in a note at the top (note is
// the comment format, like "<!-- gg: %s -->") and the editor reopens on
// the line it's about, unless you answer n.
func editUntilRead(path, note string, in io.Reader, out io.Writer, apply func(text string) error) error {
	prefix := strings.SplitN(note, "%s", 2)[0]
	reader := bufio.NewReader(in)
	line := 0
	for {
		if err := editorCmd(path, line).Run(); err != nil {
			return fmt.Errorf("the editor quit with an error (%v); nothing saved", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		if strings.HasPrefix(text, prefix) { // the note from last time
			if _, rest, ok := strings.Cut(text, "\n"); ok {
				text = rest
			}
		}
		err = apply(text)
		if err == nil {
			return nil
		}
		msg := err.Error()
		line = 1
		var le *lineError
		if errors.As(err, &le) {
			line = le.line + 1 // the note goes above it
			msg = fmt.Sprintf("line %d: %s", line, le.msg)
		}
		fmt.Fprintf(out, "%s\nedit again? [Y/n] ", msg)
		answer, _ := reader.ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a == "n" || a == "no" {
			return errors.New("nothing saved")
		}
		if err := os.WriteFile(path, []byte(fmt.Sprintf(note, msg)+"\n"+text), 0o600); err != nil {
			return err
		}
	}
}

// editorCmd opens path in $VISUAL, $EDITOR or vi, on line when it's set
// and the editor takes +N. The variable may carry arguments (code -w), so
// it goes through the shell.
func editorCmd(path string, line int) *exec.Cmd {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	at := ""
	switch filepath.Base(strings.Fields(editor + " x")[0]) {
	case "vi", "vim", "nvim", "nano", "emacs", "micro", "kak":
		if line > 0 {
			at = fmt.Sprintf(" +%d", line)
		}
	}
	cmd := exec.Command("sh", "-c", editor+at+` "$1"`, "sh", path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd
}
