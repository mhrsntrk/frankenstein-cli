package screener_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mhrsntrk/frankenstein-cli/internal/config"
	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
	"github.com/mhrsntrk/frankenstein-cli/internal/mail/fake"
	"github.com/mhrsntrk/frankenstein-cli/internal/screener"
	"github.com/mhrsntrk/frankenstein-cli/internal/store"
)

func setup(t *testing.T) (*fake.Provider, *store.Store, config.ScreenerConfig) {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() { st.Close() })

	p := fake.New()

	cfg := config.ScreenerConfig{
		ImboxID: "b-imbox", FeedID: "b-feed",
		PaperTrailID: "b-paper", ScreenedOutID: "b-out",
		Enabled: true,
	}

	return p, st, cfg
}

// seed puts a few messages in the cache for the screener to observe.
func seed(t *testing.T, st *store.Store) {
	t.Helper()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	msgs := []mail.Message{
		{
			ID: "m1", ConversationID: "c1", Subject: "Hi", Time: now.Add(-48 * time.Hour),
			From: mail.Address{Name: "Ada", Address: "ada@example.com"},
		},
		{
			ID: "m2", ConversationID: "c2", Subject: "Hi again", Time: now,
			From: mail.Address{Name: "Ada", Address: "ada@example.com"},
		},
		{
			ID: "m3", ConversationID: "c3", Subject: "Your receipt", Time: now,
			From: mail.Address{Address: "billing@shop.example"}, CategoryID: "24",
		},
		{
			ID: "m4", ConversationID: "c4", Subject: "Weekly digest", Time: now,
			From:         mail.Address{Address: "news@list.example"},
			NewsletterID: "sub-1", CategoryID: "21",
		},
		{
			// Drafts are written by the user, so their "sender" must never
			// end up in the screener.
			ID: "m5", ConversationID: "c5", Subject: "wip", Time: now,
			From: mail.Address{Address: "me@example.com"}, IsDraft: true,
		},
	}

	if err := st.PutMessages(ctx, msgs); err != nil {
		t.Fatalf("seed messages: %v", err)
	}
}

func TestObserveRecordsSendersOnce(t *testing.T) {
	ctx := context.Background()
	p, st, cfg := setup(t)

	seed(t, st)

	sc := screener.New(st, p, cfg)

	n, err := sc.Observe(ctx)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}

	// Three real senders; the draft's author is excluded.
	if n != 3 {
		t.Errorf("observed %d senders, want 3", n)
	}

	pending, err := sc.Pending(ctx, 0)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}

	if len(pending) != 3 {
		t.Fatalf("got %d pending, want 3", len(pending))
	}

	var ada screener.Sender

	for _, s := range pending {
		if s.Address == "ada@example.com" {
			ada = s
		}

		if s.Address == "me@example.com" {
			t.Error("a draft's author reached the screener")
		}
	}

	if ada.MessageCount != 2 {
		t.Errorf("Ada's message count = %d, want 2", ada.MessageCount)
	}

	if !ada.FirstSeen.Before(ada.LastSeen) {
		t.Errorf("first/last seen wrong: %v .. %v", ada.FirstSeen, ada.LastSeen)
	}

	// Observing again must be idempotent.
	if _, err := sc.Observe(ctx); err != nil {
		t.Fatalf("second observe: %v", err)
	}

	pending, _ = sc.Pending(ctx, 0)
	if len(pending) != 3 {
		t.Errorf("second observe duplicated senders: %d", len(pending))
	}
}

