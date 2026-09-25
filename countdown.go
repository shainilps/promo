package main

import (
	"fmt"
	"time"
)

// countdown tracks remaining time against the wall clock, so it doesn't
// drift the way decrementing on every tick does.
type countdown struct {
	total     time.Duration
	endAt     time.Time
	remaining time.Duration // frozen value while paused
	paused    bool
}

func newCountdown(d time.Duration) countdown {
	return countdown{total: d, endAt: time.Now().Add(d)}
}

func (c countdown) Remaining() time.Duration {
	if c.paused {
		return c.remaining
	}
	return max(time.Until(c.endAt), 0)
}

func (c countdown) Done() bool { return c.Remaining() <= 0 }

func (c countdown) Percent() float64 {
	if c.total <= 0 {
		return 1
	}
	return min(max(1-c.Remaining().Seconds()/c.total.Seconds(), 0), 1)
}

func (c *countdown) Toggle() {
	if c.paused {
		c.endAt = time.Now().Add(c.remaining)
		c.paused = false
	} else {
		c.remaining = c.Remaining()
		c.paused = true
	}
}

// Adjust changes the total length by d, keeping the total at least min.
func (c *countdown) Adjust(d, minTotal time.Duration) {
	if c.total+d < minTotal {
		d = minTotal - c.total
	}
	c.total += d
	c.endAt = c.endAt.Add(d)
	if c.paused {
		c.remaining = max(c.remaining+d, 0)
	}
}

// formatDuration rounds up to whole seconds so a fresh 25m timer shows 25:00.
func formatDuration(d time.Duration) string {
	secs := int((max(d, 0) + time.Second - 1) / time.Second)
	h, m, s := secs/3600, secs/60%60, secs%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

// shortDuration renders durations like 25m, 1h30m, 45s.
func shortDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	out := ""
	if h > 0 {
		out += fmt.Sprintf("%dh", h)
	}
	if m > 0 {
		out += fmt.Sprintf("%dm", m)
	}
	if s > 0 || out == "" {
		out += fmt.Sprintf("%ds", s)
	}
	return out
}
