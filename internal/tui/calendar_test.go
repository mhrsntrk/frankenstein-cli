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

// fakeCal records what the calendar was asked to do, and on which calendar.
type fakeCal struct {
	events  []fcal.Event
	created []fcal.EventDraft
	updated []fcal.EventDraft
	deleted []string

	// createdCal, updatedCal and deletedCal are the calendar IDs the writes
	// went to, index-aligned with the slices above.
	createdCal []string
	updatedCal []string
	deletedCal []string

	// fetchedFrom and fetchedTo are the windows EventsFrom was asked for.
	fetchedFrom []time.Time
	fetchedTo   []time.Time

	err error
}

func (c *fakeCal) Calendars(context.Context) ([]fcal.Calendar, error) {
	return []fcal.Calendar{{ID: "primary", Name: "Primary", Primary: true}}, nil
}

func (c *fakeCal) Events(context.Context, string, time.Time, time.Time) ([]fcal.Event, error) {
	return c.events, c.err
}

func (c *fakeCal) EventsFrom(_ context.Context, ids []string, from, to time.Time) ([]fcal.Event, error) {
	c.fetchedFrom = append(c.fetchedFrom, from)
	c.fetchedTo = append(c.fetchedTo, to)

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

func (c *fakeCal) CreateEvent(_ context.Context, calID string, d fcal.EventDraft) (fcal.Event, error) {
	c.created = append(c.created, d)
	c.createdCal = append(c.createdCal, calID)

	return fcal.Event{ID: "new", Title: d.Title, Start: d.Start, End: d.End}, nil
}

func (c *fakeCal) UpdateEvent(_ context.Context, calID string, d fcal.EventDraft) (fcal.Event, error) {
	c.updated = append(c.updated, d)
	c.updatedCal = append(c.updatedCal, calID)

	return fcal.Event{ID: d.ID, Title: d.Title}, nil
}

func (c *fakeCal) DeleteEvent(_ context.Context, calID, id string) error {
	c.deleted = append(c.deleted, id)
	c.deletedCal = append(c.deletedCal, calID)

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

	// Edit the selected event. enter reads it and e edits it, so that seeing
	// an event's notes does not mean opening something that can overwrite them.
	h.m.view = viewCalendar
	h.m.extraIdx = 1

	h.press(t, "enter")

	if h.m.view != viewEventDetail {
		t.Fatalf("enter opened %v, want the detail view", h.m.view)
	}

	if h.m.eventForm != nil {
		t.Error("enter opened the edit form; it should only read")
	}

	h.press(t, "e")

	if h.m.eventForm == nil {
		t.Fatal("e did not open the form")
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

// Todos are local, so they work on a machine that has never authorised a
// Google account. They used to be Google Tasks, and the manager refused to
// open without one.
func TestTodosWorkWithoutGoogle(t *testing.T) {
	h, _ := calHarness(t)

	h.m.cal = nil

	h.press(t, "s")

	if h.m.view != viewTodos {
		t.Fatalf("the todo manager did not open without a calendar; view = %v", h.m.view)
	}

	if h.m.err != nil {
		t.Errorf("opening todos complained: %v", h.m.err)
	}

	// Add one, and it has to reach the store rather than a session slice.
	h.press(t, "a")
	h.typeText(t, "Renew the domain")
	h.press(t, "enter")

	stored, err := h.ps.Todos(context.Background(), false)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	if len(stored) != 1 || stored[0].Title != "Renew the domain" {
		t.Fatalf("the store holds %+v", stored)
	}

	// And it has to come back on the ribbon.
	h.m.view = viewCalendar

	if out := h.m.View(); !strings.Contains(out, "Renew the domain") {
		t.Error("the todo did not appear on the calendar ribbon")
	}

	// Completing it takes it off the open list.
	h.m.view = viewTodos
	h.drain(t, h.m.loadBands())
	h.press(t, " ")

	stored, _ = h.ps.Todos(context.Background(), false)
	if len(stored) != 0 {
		t.Errorf("after completing, open todos = %+v", stored)
	}
}

func TestTodosManagerAddsAndCompletes(t *testing.T) {
	h, _ := calHarness(t)

	var added []string
	var completed []int64

	items := []TodoItem{{ID: 41, Title: "Renew the domain"}}

	h.m.todos = func(context.Context) ([]TodoItem, error) { return items, nil }
	h.m.addTodoFn = func(_ context.Context, title string) error {
		added = append(added, title)
		items = append(items, TodoItem{ID: int64(len(items) + 41), Title: title})

		return nil
	}
	h.m.completeTodoFn = func(_ context.Context, id int64) error {
		completed = append(completed, id)

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

	if len(completed) != 1 || completed[0] != 41 {
		t.Errorf("completed = %v, want [41]", completed)
	}
}

// Completion goes by ID: two todos sharing a title used to complete
// whichever the title lookup found first.
func TestTodosCompleteByIDNotTitle(t *testing.T) {
	h, _ := calHarness(t)

	var completed []int64

	h.m.todos = func(context.Context) ([]TodoItem, error) {
		return []TodoItem{
			{ID: 1, Title: "Call the bank"},
			{ID: 2, Title: "Call the bank"},
		}, nil
	}
	h.m.completeTodoFn = func(_ context.Context, id int64) error {
		completed = append(completed, id)

		return nil
	}

	h.press(t, "s")
	h.drain(t, h.m.loadBands())

	h.m.band.idx = 1
	h.press(t, " ")

	if len(completed) != 1 || completed[0] != 2 {
		t.Errorf("completed = %v, want [2] (the second duplicate)", completed)
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

// TestEventDetailShowsEverything is the reason the detail view exists: the
// grid has room for a title, so location, notes and attendees had nowhere to
// be read at all.
func TestEventDetailShowsEverything(t *testing.T) {
	h, cal := calHarness(t)

	start := time.Date(2026, 8, 28, 14, 0, 0, 0, time.Local)
	cal.events = []fcal.Event{{
		ID:        "e9",
		Title:     "Design review",
		Location:  "Room 4, second floor",
		Notes:     "Bring the printed mocks.",
		Attendees: []string{"ada@example.com", "grace@example.com"},
		Status:    "confirmed",
		Start:     start,
		End:       start.Add(90 * time.Minute),
	}}

	h.m.view = viewCalendar
	h.drain(t, h.m.loadEvents())

	h.m.extraIdx = 0
	h.press(t, "enter")

	if h.m.view != viewEventDetail {
		t.Fatalf("view = %v, want the detail view", h.m.view)
	}

	out := h.m.View()

	for _, want := range []string{
		"Design review",
		"Room 4, second floor",
		"Bring the printed mocks.",
		"ada@example.com",
		"grace@example.com",
		"confirmed",
		"14:00",
		"15:30",
		"1h30m",
		"Friday 28 August 2026",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the detail view is missing %q", want)
		}
	}

	// And the footer has to say how to get out and how to edit.
	for _, want := range []string{"edit", "back"} {
		if !strings.Contains(out, want) {
			t.Errorf("the footer is missing %q", want)
		}
	}

	h.press(t, "esc")

	if h.m.view != viewCalendar {
		t.Errorf("esc left the view at %v, want the calendar", h.m.view)
	}
}

// TestCalendarRefusesWithoutProvider pins the nil guard: m.cal is
// legitimately nil on a machine that never ran the setup, and c, e and D used
// to hand a command a nil provider to dereference.
func TestCalendarRefusesWithoutProvider(t *testing.T) {
	h, _ := calHarness(t)

	h.m.cal = nil
	h.m.extraIdx = 0

	for _, k := range []string{"c", "e", "D"} {
		h.press(t, k)

		if h.m.err == nil {
			t.Errorf("%s with no provider said nothing", k)
		}

		if h.m.view != viewCalendar {
			t.Errorf("%s with no provider left the calendar; view = %v", k, h.m.view)
		}
	}

	if h.m.eventForm != nil {
		t.Error("a form was opened without a provider to save into")
	}

	// The picker cannot load a list without a provider either; opening it
	// would show "loading…" forever.
	h.press(t, "g")

	if h.m.err == nil || h.m.view != viewCalendar {
		t.Errorf("g with no provider gave view %v, err %v", h.m.view, h.m.err)
	}
}

// The picker's keys can be pressed while its list is still loading.
func TestCalendarPickerHandlesEmptyList(t *testing.T) {
	h, _ := calHarness(t)

	h.m.picker = &calendarPicker{shown: map[string]bool{}}
	h.m.push(viewCalendars)

	// Neither may panic on the empty list.
	h.press(t, "w")
	h.press(t, " ")

	if h.m.view != viewCalendars {
		t.Errorf("view = %v, want viewCalendars", h.m.view)
	}
}

// A reload can shrink the band's list under its cursor; the next space would
// then index out of range.
func TestBandCursorClampsWhenListShrinks(t *testing.T) {
	h, _ := calHarness(t)

	var completed []int64

	items := []TodoItem{
		{ID: 1, Title: "One"}, {ID: 2, Title: "Two"}, {ID: 3, Title: "Three"},
	}

	h.m.todos = func(context.Context) ([]TodoItem, error) { return items, nil }
	h.m.completeTodoFn = func(_ context.Context, id int64) error {
		completed = append(completed, id)

		return nil
	}

	h.press(t, "s")
	h.drain(t, h.m.loadBands())

	h.m.band.idx = 2

	items = items[:1]
	h.drain(t, h.m.loadBands())

	if h.m.band.idx != 0 {
		t.Errorf("after the shrink band.idx = %d, want 0", h.m.band.idx)
	}

	h.press(t, " ")

	if len(completed) != 1 || completed[0] != 1 {
		t.Errorf("completed = %v, want [1]", completed)
	}
}

// Editing must not silently rewrite what the form has no fields for: the
// provider does a full PUT, so a draft without AllDay and the attendees turns
// an all-day event into a timed one and wipes the guest list.
func TestEditPreservesAllDayAndAttendees(t *testing.T) {
	h, cal := calHarness(t)

	cal.events[2].Attendees = []string{"ada@example.com", "grace@example.com"}
	h.drain(t, h.m.loadEvents())

	h.m.extraIdx = 2 // the all-day Trip to Bilbao

	h.press(t, "e")

	if h.m.eventForm == nil || h.m.eventForm.id != "e3" {
		t.Fatalf("e did not open the form on e3")
	}

	// Rename it without touching the time fields.
	h.m.eventForm.title.SetValue("Trip to Donostia")
	h.press(t, "ctrl+d")

	if len(cal.updated) != 1 {
		t.Fatalf("updated %d events, want 1", len(cal.updated))
	}

	got := cal.updated[0]

	if !got.AllDay {
		t.Error("an untouched all-day event was saved as a timed one")
	}

	if !got.Start.Equal(cal.events[2].Start) || !got.End.Equal(cal.events[2].End) {
		t.Errorf("dates changed: %v to %v, want %v to %v",
			got.Start, got.End, cal.events[2].Start, cal.events[2].End)
	}

	if len(got.Attendees) != 2 || got.Attendees[0] != "ada@example.com" {
		t.Errorf("attendees = %v, want both carried", got.Attendees)
	}

	// Actually changing the times is the one way the form can say "make this
	// a timed event", so it does.
	h.m.view = viewCalendar
	h.m.extraIdx = 2

	h.press(t, "e")
	h.m.eventForm.when.SetValue("2030-09-10 09:00")
	h.m.eventForm.duration.SetValue("1h")
	h.press(t, "ctrl+d")

	if len(cal.updated) != 2 {
		t.Fatalf("updated %d events, want 2", len(cal.updated))
	}

	if second := cal.updated[1]; second.AllDay {
		t.Error("changed times still saved as all-day")
	} else if second.End.Sub(second.Start) != time.Hour {
		t.Errorf("length = %v, want 1h", second.End.Sub(second.Start))
	}

	if got := cal.updated[1].Attendees; len(got) != 2 {
		t.Errorf("attendees on the timed save = %v, want both carried", got)
	}
}

// Edits and deletes go to the calendar the event lives on; only a new event
// goes to the configured default.
func TestWritesTargetTheEventsOwnCalendar(t *testing.T) {
	h, cal := calHarness(t)

	h.m.calendarID = "primary"
	cal.events[1].CalendarID = "work"

	h.drain(t, h.m.loadEvents())

	// Edit the work event.
	h.m.extraIdx = 1

	h.press(t, "e")
	h.m.eventForm.title.SetValue("Lunch, moved")
	h.press(t, "ctrl+d")

	if len(cal.updatedCal) != 1 || cal.updatedCal[0] != "work" {
		t.Errorf("update went to %v, want [work]", cal.updatedCal)
	}

	// Delete it.
	h.m.view = viewCalendar
	h.m.extraIdx = 1

	h.press(t, "D")

	if len(cal.deletedCal) != 1 || cal.deletedCal[0] != "work" {
		t.Errorf("delete went to %v, want [work]", cal.deletedCal)
	}

	// A new event still goes to the configured calendar.
	h.m.view = viewCalendar

	h.press(t, "c")
	h.m.eventForm.title.SetValue("Dentist")
	h.m.eventForm.when.SetValue("2030-09-10 14:00")
	h.press(t, "ctrl+d")

	if len(cal.createdCal) != 1 || cal.createdCal[0] != "primary" {
		t.Errorf("create went to %v, want [primary]", cal.createdCal)
	}
}

// The year grid steps whole years and fetches the whole year it shows.
func TestYearViewStepsYearsAndFetchesTheYear(t *testing.T) {
	h, cal := calHarness(t)

	h.press(t, "3")

	year := time.Now().Year()

	wantFrom := time.Date(year, time.January, 1, 0, 0, 0, 0, time.Local)
	wantTo := wantFrom.AddDate(1, 0, 0)

	if n := len(cal.fetchedFrom); n == 0 {
		t.Fatal("3 fetched nothing")
	} else if got := cal.fetchedFrom[n-1]; !got.Equal(wantFrom) {
		t.Errorf("window starts %v, want %v", got, wantFrom)
	} else if got := cal.fetchedTo[n-1]; !got.Equal(wantTo) {
		t.Errorf("window ends %v, want %v", got, wantTo)
	}

	// n steps a whole year forward, p back to where it was.
	h.press(t, "n")

	if got := h.m.calAnchor().Year(); got != year+1 {
		t.Errorf("after n the anchor year = %d, want %d", got, year+1)
	}

	if n := len(cal.fetchedFrom); cal.fetchedFrom[n-1].Year() != year+1 {
		t.Errorf("after n the window starts in %d, want %d", cal.fetchedFrom[n-1].Year(), year+1)
	}

	h.press(t, "p")

	if got := h.m.calAnchor().Year(); got != year {
		t.Errorf("after p the anchor year = %d, want %d", got, year)
	}
}

// A slow response from an old window must not overwrite the one on screen.
func TestStaleEventsResponseIsDropped(t *testing.T) {
	h, cal := calHarness(t)

	now := time.Now()

	cal.events = []fcal.Event{{ID: "old", Title: "Old", Start: now, End: now.Add(time.Hour)}}
	slow := h.m.loadEvents()
	slowMsg := slow() // the response exists, but arrives late

	cal.events = []fcal.Event{{ID: "new", Title: "New", Start: now, End: now.Add(time.Hour)}}
	fast := h.m.loadEvents()

	h.m.Update(fast())
	h.m.Update(slowMsg)

	if len(h.m.events) != 1 || h.m.events[0].ID != "new" {
		t.Errorf("events = %+v, want the newer window to survive", h.m.events)
	}
}

// A reload reorders the list; the cursor follows the event it was on, so
// enter, e and D keep acting on what the user is looking at.
func TestEventsReloadReanchorsTheCursor(t *testing.T) {
	h, cal := calHarness(t)

	h.drain(t, h.m.loadEvents())

	h.m.extraIdx = 1 // Lunch

	cal.events = []fcal.Event{cal.events[2], cal.events[1], cal.events[0]}
	h.drain(t, h.m.loadEvents())

	if e, ok := h.m.selectedEvent(); !ok || e.ID != "e2" {
		t.Errorf("after the reload the cursor is on %+v, want e2", e)
	}

	// An event that went away leaves the cursor clamped into the new list.
	h.m.extraIdx = 2
	cal.events = cal.events[:1]
	h.drain(t, h.m.loadEvents())

	if h.m.extraIdx != 0 {
		t.Errorf("after the shrink extraIdx = %d, want 0", h.m.extraIdx)
	}
}

// h and left have to leave the detail view the way esc does.
func TestEventDetailGoesBackOnH(t *testing.T) {
	h, _ := calHarness(t)

	h.m.extraIdx = 0
	h.press(t, "enter")

	if h.m.view != viewEventDetail {
		t.Fatalf("enter gave view %v", h.m.view)
	}

	h.press(t, "h")

	if h.m.view != viewCalendar {
		t.Errorf("h gave view %v, want the calendar", h.m.view)
	}
}

// The calendar's Day/Week/Year row is clickable, and a click inside the grid
// itself does nothing: the grid is not one item per line, so mapping a screen
// row onto the cursor only scrambled the selection.
func TestCalendarSubnavClicksAndInertBody(t *testing.T) {
	h, cal := calHarness(t)

	rows := h.m.chromeRows()
	if rows.subnav < 0 {
		t.Fatal("the calendar sub-nav row is not in the click map")
	}

	line := stripANSI(strings.Split(h.m.header(), "\n")[rows.subnav])

	colOf := func(label string) int {
		i := strings.Index(line, label)
		if i < 0 {
			t.Fatalf("the sub-nav row does not show %q: %q", label, line)
		}

		return i + len(h.m.gutter())
	}

	fetches := len(cal.fetchedFrom)

	_, cmd := h.m.click(colOf("Year"), rows.subnav)
	h.drain(t, cmd)

	if h.m.calView != calendarYear {
		t.Errorf("clicking Year gave view %v", h.m.calView)
	}

	if len(cal.fetchedFrom) == fetches {
		t.Error("clicking Year did not reload the events")
	}

	_, cmd = h.m.click(colOf("Day"), rows.subnav)
	h.drain(t, cmd)

	if h.m.calView != calendarDay {
		t.Errorf("clicking Day gave view %v", h.m.calView)
	}

	// A click in the grid must not move the selection.
	h.m.extraIdx = 1
	before := h.m.extraIdx

	h.m.click(len(h.m.gutter())+10, rows.body+5)

	if h.m.extraIdx != before {
		t.Errorf("a grid click moved extraIdx from %d to %d", before, h.m.extraIdx)
	}
}

// The agenda line covers today, not only events that start today.
func TestAgendaLineIncludesSpanningEvents(t *testing.T) {
	h, _ := calHarness(t)

	now := time.Now()

	h.m.events = []fcal.Event{
		{ID: "trip", Title: "Trip", Start: now.AddDate(0, 0, -1), End: now.AddDate(0, 0, 1)},
		{ID: "past", Title: "Yesterday only", Start: now.AddDate(0, 0, -1), End: now.AddDate(0, 0, -1).Add(time.Hour)},
	}

	line := h.m.agendaLine()

	if !strings.Contains(line, "Trip") {
		t.Errorf("an event spanning today is missing from %q", line)
	}

	if strings.Contains(line, "Yesterday only") {
		t.Errorf("an event that ended yesterday appears in %q", line)
	}
}

// The default start has to land on :00 or :30 of the wall clock, which
// Truncate does not do in a zone with a fractional UTC offset.
func TestNewEventDefaultsToWallClockHalfHour(t *testing.T) {
	kathmandu := time.FixedZone("UTC+5:45", 5*3600+45*60)

	for _, c := range []struct {
		minute, want int
	}{{7, 30}, {29, 30}, {30, 0}, {45, 0}, {0, 30}} {
		day := time.Date(2030, 5, 3, 10, c.minute, 0, 0, kathmandu)

		got := nextHalfHour(day)
		if got.Minute() != c.want {
			t.Errorf("nextHalfHour(10:%02d) = %s, want minute %02d",
				c.minute, got.Format("15:04"), c.want)
		}

		if !got.After(day) {
			t.Errorf("nextHalfHour(10:%02d) = %s did not move forward", c.minute, got.Format("15:04"))
		}
	}

	// And the form uses it: a fresh event on a future day starts on a round
	// half hour of that day's wall clock.
	f := newEventForm(nil, time.Date(2030, 5, 3, 10, 7, 0, 0, kathmandu))

	if v := f.when.Value(); !strings.HasSuffix(v, ":30") && !strings.HasSuffix(v, ":00") {
		t.Errorf("a new event defaults to %q, want a round half hour", v)
	}
}

// TestEventDetailSurvivesReload guards the reason detail is held by ID: a
// reload reorders m.events, and an index would quietly show a different event.
func TestEventDetailSurvivesReload(t *testing.T) {
	h, cal := calHarness(t)

	start := time.Date(2026, 8, 28, 9, 0, 0, 0, time.Local)
	cal.events = []fcal.Event{
		{ID: "a", Title: "First", Start: start, End: start.Add(time.Hour)},
		{ID: "b", Title: "Second", Start: start.Add(2 * time.Hour), End: start.Add(3 * time.Hour)},
	}

	h.m.view = viewCalendar
	h.drain(t, h.m.loadEvents())

	h.m.extraIdx = 1
	h.press(t, "enter")

	// The list comes back in the other order.
	cal.events = []fcal.Event{cal.events[1], cal.events[0]}
	h.drain(t, h.m.loadEvents())

	if got, ok := h.m.detailEvent(); !ok || got.Title != "Second" {
		t.Errorf("after a reload the detail view shows %+v, want Second", got)
	}
}
