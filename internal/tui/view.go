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
	}

	b.WriteString("\n")
	b.WriteString(m.footer())

	return b.String()
}

func (m *Model) header() string {
	title := "frankenstein"

	switch m.view {
	case viewThreads:
		title = m.box.Name
	case viewThread, viewMessage:
		title = truncateStr(m.thread.Conversation.Subject, m.width-4)
	}

	line := titleStyle.Render(title)

	// Today's agenda earns its line only when there is something on it.
	if m.view == viewBoxes && len(m.events) > 0 {
		var parts []string

		for _, e := range m.events {
			when := e.Start.Format("15:04")
			if e.AllDay {
				when = "all day"
			}

			parts = append(parts, fmt.Sprintf("%s %s", when, e.Title))

			if len(parts) == 3 {
				break
			}
		}

		line += "\n" + dimStyle.Render("  today: "+strings.Join(parts, " · "))
	}

	return line
}

func (m *Model) footer() string {
	if m.filtering {
		return statusStyle.Render(m.filter.View())
	}

	if m.err != nil {
		return errorStyle.Render("  " + truncateStr(m.err.Error(), maxInt(10, m.width-4)))
	}

	keys := "j/k move · enter open · esc back · r sync · q quit"
	if m.view == viewThreads {
		keys = "j/k move · enter open · / filter · esc back · q quit"
	}

	status := m.status
	if m.loading {
		status = "working..."
	}

	return statusStyle.Render(fmt.Sprintf("%s   %s", status, keys))
}

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
			unread = fmt.Sprintf("  %d", box.Unread)
		}

		line := fmt.Sprintf("  %-28s %6d%s", truncateStr(box.Name, 28), box.Total, unread)

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

