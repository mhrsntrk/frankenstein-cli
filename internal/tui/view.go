package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhrsntrk/frankenstein-cli/internal/terminal"
	"github.com/mhrsntrk/frankenstein-cli/internal/tui/heyui"
)

// Run starts the program.
func Run(m *Model) error {
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	_, err := p.Run()

	return err
}

func (m *Model) View() string {
	if m.quitting {
		return ""
	}

	if m.help {
		return m.helpView()
	}

	// Recomputed every frame rather than carried: the chrome that had to be
	// given up on one view may fit on the next.
	m.chromeLevel = chromeFull

	// The header and footer are measured rather than guessed at: both grow and
	// shrink with the view, and a stale estimate makes the list overflow and
	// scroll the header off the top of the terminal.
	header, footer := m.header(), m.footer()

	m.listRows = m.height - lineCount(header) - lineCount(footer) - 2

	// On a short terminal the chrome alone can fill the screen. Shed the parts
	// that are conveniences before the parts that say where you are, and keep
	// at least a couple of rows of content.
	for m.listRows < 2 && m.chromeLevel < chromeMinimal {
		m.chromeLevel++

		header, footer = m.header(), m.footer()
		m.listRows = m.height - lineCount(header) - lineCount(footer) - 2
	}

	m.listRows = maxInt(1, m.listRows)

	var b strings.Builder

	b.WriteString(header)
	b.WriteString("\n")

	// The composer is a popup over whatever was open, so the screen renders as
	// that view and the popup is composited on top at the end.
	bv := m.backView()

	if m.splitMail() && mailSplitView(bv) {
		bv = viewThreads // one split screen stands in for all three mail views

		b.WriteString(m.splitMailView())
	}

	switch bv {
	case viewBoxes:
		b.WriteString(m.boxesView())
	case viewThreads:
		// Rendered above when the split is on.
		if !m.splitMail() {
			b.WriteString(m.threadsView())
		}
	case viewThread:
		b.WriteString(m.threadView())
	case viewMessage:
		b.WriteString(m.messageView())
	case viewCalendar:
		b.WriteString(m.calendarView())
	case viewNotes:
		b.WriteString(m.notesView())
	case viewNoteRead:
		b.WriteString(m.noteReadView())
	case viewNoteEdit:
		b.WriteString(m.noteEditView())
	case viewMovePicker:
		b.WriteString(m.movePickerView())
	case viewEventForm:
		b.WriteString(m.eventFormView())
	case viewEventDetail:
		b.WriteString(m.eventDetailView())
	case viewHabits, viewTodos:
		b.WriteString(m.bandView())
	case viewCalendars:
		b.WriteString(m.calendarsView())
	}

	b.WriteString("\n")
	b.WriteString(footer)

	// A last guarantee. Anything that still does not fit is cut rather than
	// allowed to scroll the header away.
	frame := m.indent(clampLines(b.String(), m.height))

	// The composer floats over the finished frame, in screen coordinates, so
	// the mouse handler can use the same geometry to route clicks.
	if m.compose != nil {
		lay := composerPlace(m.width, m.height, m.composerMin, m.composerMax)

		if popup, _ := composerPopup(m.compose, lay, m.composerMin, m.account); popup != "" {
			// The popup has a width floor, so on a terminal thinner than it the
			// spliced lines overrun the screen; clipping them here shears the
			// popup's right edge instead of the whole frame's alignment.
			frame = clampLines(clipToWidth(overlay(frame, popup, lay.x, lay.y), m.width), m.height)
		}
	}

	return frame
}

// clipToWidth cuts every line of a rendered frame to at most width columns,
// measured in cells so styled text and wide glyphs are cut where they display.
func clipToWidth(s string, width int) string {
	if width < 1 {
		return ""
	}

	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if ansi.StringWidth(l) > width {
			lines[i] = ansi.Truncate(l, width, "")
		}
	}

	return strings.Join(lines, "\n")
}

// clampLines drops trailing lines beyond what the terminal can show.
func clampLines(s string, height int) string {
	if height < 1 {
		return ""
	}

	lines := strings.Split(s, "\n")
	if len(lines) <= height {
		return s
	}

	return strings.Join(lines[:height], "\n")
}

// lineCount is how many terminal rows a rendered block occupies.
func lineCount(s string) int {
	if s == "" {
		return 0
	}

	return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
}

