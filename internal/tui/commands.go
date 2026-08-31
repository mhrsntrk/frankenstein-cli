package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	fcal "github.com/mhrsntrk/frankenstein-cli/internal/calendar"
	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
	fsync "github.com/mhrsntrk/frankenstein-cli/internal/sync"
	"github.com/mhrsntrk/frankenstein-cli/internal/tui/render"
)

// --- messages ---------------------------------------------------------------

type boxesMsg []mail.Box

// convsMsg, threadMsg and bodyMsg carry their request's generation, so the
// handler can drop a response that was overtaken by a newer request instead of
// yanking the interface back to what it used to show.
type convsMsg struct {
	convs []mail.Conversation
	gen   int
}

type threadMsg struct {
	thread mail.Thread
	gen    int
}

type bodyMsg struct {
	body mail.Body
	gen  int
}

// eventsMsg carries the week, or why there isn't one. gen says which
// loadEvents produced it, so the handler can drop a response that was
// overtaken by a newer request.
type eventsMsg struct {
	events []fcal.Event
	err    error
	gen    int
}
type syncedMsg fsync.Result
type chromeMsg struct {
	account string
}

// actionMsg reports a write that finished, so the list can refresh and the
// user gets told what happened.
type actionMsg struct {
	note           string
	reload         bool
	reloadCalendar bool
	reloadBands    bool
	reloadNotes    bool

	// draftID is the server's ID for a draft that was just saved, so the open
	// composer can update it on the next save instead of creating another.
	draftID string
}

type errMsg struct{ err error }

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.loadBoxes(), m.loadChrome(), m.loadEvents(), m.syncOnce())
}

// --- reads (cache only) -----------------------------------------------------

func (m *Model) loadBoxes() tea.Cmd {
	return bg(10*time.Second, func(ctx context.Context) tea.Msg {
		boxes, err := m.store.Boxes(ctx)
		if err != nil {
			return errMsg{err}
		}

		return boxesMsg(boxes)
	})
}

func (m *Model) loadChrome() tea.Cmd {
	return bg(10*time.Second, func(ctx context.Context) tea.Msg {
		out := chromeMsg{}

		if v, err := m.store.Meta(ctx, "account_email"); err == nil {
			out.account = v
		}

		return out
	})
}

func (m *Model) loadConvs(boxID, search string) tea.Cmd {
	m.convsGen++
	gen := m.convsGen

	return bg(10*time.Second, func(ctx context.Context) tea.Msg {
		convs, err := m.store.Conversations(ctx, mail.ListOptions{
			BoxID:  boxID,
			Limit:  500,
			Search: search,
			Desc:   true,
		})
		if err != nil {
			return errMsg{err}
		}

		return convsMsg{convs: convs, gen: gen}
	})
}

// --- reads that may reach the network ---------------------------------------

func (m *Model) loadThread(id string) tea.Cmd {
	m.threadGen++
	gen := m.threadGen

	return bg(30*time.Second, func(ctx context.Context) tea.Msg {
		t, err := m.syncer.Thread(ctx, id)
		if err != nil {
			return errMsg{err}
		}

		return threadMsg{thread: t, gen: gen}
	})
}

func (m *Model) loadBody(id string) tea.Cmd {
	m.bodyGen++
	gen := m.bodyGen

	return bg(30*time.Second, func(ctx context.Context) tea.Msg {
		b, err := m.syncer.Body(ctx, id)
		if err != nil {
			return errMsg{err}
		}

		return bodyMsg{body: b, gen: gen}
	})
}

func (m *Model) loadEvents() tea.Cmd {
	if m.cal == nil {
		return nil
	}

	// Fetch around whatever the grid is showing, with a day either side so an
	// event that starts before the window still draws its tail inside it.
	anchor := m.calAnchor()
	start := time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 0, 0, 0, 0, time.Local)

	var end time.Time

	switch {
	case m.calView == calendarYear && m.view == viewCalendar:
		// The year grid shows the anchor's calendar year, so the window is
		// exactly that: January the 1st to December the 31st.
		start = time.Date(anchor.Year(), time.January, 1, 0, 0, 0, 0, time.Local)
		end = start.AddDate(1, 0, 0)
	case m.calView == calendarWeek || m.view != viewCalendar:
		// Back up to the Monday the grid starts on.
		back := (int(start.Weekday()) + 6) % 7
		start = start.AddDate(0, 0, -back-1)
		end = start.AddDate(0, 0, 9)
	default:
		start = start.AddDate(0, 0, -1)
		end = start.AddDate(0, 0, 8)
	}

	// The IDs are copied rather than aliased: the picker reassigns
	// m.calendarIDs on the update goroutine while this one is reading it.
	ids := append([]string(nil), m.calendarIDs...)

	m.eventsGen++
	gen := m.eventsGen

	return bg(20*time.Second, func(ctx context.Context) tea.Msg {
		events, err := m.cal.EventsFrom(ctx, ids, start, end)
		if err != nil {
			// A calendar failure must not take the mail client down with it,
			// but it must not read as an empty week either: that sent someone
			// looking for missing events when the API was simply switched off.
			return eventsMsg{err: err, gen: gen}
		}

		return eventsMsg{events: events, gen: gen}
	})
}

// bandsMsg carries the habits and todos drawn around the calendar grid.
type bandsMsg struct {
	habits []render.Habit
	todos  []render.Todo
}

