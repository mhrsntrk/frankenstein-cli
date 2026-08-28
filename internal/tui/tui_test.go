package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mhrsntrk/frankenstein-cli/internal/config"
	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
	"github.com/mhrsntrk/frankenstein-cli/internal/mail/fake"
	"github.com/mhrsntrk/frankenstein-cli/internal/personal"
	"github.com/mhrsntrk/frankenstein-cli/internal/screener"
	"github.com/mhrsntrk/frankenstein-cli/internal/store"
	fsync "github.com/mhrsntrk/frankenstein-cli/internal/sync"
)

// harness wires a model against a fake provider and a real on-disk cache, so
// the tests exercise the same code paths the binary does.
type harness struct {
	m  *Model
	p  *fake.Provider
	st *store.Store
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	dir := t.TempDir()

	st, err := store.Open(filepath.Join(dir, "cache.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() { st.Close() })

	p := fake.New()
	p.Own = []mail.Address{{Address: "me@example.com"}}
	p.Boxen = []mail.Box{
		{ID: "0", Name: "Inbox", Kind: mail.BoxSystem, Total: 4, Unread: 2},
		{ID: "6", Name: "Archive", Kind: mail.BoxSystem},
		{ID: "3", Name: "Trash", Kind: mail.BoxSystem},
		{ID: "4", Name: "Spam", Kind: mail.BoxSystem},
		{ID: "10", Name: "Starred", Kind: mail.BoxSystem},
		{ID: "b1", Name: "Imbox", Kind: mail.BoxLabel},
		{ID: "b2", Name: "Feed", Kind: mail.BoxLabel},
		{ID: "b3", Name: "Paper Trail", Kind: mail.BoxLabel},
		{ID: "b4", Name: "Screened Out", Kind: mail.BoxLabel},
	}

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	convs := []mail.Conversation{
		{ID: "c1", Subject: "Unread one", Time: now, NumMessages: 1, NumUnread: 1,
			Senders: []mail.Address{{Name: "Ada", Address: "ada@example.com"}}, BoxIDs: []string{"0"}},
		{ID: "c2", Subject: "Unread two", Time: now.Add(-time.Hour), NumMessages: 2, NumUnread: 1,
			Senders: []mail.Address{{Name: "News", Address: "news@list.example"}}, BoxIDs: []string{"0"}},
		{ID: "c3", Subject: "Read one", Time: now.Add(-2 * time.Hour), NumMessages: 1,
			Senders: []mail.Address{{Name: "Bob", Address: "bob@example.com"}}, BoxIDs: []string{"0"}},
	}

	if err := st.PutBoxes(ctx, p.Boxen); err != nil {
		t.Fatal(err)
	}

	if err := st.PutConversations(ctx, convs); err != nil {
		t.Fatal(err)
	}

	p.Convs = convs
	p.Msgs["c1"] = []mail.Message{{
		ID: "m1", ConversationID: "c1", Subject: "Unread one", Time: now,
		From: mail.Address{Name: "Ada", Address: "ada@example.com"},
		To:   []mail.Address{{Address: "me@example.com"}},
	}}
	p.Bodies["m1"] = mail.Body{MessageID: "m1", MIMEType: "text/plain", Content: "hello there"}

	cfg := config.Defaults()
	cfg.Screener = config.ScreenerConfig{
		ImboxID: "b1", FeedID: "b2", PaperTrailID: "b3", ScreenedOutID: "b4", Enabled: true,
	}

	sc := screener.New(st, p, cfg.Screener)
	ps := personal.New(st.DB(), filepath.Join(dir, "journal"))

	m := New(st, fsync.New(p, st), p, sc, ps, nil, cfg)
	m.width, m.height = 100, 30
	m.boxes = p.Boxen
	m.quickBoxes = pickQuickBoxes(p.Boxen, cfg.Screener)
	m.convs = convs
	m.list.SetPostings(toPostings(convs))
	m.view = viewThreads
	m.box = p.Boxen[0]
	m.account = "me@example.com"

	return &harness{m: m, p: p, st: st}
}

// press sends a key and drains the command it returns, so the resulting
// message is applied the way the runtime would apply it.
func (h *harness) press(t *testing.T, key string) {
	t.Helper()

	var msg tea.KeyMsg

	switch key {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	case "space":
		msg = tea.KeyMsg{Type: tea.KeySpace}
	case "ctrl+s":
		msg = tea.KeyMsg{Type: tea.KeyCtrlS}
	case "ctrl+a":
		msg = tea.KeyMsg{Type: tea.KeyCtrlA}
	case "ctrl+d":
		msg = tea.KeyMsg{Type: tea.KeyCtrlD}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}

	_, cmd := h.m.Update(msg)
	h.drain(t, cmd)
}

// drain runs a command and feeds its message back, following batches.
func (h *harness) drain(t *testing.T, cmd tea.Cmd) {
	t.Helper()

	for i := 0; cmd != nil && i < 20; i++ {
		msg := cmd()
		if msg == nil {
			return
		}

		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				h.drain(t, c)
			}

			return
		}

		_, cmd = h.m.Update(msg)
	}
}

