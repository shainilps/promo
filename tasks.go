package main

import (
	"bufio"
	"bytes"
	"cmp"
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
//
// Tasks under a repeat heading (## every weekday, ## daily, ## mon-fri)
// are routines. They sit at the top of the file:
//
//	## every weekday
//
//	- [ ] 09:30 check in
//	- [ ] 18:00 check out
//
// On each day a routine falls on, the daemon gives it a task under that
// day, so the file is also its log. One left unchecked is overdue that day
// only; after that it marks a missed day.

const defaultList = "inbox"

type todo struct {
	ID     int       `json:"id"` // assigned by the daemon when the file is read; not stored
	List   string    `json:"list"`
	Text   string    `json:"text"`
	Due    time.Time `json:"due"` // midnight for plain tasks, the exact time for reminders
	Remind bool      `json:"remind"`
	Done   bool      `json:"done"`
	Every  repeat    `json:"every,omitempty"` // a routine: the days it repeats on; Due holds only the time
	Of     int       `json:"of,omitempty"`    // a routine's task for one day: the routine's ID; not stored

	reminded bool      // daemon only: the notification for this reminder went out
	since    time.Time // daemon only: when a routine was added or rescheduled; it isn't due before then
}

func (t todo) key() string {
	return t.List + "\x00" + t.Text + "\x00" + t.Due.Format(time.RFC3339)
}

// repeat is the weekdays a routine falls on, bit i for time.Weekday(i).
type repeat uint8

const (
	everyDay repeat = 0x7f
	weekdays repeat = 0x3e
	weekends repeat = 0x41
)

func (r repeat) on(day time.Time) bool { return r&(1<<day.Weekday()) != 0 }

var weekdayNames = [7]string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// String is what follows "every": day, weekday, weekend, or the days
// from Monday on, with runs of three or more as a range: mon-wed, fri.
func (r repeat) String() string {
	switch r {
	case everyDay:
		return "day"
	case weekdays:
		return "weekday"
	case weekends:
		return "weekend"
	}
	var parts []string
	for i := 0; i < 7; {
		if !r.on(monFirst(i)) {
			i++
			continue
		}
		j := i
		for j+1 < 7 && r.on(monFirst(j+1)) {
			j++
		}
		switch name := func(k int) string { return weekdayNames[monFirst(k).Weekday()] }; {
		case j-i >= 2:
			parts = append(parts, name(i)+"-"+name(j))
		case j > i:
			parts = append(parts, name(i), name(j))
		default:
			parts = append(parts, name(i))
		}
		i = j + 1
	}
	return strings.Join(parts, ", ")
}

// monFirst is the i-th day of a week that starts on Monday (any such week).
func monFirst(i int) time.Time { return time.Date(2024, 1, 1+i, 0, 0, 0, 0, time.Local) }

// flag is the form --time takes: daily, weekdays, weekends, every-mon,thu.
func (r repeat) flag() string {
	switch r {
	case everyDay:
		return "daily"
	case weekdays, weekends:
		return r.String() + "s"
	}
	return "every-" + strings.ReplaceAll(r.String(), ", ", ",")
}

// parseRepeat reads when a routine repeats, and is forgiving about it:
// daily, every day, weekdays, weekends, mon-fri, mon to fri, mon, wed and
// fri, every monday, every tue,thu. One bare day (monday) isn't a repeat,
// since it reads like a date; that's every monday.
func parseRepeat(s string) (repeat, bool) {
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	rest, every := strings.CutPrefix(s, "every")
	if every {
		rest = strings.TrimLeft(rest, " -")
	}
	switch rest {
	case "day", "days", "daily":
		return everyDay, true
	case "weekday", "weekdays", "workday", "workdays":
		return weekdays, true
	case "weekend", "weekends":
		return weekends, true
	}
	day := func(name string) (int, bool) { // mon, tues, thursday, fridays ...
		for _, n := range []string{name, strings.TrimSuffix(name, "s")} {
			for i := range 7 {
				if full := strings.ToLower(monFirst(i).Weekday().String()); len(n) >= 3 && strings.HasPrefix(full, n) {
					return i, true
				}
			}
		}
		return 0, false
	}
	rest = strings.NewReplacer(" to ", "-", " - ", "-", " and ", ",", "&", ",", "/", ",").Replace(rest)
	parts := strings.FieldsFunc(rest, func(c rune) bool { return c == ',' || c == ' ' })
	if len(parts) == 0 || !every && len(parts) == 1 && !strings.Contains(parts[0], "-") {
		return 0, false
	}
	var r repeat
	for _, part := range parts {
		from, to, isRange := strings.Cut(part, "-")
		if !isRange {
			to = from
		}
		i, ok1 := day(from)
		j, ok2 := day(to)
		if !ok1 || !ok2 || j < i {
			return 0, false
		}
		for k := i; k <= j; k++ {
			r |= 1 << monFirst(k).Weekday()
		}
	}
	return r, r != 0
}

// clockOf is how a routine keeps its time: on a fixed date, so routines
// sort before every dated task.
func clockOf(h, m int) time.Time { return time.Date(2000, 1, 1, h, m, 0, 0, time.Local) }

// on puts t's time of day on day.
func on(day, t time.Time) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), t.Hour(), t.Minute(), 0, 0, time.Local)
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
	if a.Every != b.Every {
		return cmp.Or(a.Every.rank()-b.Every.rank(), int(a.Every)-int(b.Every))
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

// rank puts routines first, by schedule: daily, weekdays, weekends, the rest.
func (r repeat) rank() int {
	switch r {
	case 0:
		return 0
	case everyDay:
		return 1
	case weekdays:
		return 2
	case weekends:
		return 3
	}
	return 4
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

const whenHelp = "try 13:00, tomorrow, tomorrow 9am, 9/10/2029 13:00, daily 9am or weekdays 6pm"

// parseWhen reads a task's --time into the Due, Remind and Every of a
// task. Empty means a plain task for today; a date gives a plain task on
// that day; a time (alone, or with a date before or after it, joined by a
// space or -) makes it a reminder. In place of the date, a repeat (daily,
// weekdays, every mon, thu) makes it a routine. Past times are refused.
func parseWhen(s string, now time.Time) (todo, error) {
	s = strings.Join(strings.Fields(strings.ReplaceAll(" "+strings.ToLower(s)+" ", " at ", " ")), " ")
	today := dayOf(now)
	if s == "" {
		return todo{Due: today}, nil
	}
	// with reads a day (or repeat) and a time.
	with := func(when, clock string) (todo, bool) {
		h, m, err := parseClock(clock)
		if err != nil {
			return todo{}, false
		}
		if day, ok := parseDay(when, today); ok {
			return todo{Due: on(day, clockOf(h, m)), Remind: true}, true
		}
		if every, ok := parseRepeat(when); ok {
			return todo{Due: clockOf(h, m), Remind: true, Every: every}, true
		}
		return todo{}, false
	}
	var t todo
	ok := false
	if day, dayOK := parseDay(s, today); dayOK {
		t, ok = todo{Due: day}, true
	} else if every, everyOK := parseRepeat(s); everyOK {
		t, ok = todo{Due: clockOf(0, 0), Every: every}, true
	} else if h, m, err := parseClock(s); err == nil {
		t, ok = todo{Due: on(today, clockOf(h, m)), Remind: true}, true
	}
	for i := len(s) - 1; i > 0 && !ok; i-- {
		if s[i] == ' ' || s[i] == '-' {
			if t, ok = with(s[:i], s[i+1:]); !ok {
				t, ok = with(s[i+1:], s[:i]) // 9am tomorrow, 6pm weekdays
			}
		}
	}
	switch {
	case !ok:
		return t, fmt.Errorf("can't read time %q (%s)", s, whenHelp)
	case t.Every != 0:
	case t.Remind && !t.Due.After(now):
		return t, fmt.Errorf("%s has already passed", t.Due.Format("Mon 02 Jan 15:04"))
	case !t.Remind && t.Due.Before(today):
		return t, fmt.Errorf("%s has already passed", t.Due.Format("Mon 02 Jan 2006"))
	}
	return t, nil
}

// kind names a task in messages: task, reminder, daily reminder ...
func (t todo) kind() string {
	s := "task"
	if t.Remind {
		s = "reminder"
	}
	if t.Every != 0 {
		s = "repeating " + s
	}
	return s
}

// describeWhen is the short human form of a task's date, e.g. "today 13:00".
func describeWhen(t todo, now time.Time) string {
	if t.Every != 0 {
		return strings.TrimSpace("every " + t.Every.String() + " " + clockText(t))
	}
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

// clockText is a reminder's time, or nothing for a plain task.
func clockText(t todo) string {
	if !t.Remind {
		return ""
	}
	return t.Due.Format("15:04")
}

// isOverdue reports an unfinished task whose time has passed: a reminder
// earlier than now, or any task from a past day.
func isOverdue(t todo, now time.Time) bool {
	switch {
	case t.Done || t.Every != 0:
		return false
	case t.Of != 0 && dayOf(t.Due).Before(dayOf(now)):
		return false // a day a routine was missed; it doesn't carry over
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

// taskGroup is one heading in the task list: overdue, a day, or the routines.
type taskGroup struct {
	title   string
	overdue bool
	items   []todo
}

// groupTasks puts overdue tasks first (oldest on top), then one group per
// day from today on, then the routines. Finished tasks from past days are
// left out.
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
	var routines []todo
	for _, t := range mine {
		day := dayOf(t.Due)
		if t.Every != 0 {
			routines = append(routines, t)
			continue
		}
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
	if len(routines) > 0 {
		slices.SortStableFunc(routines, func(a, b todo) int { return a.Due.Compare(b.Due) })
		groups = append(groups, taskGroup{title: fmt.Sprintf("REPEATING · %d", len(routines)), items: routines})
	}
	return groups
}

// syncRoutines ties each routine to its task on every day it falls on,
// and gives it one for today if it has none. A task belongs to a routine
// when list, text and time match. Unchecked ones from past days stay, as
// the days the routine was missed. When a routine changes, its tasks follow
// it, or go when they no longer fit (finished ones from past days are kept
// as they were, as plain tasks). A routine added after its time today
// starts tomorrow, so it isn't overdue the moment it's made. New tasks take
// IDs from nextID. It returns the lists it changed.
func syncRoutines(todos []todo, now time.Time, nextID *int) ([]todo, []string) {
	today := dayOf(now)
	var routines []todo
	byID := map[int]todo{}
	for _, t := range todos {
		if t.Every != 0 {
			routines = append(routines, t)
			byID[t.ID] = t
		}
	}
	type slot struct {
		id  int
		day time.Time
	}
	taken := map[slot]bool{}
	same := func(r, t todo) bool {
		return t.List == r.List && t.Text == r.Text && clockText(t) == clockText(r)
	}
	late := func(r todo) bool { return r.Remind && on(today, r.Due).Before(r.since) }
	var touched []string
	out := make([]todo, 0, len(todos)+len(routines))
	for _, t := range todos {
		day := dayOf(t.Due)
		if t.Every != 0 || day.After(today) {
			out = append(out, t)
			continue
		}
		if t.Of == 0 { // read from a file: find its routine
			for _, r := range routines {
				if same(r, t) && r.Every.on(day) && !taken[slot{r.ID, day}] {
					t.Of = r.ID
					break
				}
			}
		}
		if t.Of != 0 {
			r, ok := byID[t.Of]
			fits := ok && r.Every.on(day) && !taken[slot{r.ID, day}] && !(day.Equal(today) && !t.Done && late(r))
			switch {
			case fits && (same(r, t) || day.Equal(today) || !t.Done):
				if !same(r, t) {
					due := on(day, r.Due)
					if !due.Equal(t.Due) {
						t.reminded = !due.After(now)
					}
					touched = append(touched, t.List, r.List)
					t.List, t.Text, t.Remind, t.Due = r.List, r.Text, r.Remind, due
				}
				taken[slot{r.ID, day}] = true
			case t.Done:
				t.Of = 0
			default:
				touched = append(touched, t.List)
				continue
			}
		}
		out = append(out, t)
	}
	for _, r := range routines {
		if !r.Every.on(today) || taken[slot{r.ID, today}] || late(r) {
			continue
		}
		*nextID++
		due := on(today, r.Due)
		out = append(out, todo{ID: *nextID, List: r.List, Text: r.Text, Due: due, Remind: r.Remind, Of: r.ID, reminded: !due.After(now)})
		touched = append(touched, r.List)
	}
	slices.Sort(touched)
	return out, slices.Compact(touched)
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
	anyHeading = regexp.MustCompile(`^##\s+(.+)$`)
	taskLine   = regexp.MustCompile(`^\s*[-*]\s+\[([ xX])\]\s+(.*)$`)
	timePrefix = regexp.MustCompile(`(?i)^(\d{1,2}:\d{2}(?:\s?[ap]m)?|\d{1,2}\s?[ap]m)\s+(.+)$`)
)

// parse reads one list file. Tasks above the first date heading count as
// today's, and ones under a repeat heading (## every weekday, ## daily,
// ## mon-fri) are routines; anything that isn't a heading or a task line
// is ignored.
func (s *taskStore) parse(list string, f *os.File) []todo {
	day := dayOf(time.Now())
	var every repeat
	var todos []todo
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if m := dayHeading.FindStringSubmatch(line); m != nil {
			if d, err := time.ParseInLocation("2006-01-02", m[1], time.Local); err == nil {
				day, every = d, 0
			}
			continue
		}
		if m := anyHeading.FindStringSubmatch(line); m != nil {
			if r, ok := parseRepeat(m[1]); ok {
				every = r
			}
			continue
		}
		m := taskLine.FindStringSubmatch(line)
		if m == nil || strings.TrimSpace(m[2]) == "" {
			continue
		}
		s.nextID++
		t := todo{ID: s.nextID, List: list, Text: strings.TrimSpace(m[2]), Due: day, Done: m[1] != " "}
		if every != 0 {
			t.Due, t.Done, t.Every = clockOf(0, 0), false, every
		}
		if tm := timePrefix.FindStringSubmatch(t.Text); tm != nil {
			if h, mm, err := parseClock(tm[1]); err == nil {
				t.Due = on(t.Due, clockOf(h, mm))
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
		delete(s.seen, list)
		return err
	}
	slices.SortFunc(mine, compareTodos)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# %s\n", list)
	heading := ""
	for _, t := range mine {
		h := dayOf(t.Due).Format("2006-01-02 Mon")
		if t.Every != 0 {
			h = "every " + t.Every.String()
		}
		if h != heading {
			heading = h
			fmt.Fprintf(&buf, "\n## %s\n\n", heading)
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
	if err := writeFileAtomic(path, buf.Bytes()); err != nil {
		return err
	}
	// Only this file is marked as read: a hand edit to another one still
	// has to be picked up.
	if info, err := os.Stat(path); err == nil {
		if s.seen == nil {
			s.seen = map[string]time.Time{}
		}
		s.seen[list] = info.ModTime()
	}
	return nil
}
