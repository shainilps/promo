package main

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/timer"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"
)

type Config struct {
	SoundPath string `yaml:"sound_path"`
}

func playSound(path string) {
	exec.Command("pw-play", path).Run()
}

type model struct {
	timer     timer.Model
	progress  progress.Model
	total     time.Duration
	soundPath string
}

func (m model) Init() tea.Cmd {
	return m.timer.Start()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case timer.TickMsg:
		var cmd tea.Cmd
		m.timer, cmd = m.timer.Update(msg)
		return m, cmd

	case timer.TimeoutMsg:
		{
			if m.soundPath != "" {
				go playSound(m.soundPath)
			}
			return m, tea.Quit
		}
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
		return m, nil

	case timer.StartStopMsg:
		var cmd tea.Cmd
		m.timer, cmd = m.timer.Update(msg)
		return m, cmd

	case tea.WindowSizeMsg:
		m.progress.Width = min(msg.Width-10, 80)
		return m, nil

	default:
		return m, nil
	}
}

func (m model) View() string {
	remainingTime := m.timer.Timeout
	totalTime := m.total

	formatTime := func(d time.Duration) string {
		minutes := int(d.Minutes())
		seconds := int(d.Seconds()) % 60
		return fmt.Sprintf("%02d:%02d", minutes, seconds)
	}

	timeInfo := fmt.Sprintf("%s / %s", formatTime(remainingTime), formatTime(totalTime))

	percent := 1.0 - m.timer.Timeout.Seconds()/m.total.Seconds()

	return lipgloss.Place(
		100, 3,
		lipgloss.Center, lipgloss.Center,
		lipgloss.JoinVertical(
			lipgloss.Center,
			timeInfo,
			m.progress.ViewAs(percent),
		),
	)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: promo <duration>")
		os.Exit(1)
	}

	duration, err := time.ParseDuration(os.Args[1])
	if err != nil {
		fmt.Println("Invalid duration:", err)
		os.Exit(1)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Println("Error getting home dir:", err)
		os.Exit(1)
	}

	configPath := fmt.Sprintf("%s/.config/promo/config.yaml", home)
	var config Config
	if _, err := os.Stat(configPath); err == nil {
		f, err := os.Open(configPath)
		if err != nil {
			fmt.Println("Error opening config file:", err)
			os.Exit(1)
		}
		defer f.Close()

		decoder := yaml.NewDecoder(f)
		if err := decoder.Decode(&config); err != nil {
			fmt.Println("Error decoding config file:", err)
			os.Exit(1)
		}
	}

	m := model{
		timer:     timer.NewWithInterval(duration, time.Second),
		progress:  progress.New(progress.WithDefaultGradient()),
		total:     duration,
		soundPath: config.SoundPath,
	}

	p := tea.NewProgram(m)

	if _, err := p.Run(); err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}
}