func TestNavigationDrillsInAndBack(t *testing.T) {
	h := newHarness(t)

	h.press(t, "enter")

	if h.m.view != viewThread {
		t.Fatalf("enter on a thread list gave view %v, want viewThread", h.m.view)
	}

	h.press(t, "enter")

	if h.m.view != viewMessage {
		t.Fatalf("enter on a thread gave view %v, want viewMessage", h.m.view)
	}

	if len(h.m.bodyLines) == 0 {
		t.Error("message view has no body lines")
	}

	h.press(t, "esc")

	if h.m.view != viewThread {
		t.Errorf("esc gave view %v, want viewThread", h.m.view)
	}

	h.press(t, "esc")
	h.press(t, "esc")

	if h.m.view != viewBoxes {
		t.Errorf("esc to the top gave view %v, want viewBoxes", h.m.view)
	}
}

func TestNumberKeysJumpToBox(t *testing.T) {
	h := newHarness(t)

	// The screener's boxes take the first slots.
	if len(h.m.quickBoxes) < 4 || h.m.quickBoxes[0].Name != "Imbox" {
		t.Fatalf("quick boxes wrong: %+v", h.m.quickBoxes)
	}

	h.press(t, "2")

	if h.m.box.Name != "Feed" {
		t.Errorf("pressing 2 opened %q, want Feed", h.m.box.Name)
	}

	if h.m.view != viewThreads {
		t.Errorf("pressing 2 gave view %v, want viewThreads", h.m.view)
	}
}

func TestSelectionAndBulkArchive(t *testing.T) {
	h := newHarness(t)

	h.press(t, "space")
	h.press(t, "space")

	if n := h.m.list.SelectionCount(); n != 2 {
		t.Fatalf("selected %d, want 2", n)
	}

	h.press(t, "a")

	// Archive means label Archive and unlabel the box being viewed, or the
	// thread would stay where it was.
	if !hasAll(h.p.Labelled, "c1:6", "c2:6") {
		t.Errorf("archive did not label both: %v", h.p.Labelled)
	}

	if !hasAll(h.p.Unlabelled, "c1:0", "c2:0") {
		t.Errorf("archive did not remove them from the inbox: %v", h.p.Unlabelled)
	}

	if h.m.list.SelectionCount() != 0 {
		t.Error("selection survived the action")
	}
}

func TestActionFallsBackToCursorRow(t *testing.T) {
	h := newHarness(t)

	h.press(t, "t")

	if !hasAll(h.p.Labelled, "c1:3") {
		t.Errorf("trash with no selection did not act on the cursor row: %v", h.p.Labelled)
	}
}

func TestStarLabelsWithoutRemoving(t *testing.T) {
	h := newHarness(t)

	h.press(t, "s")

	if !hasAll(h.p.Labelled, "c1:10") {
		t.Errorf("star did not label: %v", h.p.Labelled)
	}

	// Starring is additive; it must not take the thread out of the inbox.
	for _, u := range h.p.Unlabelled {
		if strings.HasSuffix(u, ":0") {
			t.Errorf("star removed the thread from its box: %v", h.p.Unlabelled)
		}
	}
}

func TestMarkReadAndUnread(t *testing.T) {
	h := newHarness(t)

	h.press(t, "e")

	if h.m.err != nil {
		t.Fatalf("mark read errored: %v", h.m.err)
	}

	h.press(t, "u")

	if h.m.err != nil {
		t.Fatalf("mark unread errored: %v", h.m.err)
	}
}

func TestScreeningDecidesAboutTheSender(t *testing.T) {
	h := newHarness(t)

	ctx := context.Background()

	if _, err := h.m.screener.Observe(ctx); err != nil {
		t.Fatal(err)
	}

	// Cursor is on c1, from ada@example.com.
	h.press(t, "d")

	if h.m.err != nil {
		t.Fatalf("screening errored: %v", h.m.err)
	}

	senders, err := h.m.screener.List(ctx, screener.Feed, 0)
	if err != nil {
		t.Fatal(err)
	}

	if len(senders) != 1 || senders[0].Address != "ada@example.com" {
		t.Fatalf("feed senders = %+v, want ada", senders)
	}

	// The label goes on, and the other three screener labels come off, so a
	// thread cannot sit in two screener boxes at once.
	if !hasAll(h.p.Labelled, "c1:b2") {
		t.Errorf("feed label missing: %v", h.p.Labelled)
	}

	if !hasAll(h.p.Unlabelled, "c1:b1", "c1:b3", "c1:b4") {
		t.Errorf("other screener labels not cleared: %v", h.p.Unlabelled)
	}
}