// --- chrome -----------------------------------------------------------------

// header draws the rows above the list, using hey-cli's own renderers so the
// chrome sits in the same shape as the rows beneath it: a top rule with the
// account on the right, a centred section row, a rule naming the box, and a
// centred row of numbered boxes.
func (m *Model) header() string {
	w := m.contentWidth()

	var b strings.Builder

	b.WriteString(heyui.TopRule(w, "frankenstein", m.account))
	b.WriteString("\n")

	if m.chromeLevel < chromeMinimal {
		b.WriteString(heyui.NavRow([]heyui.NavItem{
			{Label: "Mail"}, {Label: "Calendar"}, {Label: "Notes"},
		}, int(m.section), true, w))
		b.WriteString("\n")
	}

	b.WriteString(heyui.Rule(w, m.locationName()))
	b.WriteString("\n")

	if m.view == viewCalendar && m.chromeLevel < chromeNoBoxBar {
		b.WriteString(heyui.NavRow([]heyui.NavItem{
			{Label: "Day", Shortcut: "1"},
			{Label: "Week", Shortcut: "2"},
			{Label: "Year", Shortcut: "3"},
		}, int(m.calView), true, w))
		b.WriteString("\n")
	}

	if m.mailChrome() {
		if m.chromeLevel < chromeNoBoxBar {
			if bar := m.boxBar(w); bar != "" {
				b.WriteString(bar)
				b.WriteString("\n")
			}

			// The categories sit directly under the bar, as a sub-row of the
			// box they narrow. chromeRows counts these rows in the same order
			// to map a click, so the two must be changed together.
			if m.categoryRowShown() {
				b.WriteString(m.categoryRow(w))
				b.WriteString("\n")
			}
		}
	}

	if m.view == viewBoxes && m.chromeLevel < chromeNoExtras && len(m.events) > 0 {
		b.WriteString(m.agendaLine())
		b.WriteString("\n")
	}

	return b.String()
}

// locationName is what the rule under the section row names.
func (m *Model) locationName() string {
	switch m.view {
	case viewThreads:
		if n := m.list.selectionCount(); n > 0 {
			return fmt.Sprintf("%s · %d selected", m.box.Name, n)
		}

		return m.box.Name
	case viewThread, viewMessage:
		// The subject is provider text on its way into the title rule.
		return truncateStr(terminal.SanitizeLine(m.thread.Conversation.Subject),
			maxInt(10, m.contentWidth()/2))
	case viewCompose:
		return composeTitle(m.compose)
	case viewEventForm:
		if m.eventForm != nil && m.eventForm.id != "" {
			return "Edit event"
		}

		return "New event"
	case viewEventDetail:
		if e, ok := m.detailEvent(); ok {
			return truncateStr(e.Title, maxInt(10, m.contentWidth()/2))
		}

		return "Event"
	case viewHabits:
		return "Habits"
	case viewTodos:
		return "Todos"
	case viewCalendars:
		return "Calendars"
	case viewCalendar:
		switch m.calView {
		case calendarDay:
			return "Day"
		case calendarYear:
			return "Year"
		default:
			return "Week"
		}
	case viewNotes:
		return "Notes"
	case viewNoteRead:
		return truncateStr(m.openNote.Title, maxInt(10, m.contentWidth()/2))
	case viewNoteEdit:
		if m.noteEd != nil && m.noteEd.name != "" {
			return "Edit note"
		}

		return "New note"
	default:
		return "Mail"
	}
}

func composeTitle(c *composeState) string {
	if c == nil {
		return "Compose"
	}

	switch c.kind {
	case composeReply:
		return "Reply"
	case composeReplyAll:
		return "Reply all"
	case composeForward:
		return "Forward"
	default:
		return "Compose"
	}
}

// mailChrome reports whether the box bar belongs on screen: on the mail list
// screens, and on the split screen that stands in for all of them, looking
// through an open composer.
func (m *Model) mailChrome() bool {
	bv := m.backView()

	return bv == viewBoxes || bv == viewThreads || (m.splitMail() && mailSplitView(bv))
}

// boxActive reports whether the bar should mark the current box: anywhere a
// box's own threads are what is on screen.
func (m *Model) boxActive() bool {
	bv := m.backView()

	return bv == viewThreads || (m.splitMail() && mailSplitView(bv))
}

