package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	fcal "github.com/mhrsntrk/frankenstein-cli/internal/calendar"
	"github.com/mhrsntrk/frankenstein-cli/internal/tui/heyui"
)

// The calendar picker: which of the account's calendars are drawn, and in
// what colour.

type calendarPicker struct {
	calendars []fcal.Calendar
	shown     map[string]bool
	idx       int
}

type calendarsMsg []fcal.Calendar

func (m *Model) openCalendars() (tea.Model, tea.Cmd) {
	// Without a provider the list can never arrive, and the picker would sit
	// at "loading…" forever. Refusing with the fix beats a dead end.
	if m.cal == nil {
		m.err = fcal.ErrNotConfigured

		return m, nil
	}

	// The set is resolved once the list arrives, because "primary" only
	// becomes a real address in the presence of the list.
	m.picker = &calendarPicker{shown: map[string]bool{}}
	m.push(viewCalendars)

	return m, m.loadCalendars()
}

func (m *Model) loadCalendars() tea.Cmd {
	if m.cal == nil {
		return nil
	}

	return bg(30*time.Second, func(ctx context.Context) tea.Msg {
		cals, err := m.cal.Calendars(ctx)
		if err != nil {
			return errMsg{err}
		}

		return calendarsMsg(cals)
	})
}

func (m *Model) handleCalendarPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.picker

	switch msg.String() {
	case "esc", "q", "g":
		m.picker = nil
		m.pop()

		return m, nil

	case "j", "down":
		p.idx = clamp(p.idx+1, 0, maxInt(0, len(p.calendars)-1))

		return m, nil

	case "k", "up":
		p.idx = clamp(p.idx-1, 0, maxInt(0, len(p.calendars)-1))

		return m, nil

	case " ", "enter":
		if len(p.calendars) == 0 {
			return m, nil
		}

		id := p.calendars[p.idx].ID

		if p.shown[id] {
			// Hiding the last one would leave a blank grid with no way to tell
			// why, so the last showing calendar stays.
			if len(p.selected()) == 1 {
				m.err = fmt.Errorf("at least one calendar has to stay shown")

				return m, nil
			}

			delete(p.shown, id)
		} else {
			p.shown[id] = true
		}

		return m, m.applyCalendars(p)

	case "a":
		for _, c := range p.calendars {
			p.shown[c.ID] = true
		}

		return m, m.applyCalendars(p)

	case "w":
		// Only the one under the cursor, which has to exist: the key can be
		// pressed while the list is still loading.
		if len(p.calendars) == 0 {
			return m, nil
		}

		p.shown = map[string]bool{p.calendars[p.idx].ID: true}

		return m, m.applyCalendars(p)
	}

	return m, nil
}

func (p *calendarPicker) selected() []string {
	out := make([]string, 0, len(p.shown))

	for _, c := range p.calendars {
		if p.shown[c.ID] {
			out = append(out, c.ID)
		}
	}

	return out
}

// applyCalendars saves the choice and reloads the grid.
//
// It is written to the config rather than kept for the session: which
// calendars you look at is not something anyone wants to re-pick every time.
func (m *Model) applyCalendars(p *calendarPicker) tea.Cmd {
	m.calendarIDs = p.selected()
	m.calColours = map[string]string{}
	m.calNames = map[string]string{}

	for _, c := range p.calendars {
		m.calColours[c.ID] = heyui.ColourFor(c.Color)
		m.calNames[c.ID] = c.Name
	}

	ids := append([]string(nil), m.calendarIDs...)
	save := m.saveCalendars

	return tea.Batch(
		m.loadEvents(),
		func() tea.Msg {
			if save != nil {
				if err := save(ids); err != nil {
					return errMsg{err}
				}
			}

			return nil
		},
	)
}

func (m *Model) calendarsView() string {
	p := m.picker
	if p == nil {
		return ""
	}

	if len(p.calendars) == 0 {
		return dimStyle.Render("  loading…")
	}

	var b strings.Builder

	b.WriteString(dimStyle.Render("  Space shows or hides one. Colours are Google's own."))
	b.WriteString("\n\n")

	for i, c := range p.calendars {
		mark := " "
		if p.shown[c.ID] {
			mark = "x"
		}

		swatch := "  "
		if name := heyui.ColourFor(c.Color); name != "" {
			swatch = heyui.Swatch(name)
		}

		name := c.Name
		if c.Primary {
			name += dimStyle.Render("  (default)")
		}

		line := fmt.Sprintf("  [%s] %s %s", mark, swatch, name)

		if i == p.idx {
			line = selectedStyle.Render(padTo(line, m.contentWidth()))
		}

		b.WriteString(line + "\n")
	}

	return b.String()
}
