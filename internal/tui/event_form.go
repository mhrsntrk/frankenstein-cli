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
		f.title.SetValue(existing.Title)
		f.when.SetValue(existing.Start.Format("2006-01-02 15:04"))
		f.duration.SetValue(when.FormatDuration(existing.End.Sub(existing.Start)))
		f.location.SetValue(existing.Location)
		f.notes.SetValue(existing.Notes)
	} else {
		// A new event defaults to the next round half hour on the day being
		// looked at, which is nearly always what was meant.
		start := day.Truncate(30 * time.Minute).Add(30 * time.Minute)
		if start.Before(time.Now()) {
			start = time.Now().Truncate(30 * time.Minute).Add(30 * time.Minute)
		}

		f.when.SetValue(start.Format("2006-01-02 15:04"))
		f.duration.SetValue("30m")
	}

	f.title.Focus()

	return f
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

	return fcal.EventDraft{
		ID:       f.id,
		Title:    title,
		Start:    start,
		End:      start.Add(length),
		Location: strings.TrimSpace(f.location.Value()),
		Notes:    strings.TrimSpace(f.notes.Value()),
	}, nil
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

		return m, m.saveEvent(draft)

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

func (m *Model) saveEvent(draft fcal.EventDraft) tea.Cmd {
	calID := m.calendarID

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

func (m *Model) deleteEvent(id, title string) tea.Cmd {
	calID := m.calendarID

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
