package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
	"github.com/mhrsntrk/frankenstein-cli/internal/screener"
)

// Payloads a hostile sender could put in a name, a subject or a body. Each is
// distinctive enough that finding any fragment of it in rendered output means
// the sanitizer was bypassed, and none overlaps the SGR sequences the styles
// legitimately emit.
const (
	hostileOSC = "\x1b]0;EVILTITLE\x07"
	hostileCSI = "\x1b[2J\x1b[999;888H"
)

// assertInert fails if any part of the hostile payloads survived into a
// rendered block: the raw sequences, the bell byte, or the OSC's inner text
// as visible debris.
func assertInert(t *testing.T, where, block string) {
	t.Helper()

	for _, bad := range []string{"\x1b]", "\x07", "\x1b[2J", "999;888", "EVILTITLE"} {
		if strings.Contains(block, bad) {
			t.Errorf("%s: hostile fragment %q survived into the render", where, bad)
		}
	}
}

func TestHostileInputStaysInertInSplitPanes(t *testing.T) {
	now := time.Now()

	convs := []mail.Conversation{{
		ID:      "c0",
		Subject: "invoice " + hostileCSI + " attached",
		Senders: []mail.Address{
			{Name: hostileOSC + "PayPal", Address: "evil@example.com"},
			{Name: "Second " + hostileCSI, Address: "two@example.com"},
		},
		NumMessages: 2,
		Time:        now,
	}}

	block, _ := listPane(convs, 0, 0, nil, 60, 10)
	assertInert(t, "listPane", block)

	th := mail.Thread{
		Conversation: mail.Conversation{ID: "c0", Subject: "s"},
		Messages: []mail.Message{
			{
				ID:   "m0",
				From: mail.Address{Name: hostileOSC + "Collapsed", Address: "a@example.com"},
				Time: now.Add(-time.Hour),
			},
			{
				ID:   "m1",
				From: mail.Address{Name: "Expanded" + hostileCSI, Address: "evil" + hostileOSC + "@example.com"},
				To:   []mail.Address{{Name: hostileCSI + "You", Address: "me@example.com"}},
				Time: now,
			},
		},
	}

	block, _ = threadPane(th, 1, []string{"body"}, 0, 60, 20)
	assertInert(t, "threadPane", block)
}

func TestHostileInputStaysInertInReader(t *testing.T) {
	h := newHarness(t)
	h.m.width = 90 // single pane, so the From/To header block renders too

	h.m.thread = mail.Thread{
		Conversation: mail.Conversation{ID: "c1", Subject: "s"},
		Messages: []mail.Message{{
			ID:   "m1",
			From: mail.Address{Name: hostileOSC + "Ada", Address: "ada@example.com"},
			To:   []mail.Address{{Name: hostileCSI + "Me", Address: "me@example.com"}},
			Time: time.Now(),
		}},
	}

	// The body hides a second escape behind an HTML entity, which only exists
	// after the decode: sanitizing before it would miss the payload.
	h.m.body = mail.Body{
		MessageID: "m1",
		MIMEType:  "text/html",
		Content:   "<p>hello " + hostileCSI + " there</p><p>&#27;[31mdecoded</p>",
		Attachments: []mail.Attachment{
			{ID: "a1", Name: "invoice" + hostileOSC + ".pdf"},
		},
	}

	h.m.rewrapBody()

	joined := strings.Join(h.m.bodyLines, "\n")
	assertInert(t, "rewrapBody", joined)

	if strings.Contains(joined, "\x1b[31m") {
		t.Error("an entity-encoded escape survived the decode-then-sanitize order")
	}

	if !strings.Contains(joined, "decoded") {
		t.Error("sanitizing removed legitimate body text")
	}
}

func TestScreenerViewIsInertAndShowsTheRealAddress(t *testing.T) {
	h := newHarness(t)

	// A display name crafted to read as a harmless address, long enough that
	// naive truncation would have pushed the real one off screen.
	h.m.senders = []screener.Sender{{
		Name:         "friend@bank.example <friend@bank.example>" + hostileCSI + strings.Repeat(" trusted", 10),
		Address:      "evil@attacker.example",
		MessageCount: 3,
		LastSeen:     time.Now(),
	}}
	h.m.view = viewScreener

	out := stripANSI(h.m.screenerView())
	assertInert(t, "screenerView", h.m.screenerView())

	if !strings.Contains(out, "evil@attacker.example") {
		t.Error("the real address is not on screen untruncated")
	}
}