func (m *Model) threadsView() string {
	if m.loading {
		return dimStyle.Render("  loading...")
	}

	if len(m.convs) == 0 {
		return dimStyle.Render("  Nothing here.")
	}

	var b strings.Builder

	end := minInt(m.convFirst+m.pageSize(), len(m.convs))

	for i := m.convFirst; i < end; i++ {
		c := m.convs[i]

		from := "(nobody)"
		if len(c.Senders) > 0 {
			from = c.Senders[0].Display()
		}

		marker := " "
		if c.Unread() {
			marker = "•"
		}

		count := ""
		if c.NumMessages > 1 {
			count = fmt.Sprintf(" (%d)", c.NumMessages)
		}

		// Subject gets whatever the fixed columns leave behind.
		subjWidth := maxInt(10, m.width-46)

		line := fmt.Sprintf("%s %-10s  %-22s  %s%s",
			marker, relTime(c.Time), truncateStr(from, 22),
			truncateStr(c.Subject, subjWidth), count)

		if c.Unread() {
			line = unreadStyle.Render(line)
		}

		if i == m.convIdx {
			line = selectedStyle.Render(padTo(line, m.width))
		}

		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String()
}

func (m *Model) threadView() string {
	if m.loading {
		return dimStyle.Render("  loading...")
	}

	if len(m.thread.Messages) == 0 {
		return dimStyle.Render("  Empty thread.")
	}

	var b strings.Builder

	for i, msg := range m.thread.Messages {
		marker := " "
		if msg.Unread {
			marker = "•"
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
	if m.loading {
		return dimStyle.Render("  loading...")
	}

	var b strings.Builder

	end := minInt(m.bodyTop+m.pageSize(), len(m.bodyLines))

	for i := m.bodyTop; i < end; i++ {
		b.WriteString(m.bodyLines[i])
		b.WriteString("\n")
	}

	return b.String()
}

// rewrapBody re-flows the decrypted body to the current width. Done once on
// load and on resize rather than per frame, because the render path has to
// stay cheap.
func (m *Model) rewrapBody() {
	width := m.width - 2
	if width < 20 {
		width = 20
	}

	var head []string

	if len(m.thread.Messages) > m.msgIdx {
		msg := m.thread.Messages[m.msgIdx]

		head = []string{
			headerStyle.Render("From: ") + msg.From.String(),
			headerStyle.Render("To:   ") + joinAddrs(msg.To),
			headerStyle.Render("Date: ") + msg.Time.Format(time.RFC1123),
			"",
		}
	}

	body := renderBody(m.body)

	var lines []string

	lines = append(lines, head...)

	for _, para := range strings.Split(body, "\n") {
		lines = append(lines, wrap(para, width)...)
	}

	m.bodyLines = lines
}

func joinAddrs(in []mail.Address) string {
	out := make([]string, 0, len(in))
	for _, a := range in {
		out = append(out, a.String())
	}

	return strings.Join(out, ", ")
}

// wrap breaks a paragraph at word boundaries.
func wrap(s string, width int) []string {
	if s == "" {
		return []string{""}
	}

	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}

	var (
		lines []string
		cur   strings.Builder
	)

	for _, w := range words {
		if cur.Len() == 0 {
			cur.WriteString(w)

			continue
		}

		if cur.Len()+1+len([]rune(w)) > width {
			lines = append(lines, cur.String())
			cur.Reset()
			cur.WriteString(w)

			continue
		}

		cur.WriteString(" ")
		cur.WriteString(w)
	}

	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}

	return lines
}

// renderBody strips HTML to readable text. Deliberately crude; a terminal mail
// client does not need a browser engine.
func renderBody(b mail.Body) string {
	if !strings.Contains(strings.ToLower(b.MIMEType), "html") {
		return b.Content
	}

	s := b.Content

	for _, tag := range []string{"script", "style", "head"} {
		s = stripElement(s, tag)
	}

	s = strings.NewReplacer(
		"<br>", "\n", "<br/>", "\n", "<br />", "\n",
		"</p>", "\n\n", "</div>", "\n", "</tr>", "\n", "</li>", "\n",
		"&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'",
	).Replace(s)

	var out strings.Builder

	depth := 0

	for _, r := range s {
		switch {
		case r == '<':
			depth++
		case r == '>' && depth > 0:
			depth--
		case depth == 0:
			out.WriteRune(r)
		}
	}

	lines := strings.Split(out.String(), "\n")
	kept := make([]string, 0, len(lines))
	blank := 0

	for _, l := range lines {
		l = strings.TrimRight(l, " \t")

		if strings.TrimSpace(l) == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}

		kept = append(kept, l)
	}

	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func stripElement(s, tag string) string {
	lower := strings.ToLower(s)
	open := "<" + tag
	closeTag := "</" + tag + ">"

	for {
		i := strings.Index(lower, open)
		if i < 0 {
			return s
		}

		j := strings.Index(lower[i:], closeTag)
		if j < 0 {
			return s[:i]
		}

		end := i + j + len(closeTag)
		s = s[:i] + s[end:]
		lower = strings.ToLower(s)
	}
}

func relTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	now := time.Now()

	switch {
	case t.YearDay() == now.YearDay() && t.Year() == now.Year():
		return t.Format("15:04")
	case now.Sub(t) < 7*24*time.Hour:
		return t.Format("Mon 15:04")
	case t.Year() == now.Year():
		return t.Format("2 Jan")
	default:
		return t.Format("2 Jan 06")
	}
}

func truncateStr(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")

	if n <= 0 {
		return ""
	}

	r := []rune(s)
	if len(r) <= n {
		return s
	}

	if n == 1 {
		return "…"
	}

	return string(r[:n-1]) + "…"
}

// padTo right-pads so a reversed selection covers the full row.
func padTo(s string, width int) string {
	// Styled strings carry escape sequences, so measure printable width only.
	visible := 0
	inEscape := false

	for _, r := range s {
		switch {
		case r == '\x1b':
			inEscape = true
		case inEscape && r == 'm':
			inEscape = false
		case !inEscape:
			visible++
		}
	}

	if visible >= width {
		return s
	}

	return s + strings.Repeat(" ", width-visible)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}

	return b
}
