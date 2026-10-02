package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"
)

// One daemon owns every running pomodoro, timer and alarm, and plays the
// sounds. Each promo session is a client that attaches to it over a unix
// socket, so closing a session doesn't stop anything.

const (
	ringPomodoro = "pomodoro"
	ringTimer    = "timer"
)

// State is everything the daemon runs; clients render it.
type State struct {
	Seq    uint64     `json:"seq"`            // bumped on every change, so clients drop stale copies
	Ring   string     `json:"ring,omitempty"` // pomodoro or timer whose end sound is looping
	Pomo   pomodoro   `json:"pomo"`
	Timer  timerModel `json:"timer"`
	Alarms []alarm    `json:"alarms"` // sorted by target time
}

func (s *State) alarm(id int) *alarm {
	for i := range s.Alarms {
		if s.Alarms[i].ID == id {
			return &s.Alarms[i]
		}
	}
	return nil
}

func (s *State) ringingAlarm() *alarm {
	for i := range s.Alarms {
		if s.Alarms[i].Ringing {
			return &s.Alarms[i]
		}
	}
	return nil
}

// request is one action sent by a client. Only the fields an op needs are set.
type request struct {
	Op    string        `json:"op"`
	ID    int           `json:"id,omitempty"`
	Phase pomoPhase     `json:"phase,omitempty"`
	Dur   time.Duration `json:"dur,omitempty"`
	At    time.Time     `json:"at"`
}

func socketPath() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "promo.sock")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("promo-%d.sock", os.Getuid()))
}

type daemon struct {
	mu        sync.Mutex
	cfgPath   string
	cfg       Config
	st        State
	nextID    int
	ring      *soundLoop // pomodoro/timer end sound
	alarmLoop *soundLoop // loops while any alarm rings
	ln        net.Listener
	watchers  map[net.Conn]*json.Encoder
}

func runDaemon(cfgPath string, cfg Config) error {
	sock := socketPath()
	if c, err := net.Dial("unix", sock); err == nil {
		c.Close()
		return errors.New("promo daemon is already running")
	}
	os.Remove(sock) // stale socket from a daemon that didn't shut down cleanly
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	d := &daemon{cfgPath: cfgPath, cfg: cfg, ln: ln, watchers: map[net.Conn]*json.Encoder{}}

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
	d.alarmLoop.Stop()
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
	if d.st.ringingAlarm() == nil {
		d.alarmLoop.Stop()
		d.alarmLoop = nil
	} else if d.alarmLoop == nil {
		d.alarmLoop = startSoundLoop(d.cfg.soundFor(true))
	}
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
	changed = d.st.Timer.check(d) || changed
	now := time.Now()
	for i := range d.st.Alarms {
		al := &d.st.Alarms[i]
		if al.Ringing || now.Before(al.Target) {
			continue
		}
		al.Ringing, changed = true, true
		notify(d.cfg, "Alarm", "It's "+al.Target.Format("15:04")+".", "critical")
	}
	if changed {
		d.changed()
	}
}

// startRing loops the end-of-phase sound until a session silences it.
func (d *daemon) startRing(kind string) {
	d.stopRing()
	d.ring = startSoundLoop(d.cfg.soundFor(false))
	d.st.Ring = kind
}

func (d *daemon) stopRing() {
	d.ring.Stop()
	d.ring = nil
	d.st.Ring = ""
}

func (d *daemon) apply(r request) {
	p, t := &d.st.Pomo, &d.st.Timer
	switch r.Op {
	case "reload":
		if cfg, _, err := loadConfig(d.cfgPath); err == nil {
			d.cfg = cfg
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

	case "timer.begin":
		t.begin(r.Dur)
		if d.st.Ring == ringTimer {
			d.stopRing()
		}
	case "timer.pause":
		if t.Active && !t.Done {
			t.CD.Toggle()
		}
	case "timer.len":
		t.CD.Adjust(r.Dur-t.CD.Total, time.Second)
	case "timer.stop":
		t.Active = false
		if d.st.Ring == ringTimer {
			d.stopRing()
		}

	case "alarm.add":
		d.nextID++
		d.st.Alarms = append(d.st.Alarms, alarm{ID: d.nextID, SetAt: time.Now(), Target: r.At})
	case "alarm.remove":
		d.st.Alarms = slices.DeleteFunc(d.st.Alarms, func(al alarm) bool { return al.ID == r.ID })
	case "alarm.snooze":
		if al := d.st.alarm(r.ID); al != nil {
			al.Ringing = false
			al.SetAt, al.Target = time.Now(), time.Now().Add(dur(d.cfg.Alarm.Snooze))
		}
	}
	slices.SortStableFunc(d.st.Alarms, func(a, b alarm) int { return a.Target.Compare(b.Target) })
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

// ensureDaemon starts the daemon in its own session when none is running.
func ensureDaemon() error {
	if daemonRunning() {
		return nil
	}
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
	return errors.New("daemon did not start (try running `promo daemon` to see why)")
}