// boxBar is the numbered box switcher, drawn by hey-cli's nav renderer so the
// shortcut digits are emphasised the way its section row is.
func (m *Model) boxBar(w int) string {
	if len(m.quickBoxes) == 0 {
		return ""
	}

	items := make([]heyui.NavItem, 0, len(m.quickBoxes))
	selected := -1

	for i, b := range m.quickBoxes {
		label := b.Name
		if b.Unread > 0 {
			label = fmt.Sprintf("%s (%d)", b.Name, b.Unread)
		}

		items = append(items, heyui.NavItem{Label: label, Shortcut: fmt.Sprintf("%d", i+1)})

		if m.boxActive() && b.ID == m.box.ID {
			selected = i
		}
	}

	return heyui.NavRow(items, selected, m.boxActive(), w)
}

// categoryRow is the sub-row under the box bar: the inbox categories, drawn by
// the same renderer as the bar itself so it reads as part of the same chrome.
//
// "All" leads it because no category is the ordinary state, and a row where
// every entry narrows something would leave no way back to the whole box.
func (m *Model) categoryRow(w int) string {
	cats := m.categoryBoxes()
	if len(cats) == 0 {
		return ""
	}

	items := make([]heyui.NavItem, 0, len(cats)+1)
	items = append(items, heyui.NavItem{Label: categoryAllLabel})

	selected := 0

	for i, b := range cats {
		label := b.Name
		if b.Unread > 0 {
			label = fmt.Sprintf("%s (%d)", b.Name, b.Unread)
		}

		items = append(items, heyui.NavItem{Label: label})

		if b.ID == m.categoryID {
			selected = i + 1
		}
	}

	return heyui.NavRow(items, selected, true, w)
}

// calendarHint turns Google's wall of JSON into the one sentence that says
// what to do. Its errors carry the fix in them, buried.
func calendarHint(err error) string {
	msg := err.Error()

	if i := strings.Index(msg, "Details:"); i > 0 {
		msg = msg[:i]
	}

	if strings.Contains(msg, "has not been used in project") ||
		strings.Contains(msg, "SERVICE_DISABLED") {
		return "The Google Calendar API is not enabled on your project. " +
			"Enable it in the console, wait a minute, and press r."
	}

	if strings.Contains(msg, "invalid_grant") || strings.Contains(msg, "401") {
		return "The Google authorisation has expired. Run `frankenstein calendar setup` again."
	}

	return strings.TrimSpace(strings.TrimPrefix(msg, "list events: "))
}

func (m *Model) agendaLine() string {
	var parts []string

	// An event belongs on today's line whenever its span covers any of
	// today, not only when it starts today: a multi-day trip is still
	// happening. The bounds are local calendar days, because dividing epochs
	// misplaces the boundary in any zone that is not UTC.
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dayEnd := dayStart.AddDate(0, 0, 1)

	for _, e := range m.events {
		if !e.Start.Before(dayEnd) || !e.End.After(dayStart) {
			continue
		}

		when := e.Start.Format("15:04")

		switch {
		case e.AllDay:
			when = "all day"
		case e.Start.Before(dayStart):
			// Started on an earlier day, so its start time would mislead.
			when = "until " + e.End.Format("15:04")
		}

		parts = append(parts, fmt.Sprintf("%s %s", when, e.Title))

		if len(parts) == 3 {
			break
		}
	}

	if len(parts) == 0 {
		return dimStyle.Render("  today: nothing scheduled")
	}

	return dimStyle.Render("  today: " + strings.Join(parts, " · "))
}

func (m *Model) footer() string {
	if m.filtering {
		return statusStyle.Render(" filter: ") + m.filter.View()
	}

	if m.err != nil {
		return errorStyle.Render(" " + truncateStr(m.err.Error(), maxInt(10, m.contentWidth()-2)))
	}

	if m.flash != "" {
		return okStyle.Render(" "+m.flash) + dimStyle.Render("   ? help")
	}

	status := m.status
	if m.loading {
		status = "working…"
	}

	if m.chromeLevel >= chromeNoExtras {
		// One line: the status, and a pointer to the help that lists the rest.
		return statusStyle.Render(" "+status) + dimStyle.Render("  ? help")
	}

	return heyui.Rule(m.contentWidth(), "") + "\n" +
		statusStyle.Render(" "+status) + "\n" + m.keyHints()
}

