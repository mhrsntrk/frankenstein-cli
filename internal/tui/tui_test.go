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
	"github.com/mhrsntrk/frankenstein-cli/internal/store"
	fsync "github.com/mhrsntrk/frankenstein-cli/internal/sync"
)

// harness wires a model against a fake provider and a real on-disk cache, so
// the tests exercise the same code paths the binary does.
type harness struct {
	m  *Model
	p  *fake.Provider
	st *store.Store
	ps *personal.Store
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
		{ID: "7", Name: "Sent", Kind: mail.BoxSystem},
		{ID: "8", Name: "Drafts", Kind: mail.BoxSystem},
		{ID: "3", Name: "Trash", Kind: mail.BoxSystem},
		{ID: "4", Name: "Spam", Kind: mail.BoxSystem},
		{ID: "10", Name: "Starred", Kind: mail.BoxSystem},
		{ID: "b1", Name: "Receipts", Kind: mail.BoxLabel},
		{ID: "b2", Name: "Travel", Kind: mail.BoxLabel},
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
	ps := personal.New(st.DB(), filepath.Join(dir, "journal"))

	// Todos are wired to the real local store, exactly as skill.go wires
	// them, so the band tests exercise the path that ships.
	todos := Todos{
		List: func(ctx context.Context) ([]TodoItem, error) {
			all, err := ps.Todos(ctx, false)
			if err != nil {
				return nil, err
			}

			out := make([]TodoItem, 0, len(all))
			for _, todo := range all {
				out = append(out, TodoItem{ID: todo.ID, Title: todo.Title, Done: todo.Done()})
			}

			return out, nil
		},
		Add: func(ctx context.Context, title string) error {
			_, err := ps.AddTodo(ctx, title, nil, "")

			return err
		},
		Complete: func(ctx context.Context, id int64) error {
			return ps.CompleteTodo(ctx, id, true)
		},
	}

	m := New(st, fsync.New(p, st), p, ps, nil, todos, nil, cfg)

	// The notes folder must never be the real one: these tests write and
	// delete files.
	m.notesDir = filepath.Join(dir, "notes")

	m.width, m.height = 100, 30
	m.boxes = p.Boxen
	m.quickBoxes = pickQuickBoxes(p.Boxen)
	m.list.setConvs(convs)
	m.view = viewThreads
	m.box = p.Boxen[0]
	m.account = "me@example.com"

	return &harness{m: m, p: p, st: st, ps: ps}
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

// typeText sends each character without draining what it returns.
//
// A text input answers a keystroke with textinput.Blink, which is a tick that
// sleeps for the cursor's blink interval. Draining it costs half a second per
// character, and the real runtime never waits on a tick either.
func (h *harness) typeText(t *testing.T, s string) {
	t.Helper()

	for _, r := range s {
		h.m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
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

	// Below the split threshold the mail views are the classic drill-down;
	// the split screen's own behavior is pinned separately.
	h.m.width = 90

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

	// The Inbox leads the bar, the way it does in every other mail client.
	if len(h.m.quickBoxes) < 2 || h.m.quickBoxes[0].Name != "Inbox" {
		t.Fatalf("quick boxes wrong: %+v", h.m.quickBoxes)
	}

	h.press(t, "2")

	if h.m.box.Name != "Starred" {
		t.Errorf("pressing 2 opened %q, want Starred", h.m.box.Name)
	}

	if h.m.view != viewThreads {
		t.Errorf("pressing 2 gave view %v, want viewThreads", h.m.view)
	}
}

// TestQuickBoxesAreConventionalAndSkipLabels pins the bar's contents. The
// account also carries labels, so this shows they are kept off the bar rather
// than merely missing.
func TestQuickBoxesAreConventionalAndSkipLabels(t *testing.T) {
	h := newHarness(t)

	var names []string
	for _, b := range h.m.quickBoxes {
		names = append(names, b.Name)
	}

	got := strings.Join(names, " ")
	want := "Inbox Starred Archive Sent Drafts Spam Trash"

	if got != want {
		t.Errorf("box bar is %q, want %q", got, want)
	}

	for _, b := range h.m.quickBoxes {
		if b.Kind != mail.BoxSystem {
			t.Errorf("%q is on the bar; only system boxes belong there", b.Name)
		}
	}
}

// withCategories gives the account the inbox categories, which the fake does
// not carry, and hands back the row order they should come out in. They go in
// scrambled on purpose: the row is ordered by name, not by the provider.
func withCategories(t *testing.T, h *harness) []mail.Box {
	t.Helper()

	h.m.boxes = append(h.m.boxes,
		mail.Box{ID: "21", Name: "Promotions", Kind: mail.BoxCategory},
		mail.Box{ID: "24", Name: "Primary", Kind: mail.BoxCategory},
		mail.Box{ID: "20", Name: "Social", Kind: mail.BoxCategory},
	)

	cats := h.m.categoryBoxes()

	var names []string
	for _, b := range cats {
		names = append(names, b.Name)
	}

	if got, want := strings.Join(names, " "), "Primary Social Promotions"; got != want {
		t.Fatalf("category order is %q, want %q", got, want)
	}

	return cats
}

func TestCategoryRowIsDrawnOnlyInTheInbox(t *testing.T) {
	h := newHarness(t)
	h.m.width, h.m.height = 140, 30
	withCategories(t, h)

	_ = h.m.View()

	rows := h.m.chromeRows()
	if rows.categories < 0 {
		t.Fatal("the category row is missing from the Inbox")
	}

	if rows.categories != rows.boxes+1 {
		t.Errorf("the category row is at %d, want %d, directly under the bar",
			rows.categories, rows.boxes+1)
	}

	line := stripANSI(strings.Split(h.m.header(), "\n")[rows.categories])
	for _, want := range []string{"All", "Primary", "Social", "Promotions"} {
		if !strings.Contains(line, want) {
			t.Errorf("the category row %q is missing %q", line, want)
		}
	}

	// Sent has no categories, so the row goes away and nothing under it moves.
	sent, ok := h.m.boxByName("Sent")
	if !ok {
		t.Fatal("the account has no Sent box")
	}

	h.m.box = sent
	_ = h.m.View()

	if h.m.chromeRows().categories >= 0 {
		t.Error("the category row is drawn in Sent")
	}
}

func TestClickingACategoryFiltersTheInbox(t *testing.T) {
	h := newHarness(t)
	h.m.width, h.m.height = 140, 30
	withCategories(t, h)

	// One conversation carries the Promotions label, so the filter has
	// something to pick out rather than merely emptying the list.
	if err := h.st.PutConversations(context.Background(), []mail.Conversation{{
		ID: "c4", Subject: "Half price", Time: time.Now(), NumMessages: 1,
		Senders: []mail.Address{{Name: "Shop", Address: "shop@example.com"}},
		BoxIDs:  []string{"0", "21"},
	}}); err != nil {
		t.Fatal(err)
	}

	_ = h.m.View()

	rows := h.m.chromeRows()
	plain := stripANSI(strings.Split(h.m.header(), "\n")[rows.categories])

	col := strings.Index(plain, "Promotions")
	if col < 0 {
		t.Fatalf("Promotions is not in the category row: %q", plain)
	}

	h.clickAt(t, col+len(h.m.gutter()), rows.categories)

	if h.m.categoryID != "21" {
		t.Fatalf("clicking Promotions filtered by %q, want 21", h.m.categoryID)
	}

	// The box is a filter on the Inbox, not a box of its own.
	if h.m.box.Name != "Inbox" {
		t.Errorf("the box became %q; the category is a filter on the Inbox", h.m.box.Name)
	}

	if len(h.m.list.convs) != 1 || h.m.list.convs[0].ID != "c4" {
		t.Errorf("the filtered listing is %+v, want only c4", h.m.list.convs)
	}

	// All puts the whole box back.
	col = strings.Index(plain, "All")
	if col < 0 {
		t.Fatalf("All is not in the category row: %q", plain)
	}

	h.clickAt(t, col+len(h.m.gutter()), h.m.chromeRows().categories)

	if h.m.categoryID != "" {
		t.Fatalf("clicking All left the filter at %q", h.m.categoryID)
	}

	if len(h.m.list.convs) != 4 {
		t.Errorf("All listed %d conversations, want the whole Inbox of 4", len(h.m.list.convs))
	}
}

func TestSwitchingBoxClearsTheCategory(t *testing.T) {
	h := newHarness(t)
	h.m.width, h.m.height = 140, 30
	withCategories(t, h)

	h.press(t, "]")

	if h.m.categoryID == "" {
		t.Fatal("] did not pick a category")
	}

	// 3 is Archive on the conventional bar.
	h.press(t, "3")

	if h.m.box.Name != "Archive" {
		t.Fatalf("3 opened %q, want Archive", h.m.box.Name)
	}

	if h.m.categoryID != "" {
		t.Errorf("the category survived the box change as %q", h.m.categoryID)
	}
}

func TestBracketKeysCycleCategories(t *testing.T) {
	h := newHarness(t)
	h.m.width, h.m.height = 140, 30
	cats := withCategories(t, h)

	h.press(t, "]")

	if h.m.categoryID != cats[0].ID {
		t.Fatalf("] gave %q, want %q", h.m.categoryID, cats[0].ID)
	}

	h.press(t, "]")

	if h.m.categoryID != cats[1].ID {
		t.Fatalf("a second ] gave %q, want %q", h.m.categoryID, cats[1].ID)
	}

	h.press(t, "[")
	h.press(t, "[")

	if h.m.categoryID != "" {
		t.Fatalf("[ back past the first category gave %q, want All", h.m.categoryID)
	}

	// The row wraps, so [ from All lands on the last category.
	h.press(t, "[")

	if h.m.categoryID != cats[len(cats)-1].ID {
		t.Errorf("[ from All gave %q, want the last category %q",
			h.m.categoryID, cats[len(cats)-1].ID)
	}
}

func TestSelectionAndBulkArchive(t *testing.T) {
	h := newHarness(t)

	h.press(t, "space")
	h.press(t, "space")

	if n := h.m.list.selectionCount(); n != 2 {
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

	if h.m.list.selectionCount() != 0 {
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

func TestComposeOpensAndSends(t *testing.T) {
	h := newHarness(t)

	h.press(t, "c")

	if h.m.view != viewCompose || h.m.compose == nil {
		t.Fatalf("c gave view %v with compose %v", h.m.view, h.m.compose)
	}

	// Typing a subject must not trigger single-letter actions.
	h.m.compose.field = 0
	h.m.compose.to.SetValue("someone@example.com")
	h.m.compose.field = 3
	h.m.compose.subject.SetValue("assassinate")

	if len(h.p.Labelled) != 0 {
		t.Errorf("typing in compose triggered actions: %v", h.p.Labelled)
	}

	h.m.compose.field = 4
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

	if h.m.section != sectionNotes || h.m.view != viewNotes {
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

	if !strings.Contains(h.m.View(), "mark read, mark unread") {
		t.Error("help does not document the mail keys")
	}

	h.press(t, "j")

	if h.m.help {
		t.Error("a keypress did not close help")
	}
}

func TestEveryViewRendersWithinWidth(t *testing.T) {
	h := newHarness(t)

	h.m.notes = []personal.Note{{Name: "today", Title: "Today", Updated: time.Now()}}

	views := map[string]view{
		"boxes":    viewBoxes,
		"threads":  viewThreads,
		"thread":   viewThread,
		"message":  viewMessage,
		"calendar": viewCalendar,
		"notes":    viewNotes,
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

	for _, v := range []struct {
		name string
		view view
	}{
		{"threads", viewThreads},
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

// The header scrolled off the top because the list drew more rows than were
// left for it. Whatever the view or the terminal size, the whole screen has to
// fit in the terminal.
func TestViewNeverOverflowsTheTerminal(t *testing.T) {
	h := newHarness(t)

	h.drain(t, h.m.loadThread("c1"))
	h.drain(t, h.m.loadBody("m1"))

	// Enough rows that a list left unbounded would run past any of these.
	var many []mail.Conversation

	for i := range 200 {
		many = append(many, mail.Conversation{
			ID:          string(rune('a'+i%26)) + string(rune('a'+i/26)),
			Subject:     "Subject line number " + string(rune('0'+i%10)),
			Time:        time.Now().Add(-time.Duration(i) * time.Hour),
			NumMessages: 1,
			NumUnread:   i % 3,
			Senders:     []mail.Address{{Name: "Someone"}},
			Snippet:     "An excerpt long enough to be truncated on a narrow terminal.",
		})
	}

	h.m.list.setConvs(many)

	sizes := [][2]int{{80, 24}, {100, 30}, {200, 50}, {300, 80}, {120, 14}, {60, 10}}

	views := map[string]view{
		"boxes":    viewBoxes,
		"threads":  viewThreads,
		"thread":   viewThread,
		"message":  viewMessage,
		"calendar": viewCalendar,
		"notes":    viewNotes,
	}

	for _, size := range sizes {
		h.m.width, h.m.height = size[0], size[1]

		for name, v := range views {
			h.m.view = v

			got := lineCount(h.m.View())
			if got > h.m.height {
				t.Errorf("%s at %dx%d rendered %d lines, terminal has %d",
					name, size[0], size[1], got, h.m.height)
			}
		}
	}
}

// The header is what tells you where you are; it must survive a full list.
func TestHeaderStaysOnScreen(t *testing.T) {
	h := newHarness(t)

	var many []mail.Conversation

	for i := range 100 {
		many = append(many, mail.Conversation{
			ID: string(rune('a' + i%26)), Subject: "Subject", Time: time.Now(),
			NumMessages: 1, Senders: []mail.Address{{Name: "Someone"}},
		})
	}

	h.m.list.setConvs(many)
	h.m.width, h.m.height = 120, 30
	h.m.view = viewThreads

	out := h.m.View()

	first := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(first, "frankenstein") {
		t.Errorf("the first line is not the title bar: %q", first)
	}

	if strings.Contains(out, "HEY") {
		t.Error("upstream's wordmark is still being rendered")
	}
}

// The footer packs its bindings to the width rather than using fixed groups,
// so a wide terminal gets few long lines and a narrow one gets more short
// ones. Neither may overflow.
func TestFooterPacksToWidth(t *testing.T) {
	h := newHarness(t)
	h.m.view = viewThreads

	var lastLines int

	for _, w := range []int{60, 100, 160, 240} {
		h.m.width, h.m.height = w, 40

		hints := h.m.keyHints()

		for _, line := range strings.Split(hints, "\n") {
			if got := visibleWidth(line); got > h.m.contentWidth() {
				t.Errorf("at %d columns a footer line is %d wide, content is %d: %q",
					w, got, h.m.contentWidth(), line)
			}
		}

		lines := lineCount(hints)

		// Every binding still has to be there, however it is packed.
		for _, b := range h.m.keyBindings() {
			if !strings.Contains(hints, b.what) {
				t.Errorf("at %d columns the footer lost %q", w, b.what)
			}
		}

		if lastLines != 0 && lines > lastLines {
			t.Errorf("at %d columns the footer grew to %d lines from %d", w, lines, lastLines)
		}

		lastLines = lines
	}
}

// mouse sends a click at a screen position and drains what it returns.
func (h *harness) clickAt(t *testing.T, x, y int) {
	t.Helper()

	_, cmd := h.m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: x, Y: y,
	})
	h.drain(t, cmd)
}

func TestClickMovesTheCursorThenOpens(t *testing.T) {
	h := newHarness(t)

	// Narrow enough for the single-pane list, where a click deliberately
	// moves the cursor first and only a second click opens.
	h.m.width, h.m.height = 90, 30

	// Render once so the chrome measurements are current.
	_ = h.m.View()

	rows := h.m.chromeRows()
	gutter := len(h.m.gutter())

	// The second conversation: two lines each, and no headings between them.
	target := rows.body + 2

	h.clickAt(t, gutter+10, target)

	if got := h.m.list.cursor; got != 1 {
		t.Fatalf("clicking the second row put the cursor at %d, want 1", got)
	}

	if h.m.view != viewThreads {
		t.Fatal("the first click opened a thread; it should only move the cursor")
	}

	// Clicking the row already under the cursor opens it.
	h.clickAt(t, gutter+10, target)

	if h.m.view != viewThread {
		t.Errorf("clicking the selected row gave view %v, want viewThread", h.m.view)
	}
}

func TestSplitEnterExpandsTheNewestMessage(t *testing.T) {
	h := newHarness(t)
	h.m.width, h.m.height = 120, 30

	// On the split screen, opening a conversation fetches the thread and the
	// newest message's body in one motion, the way Proton's reading pane does.
	h.press(t, "enter")

	if h.m.view != viewMessage {
		t.Fatalf("enter on the split list gave view %v, want viewMessage", h.m.view)
	}

	if len(h.m.bodyLines) == 0 {
		t.Error("the expanded message has no body lines")
	}
}

func TestSplitClickOpensAConversation(t *testing.T) {
	h := newHarness(t)
	h.m.width, h.m.height = 120, 30

	_ = h.m.View()

	rows := h.m.chromeRows()
	gutter := len(h.m.gutter())

	// The list pane draws no section headings: conversation i sits at lines
	// 2i and 2i+1. The first one has a cached body, so a single click carries
	// all the way to the expanded message.
	h.clickAt(t, gutter+10, rows.body)

	if h.m.view != viewMessage {
		t.Fatalf("the click gave view %v, want viewMessage with the thread open", h.m.view)
	}

	if h.m.thread.Conversation.ID != "c1" {
		t.Errorf("the open thread is %q, want c1", h.m.thread.Conversation.ID)
	}

	// The second conversation has no cached messages; the click still moves
	// the cursor and opens what there is of the thread.
	h.clickAt(t, gutter+10, rows.body+2)

	if got := h.m.list.cursor; got != 1 {
		t.Fatalf("clicking the second conversation put the cursor at %d, want 1", got)
	}

	if h.m.thread.Conversation.ID != "c2" {
		t.Errorf("the open thread is %q, want c2", h.m.thread.Conversation.ID)
	}
}

func TestSplitComposerMouse(t *testing.T) {
	h := newHarness(t)
	h.m.width, h.m.height = 120, 30

	h.press(t, "c")

	if h.m.compose == nil || h.m.view != viewCompose {
		t.Fatal("c did not open the composer")
	}

	lay := composerPlace(h.m.width, h.m.height, false, false)

	// A click outside the popup is spent on nothing while it is open.
	h.clickAt(t, 0, lay.y)

	if h.m.view != viewCompose {
		t.Fatal("a click outside the open composer reached the screen behind it")
	}

	// The minimize button parks the popup and hands the screen back.
	h.clickAt(t, lay.x+lay.w-7, lay.y)

	if h.m.compose == nil || !h.m.composerMin {
		t.Fatal("the minimize button did not park the composer")
	}

	if h.m.view == viewCompose {
		t.Fatal("the minimized composer still holds the view")
	}

	// The bar restores it.
	bar := composerPlace(h.m.width, h.m.height, true, false)
	h.clickAt(t, bar.x+1, bar.y)

	if h.m.composerMin || h.m.view != viewCompose {
		t.Fatal("clicking the bar did not restore the composer")
	}

	// The close button discards it.
	h.clickAt(t, lay.x+lay.w-3, lay.y)

	if h.m.compose != nil || h.m.view == viewCompose {
		t.Fatal("the close button did not discard the composer")
	}
}

func TestClickOutsideTheContentColumnIsIgnored(t *testing.T) {
	h := newHarness(t)
	h.m.width, h.m.height = 200, 30

	_ = h.m.View()

	before := h.m.list.cursor

	// In the left margin, outside the centred column.
	h.clickAt(t, 0, h.m.chromeRows().body+1)

	if h.m.list.cursor != before {
		t.Error("a click in the margin moved the cursor")
	}
}

func TestClickOnTheBoxBarSwitchesBox(t *testing.T) {
	h := newHarness(t)
	h.m.width, h.m.height = 140, 30

	_ = h.m.View()

	rows := h.m.chromeRows()
	if rows.boxes < 0 {
		t.Skip("the box bar is not being drawn at this size")
	}

	plain := stripANSI(strings.Split(h.m.header(), "\n")[rows.boxes])

	col := strings.Index(plain, "Archive")
	if col < 0 {
		t.Fatalf("Archive is not in the box bar: %q", plain)
	}

	h.clickAt(t, col+len(h.m.gutter()), rows.boxes)

	if h.m.box.Name != "Archive" {
		t.Errorf("clicking Archive opened %q", h.m.box.Name)
	}
}

func TestWheelScrollsWithoutMovingTheCursor(t *testing.T) {
	h := newHarness(t)
	h.m.width, h.m.height = 120, 20

	var many []mail.Conversation

	for i := range 60 {
		many = append(many, mail.Conversation{
			ID: string(rune('a'+i%26)) + string(rune('a'+i/26)), Subject: "Subject",
			Time:        time.Now().Add(-time.Duration(i) * time.Hour),
			NumMessages: 1, Senders: []mail.Address{{Name: "Someone"}},
		})
	}

	h.m.list.setConvs(many)

	_ = h.m.View()

	before := h.m.list.cursor

	_, cmd := h.m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	h.drain(t, cmd)

	if h.m.list.cursor != before {
		t.Error("the wheel moved the cursor; it should only scroll")
	}
}

// t is trash in the mail views, and was being taken there before the calendar
// ever saw it. Only n and the digits worked.
func TestCalendarKeysAreNotEatenByMailActions(t *testing.T) {
	h := newHarness(t)
	h.m.section = sectionCalendar
	h.m.view = viewCalendar

	cases := []struct {
		key    string
		offset int
		grid   calendarView
	}{
		{"n", 1, calendarWeek},
		{"n", 2, calendarWeek},
		{"p", 1, calendarWeek},
		{"p", 0, calendarWeek},
		{"p", -1, calendarWeek},
		{"t", 0, calendarWeek},
		{"1", 0, calendarDay},
		{"n", 1, calendarDay},
		{"t", 0, calendarDay},
		{"2", 0, calendarWeek},
	}

	for i, c := range cases {
		h.press(t, c.key)

		if h.m.calOffset != c.offset {
			t.Errorf("step %d: %q left the offset at %d, want %d",
				i, c.key, h.m.calOffset, c.offset)
		}

		if h.m.calView != c.grid {
			t.Errorf("step %d: %q left the grid as %v, want %v",
				i, c.key, h.m.calView, c.grid)
		}
	}

	// None of it may have reached into the mailbox.
	if len(h.p.Labelled) > 0 || len(h.p.Unlabelled) > 0 {
		t.Errorf("calendar keys touched mail: labelled %v unlabelled %v",
			h.p.Labelled, h.p.Unlabelled)
	}
}

// The keys a section does not claim still have to work in it.
func TestSharedKeysStillWorkInTheCalendar(t *testing.T) {
	h := newHarness(t)
	h.m.section = sectionCalendar
	h.m.view = viewCalendar

	h.press(t, "?")

	if !h.m.help {
		t.Error("? did not open help from the calendar")
	}

	h.press(t, "j")
	h.press(t, "tab")

	if h.m.section != sectionNotes {
		t.Errorf("tab from the calendar gave section %v, want notes", h.m.section)
	}
}

func TestNotesWriteReadDelete(t *testing.T) {
	h := newHarness(t)

	// Into the Notes section: tab twice from mail.
	h.press(t, "tab")
	h.press(t, "tab")

	if h.m.section != sectionNotes || h.m.view != viewNotes {
		t.Fatalf("section %v view %v, want notes", h.m.section, h.m.view)
	}

	// A new note: n opens the editor seeded with a heading marker.
	h.press(t, "n")

	if h.m.view != viewNoteEdit || h.m.noteEd == nil {
		t.Fatal("n did not open the note editor")
	}

	h.typeText(t, "Shopping list")
	h.m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	h.m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	h.typeText(t, "- milk")

	h.press(t, "ctrl+d")

	if h.m.view != viewNotes {
		t.Fatalf("saving gave view %v, want viewNotes", h.m.view)
	}

	if len(h.m.notes) != 1 || h.m.notes[0].Title != "Shopping list" {
		t.Fatalf("saved notes = %+v, want one titled Shopping list", h.m.notes)
	}

	// The editor keeps letters to itself: typing must not trigger actions.
	h.press(t, "e")

	if h.m.view != viewNoteEdit {
		t.Fatal("e did not reopen the editor")
	}

	h.typeText(t, "q")

	if h.m.quitting {
		t.Fatal("typing q inside the editor quit the app")
	}

	h.press(t, "esc")

	// Reading: enter wraps the file into the reader.
	h.press(t, "enter")

	if h.m.view != viewNoteRead {
		t.Fatalf("enter gave view %v, want viewNoteRead", h.m.view)
	}

	if len(h.m.noteLines) == 0 || !strings.Contains(strings.Join(h.m.noteLines, "\n"), "milk") {
		t.Fatalf("reader lines %q missing the body", h.m.noteLines)
	}

	h.press(t, "esc")

	if h.m.view != viewNotes {
		t.Fatalf("esc gave view %v, want viewNotes", h.m.view)
	}

	// Deleting empties the folder again.
	h.press(t, "D")

	if len(h.m.notes) != 0 {
		t.Fatalf("after delete notes = %+v, want none", h.m.notes)
	}
}
