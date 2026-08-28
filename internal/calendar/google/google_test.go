package google

import (
	"testing"
	"time"

	"google.golang.org/api/calendar/v3"

	fcal "github.com/mhrsntrk/frankenstein-cli/internal/calendar"
)

// fcalDraft is the smallest all-day draft the tests need.
func fcalDraft(start, end time.Time) fcal.EventDraft {
	return fcal.EventDraft{Title: "away", Start: start, End: end, AllDay: true}
}

// A calendar can live in a different zone from the machine reading it, and
// Google answers in the calendar's zone. Keeping that fixed offset showed a
// Sydney calendar's clock times on a Madrid screen and filed its events under
// the wrong day, so timed events are read in the local zone.
func TestToEventReadsTimedEventsInTheLocalZone(t *testing.T) {
	e := &calendar.Event{
		Id:      "x",
		Summary: "standup",
		Start:   &calendar.EventDateTime{DateTime: "2026-08-28T10:00:00+10:00"},
		End:     &calendar.EventDateTime{DateTime: "2026-08-28T10:30:00+10:00"},
	}

	ev, err := toEvent(e)
	if err != nil {
		t.Fatalf("toEvent: %v", err)
	}

	want := time.Date(2026, 8, 28, 10, 0, 0, 0, time.FixedZone("AEST", 10*3600))
	if !ev.Start.Equal(want) {
		t.Errorf("start moved to a different instant: %v", ev.Start)
	}

	if ev.Start.Location() != time.Local {
		t.Errorf("start kept zone %v, want the local zone", ev.Start.Location())
	}

	if ev.End.Location() != time.Local {
		t.Errorf("end kept zone %v, want the local zone", ev.End.Location())
	}
}

// An all-day event carries a date, not an instant, and must stay on that date
// rather than being shifted through a zone conversion.
func TestToEventKeepsAllDayEventsOnTheirDate(t *testing.T) {
	e := &calendar.Event{
		Id:    "x",
		Start: &calendar.EventDateTime{Date: "2026-08-28"},
		End:   &calendar.EventDateTime{Date: "2026-08-29"},
	}

	ev, err := toEvent(e)
	if err != nil {
		t.Fatalf("toEvent: %v", err)
	}

	if !ev.AllDay {
		t.Error("a dated event did not come back all-day")
	}

	if ev.Start.Year() != 2026 || ev.Start.Month() != 8 || ev.Start.Day() != 28 {
		t.Errorf("start landed on %v, want 2026-08-28", ev.Start)
	}
}

// The draft's end is exclusive by the time it reaches toGoogleEvent, but an
// end on the start's own date would make a zero-day event, so it becomes the
// one-day event it meant.
func TestToGoogleEventAllDayEnds(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)

	cases := []struct {
		name string
		end  time.Time
		want string
	}{
		{"end at the start", start, "2026-09-02"},
		{"timed end later the same day", start.Add(time.Hour), "2026-09-02"},
		{"exclusive multi-day end passes through", start.AddDate(0, 0, 3), "2026-09-04"},
	}

	for _, c := range cases {
		e := toGoogleEvent(fcalDraft(start, c.end))

		if e.Start.Date != "2026-09-01" {
			t.Errorf("%s: start date = %q, want 2026-09-01", c.name, e.Start.Date)
		}

		if e.End.Date != c.want {
			t.Errorf("%s: end date = %q, want %q", c.name, e.End.Date, c.want)
		}
	}
}

// skipWarning says nothing when nothing was skipped.
func TestSkipWarning(t *testing.T) {
	if w := skipWarning(0); w != nil {
		t.Errorf("skipWarning(0) = %v, want nothing", w)
	}

	if w := skipWarning(2); len(w) != 1 {
		t.Errorf("skipWarning(2) = %v, want one warning", w)
	}
}
