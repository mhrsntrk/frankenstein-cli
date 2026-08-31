package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Mouse support.
//
// The rule for a click is deliberately two-step: the first click on a row moves
// the cursor there, and a click on the row already under the cursor opens it.
// A single click that opened straight away would make a misplaced click read a
// message and mark it seen, which is not undoable.

// handleMouse routes a mouse event to whatever is under it.
func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// The composer floats on top of everything, so it sees the event first: a
	// click inside it lands on its buttons and fields, and while it is open
	// (not minimized) the screen behind it is inert, so a stray click cannot
	// act on a list the writer can barely see.
	if m.compose != nil {
		if handled, model, cmd := m.composerMouse(msg); handled {
			return model, cmd
		}

		if m.view == viewCompose {
			return m, nil
		}
	}

	// Action and Button rather than the deprecated Type: a wheel event and a
	// click differ by action, and reading only the button would treat a scroll
	// as a press.
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		return m.scroll(-3, msg.X)

	case tea.MouseButtonWheelDown:
		return m.scroll(3, msg.X)

	case tea.MouseButtonLeft:
		if msg.Action == tea.MouseActionPress {
			return m.click(msg.X, msg.Y)
		}
	}

	return m, nil
}

// composerMouse handles an event over the popup. The first return says the
// event was inside it and is spent, whatever it did.
func (m *Model) composerMouse(msg tea.MouseMsg) (bool, tea.Model, tea.Cmd) {
	lay := composerPlace(m.width, m.height, m.composerMin, m.composerMax)

	if msg.X < lay.x || msg.X >= lay.x+lay.w || msg.Y < lay.y || msg.Y >= lay.y+lay.h {
		return false, m, nil
	}

	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return true, m, nil
	}

	// The same renderer that drew the popup says what is under the click, so
	// the hit map cannot drift from what is on screen.
	_, regions := composerPopup(m.compose, lay, m.composerMin, m.account)

	id, ok := hit(regions, msg.X-lay.x, msg.Y-lay.y)
	if !ok {
		return true, m, nil
	}

	switch id {
	case "close":
		m.compose = nil
		m.composerMin, m.composerMax = false, false

		if m.view == viewCompose {
			m.pop()
		}

	case "minimize":
		// Parking the popup hands the keyboard and the mouse back to the
		// screen behind it; the draft stays where it can be seen.
		m.composerMin = true

		if m.view == viewCompose {
			m.pop()
		}

	case "restore":
		m.composerMin = false
		m.push(viewCompose)
		m.sizeCompose()

	case "maximize":
		if m.composerMin {
			m.composerMin = false
			m.composerMax = true
			m.push(viewCompose)
		} else {
			m.composerMax = !m.composerMax
		}

		m.sizeCompose()

	case "send":
		// A send already in flight owns the draft; a second click while it
		// runs would deliver the mail twice. The composer's own flag, not the
		// model's: a background sync must not make the button dead.
		if m.compose.sending {
			return true, m, nil
		}

		m.compose.sending = true
		m.loading = true

		return true, m, m.sendCompose(true)

	case "discard":
		m.compose = nil
		m.composerMin, m.composerMax = false, false

		if m.view == viewCompose {
			m.pop()
		}

	case "togglecc":
		m.compose.field = 1
		m.focusComposeField()
		m.sizeCompose()

	case "field:to":
		m.compose.field = 0
		m.focusComposeField()

	case "field:cc":
		m.compose.field = 1
		m.focusComposeField()

	case "field:bcc":
		m.compose.field = 2
		m.focusComposeField()

	case "field:subject":
		m.compose.field = 3
		m.focusComposeField()

	case "field:body":
		m.compose.field = 4
		m.focusComposeField()
	}

	return true, m, nil
}

// scroll moves the view under the pointer without moving the cursor, which is
// what a wheel does everywhere else.
func (m *Model) scroll(delta, x int) (tea.Model, tea.Cmd) {
	if m.splitMail() && mailSplitView(m.view) {
		return m.scrollSplit(delta, x)
	}

	switch m.view {
	case viewThreads:
		// The list spends two rows on a conversation, so a three-row wheel
		// notch moves two of them.
		m.list.scrollBy(delta * 2 / 3)
	case viewMessage:
		m.bodyTop = clamp(m.bodyTop+delta, 0, maxInt(0, len(m.bodyLines)-m.bodyViewRows()))
	default:
		m.moveCursor(delta)
	}

	return m, nil
}

