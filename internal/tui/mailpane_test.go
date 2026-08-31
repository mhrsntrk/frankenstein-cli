package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
	"github.com/mhrsntrk/frankenstein-cli/internal/tui/heyui"
)

// reverseSeq matches an SGR sequence carrying the reverse attribute, wherever
// it sits among the other parameters.
var reverseSeq = regexp.MustCompile(`\x1b\[(?:[0-9]+;)*7(?:;[0-9]+)*m`)

// paneConvs fabricates a list the way real mail looks: Turkish names, an
// emoji subject, a multi-message thread with attachments.
func paneConvs() []mail.Conversation {
	now := time.Now()

	return []mail.Conversation{
		{
			ID:      "c0",
			Subject: "Quarterly numbers",
			Senders: []mail.Address{{Name: "Ada Lovelace", Address: "ada@example.com"}},

			NumMessages: 1,
			Time:        now,
		},
		{
			ID:      "c1",
			Subject: "🎉 Launch day 🚀",
			Senders: []mail.Address{
				{Name: "Mahir Şentürk", Address: "m@example.com"},
				{Name: "Baler İlhan", Address: "b@example.com"},
			},

			NumMessages:    7,
			NumUnread:      2,
			NumAttachments: 1,
			Time:           now.Add(-2 * time.Hour),
		},
		{
			ID:      "c2",
			Subject: "Re: Sardeks tanışma",
			Senders: []mail.Address{{Name: "Çiğdem Öztürk", Address: "c@example.com"}},

			NumMessages: 2,
			Time:        now.Add(-72 * time.Hour),
		},
		{
			ID:      "c3",
			Subject: "no sender at all",
			Time:    now.Add(-30 * 24 * time.Hour),
		},
	}
}

func paneThread() mail.Thread {
	now := time.Now()

	return mail.Thread{
		Conversation: mail.Conversation{ID: "c1", Subject: "🎉 Launch day 🚀", NumMessages: 3},
		Messages: []mail.Message{
			{
				ID:   "m0",
				From: mail.Address{Name: "Mahir Şentürk", Address: "m@example.com"},
				To:   []mail.Address{{Name: "Baler İlhan", Address: "b@example.com"}},
				Time: now.Add(-48 * time.Hour),
			},
			{
				ID:     "m1",
				From:   mail.Address{Name: "Baler İlhan", Address: "b@example.com"},
				To:     []mail.Address{{Name: "Mahir Şentürk", Address: "m@example.com"}},
				Time:   now.Add(-24 * time.Hour),
				Unread: true,
			},
			{
				ID:   "m2",
				From: mail.Address{Address: "noreply@example.com"},
				To:   []mail.Address{{Name: "Mahir Şentürk", Address: "m@example.com"}},
				Time: now,
			},
		},
	}
}

func paneBody() []string {
	return []string{
		"Merhaba Mahir,",
		"",
		"🎉 the launch went out this morning, çok teşekkürler for the help.",
		"Baler",
	}
}

// requireBlock asserts the pane contract: exactly height lines, every one
// exactly width printable columns.
func requireBlock(t *testing.T, block string, width, height int) []string {
	t.Helper()

	lines := strings.Split(block, "\n")
	if len(lines) != height {
		t.Fatalf("got %d lines, want %d", len(lines), height)
	}

	for i, l := range lines {
		if w := heyui.DisplayWidth(l); w != width {
			t.Errorf("line %d is %d columns, want %d: %q", i, w, width, l)
		}
	}

	return lines
}

func TestListPaneDimensions(t *testing.T) {
	convs := paneConvs()

	for _, size := range []struct{ w, h int }{{40, 20}, {80, 30}} {
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			block, _ := listPane(convs, 1, 0, func(id string) bool { return id == "c1" }, size.w, size.h, true)
			requireBlock(t, block, size.w, size.h)
		})
	}
}