func TestScreenerQueueOpensAndDecides(t *testing.T) {
	h := newHarness(t)

	if _, err := h.m.screener.Observe(context.Background()); err != nil {
		t.Fatal(err)
	}

	h.press(t, "ctrl+s")

	if h.m.view != viewScreener {
		t.Fatalf("ctrl+s gave view %v, want viewScreener", h.m.view)
	}

	if len(h.m.senders) == 0 {
		t.Fatal("screener queue is empty")
	}

	first := h.m.senders[0].Address

	h.press(t, "i")

	if h.m.err != nil {
		t.Fatalf("deciding in the queue errored: %v", h.m.err)
	}

	imbox, _ := h.m.screener.List(context.Background(), screener.Imbox, 0)

	var found bool

	for _, s := range imbox {
		if s.Address == first {
			found = true
		}
	}

	if !found {
		t.Errorf("%q was not moved to the imbox", first)
	}
}

func TestComposeOpensAndSends(t *testing.T) {
	h := newHarness(t)

	h.press(t, "c")

	if h.m.view != viewCompose || h.m.compose == nil {
		t.Fatalf("c gave view %v with compose %v", h.m.view, h.m.compose)
	}

	// Typing a subject must not trigger single-letter actions.
	h.m.compose.field = 0
	h.m.compose.to.SetValue("someone@example.com")
	h.m.compose.field = 2
	h.m.compose.subject.SetValue("assassinate")

	if len(h.p.Labelled) != 0 {
		t.Errorf("typing in compose triggered actions: %v", h.p.Labelled)
	}

	h.m.compose.field = 3
	h.m.compose.body.SetValue("body text")

	h.press(t, "ctrl+d")

	if h.m.err != nil {
		t.Fatalf("send errored: %v", h.m.err)
	}

	if h.m.compose != nil {
		t.Error("compose stayed open after sending")
	}
}

func TestComposeRejectsBadRecipients(t *testing.T) {
	h := newHarness(t)

	h.press(t, "c")
	h.m.compose.to.SetValue("not-an-address")

	h.press(t, "ctrl+d")

	if h.m.err == nil {
		t.Error("sending to a non-address was accepted")
	}
}

func TestReplyPrefillsFromTheMessage(t *testing.T) {
	h := newHarness(t)

	h.press(t, "enter") // into the thread
	h.press(t, "enter") // read the message
	h.press(t, "r")

	if h.m.compose == nil {
		t.Fatal("reply did not open a compose")
	}

	if got := h.m.compose.to.Value(); got != "ada@example.com" {
		t.Errorf("reply To = %q, want ada@example.com", got)
	}

	if got := h.m.compose.subject.Value(); !strings.HasPrefix(got, "Re: ") {
		t.Errorf("reply subject = %q, want a Re: prefix", got)
	}

	// The body should be quoted once it has been read.
	if !strings.Contains(h.m.compose.body.Value(), "> hello there") {
		t.Errorf("reply body was not quoted: %q", h.m.compose.body.Value())
	}
}

func TestForwardPrefixesSubjectAndLeavesRecipientEmpty(t *testing.T) {
	h := newHarness(t)

	h.press(t, "enter")
	h.press(t, "enter")
	h.press(t, "f")

	if h.m.compose == nil {
		t.Fatal("forward did not open a compose")
	}

	if got := h.m.compose.subject.Value(); !strings.HasPrefix(got, "Fwd: ") {
		t.Errorf("forward subject = %q, want a Fwd: prefix", got)
	}

	if got := h.m.compose.to.Value(); got != "" {
		t.Errorf("forward prefilled a recipient: %q", got)
	}
}

func TestMovePickerMovesToChosenBox(t *testing.T) {
	h := newHarness(t)

	h.press(t, "v")

	if h.m.view != viewMovePicker {
		t.Fatalf("v gave view %v, want viewMovePicker", h.m.view)
	}

	h.m.moveFilter.SetValue("Archive")

	matches := h.m.moveMatches()
	if len(matches) != 1 || matches[0].Name != "Archive" {
		t.Fatalf("filter matched %+v", matches)
	}

	h.press(t, "enter")

	if !hasAll(h.p.Labelled, "c1:6") {
		t.Errorf("move did not label the target: %v", h.p.Labelled)
	}

	if h.m.view == viewMovePicker {
		t.Error("move picker stayed open")
	}
}

