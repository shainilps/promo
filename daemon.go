package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// One daemon owns the pomodoro and the task lists, and plays the sounds
// and sends the reminders. Each gg session is a client that attaches to it over a unix
// socket, so closing a session doesn't stop anything.

const ringPomodoro = "pomodoro"

// State is everything the daemon runs; clients render it.
type State struct {
	Build string   `json:"build"`          // which gg binary the daemon runs; see ensureDaemon
	Seq   uint64   `json:"seq"`            // bumped on every change, so clients drop stale copies
	Ring  string   `json:"ring,omitempty"` // "pomodoro" while its end sound loops
	Pomo  pomodoro `json:"pomo"`

	Todos    []todo   `json:"todos"`
	Lists    []string `json:"lists"`     // every list file, including empty ones
	TasksDir string   `json:"tasks_dir"` // where the list files live
	TaskErr  string   `json:"task_err,omitempty"`
}

// request is one action sent by a client. Only the fields an op needs are set.
type request struct {
	Op    string        `json:"op"`
	ID    int           `json:"id,omitempty"`
	Phase pomoPhase     `json:"phase,omitempty"`
	Dur   time.Duration `json:"dur,omitempty"`
	At    time.Time     `json:"at"`

	Text   string `json:"text,omitempty"`
	List   string `json:"list,omitempty"`
	Remind bool   `json:"remind,omitempty"`

	State *State `json:"state,omitempty"` // restore: what the previous daemon was running
}

func socketPath() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "gg.sock")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("gg-%d.sock", os.Getuid()))
}

type daemon struct {
	mu        sync.Mutex
	cfgPath   string
	cfg       Config
	st        State
	ring      *soundLoop // pomodoro end sound
	ln        net.Listener
	watchers  map[net.Conn]*json.Encoder
	tasks     taskStore
	ticks     int
	lastNag   time.Time // last overdue nudge (or daemon start)
	ringSince time.Time // when the pomodoro end sound started
}

func runDaemon(cfgPath string, cfg Config) error {
	sock := socketPath()
	if c, err := net.Dial("unix", sock); err == nil {
		c.Close()
		return errors.New("gg daemon is already running")
	}
	os.Remove(sock) // stale socket from a daemon that didn't shut down cleanly
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	d := &daemon{cfgPath: cfgPath, cfg: cfg, ln: ln, watchers: map[net.Conn]*json.Encoder{}, lastNag: time.Now()}
	if err := d.loadTasks(); err != nil {
		ln.Close()
		return fmt.Errorf("reading tasks: %w", err)
	}
	d.st.Build = buildID()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		ln.Close()
	}()
	go func() {
		for range time.Tick(250 * time.Millisecond) {
			d.tick()
		}
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			break
		}
		go d.serve(conn)
	}
	d.mu.Lock()
	d.ring.Stop()
	d.mu.Unlock()
	return nil
}

func (d *daemon) serve(conn net.Conn) {
	defer conn.Close()
	dec, enc := json.NewDecoder(conn), json.NewEncoder(conn)
	for {
		var req request
		if err := dec.Decode(&req); err != nil {
			d.mu.Lock()
			delete(d.watchers, conn)
			d.mu.Unlock()
			return
		}
		d.mu.Lock()
		switch req.Op {
		case "watch":
			d.watchers[conn] = enc
		case "get":
		default:
			d.apply(req)
			d.changed()
		}
		conn.SetWriteDeadline(time.Now().Add(time.Second))
		enc.Encode(d.st)
		d.mu.Unlock()
		if req.Op == "stop" {
			d.ln.Close()
		}
	}
}

// changed publishes the state to every attached session. Callers hold d.mu.
func (d *daemon) changed() {
	d.st.Seq++
	for conn, enc := range d.watchers {
		conn.SetWriteDeadline(time.Now().Add(time.Second))
		if enc.Encode(d.st) != nil {
			conn.Close()
			delete(d.watchers, conn)
		}
	}
}

