// Package when parses and formats the times a person types at a terminal.
//
// Shared by the command layer and the TUI so both accept exactly the same
// input: an event created with `calendar add` and one created in the event
// form should not disagree about what "14:00" means.
package when

import (
	"fmt"
	"strings"
	"time"
)

// Parse accepts the handful of date and time formats worth typing.
//
// A bare time means today at that time, which is what someone writing "14:00"
// means. Everything is read in the local zone: a date parsed as UTC is how an
// event ends up an hour out for half the year.
func Parse(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	now := time.Now()

	switch strings.ToLower(s) {
	case "", "now":
		return now, nil
	case "today":
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local), nil
	case "tomorrow":
		return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.Local), nil
	case "yesterday":
		return time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, time.Local), nil
	}

	for _, layout := range []string{
		"2006-01-02 15:04",
		"2006-01-02T15:04",
		"2006-01-02",
		"15:04",
	} {
		t, err := time.ParseInLocation(layout, s, time.Local)
		if err != nil {
			continue
		}

		if layout == "15:04" {
			return time.Date(now.Year(), now.Month(), now.Day(),
				t.Hour(), t.Minute(), 0, 0, time.Local), nil
		}

		return t, nil
	}

	return time.Time{}, fmt.Errorf("could not read %q as a date; try 2006-01-02 15:04", s)
}

// FormatDuration renders a length the way someone would type it back.
func FormatDuration(d time.Duration) string {
	d = d.Round(time.Minute)

	h := int(d.Hours())
	m := int(d.Minutes()) % 60

	switch {
	case h == 0:
		return fmt.Sprintf("%dm", m)
	case m == 0:
		return fmt.Sprintf("%dh", h)
	default:
		return fmt.Sprintf("%dh%dm", h, m)
	}
}
