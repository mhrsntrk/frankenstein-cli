package tui

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	fcal "github.com/mhrsntrk/frankenstein-cli/internal/calendar"
	"github.com/mhrsntrk/frankenstein-cli/internal/tui/heyui"
)

// fakeCal records what the calendar was asked to do.
type fakeCal struct {
	events  []fcal.Event
	created []fcal.EventDraft
	updated []fcal.EventDraft
	deleted []string
	err     error
}

func (c *fakeCal) Calendars(context.Context) ([]fcal.Calendar, error) {
	return []fcal.Calendar{{ID: "primary", Name: "Primary", Primary: true}}, nil
}

func (c *fakeCal) Events(context.Context, string, time.Time, time.Time) ([]fcal.Event, error) {
	return c.events, c.err
}

func (c *fakeCal) EventsFrom(_ context.Context, ids []string, _, _ time.Time) ([]fcal.Event, error) {
	if c.err != nil {
		return nil, c.err
	}

	// Tag each event with the first calendar asked for, which is enough for a
	// test to tell the colour mapping worked.
	out := append([]fcal.Event(nil), c.events...)

	if len(ids) > 0 {
		for i := range out {
			if out[i].CalendarID == "" {
				out[i].CalendarID = ids[0]
			}
		}
	}

	return out, nil
}

func (c *fakeCal) CreateEvent(_ context.Context, _ string, d fcal.EventDraft) (fcal.Event, error) {
	c.created = append(c.created, d)

	return fcal.Event{ID: "new", Title: d.Title, Start: d.Start, End: d.End}, nil
}

func (c *fakeCal) UpdateEvent(_ context.Context, _ string, d fcal.EventDraft) (fcal.Event, error) {
	c.updated = append(c.updated, d)

	return fcal.Event{ID: d.ID, Title: d.Title}, nil
}

func (c *fakeCal) DeleteEvent(_ context.Context, _, id string) error {
	c.deleted = append(c.deleted, id)

	return nil
}

func calHarness(t *testing.T) (*harness, *fakeCal) {
	t.Helper()

	h := newHarness(t)

	now := time.Now()
	mon := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).
		AddDate(0, 0, -((int(now.Weekday()) + 6) % 7))

	cal := &fakeCal{events: []fcal.Event{
		{ID: "e1", Title: "Standup", Start: mon.Add(9 * time.Hour), End: mon.Add(9*time.Hour + 15*time.Minute)},
		{ID: "e2", Title: "Lunch", Start: mon.AddDate(0, 0, 2).Add(12 * time.Hour), End: mon.AddDate(0, 0, 2).Add(13 * time.Hour)},
		{ID: "e3", Title: "Trip to Bilbao", Start: mon.AddDate(0, 0, 5), End: mon.AddDate(0, 0, 6), AllDay: true},
	}}

	h.m.cal = cal
	h.m.width, h.m.height = 150, 34
	h.m.section = sectionCalendar
	h.m.view = viewCalendar
	h.m.events = cal.events

	return h, cal
}

func TestCalendarGridsAllRender(t *testing.T) {
	h, _ := calHarness(t)

	h.m.calHabits = []heyui.Habit{
		{ID: 1, Name: "read", Color: "red", Done: []time.Time{time.Now()}},
	}
	h.m.calTodos = []heyui.Todo{{ID: 1, Title: "Renew the domain"}}

	for _, c := range []struct {
		name string
		view calendarView
	}{{"day", calendarDay}, {"week", calendarWeek}, {"year", calendarYear}} {
		h.m.calView = c.view

		out := h.m.View()
		if strings.TrimSpace(out) == "" {
			t.Errorf("%s view rendered nothing", c.name)
		}

		for _, line := range strings.Split(out, "\n") {
			if w := visibleWidth(line); w > h.m.width {
				t.Errorf("%s view overflows: %d > %d", c.name, w, h.m.width)
			}
		}
	}
}