func TestScreenerClickMapsToTheRowUnderThePointer(t *testing.T) {
	h := newHarness(t)

	if _, err := h.m.screener.Observe(context.Background()); err != nil {
		t.Fatal(err)
	}

	h.press(t, "ctrl+s")

	if h.m.view != viewScreener || len(h.m.senders) < 3 {
		t.Fatalf("screener has %d senders in view %v", len(h.m.senders), h.m.view)
	}

	_ = h.m.View()

	rows := h.m.chromeRows()
	gutter := len(h.m.gutter())

	// Two header lines precede the first sender row; the window starts at 0
	// while the cursor is at the top.
	h.clickAt(t, gutter+5, rows.body+screenerHeaderLines+2)

	if h.m.senderIdx != 2 {
		t.Errorf("clicking the third sender row moved the cursor to %d, want 2", h.m.senderIdx)
	}
}

func TestSenderCursorClampsWhenTheQueueShrinks(t *testing.T) {
	h := newHarness(t)

	h.m.senders = []screener.Sender{
		{Address: "a@x"}, {Address: "b@x"}, {Address: "c@x"}, {Address: "d@x"},
	}
	h.m.senderIdx = 3

	h.m.Update(sendersMsg([]screener.Sender{{Address: "a@x"}, {Address: "b@x"}}))

	if h.m.senderIdx != 1 {
		t.Errorf("senderIdx = %d after the queue shrank to 2, want 1", h.m.senderIdx)
	}
}

func TestDoubleSendFiresOnce(t *testing.T) {
	h := newHarness(t)

	h.press(t, "c")
	h.m.compose.to.SetValue("someone@example.com")
	h.m.compose.subject.SetValue("hi")

	// The first chord starts the send; press would drain it, so Update is
	// called raw to leave the send in flight.
	_, cmd := h.m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if cmd == nil || !h.m.loading {
		t.Fatal("the first ctrl+d did not start a send")
	}

	// A second chord and a click on Send while it runs are both inert.
	if _, again := h.m.Update(tea.KeyMsg{Type: tea.KeyCtrlD}); again != nil {
		t.Error("a second ctrl+d during the send returned a command")
	}

	lay := composerPlace(h.m.width, h.m.height, false, false)
	h.m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
		X: lay.x + lay.w - 6, Y: lay.y + lay.h - 2,
	})

	h.drain(t, cmd)

	sends := 0

	for _, c := range h.p.Calls {
		if c == "Send" {
			sends++
		}
	}

	if sends != 1 {
		t.Errorf("the provider saw %d sends, want 1", sends)
	}
}

func TestEscMinimizesTheComposerInsteadOfDiscarding(t *testing.T) {
	h := newHarness(t)

	h.press(t, "c")
	h.m.compose.body.SetValue("half-written")

	h.press(t, "esc")

	if h.m.compose == nil {
		t.Fatal("esc discarded the draft")
	}

	if !h.m.composerMin || h.m.view == viewCompose {
		t.Fatalf("esc did not park the composer: min=%v view=%v", h.m.composerMin, h.m.view)
	}

	// c restores the parked draft rather than opening a fresh one over it.
	h.press(t, "c")

	if h.m.compose == nil || h.m.compose.body.Value() != "half-written" {
		t.Fatal("the parked draft was not restored")
	}
}

func TestSavingADraftKeepsTheComposerOpenAndItsID(t *testing.T) {
	h := newHarness(t)

	h.press(t, "c")
	h.m.compose.to.SetValue("someone@example.com")
	h.m.compose.body.SetValue("draft text")

	h.press(t, "ctrl+s")

	if h.m.compose == nil || h.m.view != viewCompose {
		t.Fatal("saving a draft closed the composer")
	}

	if h.m.compose.draftID == "" {
		t.Fatal("the saved draft's ID was not kept for the next save")
	}

	if h.m.flash != "draft saved" {
		t.Errorf("flash = %q, want draft saved", h.m.flash)
	}
}

