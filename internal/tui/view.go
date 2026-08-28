package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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

	switch m.view {
	case viewBoxes:
		b.WriteString(m.boxesView())
	case viewThreads:
		b.WriteString(m.threadsView())
	case viewThread:
		b.WriteString(m.threadView())
	case viewMessage:
		b.WriteString(m.messageView())
	case viewScreener:
		b.WriteString(m.screenerView())
	case viewCompose:
		b.WriteString(m.composeView())
	case viewCalendar:
		b.WriteString(m.calendarView())
	case viewJournal:
		b.WriteString(m.journalView())
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
	return m.indent(clampLines(b.String(), m.height))
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

// header draws the four rows above the list, using hey-cli's own renderers so
// the chrome sits in the same shape as the rows beneath it: a top rule with the
// account on the right, a centred section row, a rule naming the box, a centred
// row of numbered boxes, and the screener banner.
func (m *Model) header() string {
	w := m.contentWidth()

	var b strings.Builder

	b.WriteString(heyui.TopRule(w, "frankenstein", m.account))
	b.WriteString("\n")

	if m.chromeLevel < chromeMinimal {
		b.WriteString(heyui.NavRow([]heyui.NavItem{
			{Label: "Mail"}, {Label: "Calendar"}, {Label: "Journal"},
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

	if m.view == viewBoxes || m.view == viewThreads {
		if m.chromeLevel < chromeNoBoxBar {
			if bar := m.boxBar(w); bar != "" {
				b.WriteString(bar)
				b.WriteString("\n")
			}
		}

		if m.chromeLevel < chromeNoBanner {
			if banner := m.screenerBanner(w); banner != "" {
				b.WriteString(banner)
				b.WriteString("\n")
			}
		}
	}

	if m.view == viewBoxes && m.chromeLevel < chromeNoBanner && len(m.events) > 0 {
		b.WriteString(m.agendaLine())
		b.WriteString("\n")
	}

	return b.String()
}

// locationName is what the rule under the section row names.
func (m *Model) locationName() string {
	switch m.view {
	case viewThreads:
		if n := m.list.SelectionCount(); n > 0 {
			return fmt.Sprintf("%s · %d selected", m.box.Name, n)
		}

		return m.box.Name
	case viewThread, viewMessage:
		return truncateStr(m.thread.Conversation.Subject, maxInt(10, m.contentWidth()/2))
	case viewScreener:
		return "The Screener"
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
	case viewJournal:
		return "Journal"
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

		if m.view == viewThreads && b.ID == m.box.ID {
			selected = i
		}
	}

	return heyui.NavRow(items, selected, m.view == viewThreads, w)
}

// screenerBanner keeps the count of waiting senders in front of the reader. The
// screener is the product; it should not be somewhere you go looking for.
func (m *Model) screenerBanner(w int) string {
	if m.pending == 0 {
		return ""
	}

	noun := "senders"
	if m.pending == 1 {
		noun = "sender"
	}

	return heyui.Center(
		bannerStyle.Render(fmt.Sprintf(" Screen %d first-time %s · ctrl+s ", m.pending, noun)), w)
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

	today := time.Now().Format("2006-01-02")

	for _, e := range m.events {
		if e.Start.Format("2006-01-02") != today {
			continue
		}

		when := e.Start.Format("15:04")
		if e.AllDay {
			when = "all day"
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

	if m.chromeLevel >= chromeNoBanner {
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
			{"j/k", "navigate"}, {"enter", "open"}, {"1-9", "box"}, {"tab", "section"},
			{"/", "filter"}, {"space", "select"}, {"ctrl+a", "all"},
			{"c", "compose"}, {"r", "reply"}, {"f", "forward"}, {"v", "move"},
			{"i", "imbox"}, {"d", "feed"}, {"p", "paper trail"}, {"x", "screen out"},
			{"ctrl+s", "screener"}, {"e", "seen"}, {"u", "unseen"}, {"s", "star"},
			{"a", "archive"}, {"t", "trash"}, {"!", "spam"},
			{"?", "help"}, {"q", "quit"},
		}
	case viewThread, viewMessage:
		return []keyBinding{
			{"j/k", "navigate"}, {"enter", "read"}, {"r", "reply"}, {"R", "reply all"},
			{"f", "forward"}, {"a", "archive"}, {"t", "trash"},
			{"esc", "back"}, {"?", "help"}, {"q", "quit"},
		}
	case viewScreener:
		return []keyBinding{
			{"j/k", "navigate"}, {"i", "imbox"}, {"d", "feed"}, {"p", "paper trail"},
			{"x", "screen out"}, {"esc", "back"}, {"q", "quit"},
		}
	case viewCompose:
		return []keyBinding{
			{"tab", "field"}, {"ctrl+d", "send"}, {"ctrl+s", "save draft"}, {"esc", "discard"},
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

// threadsView hands the rows to hey-cli's renderer.
//
// The two-line layout, the cursor bar, the unread dot, the right-hand date
// column and the section headings are all theirs; this only sizes the list and
// tells it whether to split seen from unseen.
func (m *Model) threadsView() string {
	if m.loading && m.list.Len() == 0 {
		return dimStyle.Render("  loading…")
	}

	// The same flag drives the section headings and the unread dot, so turning
	// it off outside the Imbox also lost the dots. Any box that tracks unread
	// wants both; Sent and Drafts do not.
	m.list.HideSections(!m.tracksUnread())

	m.list.SetSize(m.contentWidth(), m.pageSize())

	return m.list.View()
}

// tracksUnread reports whether the box on screen has a meaningful read state.
// Sent and Drafts do not, so a "New for You" heading over them is nonsense.
func (m *Model) tracksUnread() bool {
	switch m.box.Name {
	case "Sent", "Drafts", "Outbox", "All Sent", "All Drafts", "All Scheduled":
		return false
	default:
		return true
	}
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

		line := fmt.Sprintf("%s %-16s  %-26s  %s",
			marker, msg.Time.Format("2 Jan 15:04"),
			truncateStr(msg.From.Display(), 26),
			truncateStr(msg.Subject, maxInt(10, m.contentWidth()-50)))

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

// --- screener ---------------------------------------------------------------

// screenerView is the decision queue: one sender per row. Deciding here is
// about a person, so it moves everything they have ever sent.
func (m *Model) screenerView() string {
	if m.loading && len(m.senders) == 0 {
		return dimStyle.Render("  loading…")
	}

	if len(m.senders) == 0 {
		return okStyle.Render("  Nobody waiting. The screener is empty.")
	}

	var b strings.Builder

	b.WriteString(dimStyle.Render("  Decide once per sender. Everything they sent moves with them."))
	b.WriteString("\n\n")

	visible := maxInt(1, m.pageSize()-2)

	start := clamp(m.senderIdx-visible/2, 0, maxInt(0, len(m.senders)-visible))
	end := minInt(start+visible, len(m.senders))

	for i := start; i < end; i++ {
		s := m.senders[i]

		name := s.Address
		if s.Name != "" {
			name = fmt.Sprintf("%s <%s>", s.Name, s.Address)
		}

		tag := ""
		if s.NewsletterID != "" {
			tag = "list"
		}

		line := fmt.Sprintf("  %-46s %5d msg  %-10s  %s",
			truncateStr(name, 46), s.MessageCount, relTime(s.LastSeen), tag)

		if i == m.senderIdx {
			line = selectedStyle.Render(padTo(line, m.contentWidth()))
		}

		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String()
}

// --- compose ----------------------------------------------------------------

func (m *Model) composeView() string {
	c := m.compose
	if c == nil {
		return ""
	}

	var b strings.Builder

	field := func(label string, i int, rendered string) {
		marker := "  "
		if c.field == i {
			marker = markStyle.Render("> ")
		}

		b.WriteString(marker + dimStyle.Render(fmt.Sprintf("%-9s", label)) + rendered + "\n")
	}

	field("To", 0, c.to.View())
	field("Cc", 1, c.cc.View())
	field("Subject", 2, c.subject.View())

	b.WriteString("\n")

	marker := "  "
	if c.field == 3 {
		marker = markStyle.Render("> ")
	}

	b.WriteString(marker + dimStyle.Render("Body") + "\n")
	b.WriteString(c.body.View())
	b.WriteString("\n")

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

	return base.AddDate(0, 0, 7*m.calOffset)
}

func (m *Model) journalView() string {
	if len(m.journal) == 0 {
		return dimStyle.Render("  Nothing written yet. Use `frankenstein journal write`.")
	}

	var b strings.Builder

	end := minInt(len(m.journal), m.pageSize())

	for i := 0; i < end; i++ {
		e := m.journal[i]

		line := fmt.Sprintf("  %-12s %s", e.Day, truncateStr(e.Title, maxInt(10, m.contentWidth()-18)))

		if i == m.extraIdx {
			line = selectedStyle.Render(padTo(line, m.contentWidth()))
		}

		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String()
}

// --- help -------------------------------------------------------------------

func (m *Model) helpView() string {
	rows := [][2]string{
		{"j k up down", "move"},
		{"g G", "top, bottom"},
		{"enter", "open"},
		{"esc backspace", "back"},
		{"1-9", "jump to a box"},
		{"tab shift+tab", "Mail, Calendar, Journal"},
		{"", ""},
		{"c", "compose"},
		{"r", "reply in a thread, sync in a list"},
		{"R", "reply all"},
		{"f", "forward"},
		{"", ""},
		{"space", "select a thread"},
		{"ctrl+a", "select all, or clear the selection"},
		{"", ""},
		{"i", "screen sender to the Imbox"},
		{"d", "screen sender to the Feed"},
		{"p", "screen sender to the Paper Trail"},
		{"x", "screen sender out"},
		{"ctrl+s", "open the screener queue"},
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
		{"click", "move the cursor; click again to open"},
		{"wheel", "scroll without moving the cursor"},
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
	b.WriteString(dimStyle.Render("  Screening decides about a sender, so it moves everything they sent."))

	return b.String()
}
