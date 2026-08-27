package tui

import (
	"fmt"
	"net/mail"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/mhrsntrk/frankenstein-cli/internal/config"
	fmail "github.com/mhrsntrk/frankenstein-cli/internal/mail"
	"github.com/mhrsntrk/frankenstein-cli/internal/screener"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

		if m.compose != nil {
			m.compose.body.SetWidth(maxInt(20, m.width-4))
			m.compose.body.SetHeight(maxInt(3, m.height-14))
		}

		if m.view == viewMessage {
			m.rewrapBody()
		}

		return m, nil

	case boxesMsg:
		m.boxes = msg
		m.quickBoxes = pickQuickBoxes(msg, m.cfg.Screener)

		return m, nil

	case chromeMsg:
		m.pending, m.account = msg.pending, msg.account

		return m, nil

	case convsMsg:
		m.convs = msg
		m.convIdx, m.convFirst = 0, 0
		m.loading = false

		return m, nil

	case sendersMsg:
		m.senders = msg
		m.senderIdx = 0
		m.loading = false

		return m, nil

	case journalMsg:
		m.journal = msg
		m.extraIdx = 0

		return m, nil

	case threadMsg:
		m.thread = fmail.Thread(msg)
		m.msgIdx = maxInt(0, len(m.thread.Messages)-1)
		m.view = viewThread
		m.loading = false

		return m, nil

	case bodyMsg:
		m.body = fmail.Body(msg)
		m.bodyTop = 0
		m.view = viewMessage
		m.loading = false
		m.rewrapBody()

		return m, nil

	case eventsMsg:
		m.events = msg

		return m, nil

	case syncedMsg:
		m.loading = false
		m.status = fmt.Sprintf("synced %d conversations", msg.Conversations)

		cmds := []tea.Cmd{m.loadBoxes(), m.loadChrome()}
		if m.view == viewThreads {
			cmds = append(cmds, m.loadConvs(m.box.ID, m.filter.Value()))
		}

		return m, tea.Batch(cmds...)

	case actionMsg:
		m.loading = false
		m.notify(msg.note)
		m.selected = map[string]bool{}

		var cmds []tea.Cmd

		if msg.reload {
			cmds = append(cmds, m.loadConvs(m.box.ID, m.filter.Value()), m.loadBoxes(), m.loadChrome())
		}

		if msg.rescreen {
			cmds = append(cmds, m.loadSenders())
		}

		// A compose that succeeded should close itself.
		if m.view == viewCompose && (msg.note == "sent" || msg.note == "draft saved") {
			m.compose = nil
			m.pop()
		}

		return m, tea.Batch(cmds...)

	case errMsg:
		m.err = msg.err
		m.loading = false

		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Text entry owns the keyboard while it is open, or typing a subject would
	// trigger every single-letter action.
	if m.compose != nil && m.view == viewCompose {
		return m.handleComposeKey(msg)
	}

	if m.view == viewMovePicker {
		return m.handleMoveKey(msg)
	}

	if m.filtering {
		switch msg.Type {
		case tea.KeyEnter:
			m.filtering = false
			m.filter.Blur()
			m.loading = true

			return m, m.loadConvs(m.box.ID, m.filter.Value())

		case tea.KeyEsc:
			m.filtering = false
			m.filter.SetValue("")
			m.filter.Blur()

			return m, m.loadConvs(m.box.ID, "")
		}

		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)

		return m, cmd
	}

	// Any keypress clears the last flash, so notices do not linger.
	m.flash = ""
	m.err = nil

	if m.help {
		m.help = false

		return m, nil
	}

	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true

		return m, tea.Quit

	case "?":
		m.help = true

		return m, nil

	case "tab":
		m.section = (m.section + 1) % 3

		return m, m.enterSection()

	case "shift+tab":
		m.section = (m.section + 2) % 3

		return m, m.enterSection()

	case "esc", "backspace", "h", "left":
		return m.goBack()

	case "enter", "l", "right":
		return m.drillIn()

	case "j", "down":
		m.moveCursor(1)

		return m, nil

	case "k", "up":
		m.moveCursor(-1)

		return m, nil

	case "g", "home":
		m.setCursor(0)

		return m, nil

	case "G", "end":
		m.setCursor(m.itemCount() - 1)

		return m, nil

	case "ctrl+d", "pgdown":
		m.moveCursor(m.pageSize() / 2)

		return m, nil

	case "ctrl+u", "pgup":
		m.moveCursor(-m.pageSize() / 2)

		return m, nil

	case "/":
		if m.view == viewThreads {
			m.filtering = true
			m.filter.Focus()

			return m, textinput.Blink
		}

		return m, nil

	case "r", "ctrl+r":
		// r replies in a thread and reloads in a list, matching hey-cli where
		// the same key means the obvious thing for what is on screen.
		if m.view == viewThread || m.view == viewMessage {
			return m.startCompose(composeReply)
		}

		m.loading = true
		m.status = "syncing"

		return m, m.syncOnce()

	case "R":
		if m.view == viewThread || m.view == viewMessage {
			return m.startCompose(composeReplyAll)
		}

		return m, nil

	case "f":
		if m.view == viewThread || m.view == viewMessage {
			return m.startCompose(composeForward)
		}

		return m, nil

	case "c":
		return m.startCompose(composeNew)

	case " ":
		if m.view == viewThreads && m.convIdx < len(m.convs) {
			id := m.convs[m.convIdx].ID

			if m.selected[id] {
				delete(m.selected, id)
			} else {
				m.selected[id] = true
			}

			m.moveCursor(1)
		}

		return m, nil

	case "ctrl+a":
		if m.view == viewThreads {
			if len(m.selected) == len(m.convs) {
				m.selected = map[string]bool{}
			} else {
				for _, c := range m.convs {
					m.selected[c.ID] = true
				}
			}
		}

		return m, nil

	case "ctrl+s":
		m.push(viewScreener)
		m.loading = true

		return m, m.loadSenders()

	case "e":
		return m.applyMark(true)

	case "u":
		return m.applyMark(false)

	case "i":
		return m.applyScreen(screener.Imbox)

	case "d":
		return m.applyScreen(screener.Feed)

	case "p":
		return m.applyScreen(screener.PaperTrail)

	case "x":
		return m.applyScreen(screener.ScreenedOut)

	case "a":
		return m.applyMove("Archive", "archived")

	case "t":
		return m.applyMove("Trash", "trashed")

	case "!":
		return m.applyMove("Spam", "marked spam")

	case "s":
		return m.applyLabel("Starred", "starred")

	case "v":
		if m.view == viewThreads || m.view == viewThread {
			return m.openMovePicker()
		}

		return m, nil
	}

	// 1-9 jump straight to a box, which is what keeps every box one keystroke
	// away instead of a walk back up the stack.
	if k := msg.String(); len(k) == 1 && k[0] >= '1' && k[0] <= '9' {
		i := int(k[0] - '1')

		if i < len(m.quickBoxes) {
			m.section = sectionMail
			m.box = m.quickBoxes[i]
			m.view = viewThreads
			m.loading = true
			m.selected = map[string]bool{}
			m.filter.SetValue("")

			return m, m.loadConvs(m.box.ID, "")
		}
	}

	return m, nil
}

