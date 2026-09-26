package main

import (
	"context"
	"os/exec"
	"time"
)

// start runs a command without blocking and reaps it in the background.
func start(name string, args ...string) {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return
	}
	go cmd.Wait()
}

// soundLoop replays a sound until Stop is called.
type soundLoop struct {
	cancel context.CancelFunc
}

func startSoundLoop(path string) *soundLoop {
	ctx, cancel := context.WithCancel(context.Background())
	if path == "" {
		return &soundLoop{cancel: cancel}
	}
	go func() {
		for ctx.Err() == nil {
			err := exec.CommandContext(ctx, "pw-play", path).Run()
			if err != nil && ctx.Err() == nil {
				// Don't spin if the player is missing or the file is broken.
				select {
				case <-ctx.Done():
				case <-time.After(time.Second):
				}
			}
		}
	}()
	return &soundLoop{cancel: cancel}
}

func (s *soundLoop) Stop() {
	if s != nil {
		s.cancel()
	}
}

func notify(cfg Config, title, body, urgency string) {
	if cfg.Notifications {
		start("notify-send", "-a", "promo", "-u", urgency, "-i", "alarm-symbolic", title, body)
	}
}
