package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
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

	return b.String()
}

// --- chrome -----------------------------------------------------------------

func (m *Model) header() string {
	var b strings.Builder

	b.WriteString(m.titleBar())
	b.WriteString("\n")
	b.WriteString(m.navRow())
	b.WriteString("\n")

	if m.view == viewBoxes || m.view == viewThreads {
		if bar := m.boxBar(); bar != "" {
			b.WriteString(bar)
			b.WriteString("\n")
		}

		if banner := m.screenerBanner(); banner != "" {
			b.WriteString(banner)
			b.WriteString("\n")
		}
	}

	if m.view == viewBoxes && len(m.events) > 0 {
		b.WriteString(m.agendaLine())
		b.WriteString("\n")
	}

	return b.String()
}

// titleBar is "── name ─────────── account ──", sized exactly to the terminal.
//
// Widths are computed on plain text and styling applied afterwards, because
// lipgloss padding is invisible to len() and silently overflows the line.
func (m *Model) titleBar() string {
	name := "frankenstein"

	switch m.view {
	case viewThreads:
		name = m.box.Name

		if n := len(m.selected); n > 0 {
			name = fmt.Sprintf("%s · %d selected", name, n)
		}
	case viewThread, viewMessage:
		name = m.thread.Conversation.Subject
	case viewScreener:
		name = "The Screener"
	case viewCompose:
		name = composeTitle(m.compose)
	case viewCalendar:
		name = "Calendar"
	case viewJournal:
		name = "Journal"
	}

	width := maxInt(24, m.width)

	name = truncateStr(name, maxInt(8, width/2))

	const lead = "── "

	left := lead + name + " "

	right := ""
	if m.account != "" {
		right = " " + m.account + " ──"

		if len([]rune(left))+len([]rune(right))+4 > width {
			right = ""
		}
	}

	fill := maxInt(0, width-len([]rune(left))-len([]rune(right)))

	return dimStyle.Render(lead) + titleStyle.Render(name) +
		dimStyle.Render(" "+strings.Repeat("─", fill)+right)
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

// navRow is the Mail / Calendar / Journal switcher.
func (m *Model) navRow() string {
	var parts []string

	for _, s := range []section{sectionMail, sectionCalendar, sectionJournal} {
		name := sectionNames[s]

		if s == m.section {
			parts = append(parts, selectedStyle.Render(" "+name+" "))

			continue
		}

		parts = append(parts, dimStyle.Render(name))
	}

	return "  " + strings.Join(parts, "  ")
}

// boxBar lists the numbered boxes, marking the one being viewed. Keeping every
// box one keystroke away is what stops navigation feeling like a filesystem.
func (m *Model) boxBar() string {
	if len(m.quickBoxes) == 0 {
		return ""
	}

	var parts []string

	for i, b := range m.quickBoxes {
		if m.view == viewThreads && b.ID == m.box.ID {
			parts = append(parts, selectedStyle.Render(fmt.Sprintf(" %d %s%s ", i+1, b.Name, countSuffix(b.Unread))))

			continue
		}

		parts = append(parts,
			keyStyle.Render(fmt.Sprintf("%d", i+1))+" "+b.Name+dimStyle.Render(countSuffix(b.Unread)))
	}

	return "  " + strings.Join(parts, dimStyle.Render(" · "))
}

func countSuffix(n int) string {
	if n <= 0 {
		return ""
	}

	return fmt.Sprintf(" (%d)", n)
}

// screenerBanner keeps the count of waiting senders in front of the user. The
// screener is the product; it should not be somewhere you go looking for.
func (m *Model) screenerBanner() string {
	if m.pending == 0 {
		return ""
	}

	noun := "senders"
	if m.pending == 1 {
		noun = "sender"
	}

	return "  " + bannerStyle.Render(fmt.Sprintf(" Screen %d first-time %s · ctrl+s ", m.pending, noun))
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
		return errorStyle.Render(" " + truncateStr(m.err.Error(), maxInt(10, m.width-2)))
	}

	if m.flash != "" {
		return okStyle.Render(" "+m.flash) + dimStyle.Render("   ? help")
	}

	status := m.status
	if m.loading {
		status = "working…"
	}

	return statusStyle.Render(" "+status) + "  " + m.keyHints()
}

func (m *Model) keyHints() string {
	switch m.view {
	case viewThreads:
		return key("enter") + " open " + key("space") + " select " + key("c") + " compose " +
			key("i") + "/" + key("d") + "/" + key("p") + " screen " + key("a") + " archive " +
			key("?") + " help"
	case viewThread, viewMessage:
		return key("enter") + " read " + key("r") + " reply " + key("f") + " forward " +
			key("a") + " archive " + key("esc") + " back " + key("?") + " help"
	case viewScreener:
		return key("i") + " imbox " + key("d") + " feed " + key("p") + " paper trail " +
			key("x") + " screen out " + key("esc") + " back"
	case viewCompose:
		return key("tab") + " field " + key("ctrl+d") + " send " + key("ctrl+s") + " save draft " +
			key("esc") + " discard"
	case viewMovePicker:
		return key("↑↓") + " choose " + key("enter") + " move " + key("esc") + " cancel"
	default:
		return key("enter") + " open " + key("tab") + " section " + key("r") + " sync " +
			key("?") + " help " + key("q") + " quit"
	}
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

// threadsView splits unread from read the way hey-cli splits "New for You"
// from "Previously Seen".
//
// There is no preview snippet: Proton's conversation metadata carries no
// excerpt, so showing one would mean decrypting a body per visible row, which
// is slow and breaks the rule that the render path never touches the network.
func (m *Model) threadsView() string {
	if m.loading && len(m.convs) == 0 {
		return dimStyle.Render("  loading…")
	}

	if len(m.convs) == 0 {
		return dimStyle.Render("  Nothing here.")
	}

	var b strings.Builder

	firstRead := -1

	for i, c := range m.convs {
		if !c.Unread() {
			firstRead = i

			break
		}
	}

	rows := 0

	for i := m.convFirst; i < len(m.convs) && rows < m.pageSize(); i++ {
		c := m.convs[i]

		if i == 0 && c.Unread() {
			b.WriteString(sectionStyle.Render("New for You"))
			b.WriteString("\n")

			rows++
		} else if i == firstRead && firstRead > 0 {
			b.WriteString(sectionStyle.Render("Previously Seen"))
			b.WriteString("\n")

			rows++
		}

		b.WriteString(m.threadRow(c, i == m.convIdx))
		b.WriteString("\n")

		rows++
	}

	return b.String()
}

func (m *Model) threadRow(c mail.Conversation, cursor bool) string {
	from := "(nobody)"
	if len(c.Senders) > 0 {
		from = c.Senders[0].Display()
	}

	marker := "  "

	switch {
	case m.selected[c.ID]:
		marker = " +"
	case c.Unread():
		marker = " •"
	}

	count := ""
	if c.NumMessages > 1 {
		count = fmt.Sprintf(" (%d)", c.NumMessages)
	}

	const senderWidth, dateWidth = 22, 10

	subjWidth := maxInt(10, m.width-(2+1+senderWidth+2+dateWidth+2))

	line := fmt.Sprintf("%s %-*s  %-*s  %*s",
		marker, senderWidth, truncateStr(from, senderWidth),
		subjWidth, truncateStr(c.Subject+count, subjWidth),
		dateWidth, relTime(c.Time))

	switch {
	case cursor:
		return selectedStyle.Render(padTo(line, m.width))
	case m.selected[c.ID]:
		return markStyle.Render(line)
	case c.Unread():
		return unreadStyle.Render(line)
	default:
		return line
	}
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