func TestSendRefusedWhenSubjectAndBodyAreEmpty(t *testing.T) {
	h := newHarness(t)

	h.press(t, "c")
	h.m.compose.to.SetValue("someone@example.com")

	h.press(t, "ctrl+d")

	if h.m.err == nil {
		t.Fatal("an empty send was accepted")
	}

	if h.m.compose == nil {
		t.Error("the refused send closed the composer")
	}
}

func TestStaleConvsResponseIsDropped(t *testing.T) {
	h := newHarness(t)

	stale := convsMsg{convs: nil, gen: h.m.convsGen}

	// A newer request supersedes the captured one before it lands.
	_ = h.m.loadConvs(h.m.box.ID, "")

	h.m.Update(stale)

	if h.m.list.Len() != 3 {
		t.Errorf("a stale conversations response was applied: %d rows", h.m.list.Len())
	}

	h.m.Update(convsMsg{convs: nil, gen: h.m.convsGen})

	if h.m.list.Len() != 0 {
		t.Error("the current-generation response was not applied")
	}
}

func TestLateThreadDoesNotYankTheUserOutOfAnotherSection(t *testing.T) {
	h := newHarness(t)

	cmd := h.m.loadThread("c1")
	msg := cmd()

	h.press(t, "tab") // to the calendar

	h.m.Update(msg)

	if h.m.view != viewCalendar {
		t.Errorf("a late thread switched the view to %v", h.m.view)
	}

	if h.m.thread.Conversation.ID != "c1" {
		t.Error("the late thread's data was thrown away rather than kept")
	}
}

func TestSelectionSurvivesSnippetArrival(t *testing.T) {
	h := newHarness(t)

	h.press(t, "space")
	h.press(t, "space")

	if n := h.m.list.SelectionCount(); n != 2 {
		t.Fatalf("selected %d, want 2", n)
	}

	cursor := h.m.list.Cursor()

	h.m.Update(snippetsMsg{"c1": "a preview line"})

	if n := h.m.list.SelectionCount(); n != 2 {
		t.Errorf("snippet arrival wiped the selection: %d of 2 left", n)
	}

	if h.m.list.Cursor() != cursor {
		t.Errorf("snippet arrival moved the cursor to %d, want %d", h.m.list.Cursor(), cursor)
	}
}

func TestActionReloadKeepsTheCursorNearby(t *testing.T) {
	h := newHarness(t)

	h.press(t, "j")

	if h.m.list.Cursor() != 1 {
		t.Fatalf("cursor = %d, want 1", h.m.list.Cursor())
	}

	// Mark read triggers a reload of the same listing; the cursor must not be
	// thrown back to the top by it.
	h.press(t, "e")

	if h.m.list.Cursor() != 1 {
		t.Errorf("the reload reset the cursor to %d, want 1", h.m.list.Cursor())
	}
}

func TestBoxSwitchClearsTheThreadPane(t *testing.T) {
	h := newHarness(t)
	h.m.width, h.m.height = 120, 30

	h.press(t, "enter") // open c1 with its body

	if h.m.thread.Conversation.ID != "c1" || len(h.m.bodyLines) == 0 {
		t.Fatal("the conversation did not open")
	}

	h.press(t, "2") // jump to Feed

	if h.m.thread.Conversation.ID != "" || len(h.m.bodyLines) != 0 {
		t.Error("the previous box's thread is still in the right pane")
	}
}

