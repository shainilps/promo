package main

import (
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/lipgloss"
)

// Catppuccin Mocha palette.
var (
	colorFocus = lipgloss.Color("#F38BA8") // red
	colorBreak = lipgloss.Color("#A6E3A1") // green
	colorAlarm = lipgloss.Color("#FAB387") // peach
	colorTask  = lipgloss.Color("#89B4FA") // blue
	colorMuted = lipgloss.Color("#7F849C") // overlay1
	colorText  = lipgloss.Color("#CDD6F4") // text
	colorError = lipgloss.Color("#EBA0AC") // maroon
	colorBase  = lipgloss.Color("#1E1E2E") // text on colored badges
	colorTrack = "#45475A"                 // surface1, empty part of bars
)

// Progress bar gradients per mode; the timer uses the configured one.
var (
	gradFocus = [2]string{"#F38BA8", "#FAB387"} // red → peach
	gradBreak = [2]string{"#94E2D5", "#A6E3A1"} // teal → green
	gradAlarm = [2]string{"#FAB387", "#F9E2AF"} // peach → yellow
)

type styles struct {
	accent   lipgloss.Color
	card     lipgloss.Style
	title    lipgloss.Style
	muted    lipgloss.Style
	text     lipgloss.Style
	bold     lipgloss.Style
	errorMsg lipgloss.Style
	success  lipgloss.Style
	badge    lipgloss.Style
	section  lipgloss.Style
	selected lipgloss.Style
	mode     lipgloss.Style
}

func newStyles(ui UIConfig) styles {
	accent := lipgloss.Color(ui.AccentColor)
	return styles{
		accent: accent,
		card: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accent).
			Padding(1, 4),
		title:    lipgloss.NewStyle().Bold(true).Foreground(accent),
		muted:    lipgloss.NewStyle().Foreground(colorMuted),
		text:     lipgloss.NewStyle().Foreground(colorText),
		bold:     lipgloss.NewStyle().Bold(true).Foreground(colorText),
		errorMsg: lipgloss.NewStyle().Foreground(colorError),
		success:  lipgloss.NewStyle().Foreground(colorBreak),
		badge: lipgloss.NewStyle().
			Foreground(colorBase).
			Background(colorMuted).
			Bold(true).
			Padding(0, 1),
		section:  lipgloss.NewStyle().Bold(true).Foreground(colorMuted).MarginTop(1),
		selected: lipgloss.NewStyle().Bold(true).Foreground(accent),
		mode: lipgloss.NewStyle().
			Foreground(colorBase).
			Background(accent).
			Bold(true).
			Padding(0, 1),
	}
}

func newBar(from, to string, width int) progress.Model {
	bar := progress.New(
		progress.WithGradient(from, to),
		progress.WithoutPercentage(),
		progress.WithWidth(width),
		progress.WithFillCharacters('━', '━'),
	)
	bar.EmptyColor = colorTrack
	return bar
}

func newHelp(st styles) help.Model {
	h := help.New()
	h.Styles.ShortKey = lipgloss.NewStyle().Foreground(st.accent)
	h.Styles.FullKey = h.Styles.ShortKey
	h.Styles.ShortDesc = st.muted
	h.Styles.FullDesc = st.muted
	h.Styles.ShortSeparator = st.muted
	h.Styles.FullSeparator = st.muted
	return h
}