// scrollSplit scrolls whichever pane the pointer is over.
func (m *Model) scrollSplit(delta, x int) (tea.Model, tea.Cmd) {
	col := x - len(m.gutter())
	listW, gap, _ := m.paneGeom()
	h := maxInt(1, m.pageSize())

	if col < listW {
		// The list pane spends two rows on a conversation, so a three-row
		// wheel notch moves two conversations.
		m.list.scrollBy(delta * 2 / 3)

		return m, nil
	}

	if col >= listW+gap {
		m.bodyTop = clamp(m.bodyTop+delta, 0,
			maxInt(0, len(m.bodyLines)-maxInt(1, threadBodyRows(m.thread, m.msgIdx, h))))
	}

	return m, nil
}

// click acts on whatever is at a screen position.
func (m *Model) click(x, y int) (tea.Model, tea.Cmd) {
	m.flash = ""
	m.err = nil

	// Everything is drawn into a centred column, so a click has to be
	// translated back out of it before any of the layout maths applies.
	col := x - len(m.gutter())
	if col < 0 || col > m.contentWidth() {
		return m, nil
	}

	rows := m.chromeRows()

	// The header rows, top to bottom.
	switch {
	case y == rows.nav && rows.nav >= 0:
		return m.clickNav(col)
	case y == rows.subnav && rows.subnav >= 0:
		return m.clickSubnav(col)
	case y == rows.boxes && rows.boxes >= 0:
		return m.clickBoxes(col)
	case y == rows.categories && rows.categories >= 0:
		return m.clickCategories(col)
	}

	if y < rows.body {
		return m, nil
	}

	return m.clickBody(col, y-rows.body)
}

// chromeRows is which screen line each part of the header occupies, or -1 when
// that part is not being drawn.
//
// It is derived by rendering the header and counting, rather than by adding up
// what should be there: the header sheds rows on a short terminal, and a click
// map that disagreed with what is on screen would act on the wrong thing.
type chromeRows struct {
	nav int

	// subnav is the calendar's Day/Week/Year row, drawn only in the
	// calendar view.
	subnav int

	boxes int

	// categories is the inbox category row, drawn only under the box bar and
	// only where a category means anything.
	categories int

	body int
}

func (m *Model) chromeRows() chromeRows {
	out := chromeRows{nav: -1, subnav: -1, boxes: -1, categories: -1}

	lines := strings.Split(m.header(), "\n")

	row := 0

	// Line 0 is always the title rule.
	row++

	if m.chromeLevel < chromeMinimal {
		out.nav = row
		row++
	}

	// The rule naming the box.
	row++

	// The calendar's sub-nav sits under that rule, exactly when header()
	// draws it.
	if m.view == viewCalendar && m.chromeLevel < chromeNoBoxBar {
		out.subnav = row
		row++
	}

	if m.mailChrome() {
		if m.chromeLevel < chromeNoBoxBar && len(m.quickBoxes) > 0 {
			out.boxes = row
			row++
		}

		if m.chromeLevel < chromeNoBoxBar && m.categoryRowShown() {
			out.categories = row
			row++
		}
	}

	// The header ends with a newline, so the body starts after it plus the
	// blank separator View writes.
	out.body = maxInt(row, len(lines)-1) + 1

	return out
}

// clickNav switches section from the Mail / Calendar / Notes row.
func (m *Model) clickNav(col int) (tea.Model, tea.Cmd) {
	labels := []string{"Mail", "Calendar", "Notes"}

	if i, ok := hitLabel(m.header(), 1, labels, col); ok {
		m.section = section(i)

		return m, m.enterSection()
	}

	return m, nil
}

// clickSubnav switches the calendar grid from its Day/Week/Year row, doing
// what the 1, 2 and 3 keys do: pick the grid and come back to today.
func (m *Model) clickSubnav(col int) (tea.Model, tea.Cmd) {
	labels := []string{"Day", "Week", "Year"}

	i, ok := hitLabel(m.header(), m.chromeRows().subnav, labels, col)
	if !ok {
		return m, nil
	}

	m.calView, m.calOffset = calendarView(i), 0

	return m, m.loadEvents()
}

// clickBoxes jumps to a box from the numbered switcher.
func (m *Model) clickBoxes(col int) (tea.Model, tea.Cmd) {
	labels := make([]string, 0, len(m.quickBoxes))
	for _, b := range m.quickBoxes {
		labels = append(labels, b.Name)
	}

	i, ok := hitLabel(m.header(), m.chromeRows().boxes, labels, col)
	if !ok {
		return m, nil
	}

	m.section = sectionMail
	m.box = m.quickBoxes[i]
	m.view = viewThreads
	m.loading = true
	m.list.clearSelection()
	m.filter.SetValue("")
	m.resetMailContext()

	return m, m.loadConvs(m.box.ID, "")
}