func TestObserveDoesNotResetADecision(t *testing.T) {
	ctx := context.Background()
	p, st, cfg := setup(t)

	seed(t, st)

	sc := screener.New(st, p, cfg)

	if _, err := sc.Observe(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := sc.Decide(ctx, "ada@example.com", screener.Imbox); err != nil {
		t.Fatalf("decide: %v", err)
	}

	// New mail arriving must not undo a decision the user already made.
	if _, err := sc.Observe(ctx); err != nil {
		t.Fatal(err)
	}

	imbox, err := sc.List(ctx, screener.Imbox, 0)
	if err != nil {
		t.Fatal(err)
	}

	if len(imbox) != 1 || imbox[0].Address != "ada@example.com" {
		t.Errorf("decision lost after observe: %+v", imbox)
	}
}

func TestDecideLabelsAndClearsOtherBoxes(t *testing.T) {
	ctx := context.Background()
	p, st, cfg := setup(t)

	seed(t, st)

	sc := screener.New(st, p, cfg)

	if _, err := sc.Observe(ctx); err != nil {
		t.Fatal(err)
	}

	n, err := sc.Decide(ctx, "ada@example.com", screener.Imbox)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}

	if n != 2 {
		t.Errorf("relabelled %d conversations, want 2", n)
	}

	for _, want := range []string{"c1:b-imbox", "c2:b-imbox"} {
		if !has(p.Labelled, want) {
			t.Errorf("missing label call %q, got %v", want, p.Labelled)
		}
	}

	// The other three screener boxes must be cleared so a thread cannot sit
	// in two at once.
	for _, want := range []string{"c1:b-feed", "c1:b-paper", "c1:b-out"} {
		if !has(p.Unlabelled, want) {
			t.Errorf("missing unlabel call %q, got %v", want, p.Unlabelled)
		}
	}

	if has(p.Unlabelled, "c1:b-imbox") {
		t.Error("the chosen box was unlabelled")
	}
}

func TestDecideRejectsGarbage(t *testing.T) {
	ctx := context.Background()
	p, st, cfg := setup(t)

	sc := screener.New(st, p, cfg)

	if _, err := sc.Decide(ctx, "someone@example.com", screener.Decision("nonsense")); err == nil {
		t.Error("an unknown decision was accepted")
	}

	if _, err := sc.Decide(ctx, "  ", screener.Imbox); err == nil {
		t.Error("an empty sender was accepted")
	}
}

