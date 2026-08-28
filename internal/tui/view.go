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
	p := tea.NewProgram(m, tea.WithAltScreen())

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

	var b strings.Builder

	b.WriteString(m.header())
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
	}

	b.WriteString("\n")
	b.WriteString(m.footer())

	return m.indent(b.String())
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

	b.WriteString(heyui.NavRow([]heyui.NavItem{
		{Label: "Mail"}, {Label: "Calendar"}, {Label: "Journal"},
	}, int(m.section), true, w))
	b.WriteString("\n")

	if m.view == viewBoxes || m.view == viewThreads {
		b.WriteString(heyui.Rule(w, m.locationName()))
		b.WriteString("\n")

		if bar := m.boxBar(w); bar != "" {
			b.WriteString(bar)
			b.WriteString("\n")
		}

		if banner := m.screenerBanner(w); banner != "" {
			b.WriteString(banner)
			b.WriteString("\n")
		}
	} else {
		b.WriteString(heyui.Rule(w, m.locationName()))
		b.WriteString("\n")
	}

	if m.view == viewBoxes && len(m.events) > 0 {
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
	case viewCalendar:
		return "Calendar"
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

	return heyui.Rule(m.contentWidth(), "") + "\n" +
		statusStyle.Render(" "+status) + "\n" + m.keyHints()
}

// keyHints is the footer's key list, wrapped across lines the way hey-cli's
// is: the whole vocabulary on screen rather than a chosen few.
func (m *Model) keyHints() string {
	var groups [][]string

	switch m.view {
	case viewThreads:
		groups = [][]string{
			{"j/k navigate", "enter open", "1-9 box", "tab section", "/ filter", "q quit"},
			{"space select", "ctrl+a all", "c compose", "r reply", "f forward", "v move"},
			{"i imbox", "d feed", "p paper trail", "x screen out", "ctrl+s screener"},
			{"e seen", "u unseen", "s star", "a archive", "t trash", "! spam", "? help"},
		}
	case viewThread, viewMessage:
		groups = [][]string{
			{"j/k navigate", "enter read", "esc back", "q quit"},
			{"r reply", "R reply all", "f forward", "a archive", "t trash", "? help"},
		}
	case viewScreener:
		groups = [][]string{
			{"j/k navigate", "esc back"},
			{"i imbox", "d feed", "p paper trail", "x screen out"},
		}
	case viewCompose:
		groups = [][]string{{"tab field", "ctrl+d send", "ctrl+s save draft", "esc discard"}}
	case viewMovePicker:
		groups = [][]string{{"up/down choose", "enter move", "esc cancel"}}
	default:
		groups = [][]string{
			{"j/k navigate", "enter open", "tab section", "r sync", "? help", "q quit"},
		}
	}

	lines := make([]string, 0, len(groups))

	for _, g := range groups {
		parts := make([]string, 0, len(g))

		for _, item := range g {
			// The key is everything before the first space; the rest is what it
			// does, which reads better dim.
			if i := strings.Index(item, " "); i > 0 {
				parts = append(parts, key(item[:i])+dimStyle.Render(" "+item[i+1:]))

				continue
			}

			parts = append(parts, key(item))
		}

		lines = append(lines, " "+strings.Join(parts, dimStyle.Render(" · ")))
	}

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
			line = selectedStyle.Render(padTo(line, m.width))
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

// readableWidth caps how wide a row gets.
//
// Upstream has no cap, because a terminal that wide is unusual. On a very wide
// one an excerpt running the full span is unreadable: the eye loses the line
// before it reaches the date. This keeps a measured column and centres it.
const readableWidth = 118

// contentWidth is the width available to a row.
func (m *Model) contentWidth() int {
	return minInt(readableWidth, maxInt(20, m.width))
}

// gutter is the left margin that centres the capped content.
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
			truncateStr(msg.Subject, maxInt(10, m.width-50)))

		if msg.Unread {
			line = unreadStyle.Render(line)
		}

		if i == m.msgIdx {
			line = selectedStyle.Render(padTo(line, m.width))
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
			line = selectedStyle.Render(padTo(line, m.width))
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
			line = selectedStyle.Render(padTo(line, m.width))
		}

		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String()
}

// --- calendar and journal ---------------------------------------------------

func (m *Model) calendarView() string {
	if m.cal == nil {
		return dimStyle.Render("  Calendar is not configured. Run `frankenstein calendar setup`.")
	}

	if len(m.events) == 0 {
		return dimStyle.Render("  Nothing scheduled in the next week.")
	}

	var b strings.Builder

	var day string

	rows := 0

	for i, e := range m.events {
		if rows >= maxInt(1, m.pageSize()-1) {
			break
		}

		d := e.Start.Format("Monday 2 January")
		if d != day {
			b.WriteString(sectionStyle.Render(d))
			b.WriteString("\n")

			day = d
			rows++
		}

		when := fmt.Sprintf("%s-%s", e.Start.Format("15:04"), e.End.Format("15:04"))
		if e.AllDay {
			when = "all day"
		}

		line := fmt.Sprintf("  %-13s %s", when, truncateStr(e.Title, maxInt(10, m.width-20)))

		if i == m.extraIdx {
			line = selectedStyle.Render(padTo(line, m.width))
		}

		b.WriteString(line)
		b.WriteString("\n")

		rows++
	}

	return b.String()
}

func (m *Model) journalView() string {
	if len(m.journal) == 0 {
		return dimStyle.Render("  Nothing written yet. Use `frankenstein journal write`.")
	}

	var b strings.Builder

	end := minInt(len(m.journal), m.pageSize())

	for i := 0; i < end; i++ {
		e := m.journal[i]

		line := fmt.Sprintf("  %-12s %s", e.Day, truncateStr(e.Title, maxInt(10, m.width-18)))

		if i == m.extraIdx {
			line = selectedStyle.Render(padTo(line, m.width))
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