// keyBinding is one footer entry: the key, and what it does.
type keyBinding struct {
	key  string
	what string
}

// keyBindings is the whole vocabulary for the current view, in the order it
// should read.
func (m *Model) keyBindings() []keyBinding {
	switch m.view {
	case viewThreads:
		return []keyBinding{
			{"j/k", "navigate"}, {"enter", "open"}, {"1-9", "box"}, {"[/]", "category"},
			{"tab", "section"},
			{"/", "filter"}, {"space", "select"}, {"ctrl+a", "all"},
			{"c", "compose"}, {"r", "reply"}, {"f", "forward"}, {"v", "move"},
			{"e", "seen"}, {"u", "unseen"}, {"s", "star"},
			{"a", "archive"}, {"t", "trash"}, {"!", "spam"},
			{"?", "help"}, {"q", "quit"},
		}
	case viewThread, viewMessage:
		return []keyBinding{
			{"j/k", "navigate"}, {"enter", "read"}, {"r", "reply"}, {"R", "reply all"},
			{"f", "forward"}, {"a", "archive"}, {"t", "trash"},
			{"esc", "back"}, {"?", "help"}, {"q", "quit"},
		}
	case viewCompose:
		return []keyBinding{
			{"tab", "field"}, {"ctrl+d", "send"}, {"ctrl+s", "save draft"}, {"esc", "minimize"},
		}
	case viewMovePicker:
		return []keyBinding{{"up/down", "choose"}, {"enter", "move"}, {"esc", "cancel"}}
	case viewEventForm:
		return []keyBinding{
			{"tab", "field"}, {"ctrl+d", "save"}, {"esc", "cancel"},
		}
	case viewEventDetail:
		return []keyBinding{
			{"e", "edit"}, {"D", "delete"}, {"esc", "back"}, {"q", "quit"},
		}
	case viewHabits:
		return []keyBinding{
			{"j/k", "navigate"}, {"space", "keep today"}, {"a", "add"},
			{"d", "archive"}, {"esc", "back"},
		}
	case viewTodos:
		return []keyBinding{
			{"j/k", "navigate"}, {"space", "complete"}, {"a", "add"}, {"esc", "back"},
		}
	case viewCalendars:
		return []keyBinding{
			{"j/k", "navigate"}, {"space", "show or hide"}, {"a", "all"},
			{"w", "only this one"}, {"esc", "back"},
		}
	case viewNotes:
		return []keyBinding{
			{"j/k", "navigate"}, {"enter", "read"}, {"n", "new"}, {"e", "edit"},
			{"D", "delete"}, {"r", "reload"}, {"tab", "section"}, {"?", "help"}, {"q", "quit"},
		}
	case viewNoteRead:
		return []keyBinding{
			{"j/k", "scroll"}, {"e", "edit"}, {"D", "delete"}, {"esc", "back"}, {"q", "quit"},
		}
	case viewNoteEdit:
		return []keyBinding{
			{"ctrl+d", "save"}, {"esc", "discard"},
		}
	case viewCalendar:
		return []keyBinding{
			{"1", "day"}, {"2", "week"}, {"3", "year"},
			{"p/n", "back, forward"}, {"t", "today"},
			{"c", "new event"}, {"enter", "details"}, {"e", "edit"}, {"D", "delete"},
			{"b", "habits"}, {"s", "todos"}, {"g", "calendars"},
			{"tab", "section"}, {"r", "reload"}, {"?", "help"}, {"q", "quit"},
		}
	default:
		return []keyBinding{
			{"j/k", "navigate"}, {"enter", "open"}, {"tab", "section"}, {"r", "sync"},
			{"?", "help"}, {"q", "quit"},
		}
	}
}

// keyHints packs the bindings into as few lines as the width allows.
//
// A fixed set of groups reads badly at both extremes: it stacks four short
// lines across a wide terminal and overflows a narrow one. Packing to the
// measured width fills whatever is there.
func (m *Model) keyHints() string {
	const sep = " · "

	bindings := m.keyBindings()
	width := m.contentWidth() - 1 // the leading space

	var (
		lines []string
		line  strings.Builder
		used  int
	)

	flush := func() {
		if line.Len() > 0 {
			lines = append(lines, " "+line.String())
			line.Reset()

			used = 0
		}
	}

	for _, b := range bindings {
		plain := b.key + " " + b.what

		cost := len(plain)
		if used > 0 {
			cost += len(sep)
		}

		if used > 0 && used+cost > width {
			flush()

			cost = len(plain)
		}

		if used > 0 {
			line.WriteString(dimStyle.Render(sep))
		}

		line.WriteString(key(b.key) + dimStyle.Render(" "+b.what))
		used += cost
	}

	flush()

	return strings.Join(lines, "\n")
}