func (d *daemon) tick() {
	d.mu.Lock()
	defer d.mu.Unlock()
	changed := d.st.Pomo.check(d)
	now := time.Now()
	// Pick up list files edited by hand every couple of seconds.
	if d.ticks++; d.ticks%8 == 0 && d.tasks.changedOnDisk() {
		d.reportTaskErr(d.loadTasks())
		changed = true
	}
	for i := range d.st.Todos {
		t := &d.st.Todos[i]
		if !t.Remind || t.Done || t.reminded || now.Before(t.Due) {
			continue
		}
		t.reminded, changed = true, true
		notify(d.cfg, "Reminder", t.Text+"  ·  "+t.List, "critical")
		if path := d.cfg.soundFor(); path != "" {
			start("pw-play", path)
		}
	}
	if every, ok := d.cfg.nagEvery(); ok && now.Sub(d.lastNag) >= every {
		d.nagOverdue(now)
	}
	// The end sound doesn't ring forever: it goes quiet after ring_for.
	if limit, ok := d.cfg.ringFor(); ok && d.st.Ring != "" && now.Sub(d.ringSince) >= limit {
		d.stopRing()
		changed = true
	}
	if changed {
		d.changed()
	}
}

// startRing loops the end-of-phase sound until a session silences it.
func (d *daemon) startRing(kind string) {
	d.stopRing()
	d.ring = startSoundLoop(d.cfg.soundFor())
	d.st.Ring, d.ringSince = kind, time.Now()
}

func (d *daemon) stopRing() {
	d.ring.Stop()
	d.ring = nil
	d.st.Ring = ""
}

func (d *daemon) apply(r request) {
	p := &d.st.Pomo
	switch r.Op {
	case "reload":
		if cfg, _, err := loadConfig(d.cfgPath); err == nil {
			moved := cfg.TasksDir != d.cfg.TasksDir
			d.cfg = cfg
			if moved {
				d.reportTaskErr(d.loadTasks())
			}
		}
	case "restore": // take over from an older daemon that was replaced
		if old := r.State; old != nil {
			d.st.Pomo = old.Pomo
			if old.Ring == ringPomodoro {
				d.startRing(old.Ring)
			}
		}
	case "silence":
		d.stopRing()
	case "stop":
		d.stopRing()

	case "pomo.begin":
		if !p.Active {
			p.begin(d.cfg)
		}
	case "pomo.next":
		p.next()
	case "pomo.pause":
		if p.Active && !p.Waiting && !p.Ready {
			p.CD.Toggle()
		}
	case "pomo.len":
		p.setLen(r.Phase, r.Dur)
	case "pomo.skip":
		if p.Active && !p.Waiting && !p.Ready {
			p.finish(d, true)
		}
	case "pomo.restart":
		if p.Active && !p.Waiting && !p.Ready {
			p.CD = newCountdown(p.CD.Total)
		}
	case "pomo.stop":
		p.Active = false
		if d.st.Ring == ringPomodoro {
			d.stopRing()
		}

	case "todo.add":
		list := cleanList(r.List)
		d.tasks.nextID++
		text := strings.Join(strings.Fields(r.Text), " ")
		d.st.Todos = append(d.st.Todos, todo{ID: d.tasks.nextID, List: list, Text: text, Due: r.At, Remind: r.Remind})
		d.saveLists(list)
	case "todo.edit":
		for i := range d.st.Todos {
			t := &d.st.Todos[i]
			if t.ID != r.ID {
				continue
			}
			old := t.List
			if !t.Due.Equal(r.At) || t.Remind != r.Remind {
				t.reminded = r.Remind && !r.At.After(time.Now())
			}
			t.List, t.Text, t.Due, t.Remind = cleanList(r.List), strings.Join(strings.Fields(r.Text), " "), r.At, r.Remind
			d.saveLists(old, t.List)
			break
		}
	case "list.remove":
		list := cleanList(r.List)
		d.st.Todos = slices.DeleteFunc(d.st.Todos, func(t todo) bool { return t.List == list })
		d.saveLists(list)
	case "tasks.reload":
		d.reportTaskErr(d.loadTasks())
	case "todo.toggle":
		for i := range d.st.Todos {
			if t := &d.st.Todos[i]; t.ID == r.ID {
				t.Done = !t.Done
				d.saveLists(t.List)
			}
		}
	case "todo.remove":
		for i, t := range d.st.Todos {
			if t.ID == r.ID {
				d.st.Todos = slices.Delete(d.st.Todos, i, i+1)
				d.saveLists(t.List)
				break
			}
		}
	case "todo.clear": // drop finished tasks from one list, or every list
		var touched []string
		d.st.Todos = slices.DeleteFunc(d.st.Todos, func(t todo) bool {
			if t.Done && (r.List == "" || t.List == r.List) {
				touched = append(touched, t.List)
				return true
			}
			return false
		})
		slices.Sort(touched)
		d.saveLists(slices.Compact(touched)...)
	}
}

