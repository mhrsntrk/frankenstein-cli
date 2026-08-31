package render

import (
	"testing"
	"time"
)

// TestSelectionIsVisible is the regression test for a selection that existed
// in the model and never reached the screen: Key() returned "" for every
// event, so the grid drew none of them highlighted and there was no way to
// tell which one enter would open.
func TestSelectionIsVisible(t *testing.T) {
	day := time.Date(2026, 8, 28, 0, 0, 0, 0, time.Local)
	evs := []Event{
		{ID: "a", Title: "Standup", StartsAt: day.Add(9 * time.Hour), EndsAt: day.Add(10 * time.Hour)},
		{ID: "b", Title: "Review", StartsAt: day.Add(14 * time.Hour), EndsAt: day.Add(15 * time.Hour)},
	}

	none := Calendar(CalendarDay, evs, nil, day, time.Monday, 100, 30, "h", "", true)
	first := Calendar(CalendarDay, evs, nil, day, time.Monday, 100, 30, "h", Key(evs[0]), true)
	second := Calendar(CalendarDay, evs, nil, day, time.Monday, 100, 30, "h", Key(evs[1]), true)

	if none == first {
		t.Error("day: selecting an event changed nothing")
	}
	if first == second {
		t.Error("day: selecting a different event changed nothing")
	}

	wnone := Calendar(CalendarWeek, evs, nil, day, time.Monday, 100, 30, "h", "", true)
	wfirst := Calendar(CalendarWeek, evs, nil, day, time.Monday, 100, 30, "h", Key(evs[0]), true)
	if wnone == wfirst {
		t.Error("week: selecting an event changed nothing")
	}
}
