package tui

import (
	"os"
	"testing"
	"time"

	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
)

// TestDumpHeyRows prints the thread list so the layout can be looked at rather
// than guessed about. Run with DUMP_VIEW=1.
func TestDumpHeyRows(t *testing.T) {
	if os.Getenv("DUMP_VIEW") == "" {
		t.Skip("set DUMP_VIEW=1 to print the rendered rows")
	}

	h := newHarness(t)
	now := time.Now()

	h.m.width, h.m.height = 150, 28
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
	h.m.list.SetPostings(toPostings(h.m.convs))
	h.m.box = mail.Box{ID: "b1", Name: "Imbox"}
	h.m.quickBoxes = []mail.Box{
		{ID: "b1", Name: "Imbox", Unread: 3}, {ID: "b2", Name: "Feed", Unread: 41},
		{ID: "b3", Name: "Paper Trail"}, {ID: "b4", Name: "Screened Out"},
		{ID: "0", Name: "Inbox", Unread: 13}, {ID: "10", Name: "Starred"},
	}
	h.m.account = "you@example.com"
	h.m.pending = 7
	h.m.view = viewThreads
	h.m.status = "synced 414 conversations"

	t.Logf("\n%s\n", h.m.View())
}
