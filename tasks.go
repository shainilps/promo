package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Tasks live in one markdown file per list, grouped under a heading per day:
//
//	# work
//
//	## 2026-10-02 Fri
//
//	- [ ] 13:00 standup
//	- [ ] fix the login bug
//	- [x] review the pr
//
// A task with a time is a reminder: the daemon notifies when it is due.

const defaultList = "inbox"

type todo struct {
	ID     int       `json:"id"` // assigned by the daemon when the file is read; not stored
	List   string    `json:"list"`
	Text   string    `json:"text"`
	Due    time.Time `json:"due"` // midnight for plain tasks, the exact time for reminders
	Remind bool      `json:"remind"`
	Done   bool      `json:"done"`

	reminded bool // daemon only: the notification for this reminder went out
}

func (t todo) key() string {
	return t.List + "\x00" + t.Text + "\x00" + t.Due.Format(time.RFC3339)
}

func dayOf(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

// compareTodos orders by day, then open before done, reminders (by time)
// before plain tasks, then the order they were added in.
func compareTodos(a, b todo) int {
	if c := dayOf(a.Due).Compare(dayOf(b.Due)); c != 0 {
		return c
	}
	if a.Done != b.Done {
		return boolInt(a.Done) - boolInt(b.Done)
	}
	if a.Remind != b.Remind {
		return boolInt(b.Remind) - boolInt(a.Remind)
	}
	if c := a.Due.Compare(b.Due); c != 0 {
		return c
	}
	return a.ID - b.ID
}

var badListChars = regexp.MustCompile(`[^a-z0-9_-]+`)

// cleanList turns a list name into something safe to use as a file name.
func cleanList(s string) string {
	s = strings.Trim(badListChars.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-"), "-")
	if s == "" {
		return defaultList
	}
	return s
}

// parseClock accepts 07:30, 7:30, 19:05, 7pm, 7:30pm and 7:30 PM.
func parseClock(s string) (hour, minute int, err error) {
	s = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	for _, layout := range []string{"15:04", "3:04pm", "3pm", "15"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Hour(), t.Minute(), nil
		}
	}
	return 0, 0, fmt.Errorf("invalid time %q (try 07:30 or 7:30pm)", s)
}

// parseDay reads today, tomorrow, yesterday, 9/10/2029 or 9/10 (this year), plus the
// 2026-10-02 form the list files use. Dates are day/month.
func parseDay(s string, today time.Time) (time.Time, bool) {
	switch s {
	case "today":
		return today, true
	case "tomorrow", "tmr", "tmrw":
		return today.AddDate(0, 0, 1), true
	case "yesterday":
		return today.AddDate(0, 0, -1), true
	}
	if t, err := time.ParseInLocation("2/1/2006", s, time.Local); err == nil {
		return t, true
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, true
	}
	if t, err := time.ParseInLocation("2/1", s, time.Local); err == nil {
		return time.Date(today.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local), true
	}
	return time.Time{}, false
}

const whenHelp = "try 13:00, tomorrow, tomorrow-9am, 9/10/2029 or 9/10/2029-13:00"

// parseWhen reads a task's --time. Empty means a plain task for today; a
// date gives a plain task on that day; a time (with or without a date in
// front, joined by -) makes it a reminder. Past times and dates are refused.
func parseWhen(s string, now time.Time) (due time.Time, remind bool, err error) {
	s = strings.ToLower(strings.TrimSpace(s))
	today := dayOf(now)
	if s == "" {
		return today, false, nil
	}
	at := func(day time.Time, h, m int) time.Time {
		return time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, time.Local)
	}
	ok := false
	if i := strings.LastIndex(s, "-"); i > 0 {
		day, dayOK := parseDay(s[:i], today)
		h, m, err := parseClock(s[i+1:])
		if dayOK && err == nil {
			due, remind, ok = at(day, h, m), true, true
		}
	}
	if !ok {
		if day, dayOK := parseDay(s, today); dayOK {
			due, ok = day, true
		} else if h, m, err := parseClock(s); err == nil {
			due, remind, ok = at(today, h, m), true, true
		}
	}
	switch {
	case !ok:
		return due, false, fmt.Errorf("can't read time %q (%s)", s, whenHelp)
	case remind && !due.After(now):
		return due, false, fmt.Errorf("%s has already passed", due.Format("Mon 02 Jan 15:04"))
	case !remind && due.Before(today):
		return due, false, fmt.Errorf("%s has already passed", due.Format("Mon 02 Jan 2006"))
	}
	return due, remind, nil
}

