package when_test

import (
	"testing"
	"time"

	"github.com/mhrsntrk/frankenstein-cli/internal/when"
)

func TestParseNamedDays(t *testing.T) {
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)

	cases := []struct {
		in   string
		want time.Time
	}{
		{"today", midnight},
		{"Today", midnight},
		{"tomorrow", midnight.AddDate(0, 0, 1)},
		{"yesterday", midnight.AddDate(0, 0, -1)},
		{"tomorrow 09:00", midnight.AddDate(0, 0, 1).Add(9 * time.Hour)},
		{"today 14:30", midnight.Add(14*time.Hour + 30*time.Minute)},
	}

	for _, c := range cases {
		got, err := when.Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.in, err)

			continue
		}

		if !got.Equal(c.want) {
			t.Errorf("Parse(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// A weekday means the one ahead. Writing "friday" on a Friday afternoon means
// next Friday, not the morning that has already gone.
func TestParseWeekdayIsAlwaysAhead(t *testing.T) {
	now := time.Now()

	for _, name := range []string{"monday", "tue", "wednesday", "thu", "friday", "sat", "sunday"} {
		got, err := when.Parse(name)
		if err != nil {
			t.Errorf("Parse(%q): %v", name, err)

			continue
		}

		if !got.After(now) {
			t.Errorf("Parse(%q) = %v, which is not in the future", name, got)
		}

		if d := got.Sub(now); d > 7*24*time.Hour {
			t.Errorf("Parse(%q) is %v away, more than a week", name, d)
		}
	}

	got, err := when.Parse("friday 17:00")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if got.Weekday() != time.Friday {
		t.Errorf("weekday = %v, want Friday", got.Weekday())
	}

	if got.Hour() != 17 || got.Minute() != 0 {
		t.Errorf("clock = %02d:%02d, want 17:00", got.Hour(), got.Minute())
	}
}

func TestParseLayouts(t *testing.T) {
	got, err := when.Parse("2026-08-28 14:00")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	want := time.Date(2026, 8, 28, 14, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("= %v, want %v", got, want)
	}

	// A bare time means today, in the local zone. Reading it as UTC is how an
	// event lands an hour out for half the year.
	now := time.Now()

	got, err = when.Parse("14:00")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if got.Hour() != 14 || got.Day() != now.Day() {
		t.Errorf("bare time = %v, want 14:00 today", got)
	}

	if _, off := got.Zone(); off != func() int { _, o := now.Zone(); return o }() {
		t.Error("a bare time did not land in the local zone")
	}
}

func TestParseRejectsNonsense(t *testing.T) {
	for _, in := range []string{"someday", "next tuesday please", "32:99", "friday 25:00"} {
		if _, err := when.Parse(in); err == nil {
			t.Errorf("Parse(%q) succeeded", in)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Minute:            "30m",
		time.Hour:                   "1h",
		90 * time.Minute:            "1h30m",
		2*time.Hour + 5*time.Minute: "2h5m",
		time.Hour + 30*time.Second:  "1h1m",
	}

	for d, want := range cases {
		if got := when.FormatDuration(d); got != want {
			t.Errorf("FormatDuration(%v) = %q, want %q", d, got, want)
		}
	}
}