func key(k string) string { return keyStyle.Render(k) }

// --- mail views -------------------------------------------------------------

func (m *Model) boxesView() string {
	if len(m.boxes) == 0 {
		return dimStyle.Render("  No boxes cached. Press r to sync.")
	}

	var b strings.Builder

	end := minInt(m.boxFirst+m.pageSize(), len(m.boxes))

	for i := m.boxFirst; i < end; i++ {
		box := m.boxes[i]

		unread := ""
		if box.Unread > 0 {
			unread = fmt.Sprintf("%d", box.Unread)
		}

		line := fmt.Sprintf("  %-28s %8d  %5s", truncateStr(box.Name, 28), box.Total, unread)

		if box.Unread > 0 {
			line = unreadStyle.Render(line)
		}

		if i == m.boxIdx {
			line = selectedStyle.Render(padTo(line, m.contentWidth()))
		}

		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String()
}

// threadsView is the thread list on a terminal too narrow for the two-pane
// layout. It draws through listPane, the same renderer the split view's list
// uses, so there is one list in the program and one order it can be in.
func (m *Model) threadsView() string {
	h := maxInt(1, m.pageSize())

	if m.loading && m.list.len() == 0 {
		return placeholderPane("loading…", m.contentWidth(), h)
	}

	m.list.clamp()

	out, _ := listPane(m.list.convs, m.list.cursor, m.list.top,
		m.list.isSelected, m.contentWidth(), h, true)

	return out
}

// splitMailView is the Proton-style mail screen: the message list on the
// left, the open conversation on the right. It stands in for the threads,
// thread and message views whenever the terminal is wide enough for both
// panes to be readable.
func (m *Model) splitMailView() string {
	h := maxInt(1, m.pageSize())
	listW, _, threadW := m.paneGeom()

	m.list.clamp()

	reading := m.readingFocus()

	left, _ := listPane(m.list.convs, m.list.cursor, m.list.top,
		m.list.isSelected, listW, h, !reading)

	if m.loading && m.list.len() == 0 {
		left = placeholderPane("loading…", listW, h)
	}

	var right string

	if m.thread.Conversation.ID == "" {
		right = placeholderPane("Select a conversation.", threadW, h)
	} else {
		right, _ = threadPane(m.thread, m.msgIdx, m.threadBodyRef(), m.bodyTop, threadW, h)
	}

	return joinPanes(left, right, h, reading)
}

// threadBodyRef is the body text the thread pane may show: the wrapped lines
// only when they belong to the message that is expanded, so switching cards
// shows "loading…" rather than the previous message's text.
func (m *Model) threadBodyRef() []string {
	if len(m.thread.Messages) == 0 {
		return nil
	}

	i := clamp(m.msgIdx, 0, len(m.thread.Messages)-1)
	if m.body.MessageID != m.thread.Messages[i].ID {
		return nil
	}

	return m.bodyLines
}

// joinPanes zips two equal-height blocks with a rule between them. Both panes
// arrive exactly as wide as promised, so a plain per-line concatenation keeps
// the separator straight without measuring anything.
//
// Each pane gets a rail on its left edge, and the one with focus draws a solid
// bar down it. The title rule already names where you are, but a word at the
// top of the screen is easy to miss with two panes of mail in front of you; a
// full-height bar beside the pane being driven is not.
func joinPanes(left, right string, h int, reading bool) string {
	l := strings.Split(left, "\n")
	r := strings.Split(right, "\n")

	sep := " " + dimStyle.Render("│") + " "

	lrail, rrail := focusRail(!reading), focusRail(reading)

	var b strings.Builder

	for i := 0; i < h; i++ {
		var ls, rs string

		if i < len(l) {
			ls = l[i]
		}

		if i < len(r) {
			rs = r[i]
		}

		b.WriteString(lrail + ls + sep + rrail + rs + "\n")
	}

	return b.String()
}

// focusRail is one cell of a pane's left edge: a solid bar when the pane has
// focus, blank when it does not.
func focusRail(on bool) string {
	if !on {
		return strings.Repeat(" ", railWidth)
	}

	return railStyle.Render("┃")
}

// placeholderPane centres a dim line in an otherwise empty pane. Every row is
// padded to the full width, because the pane to the left of a separator has
// to hold its edge.
func placeholderPane(text string, width, height int) string {
	blank := strings.Repeat(" ", width)
	lines := make([]string, 0, height)

	for y := 0; y < height; y++ {
		if y != height/2 {
			lines = append(lines, blank)

			continue
		}

		tw := heyui.DisplayWidth(text)
		pad := maxInt(0, (width-tw)/2)
		rest := maxInt(0, width-pad-tw)
		lines = append(lines, strings.Repeat(" ", pad)+dimStyle.Render(text)+strings.Repeat(" ", rest))
	}

	return strings.Join(lines, "\n")
}

// Side margins, so the content does not run into the edge of the window.
//
// There is no width cap: a wide terminal should give wide rows, and capping one
// wastes most of the screen. The margin scales gently with the window so a
// narrow terminal keeps nearly all of its columns and a wide one gets a little
// breathing room on each side.
const (
	minMargin = 1
	maxMargin = 6
)

// margin is the space left on each side of the content.
func (m *Model) margin() int {
	return clamp(m.width/40, minMargin, maxMargin)
}

// contentWidth is the width available to a row: everything the terminal has,
// less a margin on each side.
func (m *Model) contentWidth() int {
	return maxInt(20, m.width-2*m.margin())
}

// gutter is the left margin that centres the content.
func (m *Model) gutter() string {
	pad := (m.width - m.contentWidth()) / 2
	if pad < 1 {
		return ""
	}

	return strings.Repeat(" ", pad)
}

// indent shifts every line of a block into the centred column.
func (m *Model) indent(block string) string {
	g := m.gutter()
	if g == "" {
		return block
	}

	lines := strings.Split(block, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = g + l
		}
	}

	return strings.Join(lines, "\n")
}

