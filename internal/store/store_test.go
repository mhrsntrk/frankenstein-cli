package store_test

import (
	"context"
	"database/sql"
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

	inbox, err := s.Conversations(ctx, mail.ListOptions{BoxID: "0", Desc: true})
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

func TestDeleteConversationsRemovesMessages(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if err := s.PutConversations(ctx, []mail.Conversation{
		{ID: "c1", Subject: "x", Time: time.Now(), BoxIDs: []string{"0"}},
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.PutMessages(ctx, []mail.Message{
		{ID: "m1", ConversationID: "c1"},
		{ID: "m2", ConversationID: "c1"},
		{ID: "m3", ConversationID: "c2"},
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteConversations(ctx, []string{"c1"}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	msgs, _ := s.Messages(ctx, "c1")
	if len(msgs) != 0 {
		t.Errorf("conversation delete orphaned its messages: %+v", msgs)
	}

	// Another thread's messages are none of the delete's business.
	if _, err := s.Message(ctx, "m3"); err != nil {
		t.Errorf("unrelated message deleted: %v", err)
	}
}

func TestApplyMove(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if err := s.PutBoxes(ctx, []mail.Box{
		{ID: "0", Name: "Inbox", Kind: mail.BoxSystem, Total: 2, Unread: 1},
		{ID: "6", Name: "Archive", Kind: mail.BoxSystem},
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().Truncate(time.Second)

	if err := s.PutConversations(ctx, []mail.Conversation{
		{ID: "c1", Subject: "unread", Time: now, NumUnread: 1, BoxIDs: []string{"0"}},
		{ID: "c2", Subject: "read", Time: now.Add(-time.Hour), BoxIDs: []string{"0"}},
	}); err != nil {
		t.Fatal(err)
	}

	// The ghost ID is skipped: there is nothing local to move.
	if err := s.ApplyMove(ctx, []string{"c1", "c2", "ghost"}, "6", "0"); err != nil {
		t.Fatalf("apply move: %v", err)
	}

	archived, err := s.Conversations(ctx, mail.ListOptions{BoxID: "6", Desc: true})
	if err != nil {
		t.Fatal(err)
	}

	if len(archived) != 2 {
		t.Fatalf("got %d in the target box, want 2: %+v", len(archived), archived)
	}

	inbox, _ := s.Conversations(ctx, mail.ListOptions{BoxID: "0"})
	if len(inbox) != 0 {
		t.Errorf("source box still holds moved threads: %+v", inbox)
	}

	// Counters moved with the rows: everything left the inbox, the one
	// unread thread carried its unread count along.
	src, _ := s.Box(ctx, "0")
	if src.Total != 0 || src.Unread != 0 {
		t.Errorf("source counters wrong: total=%d unread=%d", src.Total, src.Unread)
	}

	dst, _ := s.Box(ctx, "6")
	if dst.Total != 2 || dst.Unread != 1 {
		t.Errorf("target counters wrong: total=%d unread=%d", dst.Total, dst.Unread)
	}
}

func TestSearchEscapesLikeWildcards(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	now := time.Now().Truncate(time.Second)

	if err := s.PutConversations(ctx, []mail.Conversation{
		{ID: "c1", Subject: "100% off", Time: now},
		{ID: "c2", Subject: "100g off", Time: now.Add(-time.Minute)},
		{ID: "c3", Subject: "a_b", Time: now.Add(-2 * time.Minute)},
		{ID: "c4", Subject: "aXb", Time: now.Add(-3 * time.Minute)},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Conversations(ctx, mail.ListOptions{Search: "100%"})
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || got[0].ID != "c1" {
		t.Errorf("%% was treated as a wildcard: %+v", got)
	}

	got, _ = s.Conversations(ctx, mail.ListOptions{Search: "a_b"})
	if len(got) != 1 || got[0].ID != "c3" {
		t.Errorf("_ was treated as a wildcard: %+v", got)
	}
}

func TestConversationsOrderAndOffset(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	now := time.Now().Truncate(time.Second)

	if err := s.PutConversations(ctx, []mail.Conversation{
		{ID: "c1", Subject: "oldest", Time: now.Add(-2 * time.Hour)},
		{ID: "c2", Subject: "middle", Time: now.Add(-time.Hour)},
		{ID: "c3", Subject: "newest", Time: now},
	}); err != nil {
		t.Fatal(err)
	}

	desc, err := s.Conversations(ctx, mail.ListOptions{Desc: true})
	if err != nil {
		t.Fatal(err)
	}

	if len(desc) != 3 || desc[0].ID != "c3" {
		t.Errorf("Desc did not list newest first: %+v", desc)
	}

	asc, _ := s.Conversations(ctx, mail.ListOptions{})
	if len(asc) != 3 || asc[0].ID != "c1" {
		t.Errorf("unset Desc did not list oldest first: %+v", asc)
	}

	// A bare Offset without a Limit must still skip rows.
	rest, _ := s.Conversations(ctx, mail.ListOptions{Offset: 1})
	if len(rest) != 2 || rest[0].ID != "c2" {
		t.Errorf("bare offset ignored: %+v", rest)
	}
}

// TestMigrateFromV0 opens a database laid out the way the first release wrote
// it — no snippet column, no version stamp, orphaned messages — and expects
// Open to bring it fully current.
func TestMigrateFromV0(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cache.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(`
		CREATE TABLE conversations (
		    id              TEXT PRIMARY KEY,
		    subject         TEXT NOT NULL DEFAULT '',
		    senders         TEXT NOT NULL DEFAULT '[]',
		    recipients      TEXT NOT NULL DEFAULT '[]',
		    num_messages    INTEGER NOT NULL DEFAULT 0,
		    num_unread      INTEGER NOT NULL DEFAULT 0,
		    num_attachments INTEGER NOT NULL DEFAULT 0,
		    time            INTEGER NOT NULL DEFAULT 0,
		    size            INTEGER NOT NULL DEFAULT 0,
		    category_id     TEXT NOT NULL DEFAULT '',
		    sort_order      INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE messages (
		    id              TEXT PRIMARY KEY,
		    conversation_id TEXT NOT NULL DEFAULT '',
		    subject         TEXT NOT NULL DEFAULT '',
		    from_addr       TEXT NOT NULL DEFAULT '{}',
		    to_addrs        TEXT NOT NULL DEFAULT '[]',
		    cc_addrs        TEXT NOT NULL DEFAULT '[]',
		    bcc_addrs       TEXT NOT NULL DEFAULT '[]',
		    reply_to_addrs  TEXT NOT NULL DEFAULT '[]',
		    time            INTEGER NOT NULL DEFAULT 0,
		    size            INTEGER NOT NULL DEFAULT 0,
		    unread          INTEGER NOT NULL DEFAULT 0,
		    category_id     TEXT NOT NULL DEFAULT '',
		    newsletter_id   TEXT NOT NULL DEFAULT '',
		    num_attachments INTEGER NOT NULL DEFAULT 0,
		    spam_score      INTEGER NOT NULL DEFAULT 0,
		    is_draft        INTEGER NOT NULL DEFAULT 0,
		    snoozed_until   INTEGER,
		    external_id     TEXT NOT NULL DEFAULT '',
		    box_ids         TEXT NOT NULL DEFAULT '[]',
		    sort_order      INTEGER NOT NULL DEFAULT 0
		);
		INSERT INTO conversations (id, subject) VALUES ('c1', 'kept');
		INSERT INTO messages (id, conversation_id) VALUES
		    ('m1', 'c1'),
		    ('m2', 'ghost'),
		    ('m3', '');
	`); err != nil {
		t.Fatalf("write v0 database: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("open v0 database: %v", err)
	}

	t.Cleanup(func() { s.Close() })

	// v1: the snippet column exists and works.
	if err := s.SetSnippet(ctx, "c1", "preview"); err != nil {
		t.Fatalf("snippet column missing after migration: %v", err)
	}

	c, err := s.Conversation(ctx, "c1")
	if err != nil {
		t.Fatalf("read migrated conversation: %v", err)
	}

	if c.Snippet != "preview" {
		t.Errorf("snippet = %q, want preview", c.Snippet)
	}

	// v2: the orphan is gone, its legitimate siblings are not.
	if _, err := s.Message(ctx, "m2"); err != mail.ErrNotFound {
		t.Errorf("orphaned message survived migration: %v", err)
	}

	if _, err := s.Message(ctx, "m1"); err != nil {
		t.Errorf("owned message lost in migration: %v", err)
	}

	if _, err := s.Message(ctx, "m3"); err != nil {
		t.Errorf("draft message lost in migration: %v", err)
	}

	// The version stamp means none of this runs again.
	var version int
	if err := s.DB().QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}

	if version == 0 {
		t.Error("migration left no version stamp")
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
