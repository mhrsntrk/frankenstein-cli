package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	fcal "github.com/mhrsntrk/frankenstein-cli/internal/calendar"
	"github.com/mhrsntrk/frankenstein-cli/internal/when"
)

// eventDetail renders everything an event carries.
//
// The grid has room for a title and not much else, so location, notes and
// attendees had nowhere to be read: enter went straight to the edit form,
// which meant the only way to see an event's notes was to open something that
// could overwrite them.
func (m *Model) eventDetailView() string {
	e, ok := m.detailEvent()
	if !ok {
		return dimStyle.Render("  That event is no longer here.")
	}

	width := maxInt(20, m.contentWidth()-2)

	var b strings.Builder

	b.WriteString("  " + titleStyle.Render(truncateStr(e.Title, width)) + "\n\n")

	field := func(label, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}

		// The label column is fixed so the values line up, and the value wraps
		// under itself rather than under the label.
		const labelWidth = 11

		lines := wrap(value, maxInt(10, width-labelWidth))

		b.WriteString("  " + dimStyle.Render(fmt.Sprintf("%-*s", labelWidth-2, label)) +
			lines[0] + "\n")

		for _, l := range lines[1:] {
			b.WriteString("  " + strings.Repeat(" ", labelWidth-2) + l + "\n")
		}
	}

	field("When", eventWhen(e))

	if !e.AllDay {
		field("Length", when.FormatDuration(e.Duration()))
	}

	if name := m.calendarName(e.CalendarID); name != "" {
		field("Calendar", name)
	}

	field("Where", e.Location)
	field("Who", strings.Join(e.Attendees, ", "))
	field("Status", e.Status)
	field("Link", e.Link)

	if strings.TrimSpace(e.Notes) != "" {
		b.WriteString("\n")

		for _, para := range strings.Split(e.Notes, "\n") {
			for _, l := range wrap(para, width) {
				b.WriteString("  " + l + "\n")
			}
		}
	}

	return b.String()
}

// eventWhen is the human sentence for an event's time.
func eventWhen(e fcal.Event) string {
	if e.AllDay {
		// An all-day event's end is the day after it finishes, which is right
		// for arithmetic and wrong to show a reader.
		last := e.End.AddDate(0, 0, -1)
		if !last.After(e.Start) {
			return e.Start.Format("Monday 2 January 2006") + ", all day"
		}

		return e.Start.Format("Mon 2 Jan") + " to " + last.Format("Mon 2 Jan 2006") + ", all day"
	}

	day := e.Start.Format("Monday 2 January 2006")

	if sameDay(e.Start, e.End) {
		return fmt.Sprintf("%s, %s to %s", day,
			e.Start.Format("15:04"), e.End.Format("15:04"))
	}

	return fmt.Sprintf("%s %s to %s %s",
		e.Start.Format("Mon 2 Jan"), e.Start.Format("15:04"),
		e.End.Format("Mon 2 Jan"), e.End.Format("15:04"))
}

// calendarName is the name of the calendar an event came from, for accounts
// showing more than one.
func (m *Model) calendarName(id string) string {
	// Only worth showing when more than one calendar is drawn: on a single
	// calendar the answer is always the same and says nothing.
	if id == "" || len(m.calNames) < 2 {
		return ""
	}

	return m.calNames[id]
}

// detailEvent is the event the detail view is showing. It is held by ID rather
// than by index so that a reload underneath it cannot silently show a
// different event.
func (m *Model) detailEvent() (fcal.Event, bool) {
	for _, e := range m.events {
		if e.ID == m.detailID {
			return e, true
		}
	}

	return fcal.Event{}, false
}

// eventDetailKey handles the detail view's own keys.
//
// It reads them before the mail actions for the same reason the calendar does:
// e is "mark seen" and D means nothing in a mailbox, and neither should reach
// an event that is only being read.
func (m *Model) eventDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "esc", "backspace":
		m.pop()

		return m, nil, true

	case "e", "enter":
		e, ok := m.detailEvent()
		if !ok {
			m.pop()

			return m, nil, true
		}

		m.eventForm = newEventForm(&e, m.calAnchor())
		m.push(viewEventForm)

		return m, textinput.Blink, true

	case "D":
		e, ok := m.detailEvent()
		if !ok {
			m.pop()

			return m, nil, true
		}

		// Back to the grid first: the event is about to stop existing, and a
		// detail view of a deleted event has nothing to show.
		m.pop()
		m.loading = true

		return m, m.deleteEvent(e.ID, e.Title), true
	}

	return m, nil, false
}