func TestListPaneRegions(t *testing.T) {
	_, regions := listPane(paneConvs(), 0, 0, nil, 40, 20, true)

	// Conversation 2 occupies rows 4 and 5; a click on either maps to it.
	for _, y := range []int{4, 5} {
		id, ok := hit(regions, 5, y)
		if !ok || id != "conv:2" {
			t.Fatalf("hit(5, %d) = %q, %v; want conv:2", y, id, ok)
		}
	}

	// With the list scrolled, indices stay absolute.
	_, regions = listPane(paneConvs(), 2, 1, nil, 40, 20, true)

	if id, ok := hit(regions, 0, 0); !ok || id != "conv:1" {
		t.Fatalf("hit(0, 0) with top=1 = %q, %v; want conv:1", id, ok)
	}
}

func TestListPaneCursorStyled(t *testing.T) {
	block, _ := listPane(paneConvs(), 2, 0, nil, 40, 20, true)
	lines := strings.Split(block, "\n")

	for _, y := range []int{4, 5} {
		if !reverseSeq.MatchString(lines[y]) {
			t.Errorf("cursor row %d carries no reverse escape: %q", y, lines[y])
		}
	}

	if reverseSeq.MatchString(lines[0]) {
		t.Errorf("non-cursor row 0 carries a reverse escape: %q", lines[0])
	}
}

func TestListPaneEmpty(t *testing.T) {
	block, regions := listPane(nil, 0, 0, nil, 40, 10, true)
	requireBlock(t, block, 40, 10)

	if len(regions) != 0 {
		t.Errorf("empty list produced %d regions", len(regions))
	}

	if !strings.Contains(block, "No conversations.") {
		t.Errorf("empty list is missing its placeholder")
	}
}

func TestListPageSize(t *testing.T) {
	for h, want := range map[int]int{20: 10, 5: 2, 1: 0} {
		if got := listPageSize(h); got != want {
			t.Errorf("listPageSize(%d) = %d, want %d", h, got, want)
		}
	}
}

func TestThreadPaneDimensions(t *testing.T) {
	th := paneThread()

	for _, size := range []struct{ w, h int }{{40, 20}, {80, 30}} {
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			block, _ := threadPane(th, 1, paneBody(), 0, size.w, size.h)
			requireBlock(t, block, size.w, size.h)
		})
	}
}

func TestThreadPaneRegions(t *testing.T) {
	_, regions := threadPane(paneThread(), 1, paneBody(), 0, 80, 30)

	// The collapsed first message is the first row.
	if id, ok := hit(regions, 2, 0); !ok || id != "msg:0" {
		t.Fatalf("hit(2, 0) = %q, %v; want msg:0", id, ok)
	}

	// The expanded header answers as the message, except on the star glyph,
	// which is drawn later and wins.
	if id, ok := hit(regions, 2, 1); !ok || id != "msg:1" {
		t.Fatalf("hit(2, 1) = %q, %v; want msg:1", id, ok)
	}

	for _, want := range []string{"star", "reply", "replyall", "forward"} {
		var found *Region

		for i := range regions {
			if regions[i].ID == want {
				found = &regions[i]
				break
			}
		}

		if found == nil {
			t.Fatalf("no %q region", want)
		}

		if id, ok := hit(regions, found.X, found.Y); !ok || id != want {
			t.Errorf("hit on %q region = %q, %v", want, id, ok)
		}
	}
}

func TestThreadPaneLoadingBody(t *testing.T) {
	block, _ := threadPane(paneThread(), 1, nil, 0, 40, 20)
	requireBlock(t, block, 40, 20)

	if !strings.Contains(block, "loading…") {
		t.Errorf("empty body shows no loading placeholder")
	}
}

func TestThreadPaneEmptyThread(t *testing.T) {
	block, regions := threadPane(mail.Thread{}, 0, nil, 0, 40, 20)
	requireBlock(t, block, 40, 20)

	if len(regions) != 0 {
		t.Errorf("empty thread produced %d regions", len(regions))
	}

	if !strings.Contains(block, "loading…") {
		t.Errorf("empty thread shows no loading placeholder")
	}
}

func TestThreadBodyRows(t *testing.T) {
	th := paneThread()

	// Three messages: two collapsed rows plus four rows of expanded chrome.
	if got := threadBodyRows(th, 1, 30); got != 24 {
		t.Errorf("threadBodyRows(30) = %d, want 24", got)
	}

	if got := threadBodyRows(th, 1, 5); got != 0 {
		t.Errorf("threadBodyRows(5) = %d, want 0", got)
	}
}