// enterSection switches between Mail, Calendar and Journal.
func (m *Model) enterSection() tea.Cmd {
	m.stack = nil

	switch m.section {
	case sectionCalendar:
		m.view = viewCalendar

		return m.loadEvents()
	case sectionJournal:
		m.view = viewJournal

		return m.loadJournal()
	default:
		m.view = viewBoxes

		return m.loadBoxes()
	}
}

// --- actions ----------------------------------------------------------------

func (m *Model) applyMark(read bool) (tea.Model, tea.Cmd) {
	ids := m.targets()
	if len(ids) == 0 {
		return m, nil
	}

	m.loading = true

	return m, m.markTargets(ids, read)
}

// applyScreen decides about the *senders* of the selected threads, which is
// the difference between filing one message and screening a person.
func (m *Model) applyScreen(d screener.Decision) (tea.Model, tea.Cmd) {
	if m.view == viewScreener {
		if m.senderIdx >= len(m.senders) {
			return m, nil
		}

		s := m.senders[m.senderIdx]
		m.loading = true

		return m, m.decideSender(s.Address, d)
	}

	ids := m.targets()
	if len(ids) == 0 {
		return m, nil
	}

	if !m.cfg.Screener.Configured() {
		m.err = fmt.Errorf("screener is not set up; run `frankenstein screener setup`")

		return m, nil
	}

	m.loading = true

	return m, m.screenTargets(ids, d)
}

