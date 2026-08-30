package tui

import (
	"os"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	fcal "github.com/mhrsntrk/frankenstein-cli/internal/calendar"
	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
	"github.com/mhrsntrk/frankenstein-cli/internal/tui/heyui"
)

// TestDumpHeyRows prints the thread list so the layout can be looked at rather
// than guessed about. Run with DUMP_VIEW=1.
func TestDumpHeyRows(t *testing.T) {
	if os.Getenv("DUMP_VIEW") == "" {
		t.Skip("set DUMP_VIEW=1 to print the rendered rows")
	}

	h := newHarness(t)
	now := time.Now()

	h.m.width, h.m.height = 200, 26
	h.m.convs = []mail.Conversation{
		{ID: "a", Subject: "Re: contract review, one more clause", Time: now.Add(-2 * time.Hour),
			NumMessages: 1, NumUnread: 1, Snippet: "Sorry to keep going on about this, but clause 4 still reads as though",
			Senders: []mail.Address{{Name: "Yuki Tanaka"}}},
		{ID: "b", Subject: "Vuelta al hogar. Ofertas hasta -30% 🎉", Time: now.Add(-5 * time.Hour),
			NumMessages: 1, NumUnread: 1, Snippet: "Descubre nuestras ofertas de temporada con descuentos de hasta el 30%",
			Senders: []mail.Address{{Name: "Club Leroy Merlin"}}},
		{ID: "c", Subject: "Photos from the weekend", Time: now.Add(-30 * time.Hour),
			NumMessages: 4, Snippet: "Here are the ones that came out well. The light on Saturday evening was",
			Senders: []mail.Address{{Name: "Sam Okonkwo"}}},
		{ID: "d", Subject: "Certificate issued for 14 Rowan Street", Time: now.Add(-50 * time.Hour),
			NumMessages: 1, Snippet: "Your building control completion certificate is attached to this message",
			Senders: []mail.Address{{Name: "Building Inspector"}}},
	}
	h.m.setPostings(h.m.convs)
	h.m.box = mail.Box{ID: "0", Name: "Inbox", Unread: 13}
	h.m.quickBoxes = []mail.Box{
		{ID: "0", Name: "Inbox", Unread: 13}, {ID: "10", Name: "Starred"},
		{ID: "6", Name: "Archive", Unread: 2}, {ID: "7", Name: "Sent"},
		{ID: "8", Name: "Drafts"}, {ID: "4", Name: "Spam"}, {ID: "3", Name: "Trash"},
	}
	h.m.boxes = append(h.m.boxes,
		mail.Box{ID: "24", Name: "Primary", Kind: mail.BoxCategory},
		mail.Box{ID: "20", Name: "Social", Kind: mail.BoxCategory},
		mail.Box{ID: "21", Name: "Promotions", Kind: mail.BoxCategory, Unread: 41},
		mail.Box{ID: "25", Name: "Newsletters", Kind: mail.BoxCategory},
		mail.Box{ID: "22", Name: "Updates", Kind: mail.BoxCategory},
		mail.Box{ID: "26", Name: "Forums", Kind: mail.BoxCategory},
	)
	h.m.account = "you@example.com"
	h.m.pending = 7
	h.m.view = viewThreads
	h.m.status = "synced 414 conversations"

	t.Logf("\n%s\n", h.m.View())
}

// TestDumpSplitMail prints the two-pane mail screen with a thread open, so
// the pane geometry can be looked at. Run with DUMP_VIEW=1.
func TestDumpSplitMail(t *testing.T) {
	if os.Getenv("DUMP_VIEW") == "" {
		t.Skip("set DUMP_VIEW=1 to print the rendered view")
	}

	h := newHarness(t)
	h.m.width, h.m.height = 160, 34
	h.m.account = "you@example.com"
	h.m.pending = 7
	h.m.status = "synced 414 conversations"

	h.press(t, "enter") // opens c1 and expands its message

	t.Logf("\n%s\n", h.m.View())
}

