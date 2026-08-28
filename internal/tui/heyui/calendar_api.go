package heyui

import "time"

// The exported face of hey-cli's calendar grids.

// Event is one entry to draw. It maps onto upstream's Recording; the fields
// HEY serves and Google does not are left alone.
type Event struct {
	ID       string
	Title    string
	AllDay   bool
	StartsAt time.Time
	EndsAt   time.Time
	Location string
	Notes    string
	// Color is the calendar's colour name, matched against hey-cli's palette.
	Color string
}

func (e Event) recording() Recording {
	return Recording{
		Title:         e.Title,
		AllDay:        e.AllDay,
		StartsAt:      e.StartsAt,
		EndsAt:        e.EndsAt,
		Location:      e.Location,
		Notes:         e.Notes,
		CalendarColor: e.Color,
		Type:          "event",
	}
}

func recordings(events []Event) []Recording {
	out := make([]Recording, 0, len(events))
	for _, e := range events {
		out = append(out, e.recording())
	}

	return out
}

// CalendarView is which grid to draw.
type CalendarView int

const (
	CalendarDay CalendarView = iota
	CalendarWeek
)

// Calendar draws the day or week grid.
//
// selectedKey identifies the highlighted event; pass the value Key returns for
// it. firstWeekDay sets which column a week starts in. use24 picks a 24-hour
// clock over am/pm.
func Calendar(
	kind CalendarView,
	events []Event,
	anchor time.Time,
	firstWeekDay time.Weekday,
	width, height int,
	hint string,
	selectedKey string,
	use24 bool,
) string {
	sel := selection{eventKey: selectedKey, day: anchor}

	if kind == CalendarDay {
		return renderDayView(recordings(events), nil, nil,
			anchor, time.Now(), hint, width, height, sel, nil, use24)
	}

	return renderWeekView(recordings(events), nil, nil,
		anchor, firstWeekDay, width, height, hint, nil, sel, use24)
}

// Key is the identifier the grid uses to mark an event as selected.
func Key(e Event) string { return e.recording().key() }