func TestSplitJKOverCardsLoadsTheBody(t *testing.T) {
	h := newHarness(t)
	h.m.width, h.m.height = 120, 30

	now := time.Now()

	h.p.Msgs["c1"] = []mail.Message{
		{ID: "m1", ConversationID: "c1", From: mail.Address{Address: "a@x"}, Time: now.Add(-time.Hour)},
		{ID: "m2", ConversationID: "c1", From: mail.Address{Address: "b@x"}, Time: now},
	}
	h.p.Bodies["m1"] = mail.Body{MessageID: "m1", MIMEType: "text/plain", Content: "first"}
	h.p.Bodies["m2"] = mail.Body{MessageID: "m2", MIMEType: "text/plain", Content: "second"}

	h.drain(t, h.m.loadThread("c1"))

	if h.m.body.MessageID != "m2" {
		t.Fatalf("opening the thread expanded %q, want m2", h.m.body.MessageID)
	}

	h.m.view = viewThread

	h.press(t, "k")

	if h.m.msgIdx != 0 {
		t.Fatalf("k left msgIdx at %d, want 0", h.m.msgIdx)
	}

	if h.m.body.MessageID != "m1" {
		t.Errorf("k did not load the newly selected card's body: showing %q", h.m.body.MessageID)
	}
}

func TestParseAddressesQuotedComma(t *testing.T) {
	got, err := parseAddresses(`"Doe, John" <j@example.com>, plain@example.com`)
	if err != nil {
		t.Fatalf("parseAddresses: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("parsed %d addresses, want 2: %+v", len(got), got)
	}

	if got[0].Name != "Doe, John" || got[0].Address != "j@example.com" {
		t.Errorf("first = %+v, want Doe, John <j@example.com>", got[0])
	}

	if got[1].Address != "plain@example.com" {
		t.Errorf("second = %+v, want plain@example.com", got[1])
	}
}

func TestReplyAllDedupesTheReplyTargetOutOfCC(t *testing.T) {
	h := newHarness(t)

	now := time.Now()
	msg := mail.Message{
		ID:   "m1",
		From: mail.Address{Name: "Ada", Address: "ada@example.com"},
		To: []mail.Address{
			{Address: "me@example.com"},
			{Address: "ada@example.com"}, // the reply target, already in To
			{Address: "bob@example.com"},
		},
		CC: []mail.Address{
			{Address: "bob@example.com"}, // repeat across To and CC
			{Address: "carol@example.com"},
		},
		Time: now,
	}

	h.m.thread = mail.Thread{
		Conversation: mail.Conversation{ID: "c1", Subject: "s"},
		Messages:     []mail.Message{msg},
	}
	h.m.body = mail.Body{MessageID: "m1", MIMEType: "text/plain", Content: "hello"}
	h.m.view = viewThread

	h.press(t, "R")

	if h.m.compose == nil {
		t.Fatal("reply-all did not open a compose")
	}

	if got := h.m.compose.to.Value(); got != "ada@example.com" {
		t.Fatalf("to = %q, want ada@example.com", got)
	}

	if got := h.m.compose.cc.Value(); got != "bob@example.com, carol@example.com" {
		t.Errorf("cc = %q, want bob and carol exactly once, no ada, no me", got)
	}
}

func TestForwardRecordsItsParentMessage(t *testing.T) {
	h := newHarness(t)

	h.press(t, "enter")
	h.press(t, "f")

	if h.m.compose == nil {
		t.Fatal("forward did not open a compose")
	}

	if h.m.compose.inReplyTo != "m1" {
		t.Errorf("forward's parent = %q, want m1", h.m.compose.inReplyTo)
	}
}

func TestReplyWaitsForTheBodyInsteadOfQuotingNothing(t *testing.T) {
	h := newHarness(t)
	h.m.width = 90 // single pane: opening the thread does not fetch the body

	// Straight into the thread without reading the message, then reply. The
	// first r must refuse and fetch; the drain delivers the body.
	_, cmd := h.m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	h.drain(t, cmd)

	if h.m.view != viewThread {
		t.Fatalf("enter gave view %v, want viewThread", h.m.view)
	}

	_, cmd = h.m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})

	if h.m.compose != nil {
		t.Fatal("reply opened a compose without the body loaded")
	}

	if h.m.flash != "body still loading" {
		t.Errorf("flash = %q, want body still loading", h.m.flash)
	}

	h.drain(t, cmd)

	// With the body now cached, the retry opens quoted.
	h.press(t, "r")

	if h.m.compose == nil {
		t.Fatal("the retry did not open a compose")
	}

	if !strings.Contains(h.m.compose.body.Value(), "> hello there") {
		t.Errorf("the retry's body is not quoted: %q", h.m.compose.body.Value())
	}
}