// applyMove labels into a system box and unlabels the current one, so the
// thread actually leaves where it was.
func (m *Model) applyMove(boxName, note string) (tea.Model, tea.Cmd) {
	ids := m.targets()
	if len(ids) == 0 {
		return m, nil
	}

	box, ok := m.boxByName(boxName)
	if !ok {
		m.err = fmt.Errorf("no %s box in the cache; run a sync", boxName)

		return m, nil
	}

	m.loading = true

	return m, m.labelTargets(ids, box, true, note)
}

// applyLabel adds a label without removing the current box.
func (m *Model) applyLabel(boxName, note string) (tea.Model, tea.Cmd) {
	ids := m.targets()
	if len(ids) == 0 {
		return m, nil
	}

	box, ok := m.boxByName(boxName)
	if !ok {
		return m, nil
	}

	m.loading = true

	return m, m.labelTargets(ids, box, false, note)
}

func (m *Model) openMovePicker() (tea.Model, tea.Cmd) {
	if len(m.targets()) == 0 {
		return m, nil
	}

	m.moveTargets = nil

	for _, b := range m.boxes {
		// Aggregate views are not somewhere a thread can be moved to.
		if strings.HasPrefix(b.Name, "All ") || b.Kind == fmail.BoxCategory {
			continue
		}

		m.moveTargets = append(m.moveTargets, b)
	}

	m.moveIdx = 0
	m.moveFilter.SetValue("")
	m.moveFilter.Focus()
	m.push(viewMovePicker)

	return m, textinput.Blink
}

func (m *Model) handleMoveKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.moveFilter.Blur()
		m.pop()

		return m, nil

	case tea.KeyEnter:
		matches := m.moveMatches()
		if len(matches) == 0 {
			return m, nil
		}

		box := matches[minInt(m.moveIdx, len(matches)-1)]

		m.moveFilter.Blur()
		m.pop()

		ids := m.targets()
		if len(ids) == 0 {
			return m, nil
		}

		m.loading = true

		return m, m.labelTargets(ids, box, true, "moved to "+box.Name+":")

	case tea.KeyUp:
		m.moveIdx = maxInt(0, m.moveIdx-1)

		return m, nil

	case tea.KeyDown:
		m.moveIdx = minInt(len(m.moveMatches())-1, m.moveIdx+1)

		return m, nil
	}

	var cmd tea.Cmd
	m.moveFilter, cmd = m.moveFilter.Update(msg)
	m.moveIdx = 0

	return m, cmd
}

func (m *Model) moveMatches() []fmail.Box {
	q := strings.ToLower(strings.TrimSpace(m.moveFilter.Value()))
	if q == "" {
		return m.moveTargets
	}

	var out []fmail.Box

	for _, b := range m.moveTargets {
		if strings.Contains(strings.ToLower(b.Name), q) {
			out = append(out, b)
		}
	}

	return out
}

// --- compose ----------------------------------------------------------------

