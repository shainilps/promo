package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const fallbackSound = "/usr/share/sounds/freedesktop/stereo/alarm-clock-elapsed.oga"

type Config struct {
	SoundPath     string         `yaml:"sound_path"`
	TasksDir      string         `yaml:"tasks_dir"`
	OverdueNag    string         `yaml:"overdue_nag"`
	DefaultTime   string         `yaml:"default_time"`
	Notifications bool           `yaml:"notifications"`
	Pomodoro      PomodoroConfig `yaml:"pomodoro"`
	Alarm         AlarmConfig    `yaml:"alarm"`
	UI            UIConfig       `yaml:"ui"`
}

type PomodoroConfig struct {
	Work  string `yaml:"work"`
	Break string `yaml:"break"`
}

type AlarmConfig struct {
	SoundPath string `yaml:"sound_path"`
	Snooze    string `yaml:"snooze"`
}

type UIConfig struct {
	AccentColor   string `yaml:"accent_color"`
	BarStartColor string `yaml:"bar_start_color"`
	BarEndColor   string `yaml:"bar_end_color"`
	BarWidth      int    `yaml:"bar_width"`
	BigClock      bool   `yaml:"big_clock"`
	Fullscreen    bool   `yaml:"fullscreen"`
	ShowHelp      bool   `yaml:"show_help"`
}

func defaultConfig() Config {
	return Config{
		DefaultTime:   "30m",
		TasksDir:      "~/.local/share/gg/tasks",
		OverdueNag:    "1h",
		Notifications: true,
		Pomodoro:      PomodoroConfig{Work: "25m", Break: "10m"},
		Alarm:         AlarmConfig{Snooze: "5m"},
		UI: UIConfig{
			AccentColor:   "#CBA6F7", // mauve
			BarStartColor: "#89B4FA", // blue
			BarEndColor:   "#CBA6F7",
			BarWidth:      60,
			BigClock:      true,
			Fullscreen:    true,
			ShowHelp:      true,
		},
	}
}

func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "gg")
	// This tool used to be called promo; carry its config over once.
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		os.Rename(filepath.Join(home, ".config", "promo"), dir)
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// loadConfig reads the config file over the defaults, so keys missing from
// older files keep their default values. Invalid values are reset to their
// defaults and reported as warnings instead of failing startup.
func loadConfig(path string) (Config, []string, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil, nil
	}
	if err != nil {
		return cfg, nil, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	return cfg, cfg.normalize(), nil
}

func saveConfig(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		return err
	}
	return writeFileAtomic(path, buf.Bytes())
}

// writeFileAtomic replaces path in one step, so readers never see half a file.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (c *Config) normalize() []string {
	def := defaultConfig()
	var warnings []string
	fixDur := func(name string, v *string, d string) {
		if validateDuration(*v) != nil {
			warnings = append(warnings, fmt.Sprintf("invalid %s %q, using %s", name, *v, d))
			*v = d
		}
	}
	fixColor := func(name string, v *string, d string) {
		if validateColor(*v) != nil {
			warnings = append(warnings, fmt.Sprintf("invalid %s %q, using %s", name, *v, d))
			*v = d
		}
	}
	if strings.TrimSpace(c.TasksDir) == "" {
		c.TasksDir = def.TasksDir
	}
	if validateNag(c.OverdueNag) != nil {
		warnings = append(warnings, fmt.Sprintf("invalid overdue_nag %q, using %s", c.OverdueNag, def.OverdueNag))
		c.OverdueNag = def.OverdueNag
	}
	fixDur("default_time", &c.DefaultTime, def.DefaultTime)
	fixDur("pomodoro.work", &c.Pomodoro.Work, def.Pomodoro.Work)
	fixDur("pomodoro.break", &c.Pomodoro.Break, def.Pomodoro.Break)
	fixDur("alarm.snooze", &c.Alarm.Snooze, def.Alarm.Snooze)
	fixColor("ui.accent_color", &c.UI.AccentColor, def.UI.AccentColor)
	fixColor("ui.bar_start_color", &c.UI.BarStartColor, def.UI.BarStartColor)
	fixColor("ui.bar_end_color", &c.UI.BarEndColor, def.UI.BarEndColor)
	if validateBarWidth(fmt.Sprint(c.UI.BarWidth)) != nil {
		warnings = append(warnings, fmt.Sprintf("invalid ui.bar_width %d, using %d", c.UI.BarWidth, def.UI.BarWidth))
		c.UI.BarWidth = def.UI.BarWidth
	}
	return warnings
}

// dur parses a duration that has already been validated.
func dur(s string) time.Duration {
	d, _ := time.ParseDuration(s)
	return d
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// soundFor picks the sound to play, falling back to the general sound and
// then the freedesktop alarm sound when a path is unset or missing.
func (c Config) soundFor(alarm bool) string {
	candidates := []string{c.SoundPath, fallbackSound}
	if alarm {
		candidates = append([]string{c.Alarm.SoundPath}, candidates...)
	}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		p = expandHome(p)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func validateDuration(s string) error {
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return fmt.Errorf("not a duration (try 25m, 1h30m, 45s)")
	}
	if d <= 0 {
		return fmt.Errorf("must be greater than zero")
	}
	return nil
}

var hexColor = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

func validateColor(s string) error {
	if !hexColor.MatchString(strings.TrimSpace(s)) {
		return fmt.Errorf("not a hex color (try #CBA6F7)")
	}
	return nil
}

func validateBarWidth(s string) error {
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &n); err != nil || fmt.Sprint(n) != strings.TrimSpace(s) {
		return fmt.Errorf("not a whole number")
	}
	if n < 10 || n > 200 {
		return fmt.Errorf("must be between 10 and 200")
	}
	return nil
}

// nagEvery is how often to nudge about overdue tasks; false means never.
func (c Config) nagEvery() (time.Duration, bool) {
	d, err := time.ParseDuration(strings.TrimSpace(c.OverdueNag))
	return d, err == nil && d > 0
}

func validateNag(s string) error {
	if strings.TrimSpace(s) == "off" {
		return nil
	}
	if err := validateDuration(s); err != nil {
		return fmt.Errorf("%v, or off", err)
	}
	if dur(strings.TrimSpace(s)) < time.Minute {
		return fmt.Errorf("at least 1m, or off")
	}
	return nil
}

func validateDir(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("can't be empty")
	}
	if info, err := os.Stat(expandHome(s)); err == nil && !info.IsDir() {
		return fmt.Errorf("is a file, not a folder")
	}
	return nil
}

func validateSoundPath(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	info, err := os.Stat(expandHome(s))
	if err != nil {
		return fmt.Errorf("file not found")
	}
	if info.IsDir() {
		return fmt.Errorf("is a directory")
	}
	return nil
}
