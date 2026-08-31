package sync_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
	"github.com/mhrsntrk/frankenstein-cli/internal/mail/fake"
	"github.com/mhrsntrk/frankenstein-cli/internal/store"
	fsync "github.com/mhrsntrk/frankenstein-cli/internal/sync"
)

func setup(t *testing.T) (*fake.Provider, *store.Store, *fsync.Syncer) {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() { st.Close() })

	p := fake.New()
	p.Boxen = []mail.Box{
		{ID: "0", Name: "Inbox", Kind: mail.BoxSystem, Total: 2},
		{ID: "6", Name: "Archive", Kind: mail.BoxSystem},
	}

	now := time.Now().Truncate(time.Second)

	p.Convs = []mail.Conversation{
		{ID: "c1", Subject: "One", Time: now, NumMessages: 1, NumUnread: 1, BoxIDs: []string{"0"}},
		{ID: "c2", Subject: "Two", Time: now.Add(-time.Hour), NumMessages: 1, BoxIDs: []string{"0"}},
	}

	p.Msgs["c1"] = []mail.Message{{ID: "m1", ConversationID: "c1", Subject: "One", Time: now}}
	p.Bodies["m1"] = mail.Body{MessageID: "m1", MIMEType: "text/plain", Content: "hello"}

	return p, st, fsync.New(p, st)
}

func TestBackfillFillsCacheAndCursor(t *testing.T) {
	ctx := context.Background()
	p, st, s := setup(t)

	res, err := s.Once(ctx)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}

	if !res.FullResync {
		t.Error("a cold cache should backfill")
	}

	if res.Conversations != 2 {
		t.Errorf("cached %d conversations, want 2", res.Conversations)
	}

	boxes, _ := st.Boxes(ctx)
	if len(boxes) != 2 {
		t.Errorf("cached %d boxes, want 2", len(boxes))
	}

	cursor, _ := st.Cursor(ctx)
	if cursor == "" {
		t.Error("backfill did not record a cursor")
	}

	// Backfill must not walk every thread's messages: that would make the
	// warm cache a full mirror.
	for _, c := range p.Calls {
		if c == "Thread" {
			t.Error("backfill fetched thread messages; it should not")
		}
	}
}

