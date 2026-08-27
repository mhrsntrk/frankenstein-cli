package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
	"github.com/mhrsntrk/frankenstein-cli/internal/store"
)

func open(t *testing.T) *store.Store {
	t.Helper()

	s, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t.Cleanup(func() { s.Close() })

	return s
}

func TestBoxesRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	in := []mail.Box{
		{ID: "0", Name: "Inbox", Path: []string{"Inbox"}, Kind: mail.BoxSystem, Total: 10, Unread: 3},
		{ID: "x", Name: "Clients", Path: []string{"Work", "Clients"}, Kind: mail.BoxFolder, Total: 2},
	}

	if err := s.PutBoxes(ctx, in); err != nil {
		t.Fatalf("put: %v", err)
	}

	out, err := s.Boxes(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if len(out) != 2 {
		t.Fatalf("got %d boxes, want 2", len(out))
	}

	if out[0].Name != "Inbox" || out[0].Unread != 3 || out[0].Kind != mail.BoxSystem {
		t.Errorf("inbox round-trip wrong: %+v", out[0])
	}

	// The nested path must survive the join/split.
	if len(out[1].Path) != 2 || out[1].Path[1] != "Clients" {
		t.Errorf("path round-trip wrong: %+v", out[1].Path)
	}

	// PutBoxes replaces wholesale, so a shrinking list must not leave stragglers.
	if err := s.PutBoxes(ctx, in[:1]); err != nil {
		t.Fatalf("put again: %v", err)
	}

	out, _ = s.Boxes(ctx)
	if len(out) != 1 {
		t.Errorf("got %d boxes after replace, want 1", len(out))
	}
}