// The bands are what made this look unlike hey-cli: without them the grid is
// just a box of hours.
func TestCalendarShowsHabitsAndTodos(t *testing.T) {
	h, _ := calHarness(t)

	h.m.calView = calendarWeek
	h.m.calHabits = []heyui.Habit{{ID: 1, Name: "read", Color: "red"}}
	h.m.calTodos = []heyui.Todo{{ID: 1, Title: "Renew the domain"}}

	out := h.m.View()

	for _, want := range []string{"Habits", "b to manage", "Sometime this week", "s to manage", "Renew the domain"} {
		if !strings.Contains(out, want) {
			t.Errorf("the week is missing %q", want)
		}
	}
}

func TestCalendarShowsAllDayEvents(t *testing.T) {
	h, _ := calHarness(t)
	h.m.calView = calendarWeek

	if out := h.m.View(); !strings.Contains(out, "All day") {
		t.Error("an all-day event did not produce an All day band")
	}
}

func TestCalendarCreateEditDelete(t *testing.T) {
	h, cal := calHarness(t)

	// Create.
	h.press(t, "c")

	if h.m.view != viewEventForm || h.m.eventForm == nil {
		t.Fatalf("c gave view %v", h.m.view)
	}

	h.m.eventForm.title.SetValue("Dentist")
	h.m.eventForm.when.SetValue("2026-09-10 14:00")
	h.m.eventForm.duration.SetValue("45m")

	h.press(t, "ctrl+d")

	if len(cal.created) != 1 {
		t.Fatalf("created %d events, want 1", len(cal.created))
	}

	got := cal.created[0]
	if got.Title != "Dentist" {
		t.Errorf("title = %q", got.Title)
	}

	if d := got.End.Sub(got.Start); d != 45*time.Minute {
		t.Errorf("length = %v, want 45m", d)
	}

	if h.m.eventForm != nil {
		t.Error("the form stayed open after saving")
	}

	// Edit the selected event.
	h.m.view = viewCalendar
	h.m.extraIdx = 1

	h.press(t, "enter")

	if h.m.eventForm == nil {
		t.Fatal("enter did not open the form")
	}

	if got := h.m.eventForm.title.Value(); got != "Lunch" {
		t.Errorf("the form was prefilled with %q, want Lunch", got)
	}

	if h.m.eventForm.id != "e2" {
		t.Errorf("the form is editing %q, want e2", h.m.eventForm.id)
	}

	h.m.eventForm.title.SetValue("Lunch with Ada")
	h.press(t, "ctrl+d")

	if len(cal.updated) != 1 || cal.updated[0].Title != "Lunch with Ada" {
		t.Errorf("updates = %+v", cal.updated)
	}

	// Delete.
	h.m.view = viewCalendar
	h.m.extraIdx = 0

	h.press(t, "D")

	if len(cal.deleted) != 1 || cal.deleted[0] != "e1" {
		t.Errorf("deleted = %v, want [e1]", cal.deleted)
	}
}

func TestEventFormRejectsBadInput(t *testing.T) {
	h, cal := calHarness(t)

	h.press(t, "c")
	h.m.eventForm.title.SetValue("")
	h.press(t, "ctrl+d")

	if h.m.err == nil {
		t.Error("an event with no title was accepted")
	}

	h.m.eventForm.title.SetValue("Thing")
	h.m.eventForm.duration.SetValue("banana")
	h.press(t, "ctrl+d")

	if h.m.err == nil {
		t.Error("an unreadable length was accepted")
	}

	if len(cal.created) != 0 {
		t.Errorf("a rejected form still created %d events", len(cal.created))
	}
}

// Typing in the form must not fire the calendar's single-letter keys.
func TestEventFormCapturesKeys(t *testing.T) {
	h, _ := calHarness(t)

	h.press(t, "c")

	before := h.m.calOffset

	for _, k := range []string{"n", "p", "t", "1", "2", "3", "D"} {
		h.press(t, k)
	}

	if h.m.calOffset != before {
		t.Error("typing in the form moved the calendar")
	}

	if h.m.view != viewEventForm {
		t.Error("typing in the form left it")
	}

	if got := h.m.eventForm.title.Value(); !strings.Contains(got, "np") {
		t.Errorf("the keys did not reach the title field: %q", got)
	}
}

