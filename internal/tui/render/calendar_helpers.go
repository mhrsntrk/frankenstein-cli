package render

// Helpers the calendar grids need, copied from the hey-cli files that define
// them (MIT): the habit palette from habits.go and the elapsed-time format
// from time_track.go.

import (
	"fmt"
	"image/color"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/mhrsntrk/frankenstein-cli/internal/terminal"
	habitvalues "github.com/mhrsntrk/frankenstein-cli/internal/tui/render/habit"
)

// heyColors stands HEY's colors up as ANSI slots, for the reason styles.go and covers.go
// give: the reader's terminal theme defines those sixteen, so a habit or a calendar wears
// its own color in the reader's palette rather than HEY's hex. Gold takes the bright
// yellow and brown the plain one, which is what a dark yellow looks like in every theme.
//
// One vocabulary covers both, because HEY has one: `Calendar::Preference::Colored` is
// where a calendar's color comes from and a habit's is the same enum. Black is a calendar
// color only, and it takes the foreground slot rather than lipgloss.Black — the reader's
// ink, which is dark on a light theme and light on a dark one, where a literal black
// would vanish into half of them.
var heyColors = map[string]color.Color{
	"blue":   lipgloss.Blue,
	"red":    lipgloss.Red,
	"gold":   lipgloss.BrightYellow,
	"green":  lipgloss.Green,
	"teal":   lipgloss.Cyan,
	"purple": lipgloss.Magenta,
	"pink":   lipgloss.BrightMagenta,
	"brown":  lipgloss.Yellow,
	"black":  lipgloss.White,
}

// formatElapsed is a stretch of time as a stopwatch reads it, seconds and all — running or
// finished, on the badge, in the menu, down the log and in its total.
//
// Always hours, even for a track a few seconds old. A stopwatch that drops the hours until it
// has one is ambiguous at a glance — 0:45 is either three quarters of a minute or three quarters
// of an hour — and it changes width on the hour, which on the day's clock row would move a badge
// that has to fit the space the time leaves it.
func formatElapsed(d time.Duration) string {
	seconds := int(max(d, 0).Seconds())
	return fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
}

// habitMarker is the ring HEY fills in when a habit is done for the day.
func habitMarker(done bool) string {
	if done {
		return "●"
	}
	return "○"
}

// habitMarkerStyle is the style for a habit's ring: its own color where HEY gave it
// one, and the alert red every other waiting thing wears where it did not.
func habitMarkerStyle(habitColor string) lipgloss.Style {
	if slot, ok := heyColors[habitColor]; ok {
		return lipgloss.NewStyle().Foreground(slot).Bold(true)
	}
	return lipgloss.NewStyle().Foreground(colorAlert).Bold(true)
}

// habitLabel is a habit as it reads in a list: its icon's emoji, then its name.
func habitLabel(habit Recording) string {
	name := terminal.SanitizeLine(habit.Title)
	if emoji := habitvalues.EmojiFor(habit.Icon); emoji != "" {
		return emoji + " " + name
	}
	return name
}
