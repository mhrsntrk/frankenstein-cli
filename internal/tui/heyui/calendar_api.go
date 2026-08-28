package heyui

import (
	"sort"
	"time"
)

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

// Habit is one tracked habit, drawn in the band above the grid.
type Habit struct {
	ID   int64
	Name string
	Icon string
	// Color is a name from hey-cli's palette.
	Color string
	// Done are the days it was kept.
	Done []time.Time
}

// Todo is one item in the "Sometime this week" ribbon.
type Todo struct {
	ID    int64
	Title string
	Done  bool
}

// CalendarView is which grid to draw.
type CalendarView int

const (
	CalendarDay CalendarView = iota
	CalendarWeek
	CalendarYear
)

func habitRecordings(habits []Habit) (defs, completions []Recording) {
	for _, h := range habits {
		defs = append(defs, Recording{
			ID: h.ID, Title: h.Name, Icon: h.Icon, Color: h.Color, Type: "habit",
		})

		// Upstream records the doing rather than flagging the habit, so each
		// kept day is its own recording pointing back at the habit.
		for _, day := range h.Done {
			completions = append(completions, Recording{
				ParentID: h.ID, StartsAt: day, Type: "habit_completion",
			})
		}
	}

	return defs, completions
}

func todoRecordings(todos []Todo) []Recording {
	out := make([]Recording, 0, len(todos))

	for _, t := range todos {
		r := Recording{ID: t.ID, Title: t.Title, Type: "todo"}
		if t.Done {
			r.CompletedAt = time.Now()
		}

		out = append(out, r)
	}

	return out
}

// TodosRibbon draws the "Sometime this week" band. It sits below the grid, so
// it is composed by the caller rather than drawn inside the view.
func TodosRibbon(todos []Todo, width int) string {
	if len(todos) == 0 {
		return ""
	}

	return hintedSectionHeader(todosSectionLabel, "s to manage", width) + "\n" +
		renderTodosRibbon(todoRecordings(todos), width)
}

// Calendar draws the day or week grid.
//
// selectedKey identifies the highlighted event; pass the value Key returns for
// it. firstWeekDay sets which column a week starts in. use24 picks a 24-hour
// clock over am/pm.
func Calendar(
	kind CalendarView,
	events []Event,
	habits []Habit,
	anchor time.Time,
	firstWeekDay time.Weekday,
	width, height int,
	hint string,
	selectedKey string,
	use24 bool,
) string {
	sel := selection{eventKey: selectedKey, day: anchor}

	defs, completions := habitRecordings(habits)

	switch kind {
	case CalendarDay:
		// The day folds a habit's completions into the habit itself, which is
		// what splitRecordings does when both arrive together.
		day, _, folded, _, _ := splitRecordings(append(recordings(events), append(defs, completions...)...))

		return renderDayView(day, folded, nil,
			anchor, time.Now(), hint, width, height, sel, nil, use24)

	case CalendarYear:
		view, _, _ := renderYearView(recordings(events), anchor, time.Now(),
			firstWeekDay, width, height, hint, sel, false)

		return view
	}

	return renderWeekView(recordings(events), defs, completions,
		anchor, firstWeekDay, width, height, hint, nil, sel, use24)
}

// Key is the identifier the grid uses to mark an event as selected.
func Key(e Event) string { return e.recording().key() }

// HabitColours are the palette names a habit's colour may take.
func HabitColours() []string {
	out := make([]string, 0, len(heyColors))
	for name := range heyColors {
		out = append(out, name)
	}

	sort.Strings(out)

	return out
}