// describeWhen is the short human form of a task's date, e.g. "today 13:00".
func describeWhen(t todo, now time.Time) string {
	day, today := dayOf(t.Due), dayOf(now)
	var s string
	switch {
	case day.Equal(today):
		s = "today"
	case day.Equal(today.AddDate(0, 0, 1)):
		s = "tomorrow"
	case day.Year() == today.Year():
		s = day.Format("Mon 02 Jan")
	default:
		s = day.Format("Mon 02 Jan 2006")
	}
	if t.Remind {
		s += " " + t.Due.Format("15:04")
	}
	return s
}

// isOverdue reports an unfinished task whose time has passed: a reminder
// earlier than now, or any task from a past day.
func isOverdue(t todo, now time.Time) bool {
	switch {
	case t.Done:
		return false
	case t.Remind:
		return t.Due.Before(now)
	}
	return dayOf(t.Due).Before(dayOf(now))
}

// overdueWhen says when an overdue task was due, e.g. "today 13:00" or "01 Oct".
func overdueWhen(t todo, now time.Time) string {
	s := "today"
	switch day := dayOf(t.Due); {
	case day.Year() != now.Year():
		s = day.Format("02 Jan 2006")
	case !day.Equal(dayOf(now)):
		s = day.Format("02 Jan")
	}
	if t.Remind {
		s += " " + t.Due.Format("15:04")
	}
	return s
}

// taskGroup is one heading in the task list: overdue, or a day.
type taskGroup struct {
	title   string
	overdue bool
	items   []todo
}

// groupTasks puts overdue tasks first (oldest on top), then one group per
// day from today on. Finished tasks from past days are left out.
// list "" means every list.
func groupTasks(todos []todo, list string, now time.Time) []taskGroup {
	today := dayOf(now)
	var mine []todo
	for _, t := range todos {
		if list == "" || t.List == list {
			mine = append(mine, t)
		}
	}
	slices.SortFunc(mine, compareTodos)

	var groups []taskGroup
	overdue := taskGroup{overdue: true}
	for _, t := range mine {
		day := dayOf(t.Due)
		if isOverdue(t, now) {
			overdue.items = append(overdue.items, t)
			continue
		}
		if day.Before(today) {
			continue
		}
		if n := len(groups); n == 0 || !dayOf(groups[n-1].items[0].Due).Equal(day) {
			groups = append(groups, taskGroup{title: dayTitle(day, today)})
		}
		groups[len(groups)-1].items = append(groups[len(groups)-1].items, t)
	}
	if n := len(overdue.items); n > 0 {
		slices.SortStableFunc(overdue.items, func(a, b todo) int { return a.Due.Compare(b.Due) })
		overdue.title = fmt.Sprintf("OVERDUE · %d", n)
		groups = append([]taskGroup{overdue}, groups...)
	}
	return groups
}

func dayTitle(day, today time.Time) string {
	date := day.Format("02 Jan")
	if day.Year() != today.Year() {
		date = day.Format("02 Jan 2006")
	}
	name := strings.ToUpper(day.Format("Monday"))
	switch {
	case day.Equal(today):
		name, date = "TODAY", day.Format("Mon ")+date
	case day.Equal(today.AddDate(0, 0, 1)):
		name, date = "TOMORROW", day.Format("Mon ")+date
	case day.Equal(today.AddDate(0, 0, -1)):
		name, date = "YESTERDAY", day.Format("Mon ")+date
	}
	return name + " · " + date
}

// taskStore reads and writes the markdown files in one folder.
type taskStore struct {
	dir    string
	nextID int
	seen   map[string]time.Time // modification time of each file as last read or written
}

