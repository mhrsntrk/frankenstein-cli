package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	fcal "github.com/mhrsntrk/frankenstein-cli/internal/calendar"
	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
	"github.com/mhrsntrk/frankenstein-cli/internal/personal"
	"github.com/mhrsntrk/frankenstein-cli/internal/screener"
	fsync "github.com/mhrsntrk/frankenstein-cli/internal/sync"
)

// --- messages ---------------------------------------------------------------

type boxesMsg []mail.Box
type convsMsg []mail.Conversation
type threadMsg mail.Thread
type bodyMsg mail.Body

// eventsMsg carries the week, or why there isn't one.
type eventsMsg struct {
	events []fcal.Event
	err    error
}
type journalMsg []personal.JournalEntry
type sendersMsg []screener.Sender
type syncedMsg fsync.Result
type chromeMsg struct {
	pending int
	account string
}

// actionMsg reports a write that finished, so the list can refresh and the
// user gets told what happened.
type actionMsg struct {
	note     string
	reload   bool
	rescreen bool
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

		if n, err := m.store.PendingSenders(ctx); err == nil {
			out.pending = n
		}

		if v, err := m.store.Meta(ctx, "account_email"); err == nil {
			out.account = v
		}

		return out
	})
}

func (m *Model) loadConvs(boxID, search string) tea.Cmd {
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

		return convsMsg(convs)
	})
}

func (m *Model) loadSenders() tea.Cmd {
	return bg(20*time.Second, func(ctx context.Context) tea.Msg {
		if m.screener == nil {
			return sendersMsg(nil)
		}

		senders, err := m.screener.Pending(ctx, 500)
		if err != nil {
			return errMsg{err}
		}

		return sendersMsg(senders)
	})
}

func (m *Model) loadJournal() tea.Cmd {
	return bg(10*time.Second, func(ctx context.Context) tea.Msg {
		if m.personal == nil {
			return journalMsg(nil)
		}

		entries, err := m.personal.JournalEntries(ctx, 100)
		if err != nil {
			return errMsg{err}
		}

		return journalMsg(entries)
	})
}

// --- reads that may reach the network ---------------------------------------

func (m *Model) loadThread(id string) tea.Cmd {
	return bg(30*time.Second, func(ctx context.Context) tea.Msg {
		t, err := m.syncer.Thread(ctx, id)
		if err != nil {
			return errMsg{err}
		}

		return threadMsg(t)
	})
}

func (m *Model) loadBody(id string) tea.Cmd {
	return bg(30*time.Second, func(ctx context.Context) tea.Msg {
		b, err := m.syncer.Body(ctx, id)
		if err != nil {
			return errMsg{err}
		}

		return bodyMsg(b)
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

	days := 8

	if m.calView == calendarWeek || m.view != viewCalendar {
		// Back up to the Monday the grid starts on.
		back := (int(start.Weekday()) + 6) % 7
		start = start.AddDate(0, 0, -back)
		days = 9
	}

	start = start.AddDate(0, 0, -1)

	return bg(20*time.Second, func(ctx context.Context) tea.Msg {
		events, err := m.cal.Events(ctx, m.calendarID, start, start.AddDate(0, 0, days))
		if err != nil {
			// A calendar failure must not take the mail client down with it,
			// but it must not read as an empty week either: that sent someone
			// looking for missing events when the API was simply switched off.
			return eventsMsg{err: err}
		}

		return eventsMsg{events: events}
	})
}

func (m *Model) syncOnce() tea.Cmd {
	return bg(5*time.Minute, func(ctx context.Context) tea.Msg {
		res, err := m.syncer.Once(ctx)
		if err != nil {
			return errMsg{err}
		}

		if m.screener != nil {
			_, _ = m.screener.Observe(ctx)
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
		if removeFromCurrent && current != "" && current != target.ID {
			if err := m.provider.Unlabel(ctx, ids, current); err != nil {
				return errMsg{err}
			}
		}

		return actionMsg{
			note:   fmt.Sprintf("%s %s", note, plural(len(ids), "thread", "threads")),
			reload: true,
		}
	})
}

// screenTargets applies a screener decision to the senders of the given
// conversations, which is the whole point: deciding about a person, not a
// thread.
func (m *Model) screenTargets(ids []string, d screener.Decision) tea.Cmd {
	addrs := make([]string, 0, len(ids))
	seen := map[string]bool{}

	for _, c := range m.convs {
		if !contains(ids, c.ID) || len(c.Senders) == 0 {
			continue
		}

		a := strings.ToLower(c.Senders[0].Address)
		if a != "" && !seen[a] {
			seen[a] = true
			addrs = append(addrs, a)
		}
	}

	if m.view == viewThread || m.view == viewMessage {
		for _, msg := range m.thread.Messages {
			a := strings.ToLower(msg.From.Address)
			if a != "" && !seen[a] {
				seen[a] = true
				addrs = append(addrs, a)

				break
			}
		}
	}

	return bg(60*time.Second, func(ctx context.Context) tea.Msg {
		if m.screener == nil {
			return errMsg{fmt.Errorf("screener is not set up; run `frankenstein screener setup`")}
		}

		n := 0

		for _, a := range addrs {
			moved, err := m.screener.Decide(ctx, a, d)
			if err != nil {
				return errMsg{err}
			}

			n += moved
		}

		return actionMsg{
			note: fmt.Sprintf("%s → %s (%s)",
				plural(len(addrs), "sender", "senders"), d,
				plural(n, "thread", "threads")),
			reload:   true,
			rescreen: true,
		}
	})
}

// decideSender screens one sender from the screener view.
func (m *Model) decideSender(address string, d screener.Decision) tea.Cmd {
	return bg(60*time.Second, func(ctx context.Context) tea.Msg {
		n, err := m.screener.Decide(ctx, address, d)
		if err != nil {
			return errMsg{err}
		}

		return actionMsg{
			note:     fmt.Sprintf("%s → %s (%s)", address, d, plural(n, "thread", "threads")),
			reload:   true,
			rescreen: true,
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

// sendCompose saves the draft and, if asked, sends it.
func (m *Model) sendCompose(send bool) tea.Cmd {
	c := m.compose
	if c == nil {
		return nil
	}

	draft := mail.Draft{
		Subject:   c.subject.Value(),
		Body:      c.body.Value(),
		MIMEType:  "text/plain",
		InReplyTo: c.inReplyTo,
	}

	to, errTo := parseAddresses(c.to.Value())
	cc, _ := parseAddresses(c.cc.Value())

	draft.To = to
	draft.CC = cc

	return bg(2*time.Minute, func(ctx context.Context) tea.Msg {
		if errTo != nil {
			return errMsg{errTo}
		}

		saved, err := m.provider.Draft(ctx, draft)
		if err != nil {
			return errMsg{err}
		}

		if !send {
			return actionMsg{note: "draft saved"}
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

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}

	return false
}