func TestConversationsAndBoxMembership(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	now := time.Now().Truncate(time.Second)

	convs := []mail.Conversation{
		{
			ID: "c1", Subject: "Hello", Time: now, NumMessages: 2, NumUnread: 1,
			Senders: []mail.Address{{Name: "Ada", Address: "ada@example.com"}},
			BoxIDs:  []string{"0", "5"},
		},
		{
			ID: "c2", Subject: "Receipt", Time: now.Add(-time.Hour), NumMessages: 1,
			Senders: []mail.Address{{Address: "billing@example.com"}},
			BoxIDs:  []string{"0", "24"}, CategoryID: "24",
		},
	}

	if err := s.PutConversations(ctx, convs); err != nil {
		t.Fatalf("put: %v", err)
	}

	inbox, err := s.Conversations(ctx, mail.ListOptions{BoxID: "0"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(inbox) != 2 {
		t.Fatalf("got %d in inbox, want 2", len(inbox))
	}

	// Newest first.
	if inbox[0].ID != "c1" {
		t.Errorf("wrong order: %s first", inbox[0].ID)
	}

	if inbox[0].Senders[0].Name != "Ada" {
		t.Errorf("senders lost: %+v", inbox[0].Senders)
	}

	if !contains(inbox[0].BoxIDs, "5") {
		t.Errorf("box membership lost: %v", inbox[0].BoxIDs)
	}

	cat, err := s.Conversations(ctx, mail.ListOptions{BoxID: "24"})
	if err != nil {
		t.Fatalf("list category: %v", err)
	}

	if len(cat) != 1 || cat[0].ID != "c2" {
		t.Errorf("category filter wrong: %+v", cat)
	}

	unread, _ := s.Conversations(ctx, mail.ListOptions{BoxID: "0", UnreadOnly: true})
	if len(unread) != 1 || unread[0].ID != "c1" {
		t.Errorf("unread filter wrong: %+v", unread)
	}

	found, _ := s.Conversations(ctx, mail.ListOptions{Search: "recei"})
	if len(found) != 1 || found[0].ID != "c2" {
		t.Errorf("search wrong: %+v", found)
	}

	// Re-putting with different boxes must replace membership, not add to it.
	convs[0].BoxIDs = []string{"6"}

	if err := s.PutConversations(ctx, convs[:1]); err != nil {
		t.Fatalf("re-put: %v", err)
	}

	inbox, _ = s.Conversations(ctx, mail.ListOptions{BoxID: "0"})
	if len(inbox) != 1 || inbox[0].ID != "c2" {
		t.Errorf("stale box membership left behind: %+v", inbox)
	}
}

func TestDeleteConversationCascadesBoxes(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if err := s.PutConversations(ctx, []mail.Conversation{
		{ID: "c1", Subject: "x", Time: time.Now(), BoxIDs: []string{"0"}},
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteConversations(ctx, []string{"c1"}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got, _ := s.Conversations(ctx, mail.ListOptions{BoxID: "0"})
	if len(got) != 0 {
		t.Errorf("conversation survived delete: %+v", got)
	}
}

func TestMessagesRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	now := time.Now().Truncate(time.Second)
	snooze := now.Add(24 * time.Hour)

	in := []mail.Message{
		{
			ID: "m2", ConversationID: "c1", Subject: "Re: Hello", Time: now,
			From:   mail.Address{Name: "Ada", Address: "ada@example.com", IsProton: true},
			To:     []mail.Address{{Address: "me@example.com"}},
			Unread: true, CategoryID: "20", NewsletterID: "sub-1",
			SpamScore: 2, BoxIDs: []string{"0"}, SnoozedUntil: &snooze,
		},
		{
			ID: "m1", ConversationID: "c1", Subject: "Hello", Time: now.Add(-time.Hour),
			From: mail.Address{Address: "ada@example.com"},
		},
	}

	if err := s.PutMessages(ctx, in); err != nil {
		t.Fatalf("put: %v", err)
	}

	out, err := s.Messages(ctx, "c1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if len(out) != 2 {
		t.Fatalf("got %d messages, want 2", len(out))
	}

	// Oldest first, so a thread reads top to bottom.
	if out[0].ID != "m1" {
		t.Errorf("wrong order: %s first", out[0].ID)
	}

	m := out[1]

	if !m.Unread || m.From.Name != "Ada" || !m.From.IsProton {
		t.Errorf("message round-trip wrong: %+v", m)
	}

	if m.NewsletterID != "sub-1" || m.CategoryID != "20" || m.SpamScore != 2 {
		t.Errorf("dropped fields: newsletter=%q category=%q spam=%d",
			m.NewsletterID, m.CategoryID, m.SpamScore)
	}

	if m.SnoozedUntil == nil || !m.SnoozedUntil.Equal(snooze) {
		t.Errorf("snooze lost: %v", m.SnoozedUntil)
	}
}

func TestBodyCacheAndEviction(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	// Bodies are foreign-keyed to messages, so the messages must exist first.
	var msgs []mail.Message

	for _, id := range []string{"m1", "m2", "m3"} {
		msgs = append(msgs, mail.Message{ID: id, ConversationID: "c1"})
	}

	if err := s.PutMessages(ctx, msgs); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Body(ctx, "m1"); err != mail.ErrNotFound {
		t.Fatalf("empty cache should miss, got %v", err)
	}

	for _, id := range []string{"m1", "m2", "m3"} {
		if err := s.PutBody(ctx, mail.Body{
			MessageID: id, MIMEType: "text/plain", Content: "body of " + id,
			Attachments: []mail.Attachment{{ID: "a1", Name: "x.pdf", Size: 10}},
		}); err != nil {
			t.Fatalf("put body: %v", err)
		}

		// Distinct access times so eviction has a defined order.
		time.Sleep(1100 * time.Millisecond)
	}

	got, err := s.Body(ctx, "m3")
	if err != nil {
		t.Fatalf("get body: %v", err)
	}

	if got.Content != "body of m3" || len(got.Attachments) != 1 {
		t.Errorf("body round-trip wrong: %+v", got)
	}

	// Keeping one should drop the two least recently touched. m3 was just
	// read, which bumps its access time above m1 and m2.
	n, err := s.EvictBodies(ctx, 1)
	if err != nil {
		t.Fatalf("evict: %v", err)
	}

	if n != 2 {
		t.Errorf("evicted %d, want 2", n)
	}

	if _, err := s.Body(ctx, "m3"); err != nil {
		t.Errorf("most recent body was evicted: %v", err)
	}

	if _, err := s.Body(ctx, "m1"); err != mail.ErrNotFound {
		t.Errorf("stale body survived eviction: %v", err)
	}
}

func TestCursor(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	got, err := s.Cursor(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if got != "" {
		t.Errorf("fresh cache should have no cursor, got %q", got)
	}

	if err := s.SetCursor(ctx, "abc"); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.Cursor(ctx); got != "abc" {
		t.Errorf("cursor = %q, want abc", got)
	}

	// Overwriting must not insert a second row.
	if err := s.SetCursor(ctx, "def"); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.Cursor(ctx); got != "def" {
		t.Errorf("cursor = %q, want def", got)
	}
}

func TestNewslettersRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	last := time.Now().Truncate(time.Second)

	if err := s.PutNewsletters(ctx, []mail.Newsletter{{
		ID: "n1", ListID: "@list", Name: "Weekly",
		Sender:             mail.Address{Address: "hi@example.dev"},
		ReceivedTotal:      9,
		ReceivedLast30Days: 3,
		Unread:             1,
		LastReceived:       last,
		CanUnsubscribe:     true,
		MoveToBoxID:        "box-2",
	}}); err != nil {
		t.Fatalf("put: %v", err)
	}

	out, err := s.Newsletters(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if len(out) != 1 {
		t.Fatalf("got %d newsletters, want 1", len(out))
	}

	n := out[0]

	if n.Name != "Weekly" || n.ReceivedLast30Days != 3 || !n.CanUnsubscribe || n.MoveToBoxID != "box-2" {
		t.Errorf("newsletter round-trip wrong: %+v", n)
	}

	if n.LastRead != nil {
		t.Errorf("LastRead should stay nil, got %v", n.LastRead)
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}

	return false
}