// clickCategories narrows the Inbox from the sub-row under the bar, doing what
// [ and ] do. The leading All entry is index 0, so the category it picks is one
// place further along than the entry that was hit.
func (m *Model) clickCategories(col int) (tea.Model, tea.Cmd) {
	cats := m.categoryBoxes()

	labels := make([]string, 0, len(cats)+1)
	labels = append(labels, categoryAllLabel)

	for _, b := range cats {
		labels = append(labels, b.Name)
	}

	i, ok := hitLabel(m.header(), m.chromeRows().categories, labels, col)
	if !ok {
		return m, nil
	}

	return m.selectCategory(i - 1)
}

// hitLabel finds which of labels was clicked on a rendered row.
//
// The row is searched as plain text because the rendered one is full of escape
// sequences, and a column counted through those would be meaningless.
func hitLabel(header string, row int, labels []string, col int) (int, bool) {
	lines := strings.Split(header, "\n")
	if row < 0 || row >= len(lines) {
		return 0, false
	}

	plain := stripANSI(lines[row])

	for i, label := range labels {
		start := strings.Index(plain, label)
		if start < 0 {
			continue
		}

		// A digit shortcut sits immediately before the label, so the click
		// target starts two columns earlier.
		from := maxInt(0, start-2)
		if col >= from && col < start+len(label) {
			return i, true
		}
	}

	return 0, false
}

// clickBody acts on a click inside the view under the header.
func (m *Model) clickBody(col, line int) (tea.Model, tea.Cmd) {
	if m.splitMail() && mailSplitView(m.view) {
		return m.clickSplit(col, line)
	}

	switch m.view {
	case viewThreads:
		i, ok := m.list.indexAtLine(line)
		if !ok {
			return m, nil
		}

		// Clicking the row already under the cursor opens it; clicking any
		// other row moves there first.
		if i == m.list.cursor {
			return m.drillIn()
		}

		m.list.setCursor(i)

		return m, nil

	case viewBoxes, viewThread, viewNotes:
		// The calendar is deliberately absent from this list: it is a grid,
		// not one item per line, so mapping a screen row onto extraIdx only
		// scrambled the selection. Its clickable surface is the Day/Week/Year
		// sub-nav in the header, and the wheel still moves the cursor.
		target := m.cursor() + (line - m.cursorLine())
		if target == m.cursor() {
			return m.drillIn()
		}

		m.setCursor(target)

		return m, nil
	}

	return m, nil
}

// clickSplit acts on a click inside the two-pane mail screen. A click in the
// list opens the conversation straight away, the way Proton's list does: the
// reading pane makes a misplaced click cheap to recover from, unlike the old
// full-screen drill-down the two-step click protected.
func (m *Model) clickSplit(col, line int) (tea.Model, tea.Cmd) {
	listW, gap, threadW := m.paneGeom()
	h := maxInt(1, m.pageSize())

	if col < listW {
		i := m.list.top + line/2

		c, ok := m.list.at(i)
		if line < 0 || line/2 >= listPageSize(h) || !ok {
			return m, nil
		}

		m.list.setCursor(i)
		m.view = viewThreads
		m.loading = true

		return m, m.loadThread(c.ID)
	}

	if col < listW+gap || m.thread.Conversation.ID == "" {
		return m, nil
	}

	// The same renderer that drew the pane says what is under the click.
	_, regions := threadPane(m.thread, m.msgIdx, m.threadBodyRef(), m.bodyTop, threadW, h)

	id, ok := hit(regions, col-listW-gap, line)
	if !ok {
		return m, nil
	}

	switch id {
	case "reply":
		return m.startCompose(composeReply)
	case "replyall":
		return m.startCompose(composeReplyAll)
	case "forward":
		return m.startCompose(composeForward)
	case "star":
		return m.applyLabel("Starred", "starred")
	}

	var i int
	if n, err := fmt.Sscanf(id, "msg:%d", &i); n == 1 && err == nil {
		if i == m.msgIdx {
			return m, nil
		}

		m.msgIdx = i
		m.bodyTop = 0
		m.view = viewThread
		m.loading = true

		return m, m.loadBody(m.thread.Messages[i].ID)
	}

	return m, nil
}

// cursorLine is which body line the cursor is drawn on, for the views that
// render one item per line.
func (m *Model) cursorLine() int {
	switch m.view {
	case viewBoxes:
		return m.cursor() - m.boxFirst
	case viewNotes:
		return m.cursor() - m.notesWindowStart()
	default:
		return m.cursor()
	}
}

// stripANSI removes escape sequences so columns can be counted.
func stripANSI(s string) string {
	var b strings.Builder

	esc := false

	for _, r := range s {
		switch {
		case r == '\x1b':
			esc = true
		case esc && r == 'm':
			esc = false
		case !esc:
			b.WriteRune(r)
		}
	}

	return b.String()
}