func (m *Model) startCompose(kind composeKind) (tea.Model, tea.Cmd) {
	to := textinput.New()
	to.Placeholder = "someone@example.com"
	to.CharLimit = 500

	cc := textinput.New()
	cc.CharLimit = 500

	subject := textinput.New()
	subject.CharLimit = 300

	body := textarea.New()
	body.SetWidth(maxInt(20, m.width-4))
	body.SetHeight(maxInt(3, m.height-14))
	body.ShowLineNumbers = false

	c := &composeState{kind: kind, to: to, cc: cc, subject: subject, body: body}

	if kind != composeNew {
		msg, ok := m.currentMessage()
		if !ok {
			m.err = fmt.Errorf("no message to reply to")

			return m, nil
		}

		subj := msg.Subject

		switch kind {
		case composeForward:
			if !strings.HasPrefix(strings.ToLower(subj), "fw:") &&
				!strings.HasPrefix(strings.ToLower(subj), "fwd:") {
				subj = "Fwd: " + subj
			}
		default:
			if !strings.HasPrefix(strings.ToLower(subj), "re:") {
				subj = "Re: " + subj
			}

			reply := msg.From
			if len(msg.ReplyTo) > 0 {
				reply = msg.ReplyTo[0]
			}

			c.to.SetValue(reply.Address)
			c.inReplyTo = msg.ID
		}

		if kind == composeReplyAll {
			var others []string

			for _, a := range append(append([]fmail.Address{}, msg.To...), msg.CC...) {
				if !strings.EqualFold(a.Address, m.account) && a.Address != "" {
					others = append(others, a.Address)
				}
			}

			c.cc.SetValue(strings.Join(others, ", "))
		}

		c.subject.SetValue(subj)

		// Quoting the body needs it decrypted, which it is whenever the user
		// has actually read the message.
		if m.body.MessageID == msg.ID {
			c.body.SetValue("\n\n" + quote(msg.From.Display(), msg.Time.Format("2 Jan 2006 15:04"), renderBody(m.body)))
			c.body.CursorStart()
		}
	}

	c.to.Focus()

	m.compose = c
	m.push(viewCompose)

	return m, textinput.Blink
}

// currentMessage is the message an action refers to: the one being read, or
// the one highlighted in a thread.
func (m *Model) currentMessage() (fmail.Message, bool) {
	if len(m.thread.Messages) == 0 {
		return fmail.Message{}, false
	}

	i := minInt(m.msgIdx, len(m.thread.Messages)-1)

	return m.thread.Messages[i], true
}

func (m *Model) handleComposeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := m.compose

	switch msg.String() {
	case "esc":
		m.compose = nil
		m.pop()

		return m, nil

	case "ctrl+s":
		m.loading = true

		return m, m.sendCompose(false)

	case "ctrl+d":
		// Sending is deliberately a two-key chord rather than enter: enter is
		// a newline in the body, and an accidental send cannot be undone.
		m.loading = true

		return m, m.sendCompose(true)

	case "tab":
		c.field = (c.field + 1) % 4
		m.focusComposeField()

		return m, nil

	case "shift+tab":
		c.field = (c.field + 3) % 4
		m.focusComposeField()

		return m, nil
	}

	var cmd tea.Cmd

	switch c.field {
	case 0:
		c.to, cmd = c.to.Update(msg)
	case 1:
		c.cc, cmd = c.cc.Update(msg)
	case 2:
		c.subject, cmd = c.subject.Update(msg)
	default:
		c.body, cmd = c.body.Update(msg)
	}

	return m, cmd
}

func (m *Model) focusComposeField() {
	c := m.compose

	c.to.Blur()
	c.cc.Blur()
	c.subject.Blur()
	c.body.Blur()

	switch c.field {
	case 0:
		c.to.Focus()
	case 1:
		c.cc.Focus()
	case 2:
		c.subject.Focus()
	default:
		c.body.Focus()
	}
}

func quote(who, when, body string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "On %s, %s wrote:\n", when, who)

	for _, line := range strings.Split(body, "\n") {
		b.WriteString("> ")
		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String()
}

// parseAddresses accepts a comma-separated list, with or without display
// names.
func parseAddresses(in string) ([]fmail.Address, error) {
	var out []fmail.Address

	for _, part := range strings.Split(in, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		a, err := mail.ParseAddress(part)
		if err != nil {
			if !strings.Contains(part, "@") {
				return nil, fmt.Errorf("not an email address: %q", part)
			}

			out = append(out, fmail.Address{Address: part})

			continue
		}

		out = append(out, fmail.Address{Name: a.Name, Address: a.Address})
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("no recipients")
	}

	return out, nil
}

// --- navigation -------------------------------------------------------------

func (m *Model) goBack() (tea.Model, tea.Cmd) {
	switch m.view {
	case viewMessage:
		m.view = viewThread
	case viewThread:
		m.view = viewThreads
	case viewThreads:
		m.view = viewBoxes
		m.filter.SetValue("")
		m.selected = map[string]bool{}
	case viewScreener, viewCompose, viewMovePicker:
		m.compose = nil
		m.pop()
	}

	return m, nil
}