// loadBands fills the habits band and the todo ribbon.
//
// Habits are local, so they are cheap. Todos are a Google call, which is why
// this is its own command rather than part of loading events.
func (m *Model) loadBands() tea.Cmd {
	return bg(30*time.Second, func(ctx context.Context) tea.Msg {
		out := bandsMsg{}

		if m.personal != nil {
			if habits, err := m.personal.Habits(ctx, false); err == nil {
				for _, h := range habits {
					out.habits = append(out.habits, render.Habit{
						ID:    h.ID,
						Name:  h.Name,
						Done:  h.DoneDays,
						Color: habitColour(h.ID),
					})
				}
			}
		}

		if m.todos != nil {
			if items, err := m.todos(ctx); err == nil {
				// The store's own ID rides along, because completing goes by
				// it: an index invented here would complete the wrong todo
				// once the list reordered.
				for _, t := range items {
					out.todos = append(out.todos, render.Todo{
						ID: t.ID, Title: t.Title, Done: t.Done,
					})
				}
			}
		}

		return out
	})
}

// habitColour spreads habits across hey-cli's palette deterministically, so a
// habit keeps its colour between runs.
func habitColour(id int64) string {
	names := render.HabitColours()
	if len(names) == 0 {
		return ""
	}

	return names[int(id)%len(names)]
}

func (m *Model) syncOnce() tea.Cmd {
	return bg(5*time.Minute, func(ctx context.Context) tea.Msg {
		res, err := m.syncer.Once(ctx)
		if err != nil {
			return errMsg{err}
		}

		return syncedMsg(res)
	})
}

// --- writes -----------------------------------------------------------------

// labelTargets adds a box to conversations, optionally removing them from the
// box currently being viewed so the row disappears the way a move should.
func (m *Model) labelTargets(ids []string, target mail.Box, removeFromCurrent bool, note string) tea.Cmd {
	current := m.box.ID

	return bg(60*time.Second, func(ctx context.Context) tea.Msg {
		if err := m.provider.Label(ctx, ids, target.ID); err != nil {
			return errMsg{err}
		}

		// Moving out of a system box means unlabelling it; leaving that off is
		// what turns a "move" into a "copy".
		removeID := ""
		if removeFromCurrent && current != "" && current != target.ID {
			removeID = current

			if err := m.provider.Unlabel(ctx, ids, current); err != nil {
				return errMsg{err}
			}
		}

		// Reflect the move in the cache at once, so the reload that follows
		// shows the row gone instead of waiting for the next sync tick. A
		// failure here only delays the disappearance until that sync, so it
		// is not worth failing the action over.
		if m.syncer != nil {
			_ = m.syncer.ApplyLocalMove(ctx, ids, target.ID, removeID)
		}

		return actionMsg{
			note:   fmt.Sprintf("%s %s", note, plural(len(ids), "thread", "threads")),
			reload: true,
		}
	})
}

func (m *Model) markTargets(ids []string, read bool) tea.Cmd {
	boxID := m.box.ID

	return bg(60*time.Second, func(ctx context.Context) tea.Msg {
		var err error

		if read {
			err = m.provider.MarkRead(ctx, ids)
		} else {
			err = m.provider.MarkUnread(ctx, ids, boxID)
		}

		if err != nil {
			return errMsg{err}
		}

		verb := "marked read"
		if !read {
			verb = "marked unread"
		}

		return actionMsg{
			note:   fmt.Sprintf("%s %s", plural(len(ids), "thread", "threads"), verb),
			reload: true,
		}
	})
}

// sendCompose saves the draft and, if asked, sends it. A draft saved before
// keeps its ID, so saving again updates it rather than creating a sibling.
func (m *Model) sendCompose(send bool) tea.Cmd {
	c := m.compose
	if c == nil {
		return nil
	}

	draft := mail.Draft{
		ID:        c.draftID,
		Subject:   c.subject.Value(),
		Body:      c.body.Value(),
		MIMEType:  "text/plain",
		InReplyTo: c.inReplyTo,
	}

	// A send with neither a subject nor a body is a misfire, not a message.
	// Refusing it with a reason beats delivering an empty mail.
	if send && strings.TrimSpace(draft.Subject) == "" && strings.TrimSpace(draft.Body) == "" {
		return func() tea.Msg {
			return errMsg{fmt.Errorf("nothing to send: subject and body are both empty")}
		}
	}

	to, errTo := parseAddresses(c.to.Value())
	cc, errCC := parseOptionalAddresses(c.cc.Value())
	bcc, errBCC := parseOptionalAddresses(c.bcc.Value())

	draft.To = to
	draft.CC = cc
	draft.BCC = bcc

	return bg(2*time.Minute, func(ctx context.Context) tea.Msg {
		if errTo != nil {
			return errMsg{errTo}
		}

		if errCC != nil {
			return errMsg{fmt.Errorf("cc: %w", errCC)}
		}

		if errBCC != nil {
			return errMsg{fmt.Errorf("bcc: %w", errBCC)}
		}

		saved, err := m.provider.Draft(ctx, draft)
		if err != nil {
			return errMsg{err}
		}

		if !send {
			return actionMsg{note: "draft saved", draftID: saved.ID}
		}

		if _, err := m.provider.Send(ctx, saved.ID); err != nil {
			return errMsg{err}
		}

		return actionMsg{note: "sent", reload: true}
	})
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}

	return fmt.Sprintf("%d %s", n, many)
}