// loadTasks (re)reads every list file. Reminders keep their sent state
// across reloads, and ones already past when first read don't fire.
func (d *daemon) loadTasks() error {
	sent := map[string]bool{}
	for _, t := range d.st.Todos {
		sent[t.key()] = t.reminded
	}
	d.tasks.dir = expandHome(d.cfg.TasksDir)
	todos, lists, err := d.tasks.load()
	if err != nil {
		return err
	}
	now := time.Now()
	for i := range todos {
		t := &todos[i]
		r, known := sent[t.key()]
		t.reminded = r || (!known && t.Remind && t.Due.Before(now))
	}
	d.st.Todos, d.st.Lists, d.st.TasksDir = todos, lists, d.tasks.dir
	return nil
}

// nagOverdue sends one notification listing unchecked past-due tasks.
// Reminders that came due since the last nudge are skipped; they just got
// their own notification.
func (d *daemon) nagOverdue(now time.Time) {
	var late []todo
	for _, t := range d.st.Todos {
		if isOverdue(t, now) && t.Due.Before(d.lastNag) {
			late = append(late, t)
		}
	}
	d.lastNag = now
	if len(late) == 0 {
		return
	}
	slices.SortStableFunc(late, func(a, b todo) int { return a.Due.Compare(b.Due) })
	var lines []string
	for i, t := range late {
		if i == 5 {
			lines = append(lines, fmt.Sprintf("+%d more", len(late)-i))
			break
		}
		lines = append(lines, fmt.Sprintf("%s  %s  · %s", overdueWhen(t, now), t.Text, t.List))
	}
	title := "1 task still unchecked"
	if len(late) > 1 {
		title = fmt.Sprintf("%d tasks still unchecked", len(late))
	}
	notify(d.cfg, title, strings.Join(lines, "\n"), "normal")
}

// saveLists writes the given lists back to their files.
func (d *daemon) saveLists(lists ...string) {
	var err error
	for _, l := range lists {
		err = errors.Join(err, d.tasks.save(l, d.st.Todos))
	}
	d.reportTaskErr(err)
	d.st.Lists = slices.Sorted(maps.Keys(d.tasks.seen))
}

func (d *daemon) reportTaskErr(err error) {
	d.st.TaskErr = ""
	if err != nil {
		d.st.TaskErr = err.Error()
	}
}

// call sends one request to the daemon and returns the resulting state.
func call(req request) (State, error) {
	var s State
	conn, err := net.DialTimeout("unix", socketPath(), time.Second)
	if err != nil {
		return s, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return s, err
	}
	err = json.NewDecoder(conn).Decode(&s)
	return s, err
}

// watch streams every state change; the channel closes when the daemon goes away.
func watch() (<-chan State, error) {
	conn, err := net.DialTimeout("unix", socketPath(), time.Second)
	if err != nil {
		return nil, err
	}
	if err := json.NewEncoder(conn).Encode(request{Op: "watch"}); err != nil {
		conn.Close()
		return nil, err
	}
	ch := make(chan State, 16)
	go func() {
		defer conn.Close()
		defer close(ch)
		dec := json.NewDecoder(conn)
		for {
			var s State
			if dec.Decode(&s) != nil {
				return
			}
			ch <- s
		}
	}()
	return ch, nil
}

func daemonRunning() bool {
	conn, err := net.DialTimeout("unix", socketPath(), time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// buildID fingerprints the running binary, so a session can tell when the
// daemon was started from an older build.
func buildID() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	f, err := os.Open(exe)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// ensureDaemon starts the daemon when none is running, and replaces one
// started from a different build: an old daemon silently ignores requests
// it doesn't know (like editing a task). What it was running is handed over.
func ensureDaemon() error {
	if !daemonRunning() {
		return startDaemon()
	}
	old, err := call(request{Op: "get"})
	if err != nil || old.Build == buildID() {
		return err
	}
	if _, err := call(request{Op: "stop"}); err != nil {
		return fmt.Errorf("stopping the old daemon: %w", err)
	}
	for i := 0; daemonRunning(); i++ {
		if i == 100 {
			return errors.New("the old daemon didn't stop; run `gg stop` and try again")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := startDaemon(); err != nil {
		return err
	}
	_, err = call(request{Op: "restore", State: &old})
	return err
}

func startDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "daemon")
	cmd.Dir = "/"
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting daemon: %w", err)
	}
	go cmd.Wait()
	for range 100 {
		if daemonRunning() {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("daemon did not start (try running `gg daemon` to see why)")
}
