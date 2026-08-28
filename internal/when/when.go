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

	if s == "" || strings.EqualFold(s, "now") {
		return now, nil
	}

	// A named day, optionally with a time after it. The time is split off
	// first because "tomorrow" and "tomorrow 09:00" are the same thought, and
	// only the first of them used to parse.
	if day, clock, ok := splitDayAndTime(s); ok {
		if base, ok := namedDay(day, now); ok {
			return at(base, clock), nil
		}
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

	return time.Time{}, fmt.Errorf(
		"could not read %q as a date; try 2006-01-02 15:04, 14:00, tomorrow, or friday 17:00", s)
}

// splitDayAndTime pulls a trailing HH:MM off a named day.
func splitDayAndTime(s string) (day string, clock time.Duration, ok bool) {
	fields := strings.Fields(s)

	switch len(fields) {
	case 1:
		return fields[0], -1, true
	case 2:
		t, err := time.Parse("15:04", fields[1])
		if err != nil {
			return "", 0, false
		}

		return fields[0], time.Duration(t.Hour())*time.Hour +
			time.Duration(t.Minute())*time.Minute, true
	}

	return "", 0, false
}

// namedDay resolves today, tomorrow, yesterday and the weekday names.
//
// A weekday means the next one to come, and never today: somebody who writes
// "friday" on a Friday afternoon means the Friday ahead, not the morning that
// has already gone.
func namedDay(name string, now time.Time) (time.Time, bool) {
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)

	switch strings.ToLower(name) {
	case "today":
		return midnight, true
	case "tomorrow":
		return midnight.AddDate(0, 0, 1), true
	case "yesterday":
		return midnight.AddDate(0, 0, -1), true
	}

	weekdays := map[string]time.Weekday{
		"sunday": time.Sunday, "sun": time.Sunday,
		"monday": time.Monday, "mon": time.Monday,
		"tuesday": time.Tuesday, "tue": time.Tuesday, "tues": time.Tuesday,
		"wednesday": time.Wednesday, "wed": time.Wednesday,
		"thursday": time.Thursday, "thu": time.Thursday, "thurs": time.Thursday,
		"friday": time.Friday, "fri": time.Friday,
		"saturday": time.Saturday, "sat": time.Saturday,
	}

	want, ok := weekdays[strings.ToLower(name)]
	if !ok {
		return time.Time{}, false
	}

	delta := (int(want) - int(midnight.Weekday()) + 7) % 7
	if delta == 0 {
		delta = 7
	}

	return midnight.AddDate(0, 0, delta), true
}

// at puts a clock time on a day. A negative clock means the day was given
// without one, which stays at midnight.
func at(day time.Time, clock time.Duration) time.Time {
	if clock < 0 {
		return day
	}

	return day.Add(clock)
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