func TestCalendarReportsFailureRatherThanEmptiness(t *testing.T) {
	h, cal := calHarness(t)

	cal.err = errServiceDisabled{}
	h.m.events = nil

	h.drain(t, h.m.loadEvents())

	out := h.m.View()

	if strings.Contains(out, "Nothing scheduled") {
		t.Error("a failed read was shown as an empty calendar")
	}

	if !strings.Contains(out, "not enabled") {
		t.Errorf("the disabled-API case is not explained:\n%s", out)
	}
}

type errServiceDisabled struct{}

func (errServiceDisabled) Error() string {
	return "list events: googleapi: Error 403: Google Calendar API has not been used in project 1 before or it is disabled"
}

// The footer advertises b and s; both have to do something.
func TestHabitsManagerWorks(t *testing.T) {
	h, _ := calHarness(t)

	h.press(t, "b")

	if h.m.view != viewHabits {
		t.Fatalf("b gave view %v, want viewHabits", h.m.view)
	}

	// Add one.
	h.press(t, "a")

	if !h.m.band.adding {
		t.Fatal("a did not open the input")
	}

	h.m.band.input.SetValue("read")
	h.press(t, "enter")

	habits, err := h.m.personal.Habits(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}

	if len(habits) != 1 || habits[0].Name != "read" {
		t.Fatalf("habits = %+v", habits)
	}

	// Keep it today, then clear it.
	h.drain(t, h.m.loadBands())
	h.press(t, " ")

	habits, _ = h.m.personal.Habits(context.Background(), false)
	if !habits[0].DoneToday {
		t.Error("space did not mark the habit kept")
	}

	h.drain(t, h.m.loadBands())
	h.press(t, " ")

	habits, _ = h.m.personal.Habits(context.Background(), false)
	if habits[0].DoneToday {
		t.Error("space a second time did not clear it")
	}

	// It renders.
	if out := h.m.View(); !strings.Contains(out, "read") {
		t.Error("the habit is not in the view")
	}

	h.press(t, "esc")

	if h.m.view != viewCalendar {
		t.Errorf("esc gave view %v, want viewCalendar", h.m.view)
	}
}

func TestTodosManagerNeedsGoogle(t *testing.T) {
	h, _ := calHarness(t)

	// The harness has no todo functions, which is what an unconfigured Google
	// account looks like.
	h.press(t, "s")

	if h.m.view == viewTodos {
		t.Error("the todo manager opened without a todo source")
	}

	if h.m.err == nil {
		t.Error("opening todos without Google said nothing")
	}
}

func TestTodosManagerAddsAndCompletes(t *testing.T) {
	h, _ := calHarness(t)

	var added, completed []string

	items := []TodoItem{{Title: "Renew the domain"}}

	h.m.todos = func(context.Context) ([]TodoItem, error) { return items, nil }
	h.m.addTodoFn = func(_ context.Context, title string) error {
		added = append(added, title)
		items = append(items, TodoItem{Title: title})

		return nil
	}
	h.m.completeTodoFn = func(_ context.Context, title string) error {
		completed = append(completed, title)

		return nil
	}

	h.press(t, "s")

	if h.m.view != viewTodos {
		t.Fatalf("s gave view %v, want viewTodos", h.m.view)
	}

	h.press(t, "a")
	h.m.band.input.SetValue("Book the flights")
	h.press(t, "enter")

	if len(added) != 1 || added[0] != "Book the flights" {
		t.Errorf("added = %v", added)
	}

	h.drain(t, h.m.loadBands())
	h.press(t, " ")

	if len(completed) != 1 {
		t.Errorf("completed = %v", completed)
	}
}

// TestDumpCalendar prints the calendar so the layout can be looked at rather
// than guessed about. DUMP_VIEW=1 go test ./internal/tui -run TestDumpCalendar -v
func TestDumpCalendar(t *testing.T) {
	if os.Getenv("DUMP_VIEW") == "" {
		t.Skip("set DUMP_VIEW=1 to print the rendered calendar")
	}

	h, _ := calHarness(t)

	h.m.calHabits = []heyui.Habit{
		{ID: 1, Name: "read", Color: "red", Done: []time.Time{time.Now()}},
	}
	h.m.calTodos = []heyui.Todo{{ID: 1, Title: "Renew the domain"}}
	h.m.calColours = map[string]string{"work": "blue", "personal": "green"}

	for i := range h.m.events {
		if i%2 == 0 {
			h.m.events[i].CalendarID = "work"

			continue
		}

		h.m.events[i].CalendarID = "personal"
	}

	t.Logf("\n%s\n", h.m.View())
}