func TestDecideIsCaseInsensitive(t *testing.T) {
	ctx := context.Background()
	p, st, cfg := setup(t)

	seed(t, st)

	sc := screener.New(st, p, cfg)

	if _, err := sc.Observe(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := sc.Decide(ctx, "ADA@Example.COM", screener.Feed); err != nil {
		t.Fatalf("decide: %v", err)
	}

	feed, _ := sc.List(ctx, screener.Feed, 0)
	if len(feed) != 1 {
		t.Errorf("case-different address made a second sender: %+v", feed)
	}
}

func TestSuggestUsesProviderSignals(t *testing.T) {
	ctx := context.Background()
	p, st, cfg := setup(t)

	seed(t, st)

	sc := screener.New(st, p, cfg)

	if _, err := sc.Observe(ctx); err != nil {
		t.Fatal(err)
	}

	pending, _ := sc.Pending(ctx, 0)

	for _, s := range pending {
		got, why := sc.Suggest(ctx, s)

		switch s.Address {
		case "news@list.example":
			if got != screener.Feed {
				t.Errorf("a tracked mailing list suggested %q, want feed", got)
			}

			if !strings.Contains(why, "mailing list") {
				t.Errorf("reason for a list was %q", why)
			}

		case "billing@shop.example":
			// Category 24 is the default box, which holds personal mail and
			// banking alike, so it suggests the Imbox rather than filing
			// correspondence somewhere unread.
			if got != screener.Imbox {
				t.Errorf("a default-category sender suggested %q, want imbox", got)
			}

		case "ada@example.com":
			if got != screener.Pending {
				t.Errorf("an unclassified sender suggested %q, want pending", got)
			}
		}
	}
}

func TestRouteNewslettersPushesServerSideRules(t *testing.T) {
	ctx := context.Background()
	p, st, cfg := setup(t)

	seed(t, st)

	if err := st.PutNewsletters(ctx, []mail.Newsletter{
		{ID: "sub-1", Name: "Weekly", Sender: mail.Address{Address: "news@list.example"}},
	}); err != nil {
		t.Fatal(err)
	}

	sc := screener.New(st, p, cfg)

	if _, err := sc.Observe(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := sc.Decide(ctx, "news@list.example", screener.PaperTrail); err != nil {
		t.Fatal(err)
	}

	routed, err := sc.RouteNewsletters(ctx)
	if err != nil {
		t.Fatalf("route: %v", err)
	}

	if len(routed) != 1 || routed[0] != "Weekly" {
		t.Errorf("routed %v, want [Weekly]", routed)
	}

	// Paper Trail is keep-but-never-read, so it should be marked read on
	// arrival.
	if !has(p.Routed, "sub-1:b-paper:true") {
		t.Errorf("route call wrong: %v", p.Routed)
	}
}

func TestRouteNewslettersNeedsSetup(t *testing.T) {
	ctx := context.Background()
	p, st, _ := setup(t)

	sc := screener.New(st, p, config.ScreenerConfig{})

	if _, err := sc.RouteNewsletters(ctx); err == nil {
		t.Error("routing without setup should fail")
	}
}

func TestSetupReusesExistingBoxes(t *testing.T) {
	ctx := context.Background()
	p, _, _ := setup(t)

	p.Boxen = []mail.Box{{ID: "existing", Name: "Imbox", Kind: mail.BoxLabel}}

	cfg, err := screener.Setup(ctx, p)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	if cfg.ImboxID != "existing" {
		t.Errorf("Imbox ID = %q, want the existing box", cfg.ImboxID)
	}

	if !cfg.Configured() || !cfg.Enabled {
		t.Errorf("setup left an unusable config: %+v", cfg)
	}

	// Running twice must not create duplicates.
	before := len(p.Boxen)

	if _, err := screener.Setup(ctx, p); err != nil {
		t.Fatalf("second setup: %v", err)
	}

	if len(p.Boxen) != before {
		t.Errorf("second setup created %d more boxes", len(p.Boxen)-before)
	}
}

func TestDecideWithoutSetupRecordsButDoesNotLabel(t *testing.T) {
	ctx := context.Background()
	p, st, _ := setup(t)

	seed(t, st)

	sc := screener.New(st, p, config.ScreenerConfig{})

	if _, err := sc.Observe(ctx); err != nil {
		t.Fatal(err)
	}

	n, err := sc.Decide(ctx, "ada@example.com", screener.Imbox)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}

	if n != 0 {
		t.Errorf("relabelled %d without setup, want 0", n)
	}

	if len(p.Labelled) != 0 {
		t.Errorf("labelled without setup: %v", p.Labelled)
	}

	// The decision is still recorded, so a later `screener setup` can apply it.
	imbox, _ := sc.List(ctx, screener.Imbox, 0)
	if len(imbox) != 1 {
		t.Errorf("decision not recorded: %+v", imbox)
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

// The cache is warm, not a mirror: message rows exist only for threads that
// have been opened. A screener that read messages alone would be empty right
// after a first sync, which is when it matters most.
func TestObserveWorksFromConversationsAlone(t *testing.T) {
	ctx := context.Background()
	p, st, cfg := setup(t)

	now := time.Now().Truncate(time.Second)

	if err := st.PutConversations(ctx, []mail.Conversation{
		{
			ID: "c1", Subject: "Digest", Time: now, NumMessages: 3,
			Senders: []mail.Address{{Name: "Weekly", Address: "news@list.example"}},
			BoxIDs:  []string{"0"},
		},
		{
			ID: "c2", Subject: "Hi", Time: now.Add(-time.Hour), NumMessages: 1,
			Senders: []mail.Address{{Address: "ada@example.com"}},
			BoxIDs:  []string{"0"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// No messages cached at all.
	sc := screener.New(st, p, cfg)

	n, err := sc.Observe(ctx)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}

	if n != 2 {
		t.Fatalf("observed %d senders from conversations, want 2", n)
	}

	pending, _ := sc.Pending(ctx, 0)
	if len(pending) != 2 {
		t.Fatalf("got %d pending, want 2", len(pending))
	}

	for _, s := range pending {
		if s.Address == "news@list.example" && s.MessageCount != 3 {
			t.Errorf("message count = %d, want the conversation's 3", s.MessageCount)
		}
	}
}

// Conversations carry no newsletter link, so it has to come from the cached
// subscriptions by sender address, or the Feed suggestion never fires on a
// freshly synced cache.
func TestObserveLinksNewslettersBySenderAddress(t *testing.T) {
	ctx := context.Background()
	p, st, cfg := setup(t)

	now := time.Now().Truncate(time.Second)

	if err := st.PutConversations(ctx, []mail.Conversation{{
		ID: "c1", Subject: "Digest", Time: now, NumMessages: 1,
		Senders: []mail.Address{{Address: "news@list.example"}},
	}}); err != nil {
		t.Fatal(err)
	}

	if err := st.PutNewsletters(ctx, []mail.Newsletter{{
		ID: "sub-1", Name: "Weekly",
		Sender: mail.Address{Address: "News@List.Example"},
	}}); err != nil {
		t.Fatal(err)
	}

	sc := screener.New(st, p, cfg)

	if _, err := sc.Observe(ctx); err != nil {
		t.Fatal(err)
	}

	pending, _ := sc.Pending(ctx, 0)
	if len(pending) != 1 {
		t.Fatalf("got %d pending, want 1", len(pending))
	}

	if pending[0].NewsletterID != "sub-1" {
		t.Errorf("newsletter link = %q, want sub-1", pending[0].NewsletterID)
	}

	if got, _ := sc.Suggest(ctx, pending[0]); got != screener.Feed {
		t.Errorf("suggestion = %q, want feed", got)
	}
}

// Sent threads have the account as their sender; without filtering, the user
// would end up in their own screener.
func TestObserveExcludesOwnAddresses(t *testing.T) {
	ctx := context.Background()
	p, st, cfg := setup(t)

	p.Own = []mail.Address{{Address: "me@example.com"}}

	now := time.Now().Truncate(time.Second)

	if err := st.PutConversations(ctx, []mail.Conversation{
		{
			ID: "sent1", Subject: "Re: thing", Time: now, NumMessages: 1,
			Senders: []mail.Address{{Address: "ME@example.com"}},
		},
		{
			ID: "c1", Subject: "Hi", Time: now, NumMessages: 1,
			Senders: []mail.Address{{Address: "ada@example.com"}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	sc := screener.New(st, p, cfg)

	if _, err := sc.Observe(ctx); err != nil {
		t.Fatal(err)
	}

	pending, _ := sc.Pending(ctx, 0)

	for _, s := range pending {
		if s.Address == "me@example.com" {
			t.Error("the account's own address reached the screener")
		}
	}

	if len(pending) != 1 {
		t.Errorf("got %d pending, want 1", len(pending))
	}
}

// Proton rejects any colour outside its own palette with 422 Code 2001, so
// setup borrows colours the account already uses rather than inventing them.
func TestPaletteBorrowsFromExistingLabels(t *testing.T) {
	ctx := context.Background()
	p, _, _ := setup(t)

	p.Boxen = []mail.Box{
		{ID: "0", Name: "Inbox", Kind: mail.BoxSystem, Color: "#8080FF"},
		{ID: "1", Name: "Archive", Kind: mail.BoxSystem, Color: "#8080FF"},
		{ID: "w", Name: "Work", Kind: mail.BoxFolder, Color: "#EC3E7C"},
		{ID: "p", Name: "Personal", Kind: mail.BoxLabel, Color: "#F78400"},
	}

	if _, err := screener.Setup(ctx, p); err != nil {
		t.Fatalf("setup: %v", err)
	}

	used := map[string]bool{"#EC3E7C": false, "#F78400": false}

	for _, b := range p.Boxen {
		if _, ok := used[b.Color]; ok && b.Kind == mail.BoxLabel {
			used[b.Color] = true
		}
	}

	if !used["#EC3E7C"] && !used["#F78400"] {
		t.Error("setup did not reuse any colour the account already had")
	}

	// Every created box must carry a colour; an empty one is rejected too.
	for _, b := range p.Boxen {
		if b.Color == "" {
			t.Errorf("created %q with no colour", b.Name)
		}
	}
}

// flaky wraps the fake provider so a test can fail exactly one kind of call.
type flaky struct {
	*fake.Provider

	failLabel   bool
	failUnlabel bool
	failAddrs   bool
}

func (f *flaky) Label(ctx context.Context, ids []string, boxID string) error {
	if f.failLabel {
		return errors.New("label: network down")
	}

	return f.Provider.Label(ctx, ids, boxID)
}

func (f *flaky) Unlabel(ctx context.Context, ids []string, boxID string) error {
	if f.failUnlabel {
		return errors.New("unlabel: network down")
	}

	return f.Provider.Unlabel(ctx, ids, boxID)
}

func (f *flaky) Addresses(ctx context.Context) ([]mail.Address, error) {
	if f.failAddrs {
		return nil, errors.New("addresses: network down")
	}

	return f.Provider.Addresses(ctx)
}

// Screening a sender out has to actually empty the inbox of them: the target
// label alone would leave every thread sitting in Inbox too. Only Imbox keeps
// it, because in HEY terms the Imbox is the inbox.
func TestScreenedOutLeavesTheInbox(t *testing.T) {
	ctx := context.Background()
	p, st, cfg := setup(t)

	seed(t, st)

	sc := screener.New(st, p, cfg)

	if _, err := sc.Observe(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := sc.Decide(ctx, "billing@shop.example", screener.ScreenedOut); err != nil {
		t.Fatalf("decide: %v", err)
	}

	if !has(p.Labelled, "c3:b-out") {
		t.Errorf("missing label call, got %v", p.Labelled)
	}

	if !has(p.Unlabelled, "c3:0") {
		t.Errorf("screened-out mail was left in the inbox: %v", p.Unlabelled)
	}

	// The Imbox is the inbox, so an Imbox decision must not remove it.
	if _, err := sc.Decide(ctx, "ada@example.com", screener.Imbox); err != nil {
		t.Fatalf("decide: %v", err)
	}

	for _, unwanted := range []string{"c1:0", "c2:0"} {
		if has(p.Unlabelled, unwanted) {
			t.Errorf("an imbox decision removed the inbox: %v", p.Unlabelled)
		}
	}
}

// Decide only labels the conversations that exist at decision time; mail that
// arrives later has to be picked up by Reapply, or a decided sender lands
// back in the inbox forever.
func TestReapplyRoutesNewMailFromDecidedSender(t *testing.T) {
	ctx := context.Background()
	p, st, cfg := setup(t)

	now := time.Now().Truncate(time.Second)

	if err := st.PutConversations(ctx, []mail.Conversation{{
		ID: "c1", Subject: "Hi", Time: now, NumMessages: 1,
		Senders: []mail.Address{{Address: "ada@example.com"}},
		BoxIDs:  []string{"0"},
	}}); err != nil {
		t.Fatal(err)
	}

	sc := screener.New(st, p, cfg)

	if _, err := sc.Observe(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := sc.Decide(ctx, "ada@example.com", screener.Imbox); err != nil {
		t.Fatal(err)
	}

	// The next sync writes back what Decide did, and brings new mail from the
	// same sender that carries no screener label yet.
	if err := st.PutConversations(ctx, []mail.Conversation{
		{
			ID: "c1", Subject: "Hi", Time: now, NumMessages: 1,
			Senders: []mail.Address{{Address: "ada@example.com"}},
			BoxIDs:  []string{"0", "b-imbox"},
		},
		{
			ID: "c9", Subject: "Hi again", Time: now.Add(time.Hour), NumMessages: 1,
			Senders: []mail.Address{{Address: "ada@example.com"}},
			BoxIDs:  []string{"0"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	n, err := sc.Reapply(ctx)
	if err != nil {
		t.Fatalf("reapply: %v", err)
	}

	if n != 1 {
		t.Errorf("reapplied %d conversations, want 1", n)
	}

	if !has(p.Labelled, "c9:b-imbox") {
		t.Errorf("new mail was not routed: %v", p.Labelled)
	}

	if count(p.Labelled, "c1:b-imbox") != 1 {
		t.Errorf("an already-routed conversation was relabelled: %v", p.Labelled)
	}

	// Once the sync reflects the routing, Reapply has nothing to do.
	if err := st.PutConversations(ctx, []mail.Conversation{{
		ID: "c9", Subject: "Hi again", Time: now.Add(time.Hour), NumMessages: 1,
		Senders: []mail.Address{{Address: "ada@example.com"}},
		BoxIDs:  []string{"0", "b-imbox"},
	}}); err != nil {
		t.Fatal(err)
	}

	before := len(p.Labelled)

	if n, err := sc.Reapply(ctx); err != nil || n != 0 {
		t.Errorf("idle reapply did %d conversations, err %v", n, err)
	}

	if len(p.Labelled) != before {
		t.Errorf("idle reapply hit the provider: %v", p.Labelled[before:])
	}
}

// If the provider refuses the target label, nothing was applied, so nothing
// may be recorded either: the sender stays pending and retrying is just
// deciding again.
func TestDecideProviderFailureLeavesDecisionUncommitted(t *testing.T) {
	ctx := context.Background()
	_, st, cfg := setup(t)

	seed(t, st)

	fp := &flaky{Provider: fake.New(), failLabel: true}
	sc := screener.New(st, fp, cfg)

	if _, err := sc.Observe(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := sc.Decide(ctx, "ada@example.com", screener.Imbox); err == nil {
		t.Fatal("a failed label call was reported as success")
	}

	imbox, _ := sc.List(ctx, screener.Imbox, 0)
	if len(imbox) != 0 {
		t.Errorf("decision committed despite provider failure: %+v", imbox)
	}

	pending, _ := sc.Pending(ctx, 0)
	if len(pending) != 3 {
		t.Errorf("got %d pending, want the sender still among 3", len(pending))
	}

	// With the provider back, the retry is a plain re-decide.
	fp.failLabel = false

	n, err := sc.Decide(ctx, "ada@example.com", screener.Imbox)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}

	if n != 2 {
		t.Errorf("retry relabelled %d conversations, want 2", n)
	}
}

// When the target label lands but the cleanup half fails, the decision is
// committed anyway -- the mail is in the right box, just not only there --
// and the next Reapply pass finishes the job.
func TestDecidePartialFailureRecoversViaReapply(t *testing.T) {
	ctx := context.Background()
	_, st, cfg := setup(t)

	now := time.Now().Truncate(time.Second)

	// A thread previously filed under Feed, being re-decided into the Imbox.
	if err := st.PutConversations(ctx, []mail.Conversation{{
		ID: "c1", Subject: "Hi", Time: now, NumMessages: 1,
		Senders: []mail.Address{{Address: "ada@example.com"}},
		BoxIDs:  []string{"0", "b-feed"},
	}}); err != nil {
		t.Fatal(err)
	}

	fp := &flaky{Provider: fake.New(), failUnlabel: true}
	sc := screener.New(st, fp, cfg)

	if _, err := sc.Observe(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := sc.Decide(ctx, "ada@example.com", screener.Imbox); err == nil {
		t.Fatal("a failed cleanup was reported as success")
	}

	if !has(fp.Labelled, "c1:b-imbox") {
		t.Fatalf("target label never applied: %v", fp.Labelled)
	}

	imbox, _ := sc.List(ctx, screener.Imbox, 0)
	if len(imbox) != 1 {
		t.Fatalf("decision not committed after the label landed: %+v", imbox)
	}

	// The provider recovers, and the next sync shows the half-applied truth:
	// the new label on, the old one still there.
	fp.failUnlabel = false

	if err := st.PutConversations(ctx, []mail.Conversation{{
		ID: "c1", Subject: "Hi", Time: now, NumMessages: 1,
		Senders: []mail.Address{{Address: "ada@example.com"}},
		BoxIDs:  []string{"0", "b-imbox", "b-feed"},
	}}); err != nil {
		t.Fatal(err)
	}

	n, err := sc.Reapply(ctx)
	if err != nil {
		t.Fatalf("reapply: %v", err)
	}

	if n != 1 {
		t.Errorf("reapplied %d conversations, want 1", n)
	}

	if !has(fp.Unlabelled, "c1:b-feed") {
		t.Errorf("the stale label was never cleared: %v", fp.Unlabelled)
	}
}

// A conversation row and its opened messages describe the same mail; counting
// both would double every opened thread.
func TestObserveCountsOpenedThreadsOnce(t *testing.T) {
	ctx := context.Background()
	p, st, cfg := setup(t)

	now := time.Now().Truncate(time.Second)

	if err := st.PutConversations(ctx, []mail.Conversation{{
		ID: "c1", Subject: "Hi", Time: now, NumMessages: 3,
		Senders: []mail.Address{{Address: "ada@example.com"}},
		BoxIDs:  []string{"0"},
	}}); err != nil {
		t.Fatal(err)
	}

	if err := st.PutMessages(ctx, []mail.Message{
		{
			ID: "m1", ConversationID: "c1", Subject: "Hi", Time: now.Add(-time.Hour),
			From: mail.Address{Address: "ada@example.com"},
		},
		{
			ID: "m2", ConversationID: "c1", Subject: "Re: Hi", Time: now,
			From: mail.Address{Address: "ada@example.com"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	sc := screener.New(st, p, cfg)

	if _, err := sc.Observe(ctx); err != nil {
		t.Fatal(err)
	}

	pending, _ := sc.Pending(ctx, 0)
	if len(pending) != 1 {
		t.Fatalf("got %d pending, want 1", len(pending))
	}

	if pending[0].MessageCount != 3 {
		t.Errorf("message count = %d, want the conversation's 3", pending[0].MessageCount)
	}
}

// A transient Addresses failure must fail the pass, not run it with an empty
// own-address list: that would pour the user's own sent mail into the
// screener.
func TestObserveFailsWhenOwnAddressesUnavailable(t *testing.T) {
	ctx := context.Background()
	_, st, cfg := setup(t)

	seed(t, st)

	fp := &flaky{Provider: fake.New(), failAddrs: true}
	sc := screener.New(st, fp, cfg)

	if _, err := sc.Observe(ctx); err == nil {
		t.Fatal("observe swallowed the addresses error")
	}

	pending, _ := sc.Pending(ctx, 0)
	if len(pending) != 0 {
		t.Errorf("a failed observe still recorded %d senders", len(pending))
	}
}

// A rule pointing at the right box with the wrong read-on-arrival flag is not
// "already routed"; the flag alone must trigger a push.
func TestRouteNewslettersUpdatesReadFlag(t *testing.T) {
	ctx := context.Background()
	p, st, cfg := setup(t)

	seed(t, st)

	if err := st.PutNewsletters(ctx, []mail.Newsletter{{
		ID: "sub-1", Name: "Weekly",
		Sender:      mail.Address{Address: "news@list.example"},
		MoveToBoxID: "b-paper", MarkAsRead: false,
	}}); err != nil {
		t.Fatal(err)
	}

	sc := screener.New(st, p, cfg)

	if _, err := sc.Observe(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := sc.Decide(ctx, "news@list.example", screener.PaperTrail); err != nil {
		t.Fatal(err)
	}

	routed, err := sc.RouteNewsletters(ctx)
	if err != nil {
		t.Fatalf("route: %v", err)
	}

	if len(routed) != 1 || routed[0] != "Weekly" {
		t.Errorf("routed %v, want [Weekly]", routed)
	}

	if !has(p.Routed, "sub-1:b-paper:true") {
		t.Errorf("route call wrong: %v", p.Routed)
	}

	// With box and flag both right, there is nothing left to push.
	if err := st.PutNewsletters(ctx, []mail.Newsletter{{
		ID: "sub-1", Name: "Weekly",
		Sender:      mail.Address{Address: "news@list.example"},
		MoveToBoxID: "b-paper", MarkAsRead: true,
	}}); err != nil {
		t.Fatal(err)
	}

	if routed, err := sc.RouteNewsletters(ctx); err != nil || len(routed) != 0 {
		t.Errorf("an already-correct rule was pushed again: %v, err %v", routed, err)
	}
}

func count(hay []string, needle string) int {
	n := 0

	for _, h := range hay {
		if h == needle {
			n++
		}
	}

	return n
}