func TestIncrementalAppliesDeltas(t *testing.T) {
	ctx := context.Background()
	p, st, s := setup(t)

	if _, err := s.Once(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	now := time.Now().Truncate(time.Second)

	p.Deltas = []mail.Delta{{
		Cursor: "cursor-1",
		Conversations: []mail.ConversationChange{
			{Kind: mail.ChangeCreate, ID: "c3", Conversation: mail.Conversation{
				ID: "c3", Subject: "Three", Time: now, NumMessages: 1, NumUnread: 1,
				BoxIDs: []string{"0"},
			}},
			{Kind: mail.ChangeDelete, ID: "c2"},
		},
		Messages: []mail.MessageChange{
			{Kind: mail.ChangeCreate, ID: "m3", Message: mail.Message{
				ID: "m3", ConversationID: "c3", Subject: "Three", Time: now,
				From: mail.Address{Address: "new@example.com"},
			}},
		},
	}}

	res, err := s.Once(ctx)
	if err != nil {
		t.Fatalf("incremental: %v", err)
	}

	if res.FullResync {
		t.Error("a warm cache should not resync")
	}

	convs, _ := st.Conversations(ctx, mail.ListOptions{BoxID: "0"})

	var ids []string
	for _, c := range convs {
		ids = append(ids, c.ID)
	}

	if !has(ids, "c3") {
		t.Errorf("created conversation missing: %v", ids)
	}

	if has(ids, "c2") {
		t.Errorf("deleted conversation survived: %v", ids)
	}

	if cursor, _ := st.Cursor(ctx); cursor != "cursor-1" {
		t.Errorf("cursor = %q, want cursor-1", cursor)
	}
}

func TestResyncFlagTriggersBackfill(t *testing.T) {
	ctx := context.Background()
	p, st, s := setup(t)

	if _, err := s.Once(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	// A Refresh from the provider means our cursor is too old to reconcile
	// from and the cache must be rebuilt rather than patched.
	p.Deltas = []mail.Delta{{Cursor: "cursor-9", Resync: true}}

	res, err := s.Once(ctx)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}

	if !res.FullResync {
		t.Error("a resync delta should trigger a backfill")
	}

	if res.Conversations == 0 {
		t.Error("the backfill after a resync fetched nothing")
	}

	if cursor, _ := st.Cursor(ctx); cursor == "" {
		t.Error("resync left no cursor")
	}
}

func TestBodyUsesCacheOnSecondRead(t *testing.T) {
	ctx := context.Background()
	p, _, s := setup(t)

	if _, err := s.Once(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Thread(ctx, "c1"); err != nil {
		t.Fatalf("thread: %v", err)
	}

	if _, err := s.Body(ctx, "m1"); err != nil {
		t.Fatalf("first body: %v", err)
	}

	before := countCalls(p.Calls, "Body")

	if _, err := s.Body(ctx, "m1"); err != nil {
		t.Fatalf("second body: %v", err)
	}

	if got := countCalls(p.Calls, "Body"); got != before {
		t.Errorf("second read hit the network: %d calls, want %d", got, before)
	}
}

func TestNewslettersUnsupportedIsNotAnError(t *testing.T) {
	ctx := context.Background()
	p, _, s := setup(t)

	p.SupportsNewsletters = false

	res, err := s.Once(ctx)
	if err != nil {
		t.Fatalf("a provider without newsletters should still sync: %v", err)
	}

	if res.Newsletters != 0 {
		t.Errorf("got %d newsletters from a provider that has none", res.Newsletters)
	}
}

// The backfill only adds. It used to sweep away whatever it had not seen,
// which meant one short page from the provider could make it delete a whole
// mailbox's worth of live mail; a thread the server no longer has is a stale
// row, and a stale row is the cheaper mistake.
func TestBackfillNeverDeletesWhatItDidNotSee(t *testing.T) {
	ctx := context.Background()
	p, st, s := setup(t)

	if _, err := s.Once(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	// The provider stops offering c2, exactly as it would for a thread that
	// was deleted server-side, and for one the listing simply did not reach.
	p.Convs = p.Convs[:1]

	if _, err := s.Backfill(ctx); err != nil {
		t.Fatalf("resync backfill: %v", err)
	}

	if _, err := st.Conversation(ctx, "c2"); err != nil {
		t.Errorf("the backfill deleted a conversation it merely did not see: %v", err)
	}

	if _, err := st.Conversation(ctx, "c1"); err != nil {
		t.Errorf("live conversation lost: %v", err)
	}
}

// A shallow pass says nothing about what lies below it, so it must leave the
// rest of the cache alone.
func TestShallowBackfillLeavesDeeperMailAlone(t *testing.T) {
	ctx := context.Background()
	_, st, s := setup(t)

	if _, err := s.Once(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	s.BackfillDepth = 1

	if _, err := s.Backfill(ctx); err != nil {
		t.Fatalf("shallow backfill: %v", err)
	}

	if _, err := st.Conversation(ctx, "c2"); err != nil {
		t.Errorf("conversation below the depth window was deleted: %v", err)
	}
}

func TestIncrementalAppliesEventsInOrder(t *testing.T) {
	ctx := context.Background()
	p, st, s := setup(t)

	if _, err := s.Once(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	now := time.Now().Truncate(time.Second)

	// Delete then recreate in one delta. Batched by kind this nets out to
	// the wrong end state; in stream order the recreate wins.
	p.Deltas = []mail.Delta{{
		Cursor: "cursor-1",
		Conversations: []mail.ConversationChange{
			{Kind: mail.ChangeDelete, ID: "c1"},
			{Kind: mail.ChangeCreate, ID: "c1", Conversation: mail.Conversation{
				ID: "c1", Subject: "One again", Time: now, NumMessages: 1,
				BoxIDs: []string{"0"},
			}},
		},
	}}

	if _, err := s.Once(ctx); err != nil {
		t.Fatalf("incremental: %v", err)
	}

	c, err := st.Conversation(ctx, "c1")
	if err != nil {
		t.Fatalf("recreated conversation missing: %v", err)
	}

	if c.Subject != "One again" {
		t.Errorf("subject = %q, want the recreated one", c.Subject)
	}
}

func TestUpdateEventDoesNotClobberWithZeroes(t *testing.T) {
	ctx := context.Background()
	p, st, s := setup(t)

	if _, err := s.Once(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	// An update carrying only the changed count: zero Time, empty subject,
	// no senders. Only NumUnread going to 0 is a real change here.
	p.Deltas = []mail.Delta{{
		Cursor: "cursor-1",
		Conversations: []mail.ConversationChange{
			{Kind: mail.ChangeUpdate, ID: "c1", Conversation: mail.Conversation{
				ID: "c1", NumUnread: 0,
			}},
		},
	}}

	if _, err := s.Once(ctx); err != nil {
		t.Fatalf("incremental: %v", err)
	}

	c, err := st.Conversation(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}

	if c.NumUnread != 0 {
		t.Errorf("the real change was dropped: unread = %d", c.NumUnread)
	}

	if c.Subject != "One" || c.NumMessages != 1 || c.Time.Unix() <= 0 {
		t.Errorf("partial update clobbered cached fields: %+v", c)
	}

	if !has(c.BoxIDs, "0") {
		t.Errorf("partial update wiped box membership: %v", c.BoxIDs)
	}
}

func TestMessageOnlyDeltaRefreshesBoxes(t *testing.T) {
	ctx := context.Background()
	p, st, s := setup(t)

	if _, err := s.Once(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	// A message marked read moves its box's unread count without any
	// conversation event, so a message-only delta must refresh boxes too.
	p.Boxen[0].Unread = 5

	p.Deltas = []mail.Delta{{
		Cursor: "cursor-1",
		Messages: []mail.MessageChange{
			{Kind: mail.ChangeUpdate, ID: "m1", Message: mail.Message{
				ID: "m1", ConversationID: "c1", Subject: "One",
			}},
		},
	}}

	if _, err := s.Once(ctx); err != nil {
		t.Fatalf("incremental: %v", err)
	}

	box, err := st.Box(ctx, "0")
	if err != nil {
		t.Fatal(err)
	}

	if box.Unread != 5 {
		t.Errorf("box unread = %d after message-only delta, want 5", box.Unread)
	}
}

func TestThreadKeepsNewerCachedHeader(t *testing.T) {
	ctx := context.Background()
	p, st, s := setup(t)

	if _, err := s.Once(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	// The incremental loop got ahead of this thread fetch: the cache holds a
	// higher Order than the snapshot the provider will return.
	if err := st.PutConversations(ctx, []mail.Conversation{{
		ID: "c1", Subject: "Newer", Time: time.Now(), NumMessages: 2,
		BoxIDs: []string{"0"}, Order: 10,
	}}); err != nil {
		t.Fatal(err)
	}

	p.Convs[0].Order = 5

	if _, err := s.Thread(ctx, "c1"); err != nil {
		t.Fatalf("thread: %v", err)
	}

	c, err := st.Conversation(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}

	if c.Subject != "Newer" {
		t.Errorf("stale thread snapshot overwrote a newer cached header: %+v", c)
	}

	// The messages are new data either way and must still be cached.
	msgs, _ := st.Messages(ctx, "c1")
	if len(msgs) != 1 {
		t.Errorf("thread messages not cached: %+v", msgs)
	}
}

func TestApplyLocalMove(t *testing.T) {
	ctx := context.Background()
	_, st, s := setup(t)

	if _, err := s.Once(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if err := s.ApplyLocalMove(ctx, []string{"c1"}, "6", "0"); err != nil {
		t.Fatalf("apply move: %v", err)
	}

	archived, _ := st.Conversations(ctx, mail.ListOptions{BoxID: "6", Desc: true})
	if len(archived) != 1 || archived[0].ID != "c1" {
		t.Errorf("move did not land in the target box: %+v", archived)
	}

	inbox, _ := st.Conversations(ctx, mail.ListOptions{BoxID: "0", Desc: true})
	for _, c := range inbox {
		if c.ID == "c1" {
			t.Errorf("moved conversation still in the source box: %+v", inbox)
		}
	}
}

func has(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}

	return false
}

func countCalls(calls []string, name string) int {
	n := 0

	for _, c := range calls {
		if c == name {
			n++
		}
	}

	return n
}
