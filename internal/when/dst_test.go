package when

import (
	"testing"
	"time"
)

// Adding "09:00" to midnight as a duration crosses the missing hour on a
// spring-forward day and lands at 10:00, which is why at builds the time from
// its components instead. Madrid's 2026 change is a fixed date, so the case
// does not depend on the machine's zone.
func TestAtSurvivesSpringForward(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Madrid")
	if err != nil {
		t.Skipf("no tzdata for Europe/Madrid: %v", err)
	}

	// 2026-03-29 is the day Europe springs forward: 02:00 becomes 03:00.
	day := time.Date(2026, 3, 29, 0, 0, 0, 0, loc)

	// Guard against zone data without the transition, where the assertion
	// below would pass without proving anything.
	if day.Add(9*time.Hour).Hour() == 9 {
		t.Skip("zone data reports no transition on 2026-03-29")
	}

	got := at(day, 9, 0)

	if got.Hour() != 9 || got.Minute() != 0 {
		t.Errorf("at(2026-03-29, 09:00) = %v, want the clock to read 09:00", got)
	}

	if got.Day() != 29 {
		t.Errorf("at moved to another day: %v", got)
	}
}

// A day given without a clock stays at midnight.
func TestAtWithoutAClockStaysAtMidnight(t *testing.T) {
	day := time.Date(2026, 8, 28, 0, 0, 0, 0, time.Local)

	if got := at(day, -1, 0); !got.Equal(day) {
		t.Errorf("at(day, -1, 0) = %v, want %v", got, day)
	}
}
