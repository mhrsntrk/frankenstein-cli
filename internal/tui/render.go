package tui

import (
	"html"
	"strings"
	"time"

	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
)

// rewrapBody re-flows the decrypted body to the current width.
//
// Done once on load and on resize rather than per frame, because the render
// path has to stay cheap: View() runs on every keystroke.
func (m *Model) rewrapBody() {
	width := maxInt(20, m.contentWidth()-2)

	var lines []string

	if msg, ok := m.currentMessage(); ok {
		lines = append(lines,
			dimStyle.Render("From: ")+msg.From.String(),
			dimStyle.Render("To:   ")+joinAddrs(msg.To),
			dimStyle.Render("Date: ")+msg.Time.Format(time.RFC1123),
		)

		if len(m.body.Attachments) > 0 {
			names := make([]string, 0, len(m.body.Attachments))
			for _, a := range m.body.Attachments {
				names = append(names, a.Name)
			}

			lines = append(lines, dimStyle.Render("Files: ")+strings.Join(names, ", "))
		}

		lines = append(lines, "")
	}

	for _, para := range strings.Split(renderBody(m.body), "\n") {
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
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}

	var (
		lines []string
		cur   strings.Builder
	)

	for _, w := range words {
		switch {
		case cur.Len() == 0:
			cur.WriteString(w)
		case cur.Len()+1+len([]rune(w)) > width:
			lines = append(lines, cur.String())
			cur.Reset()
			cur.WriteString(w)
		default:
			cur.WriteString(" ")
			cur.WriteString(w)
		}
	}

	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}

	return lines
}

// renderBody strips HTML down to readable text.
//
// Deliberately crude: a terminal mail client does not need a browser engine,
// and most of what arrives is either plain text already or a marketing layout
// that would be unreadable however carefully it was rendered.
func renderBody(b mail.Body) string {
	if !strings.Contains(strings.ToLower(b.MIMEType), "html") {
		return b.Content
	}

	s := b.Content

	for _, tag := range []string{"script", "style", "head"} {
		s = stripElement(s, tag)
	}

	// Block ends become newlines before the tags go, or paragraphs run together.
	s = strings.NewReplacer(
		"<br>", "\n", "<br/>", "\n", "<br />", "\n",
		"</p>", "\n\n", "</div>", "\n", "</tr>", "\n", "</li>", "\n",
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

	// Entity decoding happens after the tags are gone, and through the standard
	// library rather than a hand-written list: marketing mail is full of
	// &#847;, &hairsp; and &euro;, and a partial table leaves them on screen.
	return collapseBlankLines(html.UnescapeString(out.String()))
}

// collapseBlankLines squeezes the runs of empty lines that tag stripping
// leaves behind.
func collapseBlankLines(s string) string {
	lines := strings.Split(s, "\n")
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

// stripElement removes an element and its contents, tolerating an unclosed tag
// at the end of a truncated document.
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

		s = s[:i] + s[i+j+len(closeTag):]
		lower = strings.ToLower(s)
	}
}

// relTime renders a timestamp the way a mail client does: time today, weekday
// this week, date beyond that.
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

// padTo right-pads so a reversed selection covers the full row. Styled strings
// carry escape sequences, so only printable columns are counted.
func padTo(s string, width int) string {
	if visible := visibleWidth(s); visible < width {
		return s + strings.Repeat(" ", width-visible)
	}

	return s
}

// visibleWidth counts printable columns, ignoring ANSI escapes.
func visibleWidth(s string) int {
	n, esc := 0, false

	for _, r := range s {
		switch {
		case r == '\x1b':
			esc = true
		case esc && r == 'm':
			esc = false
		case !esc:
			n++
		}
	}

	return n
}

// scrollTo keeps the cursor inside the visible window with minimal movement.
func scrollTo(first, cursor, size int) int {
	if size < 1 {
		return 0
	}

	if cursor < first {
		return cursor
	}

	if cursor >= first+size {
		return cursor - size + 1
	}

	return first
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}

	if v > hi {
		return hi
	}

	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}

	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}

	return b
}
