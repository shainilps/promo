package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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
	RingFor       string         `yaml:"ring_for"`
	Notifications bool           `yaml:"notifications"`
	Pomodoro      PomodoroConfig `yaml:"pomodoro"`
	UI            UIConfig       `yaml:"ui"`
}

type PomodoroConfig struct {
	Work  string `yaml:"work"`
	Break string `yaml:"break"`
}

type UIConfig struct {
	AccentColor string `yaml:"accent_color"`
	BarWidth    int    `yaml:"bar_width"`
	BigClock    bool   `yaml:"big_clock"`
	Fullscreen  bool   `yaml:"fullscreen"`
	ShowHelp    bool   `yaml:"show_help"`
}

func defaultConfig() Config {
	return Config{
		TasksDir:      "~/.local/share/gg/tasks",
		OverdueNag:    "1h",
		RingFor:       "1m",
		Notifications: true,
		Pomodoro:      PomodoroConfig{Work: "25m", Break: "10m"},
		UI: UIConfig{
			AccentColor: "#CBA6F7", // mauve
			BarWidth:    60,
			BigClock:    true,
			Fullscreen:  true,
			ShowHelp:    true,
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
	fixOff := func(name string, v *string, d string) {
		if validateOff(*v) != nil {
			warnings = append(warnings, fmt.Sprintf("invalid %s %q, using %s", name, *v, d))
			*v = d
		}
	}
	fixOff("overdue_nag", &c.OverdueNag, def.OverdueNag)
	fixOff("ring_for", &c.RingFor, def.RingFor)
	fixDur("pomodoro.work", &c.Pomodoro.Work, def.Pomodoro.Work)
	fixDur("pomodoro.break", &c.Pomodoro.Break, def.Pomodoro.Break)
	fixColor("ui.accent_color", &c.UI.AccentColor, def.UI.AccentColor)
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

// soundFor picks the sound to play: the configured one, or the
// freedesktop alarm sound when it is unset or missing.
func (c Config) soundFor() string {
	for _, p := range []string{c.SoundPath, fallbackSound} {
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

// offDur reads a duration setting that can also be "off" (false).
func offDur(s string) (time.Duration, bool) {
	d, err := time.ParseDuration(strings.TrimSpace(s))
	return d, err == nil && d > 0
}

// nagEvery is how often to nudge about overdue tasks; false means never.
func (c Config) nagEvery() (time.Duration, bool) { return offDur(c.OverdueNag) }

// ringFor is how long the pomodoro end sound rings; false means until stopped.
func (c Config) ringFor() (time.Duration, bool) { return offDur(c.RingFor) }

// validateOff accepts "off" or a duration of at least a minute.
func validateOff(s string) error {
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

var yamlLine = regexp.MustCompile(`line (\d+)`)

// editConfig opens the config in the editor. It's written back only once
// it reads and every value is valid; otherwise the editor reopens on the
// problem. The daemon then picks it up.
func editConfig(path string, in io.Reader, out io.Writer) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err := saveConfig(path, defaultConfig()); err != nil {
			return err
		}
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "gg-config-*.yaml")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	f.Write(data)
	f.Close()

	return editUntilRead(tmp, "# gg: %s", in, out, func(text string) error {
		if text == string(data) {
			fmt.Fprintln(out, "no changes")
			return nil
		}
		cfg := defaultConfig()
		if err := yaml.Unmarshal([]byte(text), &cfg); err != nil {
			msg := strings.TrimPrefix(err.Error(), "yaml: ")
			if m := yamlLine.FindStringSubmatch(msg); m != nil {
				var n int
				fmt.Sscan(m[1], &n)
				return &lineError{n, strings.TrimSpace(yamlLine.ReplaceAllString(msg, ""))}
			}
			return errors.New(msg)
		}
		if warnings := cfg.normalize(); len(warnings) > 0 {
			return errors.New(strings.Join(warnings, "; "))
		}
		if err := writeFileAtomic(path, []byte(text)); err != nil {
			return err
		}
		if _, err := call(request{Op: "reload"}); err != nil {
			return err
		}
		fmt.Fprintln(out, "saved "+shortHome(path))
		return nil
	})
}