func TestMovePickerHidesAggregateBoxes(t *testing.T) {
	h := newHarness(t)

	h.m.boxes = append(h.m.boxes,
		mail.Box{ID: "5", Name: "All Mail", Kind: mail.BoxSystem},
		mail.Box{ID: "24", Name: "Primary", Kind: mail.BoxCategory})

	h.press(t, "v")

	for _, b := range h.m.moveTargets {
		if b.Name == "All Mail" || b.Kind == mail.BoxCategory {
			t.Errorf("%q should not be a move target", b.Name)
		}
	}
}

func TestSectionSwitching(t *testing.T) {
	h := newHarness(t)

	h.press(t, "tab")

	if h.m.section != sectionCalendar || h.m.view != viewCalendar {
		t.Errorf("tab gave section %v view %v", h.m.section, h.m.view)
	}

	h.press(t, "tab")

	if h.m.section != sectionJournal || h.m.view != viewJournal {
		t.Errorf("tab twice gave section %v view %v", h.m.section, h.m.view)
	}

	h.press(t, "tab")

	if h.m.section != sectionMail {
		t.Errorf("tab three times gave section %v, want mail", h.m.section)
	}
}

func TestFilterCapturesKeysWhileOpen(t *testing.T) {
	h := newHarness(t)

	h.press(t, "/")

	if !h.m.filtering {
		t.Fatal("/ did not open the filter")
	}

	// "q" while filtering is a character, not quit.
	h.press(t, "q")

	if h.m.quitting {
		t.Error("typing q in the filter quit the program")
	}

	if got := h.m.filter.Value(); got != "q" {
		t.Errorf("filter value = %q, want q", got)
	}

	h.press(t, "esc")

	if h.m.filtering {
		t.Error("esc did not close the filter")
	}
}

func TestHelpTogglesAndAnyKeyCloses(t *testing.T) {
	h := newHarness(t)

	h.press(t, "?")

	if !h.m.help {
		t.Fatal("? did not open help")
	}

	if !strings.Contains(h.m.View(), "screen sender to the Imbox") {
		t.Error("help does not document screening")
	}

	h.press(t, "j")

	if h.m.help {
		t.Error("a keypress did not close help")
	}
}

func TestEveryViewRendersWithinWidth(t *testing.T) {
	h := newHarness(t)

	if _, err := h.m.screener.Observe(context.Background()); err != nil {
		t.Fatal(err)
	}

	h.drain(t, h.m.loadSenders())

	h.m.journal = []personal.JournalEntry{{Day: "2026-08-28", Title: "Today"}}
	h.m.pending = 7

	views := map[string]view{
		"boxes":    viewBoxes,
		"threads":  viewThreads,
		"thread":   viewThread,
		"message":  viewMessage,
		"screener": viewScreener,
		"calendar": viewCalendar,
		"journal":  viewJournal,
	}

	// Populate the thread and message views.
	h.drain(t, h.m.loadThread("c1"))
	h.drain(t, h.m.loadBody("m1"))

	for name, v := range views {
		h.m.view = v

		for _, line := range strings.Split(h.m.View(), "\n") {
			if w := visibleWidth(line); w > h.m.width {
				t.Errorf("%s: line overflows %d columns (got %d): %q", name, h.m.width, w, line)
			}
		}
	}
}

func TestQuitSetsQuitting(t *testing.T) {
	h := newHarness(t)

	h.press(t, "q")

	if !h.m.quitting {
		t.Error("q did not quit")
	}

	if h.m.View() != "" {
		t.Error("a quitting model still rendered")
	}
}

// TestDumpViews prints each screen so the layout can be looked at rather than
// guessed about. Run with DUMP_VIEW=1 go test ./internal/tui -run TestDumpViews -v
func TestDumpViews(t *testing.T) {
	if os.Getenv("DUMP_VIEW") == "" {
		t.Skip("set DUMP_VIEW=1 to print the rendered screens")
	}

	h := newHarness(t)

	if _, err := h.m.screener.Observe(context.Background()); err != nil {
		t.Fatal(err)
	}

	h.drain(t, h.m.loadSenders())
	h.m.pending = len(h.m.senders)

	for _, v := range []struct {
		name string
		view view
	}{
		{"threads", viewThreads},
		{"screener", viewScreener},
		{"help", viewBoxes},
	} {
		h.m.view = v.view
		h.m.help = v.name == "help"

		t.Logf("\n=== %s ===\n%s\n", v.name, h.m.View())

		h.m.help = false
	}
}

func hasAll(hay []string, needles ...string) bool {
	for _, n := range needles {
		found := false

		for _, h := range hay {
			if h == n {
				found = true

				break
			}
		}

		if !found {
			return false
		}
	}

	return true
}
