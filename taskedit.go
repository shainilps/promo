package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// gg add [list] with no text opens $EDITOR on a draft in the same markdown
// as gg itself, so several tasks can be written at once. Everything in it
// is added.

const draftHelp = `<!--
  write your tasks, then save and quit. leave it empty to add nothing.

  # work                  the list for the tasks below (a new name makes a new list)
  ## tomorrow             the day for the tasks below: today, tomorrow, friday, 9/10, 9/10/2029
  ## weekdays             the tasks below repeat: daily, weekdays, weekends, mon-fri, every mon, thu
  - [ ] review the pr     a task
  - [ ] 13:00 dentist     a reminder at 13:00 (9am and 7:30pm work too)
-->
`

func draftTemplate(list, day string) string {
	if day == "" {
		day = "today"
	}
	return fmt.Sprintf("# %s\n\n## %s\n\n- [ ] \n\n%s", list, day, draftHelp)
}

// parseDraft reads the tasks written in the editor. They're new, so a time
// or day that has already passed is refused.
func parseDraft(text, list string, now time.Time) ([]todo, error) {
	todos, err := parseDoc(text, list, now)
	if err != nil {
		return nil, err
	}
	for _, t := range todos {
		switch {
		case t.Every != 0:
		case t.Remind && !t.Due.After(now):
			return nil, &lineError{t.line, t.Due.Format("Mon 02 Jan 15:04") + " has already passed"}
		case !t.Remind && dayOf(t.Due).Before(dayOf(now)):
			return nil, &lineError{t.line, t.Due.Format("Mon 02 Jan 2006") + " has already passed"}
		}
	}
	return todos, nil
}

// editTasks opens the draft in the editor and adds what was written.
func editTasks(list, when string, in io.Reader, out io.Writer) error {
	if when != "" {
		if t, err := parseWhen(when, time.Now()); err != nil {
			return err
		} else if t.Remind || t.Every != 0 {
			return fmt.Errorf("--time with the editor takes a day, not a time or a repeat (put those in the draft)")
		}
	}
	f, err := os.CreateTemp("", "gg-add-*.md")
	if err != nil {
		return err
	}
	path := f.Name()
	defer os.Remove(path)
	f.WriteString(draftTemplate(list, strings.TrimSpace(when)))
	f.Close()

	return editUntilRead(path, "<!-- gg: %s -->", in, out, func(text string) error {
		todos, err := parseDraft(text, list, time.Now())
		if err != nil {
			return err
		}
		return addDrafted(todos, out)
	})
}

func addDrafted(todos []todo, out io.Writer) error {
	if len(todos) == 0 {
		fmt.Fprintln(out, "nothing added")
		return nil
	}
	now := time.Now()
	for _, t := range todos {
		if _, err := call(request{Op: "todo.add", Text: t.Text, List: t.List, At: t.Due, Remind: t.Remind, Every: t.Every}); err != nil {
			return err
		}
		fmt.Fprintf(out, "added %s to %s · %s · %s\n", t.kind(), t.List, describeWhen(t, now), t.Text)
	}
	return nil
}