// TestDumpComposer prints the popup floating over the split screen. Run with
// DUMP_VIEW=1.
func TestDumpComposer(t *testing.T) {
	if os.Getenv("DUMP_VIEW") == "" {
		t.Skip("set DUMP_VIEW=1 to print the rendered view")
	}

	h := newHarness(t)
	h.m.width, h.m.height = 160, 34
	h.m.account = "you@example.com"

	h.press(t, "enter")
	h.press(t, "c")
	h.typeText(t, "ada@example.com")

	t.Logf("\n%s\n", h.m.View())

	// And parked as the minimized bar.
	h.m.composerMin = true
	h.m.pop()

	t.Logf("minimized:\n%s\n", h.m.View())
}

// TestDumpEventDetail prints the event detail view. Run with DUMP_VIEW=1.
func TestDumpEventDetail(t *testing.T) {
	if os.Getenv("DUMP_VIEW") == "" {
		t.Skip("set DUMP_VIEW=1 to print the rendered view")
	}

	h, cal := calHarness(t)
	h.m.width, h.m.height = 110, 30

	start := time.Date(2026, 8, 28, 14, 0, 0, 0, time.Local)
	cal.events = []fcal.Event{{
		ID:        "e9",
		Title:     "Design review: the calendar grid and the ribbon",
		Location:  "Room 4, second floor, the one with the broken blind",
		Notes:     "Bring the printed mocks.\n\nAda wants to go through the week view first, then the year.",
		Attendees: []string{"ada@example.com", "grace@example.com", "alan@example.com"},
		Status:    "confirmed",
		Link:      "https://meet.example.com/abc-defg-hij",
		Start:     start,
		End:       start.Add(90 * time.Minute),
	}}

	h.m.calNames = map[string]string{"work": "Work", "primary": "you@example.com"}
	h.m.view = viewCalendar
	h.drain(t, h.m.loadEvents())
	h.m.events[0].CalendarID = "work"
	h.m.extraIdx = 0
	h.press(t, "enter")

	t.Logf("\n%s\n", h.m.View())
}

// TestDumpCalendarDay prints the day grid with an event selected, so the
// highlight can be looked at. Run with DUMP_VIEW=1.
func TestDumpCalendarDay(t *testing.T) {
	if os.Getenv("DUMP_VIEW") == "" {
		t.Skip("set DUMP_VIEW=1 to print the rendered view")
	}

	h, cal := calHarness(t)
	h.m.width, h.m.height = 110, 32
	h.m.calView = calendarDay

	day := time.Now()
	at := func(hh, mm int) time.Time {
		return time.Date(day.Year(), day.Month(), day.Day(), hh, mm, 0, 0, time.Local)
	}

	cal.events = []fcal.Event{
		{ID: "a", Title: "Standup", Start: at(9, 30), End: at(9, 45)},
		{ID: "b", Title: "Design review", Start: at(14, 0), End: at(15, 30)},
		{ID: "c", Title: "Call with the bank", Start: at(16, 0), End: at(16, 30)},
	}

	h.m.view = viewCalendar
	h.drain(t, h.m.loadEvents())
	h.m.calTodos = []heyui.Todo{{ID: 1, Title: "Renew the domain"}}
	h.m.extraIdx = 1

	t.Logf("selected = Design review\n%s\n", h.m.View())
}

// TestDumpNotes prints the notes list and the editor. Run with DUMP_VIEW=1.
func TestDumpNotes(t *testing.T) {
	if os.Getenv("DUMP_VIEW") == "" {
		t.Skip("set DUMP_VIEW=1 to print the rendered view")
	}

	h := newHarness(t)
	h.m.width, h.m.height = 120, 30
	h.m.account = "you@example.com"

	h.press(t, "tab")
	h.press(t, "tab")
	h.press(t, "n")
	h.typeText(t, "Palmarès launch checklist")
	h.m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	h.m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	h.typeText(t, "- domains")
	h.press(t, "ctrl+d")

	t.Logf("list:\n%s\n", h.m.View())

	h.press(t, "enter")
	t.Logf("reader:\n%s\n", h.m.View())

	h.press(t, "e")
	t.Logf("editor:\n%s\n", h.m.View())
}