func (s *taskStore) path(list string) string { return filepath.Join(s.dir, list+".md") }

// scan lists the .md files with their modification times.
func (s *taskStore) scan() map[string]time.Time {
	files := map[string]time.Time{}
	entries, _ := os.ReadDir(s.dir)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".md") {
			continue
		}
		if info, err := e.Info(); err == nil {
			files[strings.TrimSuffix(name, ".md")] = info.ModTime()
		}
	}
	return files
}

// changedOnDisk reports whether a file was edited, added or removed by
// something other than the daemon.
func (s *taskStore) changedOnDisk() bool {
	now := s.scan()
	if len(now) != len(s.seen) {
		return true
	}
	for name, mt := range now {
		if prev, ok := s.seen[name]; !ok || !prev.Equal(mt) {
			return true
		}
	}
	return false
}

// load reads every list. The returned list names include empty files.
func (s *taskStore) load() ([]todo, []string, error) {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return nil, nil, err
	}
	s.seen = s.scan()
	var todos []todo
	var lists []string
	for name := range s.seen {
		f, err := os.Open(s.path(name))
		if err != nil {
			return nil, nil, err
		}
		todos = append(todos, s.parse(name, f)...)
		f.Close()
		lists = append(lists, name)
	}
	slices.Sort(lists)
	return todos, lists, nil
}

var (
	dayHeading = regexp.MustCompile(`^##\s+(\d{4}-\d{2}-\d{2})`)
	taskLine   = regexp.MustCompile(`^\s*[-*]\s+\[([ xX])\]\s+(.*)$`)
	timePrefix = regexp.MustCompile(`(?i)^(\d{1,2}:\d{2}(?:\s?[ap]m)?|\d{1,2}\s?[ap]m)\s+(.+)$`)
)

// parse reads one list file. Tasks above the first date heading count as
// today's; anything that isn't a heading or a task line is ignored.
func (s *taskStore) parse(list string, f *os.File) []todo {
	day := dayOf(time.Now())
	var todos []todo
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if m := dayHeading.FindStringSubmatch(line); m != nil {
			if d, err := time.ParseInLocation("2006-01-02", m[1], time.Local); err == nil {
				day = d
			}
			continue
		}
		m := taskLine.FindStringSubmatch(line)
		if m == nil || strings.TrimSpace(m[2]) == "" {
			continue
		}
		s.nextID++
		t := todo{ID: s.nextID, List: list, Text: strings.TrimSpace(m[2]), Due: day, Done: m[1] != " "}
		if tm := timePrefix.FindStringSubmatch(t.Text); tm != nil {
			if h, mm, err := parseClock(tm[1]); err == nil {
				t.Due = day.Add(time.Duration(h)*time.Hour + time.Duration(mm)*time.Minute)
				t.Remind, t.Text = true, tm[2]
			}
		}
		todos = append(todos, t)
	}
	return todos
}

// save rewrites one list's file from the tasks in it, or removes the file
// once the list is empty.
func (s *taskStore) save(list string, todos []todo) error {
	var mine []todo
	for _, t := range todos {
		if t.List == list {
			mine = append(mine, t)
		}
	}
	path := s.path(list)
	if len(mine) == 0 {
		err := os.Remove(path)
		if os.IsNotExist(err) {
			err = nil
		}
		s.seen = s.scan()
		return err
	}
	slices.SortFunc(mine, compareTodos)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# %s\n", list)
	var day time.Time
	for _, t := range mine {
		if d := dayOf(t.Due); !d.Equal(day) {
			day = d
			fmt.Fprintf(&buf, "\n## %s\n\n", day.Format("2006-01-02 Mon"))
		}
		box := " "
		if t.Done {
			box = "x"
		}
		text := t.Text
		if t.Remind {
			text = t.Due.Format("15:04") + " " + text
		}
		fmt.Fprintf(&buf, "- [%s] %s\n", box, text)
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	err := writeFileAtomic(path, buf.Bytes())
	s.seen = s.scan()
	return err
}