func (m *Model) drillIn() (tea.Model, tea.Cmd) {
	switch m.view {
	case viewBoxes:
		if len(m.boxes) == 0 {
			return m, nil
		}

		m.box = m.boxes[m.boxIdx]
		m.view = viewThreads
		m.loading = true

		return m, m.loadConvs(m.box.ID, "")

	case viewThreads:
		if len(m.convs) == 0 {
			return m, nil
		}

		m.loading = true

		return m, m.loadThread(m.convs[m.convIdx].ID)

	case viewThread:
		if len(m.thread.Messages) == 0 {
			return m, nil
		}

		m.loading = true

		return m, m.loadBody(m.thread.Messages[m.msgIdx].ID)

	case viewJournal:
		return m, nil
	}

	return m, nil
}

func (m *Model) itemCount() int {
	switch m.view {
	case viewBoxes:
		return len(m.boxes)
	case viewThreads:
		return len(m.convs)
	case viewThread:
		return len(m.thread.Messages)
	case viewMessage:
		return len(m.bodyLines)
	case viewScreener:
		return len(m.senders)
	case viewCalendar:
		return len(m.events)
	case viewJournal:
		return len(m.journal)
	}

	return 0
}

func (m *Model) pageSize() int {
	n := m.height - m.chromeHeight()
	if n < 1 {
		return 1
	}

	return n
}

// chromeHeight is how many rows the header and footer take, so the list knows
// what is left for it.
func (m *Model) chromeHeight() int {
	h := 2 // title bar, footer

	if m.view == viewBoxes || m.view == viewThreads {
		h++ // nav row

		if len(m.quickBoxes) > 0 {
			h++
		}

		if m.pending > 0 {
			h++
		}
	}

	if m.view == viewBoxes && len(m.events) > 0 {
		h++
	}

	return h + 1
}

func (m *Model) moveCursor(delta int) {
	if m.view == viewMessage {
		m.bodyTop = clamp(m.bodyTop+delta, 0, maxInt(0, len(m.bodyLines)-m.pageSize()))

		return
	}

	m.setCursor(m.cursor() + delta)
}

func (m *Model) cursor() int {
	switch m.view {
	case viewBoxes:
		return m.boxIdx
	case viewThreads:
		return m.convIdx
	case viewThread:
		return m.msgIdx
	case viewScreener:
		return m.senderIdx
	case viewCalendar, viewJournal:
		return m.extraIdx
	}

	return 0
}

func (m *Model) setCursor(i int) {
	n := m.itemCount()
	if n == 0 {
		return
	}

	i = clamp(i, 0, n-1)

	switch m.view {
	case viewBoxes:
		m.boxIdx = i
		m.boxFirst = scrollTo(m.boxFirst, i, m.pageSize())
	case viewThreads:
		m.convIdx = i
		m.convFirst = scrollTo(m.convFirst, i, m.pageSize())
	case viewThread:
		m.msgIdx = i
	case viewScreener:
		m.senderIdx = i
	case viewCalendar, viewJournal:
		m.extraIdx = i
	}
}

// pickQuickBoxes chooses what the number keys bind to: the screener's boxes
// first, then the system boxes a person actually reads.
func pickQuickBoxes(boxes []fmail.Box, sc config.ScreenerConfig) []fmail.Box {
	byID := make(map[string]fmail.Box, len(boxes))
	for _, b := range boxes {
		byID[b.ID] = b
	}

	var out []fmail.Box

	for _, id := range []string{sc.ImboxID, sc.FeedID, sc.PaperTrailID, sc.ScreenedOutID} {
		if b, ok := byID[id]; ok && id != "" {
			out = append(out, b)
		}
	}

	for _, name := range []string{"Inbox", "Starred", "Archive", "Sent", "Drafts"} {
		for _, b := range boxes {
			if b.Kind == fmail.BoxSystem && b.Name == name {
				out = append(out, b)

				break
			}
		}
	}

	if len(out) > 9 {
		out = out[:9]
	}

	return out
}
