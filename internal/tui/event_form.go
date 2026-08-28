package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	fcal "github.com/mhrsntrk/frankenstein-cli/internal/calendar"
	"github.com/mhrsntrk/frankenstein-cli/internal/when"
)

// eventForm is the create-and-edit form for a calendar entry.
//
// Deliberately five plain fields rather than a date picker: typing "14:00" and
// "90m" is faster than driving a widget, and the parser already accepts the
// handful of formats a person actually types.
type eventForm struct {
	title    textinput.Model
	when     textinput.Model
	duration textinput.Model
	location textinput.Model
	notes    textinput.Model

	field int

	// id is empty for a new event and set when editing one.
	id string

	// calendarID is the calendar the event being edited lives on, so a save
	// goes back where the event came from rather than to the configured
	// default. Empty for a new event.
	calendarID string

	// allDay and attendees are carried from the event being edited: the form
	// has no fields for them, and the provider does a full PUT, so a draft
	// that dropped them would silently convert an all-day event to a timed
	// one and wipe the guest list.
	allDay    bool
	attendees []string

	// seededWhen and seededDuration are what the time fields were prefilled
	// with, and origStart and origEnd the event's own times. A save compares
	// the fields against the seed to tell whether the times were actually
	// touched: an untouched all-day event keeps AllDay and its exact date
	// range, because round-tripping midnight through the parser is how a
	// date-only event turns into a timed one.
	seededWhen     string
	seededDuration string
	origStart      time.Time
	origEnd        time.Time
}

const eventFormFields = 5

func newEventForm(existing *fcal.Event, day time.Time) *eventForm {
	field := func(placeholder string) textinput.Model {
		t := textinput.New()
		t.Placeholder = placeholder
		t.CharLimit = 300

		return t
	}

	f := &eventForm{
		title:    field("Standup"),
		when:     field("2006-01-02 15:04, or 14:00 for today"),
		duration: field("30m"),
		location: field("optional"),
		notes:    field("optional"),
	}

	if existing != nil {
		f.id = existing.ID
		f.calendarID = existing.CalendarID
		f.allDay = existing.AllDay
		f.attendees = append([]string(nil), existing.Attendees...)
		f.origStart, f.origEnd = existing.Start, existing.End
		f.title.SetValue(existing.Title)
		f.when.SetValue(existing.Start.Format("2006-01-02 15:04"))
		f.duration.SetValue(when.FormatDuration(existing.End.Sub(existing.Start)))
		f.location.SetValue(existing.Location)
		f.notes.SetValue(existing.Notes)
		f.seededWhen = f.when.Value()
		f.seededDuration = f.duration.Value()
	} else {
		// A new event defaults to the next round half hour on the day being
		// looked at, which is nearly always what was meant.
		start := nextHalfHour(day)
		if start.Before(time.Now()) {
			start = nextHalfHour(time.Now())
		}

		f.when.SetValue(start.Format("2006-01-02 15:04"))
		f.duration.SetValue("30m")
	}

	f.title.Focus()

	return f
}

// nextHalfHour rounds up to the next :00 or :30 on the wall clock. Truncate
// rounds in absolute time, so in a zone with a fractional UTC offset it lands
// off the half hour.
func nextHalfHour(t time.Time) time.Time {
	minute := 30
	if t.Minute() >= 30 {
		// time.Date normalises minute 60 into the next hour.
		minute = 60
	}

	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), minute, 0, 0, t.Location())
}

func (f *eventForm) inputs() []*textinput.Model {
	return []*textinput.Model{&f.title, &f.when, &f.duration, &f.location, &f.notes}
}

func (f *eventForm) focus() {
	for i, in := range f.inputs() {
		if i == f.field {
			in.Focus()

			continue
		}

		in.Blur()
	}
}