func TestWrapCountsRunesNotBytes(t *testing.T) {
	// Two four-rune Turkish words and a width they exactly fit: byte counting
	// saw the first word as eight and wrapped early.
	lines := wrap("şşşş şşşş", 9)

	if len(lines) != 1 || lines[0] != "şşşş şşşş" {
		t.Errorf("wrap counted bytes: %q", lines)
	}
}

func TestStarWithoutAStarredBoxSaysSo(t *testing.T) {
	h := newHarness(t)

	var boxes []mail.Box

	for _, b := range h.m.boxes {
		if b.Name != "Starred" {
			boxes = append(boxes, b)
		}
	}

	h.m.boxes = boxes

	h.press(t, "s")

	if h.m.err == nil {
		t.Error("starring without a Starred box failed silently")
	}
}

func TestComposerOverlayNeverWidensTheFrame(t *testing.T) {
	h := newHarness(t)

	// Thinner than the composer's minimum width, so the popup overflows and
	// must be clipped rather than allowed to shear the frame.
	h.m.width, h.m.height = 20, 12

	h.press(t, "c")

	if h.m.compose == nil {
		t.Fatal("c did not open the composer")
	}

	for i, line := range strings.Split(h.m.View(), "\n") {
		if w := visibleWidth(line); w > h.m.width {
			t.Errorf("line %d is %d columns on a %d-column terminal: %q", i, w, h.m.width, line)
		}
	}
}

// Mail opens on the mail. The box list is somewhere to go when the bar does
// not have the box you want, not the screen the tool starts on.
func TestMailOpensOnTheInboxThreadList(t *testing.T) {
	h := newHarness(t)

	// A fresh model, as Run would build it, before any box has arrived.
	h.m.view = viewThreads
	h.m.box = mail.Box{}

	if h.m.view != viewThreads {
		t.Fatalf("opening view = %v, want viewThreads", h.m.view)
	}

	// The boxes landing is what picks the box the list is of.
	h.drain(t, h.m.loadBoxes())

	if h.m.box.Name != "Inbox" {
		t.Errorf("opened on box %q, want Inbox", h.m.box.Name)
	}

	if h.m.view != viewThreads {
		t.Errorf("view after boxes = %v, want viewThreads", h.m.view)
	}

	// esc still reaches the full box list, which is where the screener's own
	// boxes live now that they are off the bar.
	h.press(t, "esc")

	if h.m.view != viewBoxes {
		t.Errorf("esc gave view %v, want viewBoxes", h.m.view)
	}
}

// Tab away and back returns to the box that was open, rather than starting
// over at the list of boxes.
func TestReturningToMailKeepsTheOpenBox(t *testing.T) {
	h := newHarness(t)

	h.press(t, "2") // Starred

	if h.m.box.Name != "Starred" {
		t.Fatalf("box = %q, want Starred", h.m.box.Name)
	}

	h.press(t, "tab") // Calendar
	h.press(t, "tab") // Notes
	h.press(t, "tab") // back to Mail

	if h.m.section != sectionMail {
		t.Fatalf("section = %v, want mail", h.m.section)
	}

	if h.m.view != viewThreads {
		t.Errorf("view = %v, want viewThreads", h.m.view)
	}

	if h.m.box.Name != "Starred" {
		t.Errorf("box = %q, want the Starred box still open", h.m.box.Name)
	}
}

// The send guard belongs to the composer, not to the model: a sync in the
// background sets m.loading, and that must not leave the composer unable to
// send the mail the user has written.
func TestBackgroundLoadingDoesNotBlockSending(t *testing.T) {
	h := newHarness(t)

	h.press(t, "c")
	h.m.compose.to.SetValue("someone@example.com")
	h.m.compose.subject.SetValue("hello")

	// Something else is in flight, as it is whenever a sync is running.
	h.m.loading = true

	_, cmd := h.m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if cmd == nil {
		t.Fatal("the send was refused while an unrelated load was in flight")
	}

	h.drain(t, cmd)

	sent := false

	for _, c := range h.p.Calls {
		if c == "Send" {
			sent = true
		}
	}

	if !sent {
		t.Error("nothing was sent")
	}
}
