package tui

import (
	"context"
	"os"
	"testing"
	"time"

	fcal "github.com/mhrsntrk/frankenstein-cli/internal/calendar"
)

func TestDumpCalendar(t *testing.T) {
	if os.Getenv("DUMP_VIEW") == "" {
		t.Skip("set DUMP_VIEW=1")
	}

	h := newHarness(t)
	h.m.width, h.m.height = 150, 30
	h.m.section = sectionCalendar
	h.m.view = viewCalendar
	h.m.cal = stubCal{}

	// The grid anchors on today, so the fixture has to sit in this week or
	// nothing lands in it.
	now := time.Now()
	mon := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).
		AddDate(0, 0, -((int(now.Weekday()) + 6) % 7))

	h.m.events = []fcal.Event{
		{Title: "Aprendiendo Español", Start: mon.AddDate(0, 0, 1).Add(20*time.Hour + 30*time.Minute), End: mon.AddDate(0, 0, 1).Add(22 * time.Hour)},
		{Title: "Micha / Mahir", Start: mon.AddDate(0, 0, 4).Add(8*time.Hour + 30*time.Minute), End: mon.AddDate(0, 0, 4).Add(9 * time.Hour)},
		{Title: "Standup", Start: mon.Add(9 * time.Hour), End: mon.Add(9*time.Hour + 15*time.Minute)},
		{Title: "Trip to Bilbao", Start: mon.AddDate(0, 0, 5), End: mon.AddDate(0, 0, 6), AllDay: true},
	}

	t.Logf("\n%s\n", h.m.View())
}

// stubCal exists only so calendarView draws instead of reporting that the
// calendar is unconfigured.
type stubCal struct{}

func (stubCal) Calendars(context.Context) ([]fcal.Calendar, error) { return nil, nil }
func (stubCal) Events(context.Context, string, time.Time, time.Time) ([]fcal.Event, error) {
	return nil, nil
}
func (stubCal) CreateEvent(context.Context, string, fcal.EventDraft) (fcal.Event, error) {
	return fcal.Event{}, nil
}
func (stubCal) UpdateEvent(context.Context, string, fcal.EventDraft) (fcal.Event, error) {
	return fcal.Event{}, nil
}
func (stubCal) DeleteEvent(context.Context, string, string) error { return nil }