// draft turns the typed values into an event, or says what is wrong with them.
func (f *eventForm) draft() (fcal.EventDraft, error) {
	title := strings.TrimSpace(f.title.Value())
	if title == "" {
		return fcal.EventDraft{}, fmt.Errorf("an event needs a title")
	}

	start, err := when.Parse(f.when.Value())
	if err != nil {
		return fcal.EventDraft{}, err
	}

	length := 30 * time.Minute

	if v := strings.TrimSpace(f.duration.Value()); v != "" {
		length, err = time.ParseDuration(v)
		if err != nil {
			return fcal.EventDraft{}, fmt.Errorf("could not read %q as a length; try 45m or 1h30m", v)
		}
	}

	if length <= 0 {
		return fcal.EventDraft{}, fmt.Errorf("an event needs a length")
	}

	d := fcal.EventDraft{
		ID:        f.id,
		Title:     title,
		Start:     start,
		End:       start.Add(length),
		Location:  strings.TrimSpace(f.location.Value()),
		Notes:     strings.TrimSpace(f.notes.Value()),
		Attendees: f.attendees,
	}

	// An all-day event whose time fields were left as seeded stays all-day,
	// with its own date range: the parsed midnight would otherwise turn a
	// date into an instant. Touching either field is the one way the form can
	// express "make this a timed event", so that is what it does.
	if f.allDay && f.when.Value() == f.seededWhen &&
		strings.TrimSpace(f.duration.Value()) == f.seededDuration {
		d.AllDay = true
		d.Start, d.End = f.origStart, f.origEnd
	}

	return d, nil
}

// --- keys -------------------------------------------------------------------

func (m *Model) handleEventFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.eventForm

	switch msg.String() {
	case "esc":
		m.eventForm = nil
		m.pop()

		return m, nil

	case "ctrl+d", "ctrl+s":
		draft, err := f.draft()
		if err != nil {
			m.err = err

			return m, nil
		}

		m.loading = true

		return m, m.saveEvent(draft, f.calendarID)

	case "tab", "down":
		f.field = (f.field + 1) % eventFormFields
		f.focus()

		return m, nil

	case "shift+tab", "up":
		f.field = (f.field + eventFormFields - 1) % eventFormFields
		f.focus()

		return m, nil
	}

	var cmd tea.Cmd

	in := f.inputs()[f.field]
	*in, cmd = in.Update(msg)

	return m, cmd
}

// --- commands ---------------------------------------------------------------

// saveEvent writes a draft to eventCalID, the calendar the event lives on, so
// an edit goes back where the event came from. A new event carries no calendar
// of its own and goes to the configured one.
func (m *Model) saveEvent(draft fcal.EventDraft, eventCalID string) tea.Cmd {
	// The entry points refuse without a provider, but a command that
	// dereferenced nil would panic inside the runtime, so it checks again.
	if m.cal == nil {
		return func() tea.Msg { return errMsg{fcal.ErrNotConfigured} }
	}

	calID := eventCalID
	if calID == "" {
		calID = m.calendarID
	}

	return bg(60*time.Second, func(ctx context.Context) tea.Msg {
		var err error

		if draft.ID == "" {
			_, err = m.cal.CreateEvent(ctx, calID, draft)
		} else {
			_, err = m.cal.UpdateEvent(ctx, calID, draft)
		}

		if err != nil {
			return errMsg{err}
		}

		note := "event created"
		if draft.ID != "" {
			note = "event updated"
		}

		return actionMsg{note: note, reloadCalendar: true}
	})
}

// deleteEvent removes an event from eventCalID, the calendar it lives on,
// falling back to the configured one for events that carry none.
func (m *Model) deleteEvent(eventCalID, id, title string) tea.Cmd {
	if m.cal == nil {
		return func() tea.Msg { return errMsg{fcal.ErrNotConfigured} }
	}

	calID := eventCalID
	if calID == "" {
		calID = m.calendarID
	}

	return bg(60*time.Second, func(ctx context.Context) tea.Msg {
		if err := m.cal.DeleteEvent(ctx, calID, id); err != nil {
			return errMsg{err}
		}

		return actionMsg{note: "deleted " + title, reloadCalendar: true}
	})
}

// --- view -------------------------------------------------------------------

func (m *Model) eventFormView() string {
	f := m.eventForm
	if f == nil {
		return ""
	}

	labels := []string{"Title", "When", "Length", "Where", "Notes"}

	var b strings.Builder

	for i, in := range f.inputs() {
		marker := "  "
		if f.field == i {
			marker = markStyle.Render("> ")
		}

		b.WriteString(marker + dimStyle.Render(fmt.Sprintf("%-8s", labels[i])) + in.View() + "\n")
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  When accepts 2026-09-01 14:00, 14:00 for today, or tomorrow.\n"))
	b.WriteString(dimStyle.Render("  Length accepts 45m, 1h30m.\n"))

	return b.String()
}
