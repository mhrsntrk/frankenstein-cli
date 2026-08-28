package tui

import (
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
	// Text entry keeps the mouse as well as the keyboard: a click inside a
	// compose form should not act on the list behind it.
	if m.compose != nil && m.view == viewCompose {
		return m, nil
	}

	// Action and Button rather than the deprecated Type: a wheel event and a
	// click differ by action, and reading only the button would treat a scroll
	// as a press.
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		return m.scroll(-3)

	case tea.MouseButtonWheelDown:
		return m.scroll(3)

	case tea.MouseButtonLeft:
		if msg.Action == tea.MouseActionPress {
			return m.click(msg.X, msg.Y)
		}
	}

	return m, nil
}

// scroll moves the view without moving the cursor, which is what a wheel does
// everywhere else.
func (m *Model) scroll(delta int) (tea.Model, tea.Cmd) {
	switch m.view {
	case viewThreads:
		m.list.ScrollBy(delta)
	case viewMessage:
		m.bodyTop = clamp(m.bodyTop+delta, 0, maxInt(0, len(m.bodyLines)-m.pageSize()))
	default:
		m.moveCursor(delta)
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
	case y == rows.boxes && rows.boxes >= 0:
		return m.clickBoxes(col)
	case y == rows.banner && rows.banner >= 0:
		m.push(viewScreener)
		m.loading = true

		return m, m.loadSenders()
	}

	if y < rows.body {
		return m, nil
	}

	return m.clickBody(y - rows.body)
}

// chromeRows is which screen line each part of the header occupies, or -1 when
// that part is not being drawn.
//
// It is derived by rendering the header and counting, rather than by adding up
// what should be there: the header sheds rows on a short terminal, and a click
// map that disagreed with what is on screen would act on the wrong thing.
type chromeRows struct {
	nav    int
	boxes  int
	banner int
	body   int
}

func (m *Model) chromeRows() chromeRows {
	out := chromeRows{nav: -1, boxes: -1, banner: -1}

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

	if m.view == viewBoxes || m.view == viewThreads {
		if m.chromeLevel < chromeNoBoxBar && len(m.quickBoxes) > 0 {
			out.boxes = row
			row++
		}

		if m.chromeLevel < chromeNoBanner && m.pending > 0 {
			out.banner = row
			row++
		}
	}

	// The header ends with a newline, so the body starts after it plus the
	// blank separator View writes.
	out.body = maxInt(row, len(lines)-1) + 1

	return out
}

// clickNav switches section from the Mail / Calendar / Journal row.
func (m *Model) clickNav(col int) (tea.Model, tea.Cmd) {
	labels := []string{"Mail", "Calendar", "Journal"}

	if i, ok := hitLabel(m.header(), 1, labels, col); ok {
		m.section = section(i)

		return m, m.enterSection()
	}

	return m, nil
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
	m.list.ClearSelection()
	m.filter.SetValue("")

	return m, m.loadConvs(m.box.ID, "")
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
func (m *Model) clickBody(line int) (tea.Model, tea.Cmd) {
	switch m.view {
	case viewThreads:
		i, ok := m.list.IndexAtLine(line)
		if !ok {
			return m, nil
		}

		// Clicking the row already under the cursor opens it; clicking any
		// other row moves there first.
		if i == m.list.Cursor() {
			return m.drillIn()
		}

		m.list.SetCursor(i)

		return m, nil

	case viewBoxes, viewThread, viewScreener, viewCalendar, viewJournal:
		target := m.cursor() + (line - m.cursorLine())
		if target == m.cursor() {
			return m.drillIn()
		}

		m.setCursor(target)

		return m, nil
	}

	return m, nil
}

// cursorLine is which body line the cursor is drawn on, for the views that
// render one item per line.
func (m *Model) cursorLine() int {
	switch m.view {
	case viewBoxes:
		return m.cursor() - m.boxFirst
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