func TestCalendarPickerTogglesAndPersists(t *testing.T) {
	h, _ := calHarness(t)

	var saved [][]string

	h.m.saveCalendars = func(ids []string) error {
		saved = append(saved, append([]string(nil), ids...))

		return nil
	}

	h.press(t, "g")

	if h.m.view != viewCalendars {
		t.Fatalf("g gave view %v, want viewCalendars", h.m.view)
	}

	h.m.picker.calendars = []fcal.Calendar{
		{ID: "primary", Name: "Personal", Primary: true, Color: "#0b8043"},
		{ID: "work", Name: "Work", Color: "#3f51b5"},
		{ID: "family", Name: "Family", Color: "#d50000"},
	}
	h.m.picker.shown = map[string]bool{"primary": true}

	// Show a second one.
	h.m.picker.idx = 1
	h.press(t, " ")

	if !h.m.picker.shown["work"] {
		t.Error("space did not show the calendar")
	}

	if len(saved) == 0 {
		t.Fatal("the choice was not persisted")
	}

	if got := saved[len(saved)-1]; len(got) != 2 {
		t.Errorf("saved %v, want two calendars", got)
	}

	// Hide it again.
	h.press(t, " ")

	if h.m.picker.shown["work"] {
		t.Error("space did not hide the calendar")
	}

	// The last one cannot be hidden, or the grid goes blank with no reason.
	h.m.picker.idx = 0
	h.press(t, " ")

	if !h.m.picker.shown["primary"] {
		t.Error("the last showing calendar was hidden")
	}

	if h.m.err == nil {
		t.Error("hiding the last calendar said nothing")
	}

	// a shows all, w shows only the one under the cursor.
	h.press(t, "a")

	if len(h.m.picker.selected()) != 3 {
		t.Errorf("a selected %d, want 3", len(h.m.picker.selected()))
	}

	h.m.picker.idx = 2
	h.press(t, "w")

	if got := h.m.picker.selected(); len(got) != 1 || got[0] != "family" {
		t.Errorf("w selected %v, want [family]", got)
	}
}

// Each calendar has to draw in a colour a reader recognises, or several at
// once is unreadable.
func TestCalendarColoursComeFromTheProvider(t *testing.T) {
	h, _ := calHarness(t)

	h.press(t, "g")
	h.drain(t, func() tea.Msg {
		return calendarsMsg{
			{ID: "primary", Name: "Personal", Color: "#0b8043"}, // green
			{ID: "work", Name: "Work", Color: "#3f51b5"},        // blue
			{ID: "family", Name: "Family", Color: "#d50000"},    // red
		}
	})

	for id, want := range map[string]string{
		"primary": "green",
		"work":    "blue",
		"family":  "red",
	} {
		if got := h.m.calColours[id]; got != want {
			t.Errorf("%s mapped to %q, want %q", id, got, want)
		}
	}

	// And the colour reaches the event the grid draws.
	h.m.view = viewCalendars

	if out := h.m.View(); !strings.Contains(out, "Work") {
		t.Error("the picker does not list the calendars")
	}
}

func TestEventsAreFetchedFromEveryShownCalendar(t *testing.T) {
	h, cal := calHarness(t)

	h.m.calendarIDs = []string{"primary", "work"}
	h.m.calColours = map[string]string{"primary": "green", "work": "blue"}

	h.drain(t, h.m.loadEvents())

	if len(h.m.events) != len(cal.events) {
		t.Fatalf("got %d events, want %d", len(h.m.events), len(cal.events))
	}

	// Every event knows which calendar it came from, which is what the colour
	// lookup keys on.
	for _, e := range h.m.events {
		if e.CalendarID == "" {
			t.Errorf("%q has no calendar", e.Title)
		}
	}
}
