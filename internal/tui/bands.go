package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// The habit and todo managers behind the calendar's two bands.
//
// Both are the same shape: a list, space to toggle, a to add, d to remove. They
// are separate views rather than one because the two are stored in different
// places -- habits locally, todos in Google Tasks -- and the failure modes
// differ accordingly.

// bandEditor is the shared state for both managers.
type bandEditor struct {
	adding bool
	input  textinput.Model
	idx    int
}

func newBandEditor(placeholder string) *bandEditor {
	in := textinput.New()
	in.Placeholder = placeholder
	in.CharLimit = 120

	return &bandEditor{input: in}
}

func (m *Model) openHabits() (tea.Model, tea.Cmd) {
	m.band = newBandEditor("read for twenty minutes")
	m.push(viewHabits)

	return m, m.loadBands()
}

func (m *Model) openTodos() (tea.Model, tea.Cmd) {
	if m.todos == nil {
		m.err = fmt.Errorf("todos are not available in this session")

		return m, nil
	}

	m.band = newBandEditor("renew the domain")
	m.push(viewTodos)

	return m, m.loadBands()
}

// handleBandKey drives both managers.
func (m *Model) handleBandKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	b := m.band
	habits := m.view == viewHabits

	if b.adding {
		switch msg.Type {
		case tea.KeyEsc:
			b.adding = false
			b.input.SetValue("")
			b.input.Blur()

			return m, nil

		case tea.KeyEnter:
			name := strings.TrimSpace(b.input.Value())

			b.adding = false
			b.input.SetValue("")
			b.input.Blur()

			if name == "" {
				return m, nil
			}

			m.loading = true

			if habits {
				return m, m.addHabit(name)
			}

			return m, m.addTodo(name)
		}

		var cmd tea.Cmd
		b.input, cmd = b.input.Update(msg)

		return m, cmd
	}

	count := len(m.calHabits)
	if !habits {
		count = len(m.calTodos)
	}

	switch msg.String() {
	case "esc", "q":
		m.band = nil
		m.pop()

		return m, nil

	case "j", "down":
		b.idx = clamp(b.idx+1, 0, maxInt(0, count-1))

		return m, nil

	case "k", "up":
		b.idx = clamp(b.idx-1, 0, maxInt(0, count-1))

		return m, nil

	case "a":
		b.adding = true
		b.input.Focus()

		return m, textinput.Blink

	case " ", "enter":
		if count == 0 {
			return m, nil
		}

		m.loading = true

		if habits {
			return m, m.toggleHabit(m.calHabits[b.idx].Name)
		}

		// Completion goes by ID rather than title: two todos can share a
		// title, and a title lookup would complete whichever came first.
		todo := m.calTodos[b.idx]

		return m, m.completeTodo(todo.ID, todo.Title)

	case "d":
		if habits && count > 0 {
			m.loading = true

			return m, m.archiveHabit(m.calHabits[b.idx].Name)
		}

		return m, nil
	}

	return m, nil
}

// --- commands ---------------------------------------------------------------

func (m *Model) addHabit(name string) tea.Cmd {
	return bg(20*time.Second, func(ctx context.Context) tea.Msg {
		if _, err := m.personal.AddHabit(ctx, name); err != nil {
			return errMsg{err}
		}

		return actionMsg{note: "tracking " + name, reloadBands: true}
	})
}

func (m *Model) toggleHabit(name string) tea.Cmd {
	return bg(20*time.Second, func(ctx context.Context) tea.Msg {
		habits, err := m.personal.Habits(ctx, false)
		if err != nil {
			return errMsg{err}
		}

		done := false

		for _, h := range habits {
			if h.Name == name {
				done = h.DoneToday
			}
		}

		h, err := m.personal.CheckHabit(ctx, name, time.Now(), !done)
		if err != nil {
			return errMsg{err}
		}

		note := fmt.Sprintf("%s: %d day streak", h.Name, h.Streak)
		if done {
			note = "cleared " + h.Name
		}

		return actionMsg{note: note, reloadBands: true}
	})
}

func (m *Model) archiveHabit(name string) tea.Cmd {
	return bg(20*time.Second, func(ctx context.Context) tea.Msg {
		if err := m.personal.ArchiveHabit(ctx, name); err != nil {
			return errMsg{err}
		}

		return actionMsg{note: "archived " + name, reloadBands: true}
	})
}

func (m *Model) addTodo(title string) tea.Cmd {
	return bg(60*time.Second, func(ctx context.Context) tea.Msg {
		if m.addTodoFn == nil {
			return errMsg{fmt.Errorf("todos are not configured")}
		}

		if err := m.addTodoFn(ctx, title); err != nil {
			return errMsg{err}
		}

		return actionMsg{note: "added " + title, reloadBands: true}
	})
}

// completeTodo marks one todo done by its ID; the title only names it in the
// notice.
func (m *Model) completeTodo(id int64, title string) tea.Cmd {
	return bg(60*time.Second, func(ctx context.Context) tea.Msg {
		if m.completeTodoFn == nil {
			return errMsg{fmt.Errorf("todos are not configured")}
		}

		if err := m.completeTodoFn(ctx, id); err != nil {
			return errMsg{err}
		}

		return actionMsg{note: "done: " + title, reloadBands: true}
	})
}

// --- view -------------------------------------------------------------------

func (m *Model) bandView() string {
	b := m.band
	if b == nil {
		return ""
	}

	var out strings.Builder

	if b.adding {
		out.WriteString("  " + dimStyle.Render("new: ") + b.input.View() + "\n\n")
	}

	if m.view == viewHabits {
		if len(m.calHabits) == 0 {
			out.WriteString(dimStyle.Render("  Nothing tracked yet. Press a to add one."))

			return out.String()
		}

		for i, h := range m.calHabits {
			mark := " "
			if len(h.Done) > 0 && sameDay(h.Done[0], time.Now()) {
				mark = "x"
			}

			line := fmt.Sprintf("  [%s] %-32s %d kept", mark, truncateStr(h.Name, 32), len(h.Done))

			if i == b.idx {
				line = selectedStyle.Render(padTo(line, m.contentWidth()))
			}

			out.WriteString(line + "\n")
		}

		return out.String()
	}

	if len(m.calTodos) == 0 {
		out.WriteString(dimStyle.Render("  Nothing to do. Press a to add something."))

		return out.String()
	}

	for i, t := range m.calTodos {
		mark := " "
		if t.Done {
			mark = "x"
		}

		line := fmt.Sprintf("  [%s] %s", mark, truncateStr(t.Title, maxInt(10, m.contentWidth()-8)))

		if i == b.idx {
			line = selectedStyle.Render(padTo(line, m.contentWidth()))
		}

		out.WriteString(line + "\n")
	}

	return out.String()
}

func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}
