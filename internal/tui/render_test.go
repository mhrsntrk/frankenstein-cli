package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mhrsntrk/frankenstein-cli/internal/config"
	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
)

// fixture builds a model with enough state to render a realistic screen.
// Bubble Tea needs a terminal to run, but View() is a pure function of the
// model, so the layout can be checked without one.
func fixture() *Model {
	now := time.Date(2026, 8, 28, 14, 30, 0, 0, time.Local)

	m := New(nil, nil, nil, "", config.ScreenerConfig{
		ImboxID: "b1", FeedID: "b2", PaperTrailID: "b3", Enabled: true,
	})

	m.width, m.height = 100, 24
	m.account = "m@example.com"
	m.pending = 122
	m.status = "synced 414 conversations"
	m.view = viewThreads
	m.box = mail.Box{ID: "0", Name: "Inbox", Kind: mail.BoxSystem}

	m.quickBoxes = []mail.Box{
		{ID: "b1", Name: "Imbox", Unread: 3},
		{ID: "b2", Name: "Feed", Unread: 41},
		{ID: "b3", Name: "Paper Trail"},
		{ID: "0", Name: "Inbox", Kind: mail.BoxSystem, Unread: 13},
		{ID: "6", Name: "Archive", Kind: mail.BoxSystem},
	}

	mk := func(id, from, subject string, ago time.Duration, unread, n int) mail.Conversation {
		return mail.Conversation{
			ID: id, Subject: subject, Time: now.Add(-ago),
			Senders: []mail.Address{{Name: from}}, NumMessages: n, NumUnread: unread,
		}
	}

	m.convs = []mail.Conversation{
		mk("c1", "Club Leroy Merlin", "Vuelta al hogar. Ofertas hasta -30%", time.Hour, 1, 1),
		mk("c2", "AlphaSignal", "Z.ai 320B MoE beats Claude coding", 3*time.Hour, 1, 1),
		mk("c3", "Apple Developer", "Tax and Price Updates for Apps, In-App Purchases", 20*time.Hour, 0, 2),
		mk("c4", "Diego Escalona", "Hello", 21*time.Hour, 0, 1),
		mk("c5", "TLDR Crypto", "Fast Ethereum Roadmap, Automated Token Portfolios", 26*time.Hour, 0, 1),
	}

	return m
}

func TestViewRendersWithinWidth(t *testing.T) {
	m := fixture()

	for _, line := range strings.Split(m.View(), "\n") {
		if w := visibleWidth(line); w > m.width {
			t.Errorf("line overflows %d columns (got %d): %q", m.width, w, line)
		}
	}
}

func TestViewShowsTheChrome(t *testing.T) {
	m := fixture()
	out := m.View()

	for _, want := range []string{
		"Inbox",                 // title bar
		"m@example.com",         // account
		"Imbox",                 // box switcher
		"Screen 122 first-time", // screener banner
		"New for You",           // unread section
		"Previously Seen",       // read section
		"Club Leroy Merlin",     // a row
		"quit",                  // footer
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q from the rendered view", want)
		}
	}
}

func TestViewSectionsOnlyWhenBothPresent(t *testing.T) {
	m := fixture()

	// All read: there is no "New for You" boundary to announce.
	for i := range m.convs {
		m.convs[i].NumUnread = 0
	}

	if out := m.View(); strings.Contains(out, "New for You") {
		t.Error("announced an unread section with nothing unread")
	}
}

func TestBannerHidesWhenNobodyWaiting(t *testing.T) {
	m := fixture()
	m.pending = 0

	if strings.Contains(m.View(), "first-time") {
		t.Error("screener banner shown with nobody pending")
	}
}

// visibleWidth counts printable columns, ignoring ANSI escapes.
func visibleWidth(s string) int {
	n, esc := 0, false

	for _, r := range s {
		switch {
		case r == '\x1b':
			esc = true
		case esc && r == 'm':
			esc = false
		case !esc:
			n++
		}
	}

	return n
}

// TestDumpView is not an assertion; it prints the screen so the layout can be
// looked at rather than guessed about. Run with -run TestDumpView -v.
func TestDumpView(t *testing.T) {
	if os.Getenv("DUMP_VIEW") == "" {
		t.Skip("set DUMP_VIEW=1 to print the rendered screen")
	}

	t.Log("\n" + fixture().View())
}