func (m *Model) threadView() string {
	if m.loading && len(m.thread.Messages) == 0 {
		return dimStyle.Render("  loading…")
	}

	if len(m.thread.Messages) == 0 {
		return dimStyle.Render("  Empty thread.")
	}

	var b strings.Builder

	for i, msg := range m.thread.Messages {
		marker := "  "
		if msg.Unread {
			marker = " •"
		}

		// The sender and the subject are provider text, sanitized at this
		// render boundary like the split view's panes do.
		line := fmt.Sprintf("%s %-16s  %-26s  %s",
			marker, msg.Time.Format("2 Jan 15:04"),
			truncateStr(terminal.SanitizeLine(msg.From.Display()), 26),
			truncateStr(terminal.SanitizeLine(msg.Subject), maxInt(10, m.contentWidth()-50)))

		if msg.Unread {
			line = unreadStyle.Render(line)
		}

		if i == m.msgIdx {
			line = selectedStyle.Render(padTo(line, m.contentWidth()))
		}

		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String()
}

func (m *Model) messageView() string {
	if m.loading && len(m.bodyLines) == 0 {
		return dimStyle.Render("  loading…")
	}

	var b strings.Builder

	end := minInt(m.bodyTop+m.pageSize(), len(m.bodyLines))

	for i := m.bodyTop; i < end; i++ {
		b.WriteString(m.bodyLines[i])
		b.WriteString("\n")
	}

	return b.String()
}

// --- move picker ------------------------------------------------------------

func (m *Model) movePickerView() string {
	var b strings.Builder

	b.WriteString("  " + dimStyle.Render("move to: ") + m.moveFilter.View() + "\n\n")

	matches := m.moveMatches()
	if len(matches) == 0 {
		b.WriteString(dimStyle.Render("  no box matches"))

		return b.String()
	}

	end := minInt(len(matches), maxInt(1, m.pageSize()-2))

	for i := 0; i < end; i++ {
		line := fmt.Sprintf("  %-30s %s",
			truncateStr(matches[i].Name, 30), dimStyle.Render(string(matches[i].Kind)))

		if i == m.moveIdx {
			line = selectedStyle.Render(padTo(line, m.contentWidth()))
		}

		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String()
}

// --- calendar and journal ---------------------------------------------------

// calendarView draws hey-cli's own day, week or year grid, with the habits
// band above it and the todo ribbon below.
func (m *Model) calendarView() string {
	if m.cal == nil {
		return dimStyle.Render("  Calendar is not configured. Run `frankenstein calendar setup`.")
	}

	if m.calErr != nil {
		return errorStyle.Render("  The calendar could not be read.") + "\n\n" +
			dimStyle.Render("  "+truncateStr(calendarHint(m.calErr), maxInt(20, m.contentWidth()-2)))
	}

	events := make([]heyui.Event, 0, len(m.events))

	for _, e := range m.events {
		events = append(events, heyui.Event{
			ID:       e.ID,
			Title:    e.Title,
			AllDay:   e.AllDay,
			StartsAt: e.Start,
			EndsAt:   e.End,
			Location: e.Location,
			Notes:    e.Notes,
			Color:    m.calColours[e.CalendarID],
		})
	}

	selected := ""
	if i := m.extraIdx; i >= 0 && i < len(events) {
		selected = heyui.Key(events[i])
	}

	hint := "p/n " + m.calPeriodName()

	// The todo ribbon takes rows from the grid, so the grid is told what is
	// left rather than the whole height.
	ribbon := heyui.TodosRibbon(m.calTodos, m.contentWidth())

	height := m.pageSize() - lineCount(ribbon)
	if height < 6 {
		height = maxInt(1, m.pageSize())
		ribbon = ""
	}

	grid := heyui.Calendar(m.calKind(), events, m.calHabits, m.calAnchor(),
		time.Monday, m.contentWidth(), height, hint, selected, true)

	if ribbon == "" {
		return grid
	}

	return grid + "\n" + ribbon
}

// calKind maps the section's view onto the renderer's.
func (m *Model) calKind() heyui.CalendarView {
	switch m.calView {
	case calendarDay:
		return heyui.CalendarDay
	case calendarYear:
		return heyui.CalendarYear
	default:
		return heyui.CalendarWeek
	}
}

// calPeriodName is what p and n step through.
func (m *Model) calPeriodName() string {
	switch m.calView {
	case calendarDay:
		return "day"
	case calendarYear:
		return "year"
	default:
		return "week"
	}
}

// calAnchor is the day or week being shown.
func (m *Model) calAnchor() time.Time {
	base := time.Now()

	if m.calOffset == 0 {
		return base
	}

	if m.calView == calendarDay {
		return base.AddDate(0, 0, m.calOffset)
	}

	// The year grid steps whole years; walking it a week at a time would take
	// 52 keypresses to change what is on screen.
	if m.calView == calendarYear {
		return base.AddDate(m.calOffset, 0, 0)
	}

	return base.AddDate(0, 0, 7*m.calOffset)
}

// --- help -------------------------------------------------------------------

func (m *Model) helpView() string {
	rows := [][2]string{
		{"j k up down", "move"},
		{"g G", "top, bottom"},
		{"enter", "open"},
		{"esc backspace", "back"},
		{"1-9", "jump to a box"},
		{"[ ]", "previous, next inbox category"},
		{"tab shift+tab", "Mail, Calendar, Notes"},
		{"", ""},
		{"c", "compose"},
		{"r", "reply in a thread, sync in a list"},
		{"R", "reply all"},
		{"f", "forward"},
		{"", ""},
		{"space", "select a thread"},
		{"ctrl+a", "select all, or clear the selection"},
		{"", ""},
		{"e u", "mark read, mark unread"},
		{"s", "star"},
		{"a t !", "archive, trash, spam"},
		{"v", "move to a box"},
		{"", ""},
		{"/", "filter the list"},
		{"?", "close this help"},
		{"q ctrl+c", "quit"},
		{"", ""},
		{"click", "open a conversation, a message, a button"},
		{"wheel", "scroll the pane under the pointer"},
	}

	var b strings.Builder

	b.WriteString(titleStyle.Render("  frankenstein - keys"))
	b.WriteString("\n\n")

	for _, r := range rows {
		if r[0] == "" {
			b.WriteString("\n")

			continue
		}

		b.WriteString(fmt.Sprintf("  %s  %s\n",
			keyStyle.Render(fmt.Sprintf("%-14s", r[0])), dimStyle.Render(r[1])))
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  Actions apply to the selection, or to the row under the cursor."))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  The list is newest first, whether a conversation has been read or not."))

	return b.String()
}
