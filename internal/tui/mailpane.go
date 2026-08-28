package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
	"github.com/mhrsntrk/frankenstein-cli/internal/terminal"
	"github.com/mhrsntrk/frankenstein-cli/internal/tui/heyui"
)

// The two-pane mail view, Proton style: a message list on the left, the open
// conversation on the right. Everything here is a pure renderer -- input in,
// string and regions out -- so it can be tested without a Model and reused by
// whichever view ends up compositing the panes.
//
// Every string that came from the provider -- a sender's name, an address, a
// subject -- is hostile input, and this file is its render boundary: initials,
// joinDisplays and listRows/threadPane sanitize it on the way into a line, so
// no caller has to remember to.

// The shared style block covers most of what the panes need; these two are the
// looks it does not have: the initials block Proton draws as a sender avatar,
// and the bright subject an unread row gets.
var (
	paneInitialsStyle = lipgloss.NewStyle().Foreground(heyui.Primary()).Bold(true)
	paneBrightStyle   = lipgloss.NewStyle().Foreground(heyui.Bright())
)

// paneStyler decorates every segment of a row, so a cursor row reads as one
// highlighted line rather than a patchwork of highlighted fragments.
type paneStyler func(lipgloss.Style) lipgloss.Style

func plainRow(s lipgloss.Style) lipgloss.Style { return s }

func cursorRow(s lipgloss.Style) lipgloss.Style { return heyui.SelectionStyle(s.Reverse(true)) }

// fitTo truncates and right-pads plain text to exactly w printable columns.
// The shared truncateStr counts runes, which lies about emoji; this one
// measures with the calibrated widths, because sender names and subjects
// carry Turkish letters and emoji in real mail.
func fitTo(s string, w int) string {
	if w <= 0 {
		return ""
	}

	s = strings.ReplaceAll(s, "\n", " ")

	if heyui.DisplayWidth(s) > w {
		s = heyui.Truncate(s, w)
	}

	if pad := w - heyui.DisplayWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}

	return s
}

// fitLine is the styled backstop: it clips an already-rendered line to width
// columns and pads it out, so every row leaves here exactly as wide as the
// pane whatever a segment did.
func fitLine(line string, width int) string {
	if heyui.DisplayWidth(line) > width {
		line = ansi.Truncate(line, width, "")
	}

	if pad := width - heyui.DisplayWidth(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}

	return line
}

// initials is the two-letter block Proton renders as the sender avatar: the
// first letter of the first two words of the display name, uppercased, always
// exactly two cells.
func initials(a mail.Address) string {
	var b strings.Builder

	// Sanitized first, or a display name opening with an escape byte would
	// hand that byte to the terminal as its "initial".
	for i, f := range strings.Fields(terminal.SanitizeLine(a.Display())) {
		if i == 2 {
			break
		}

		b.WriteRune([]rune(f)[0])
	}

	return fitTo(strings.ToUpper(b.String()), 2)
}

func joinDisplays(in []mail.Address) string {
	out := make([]string, 0, len(in))
	for _, a := range in {
		out = append(out, terminal.SanitizeLine(a.Display()))
	}

	return strings.Join(out, ", ")
}

// listPageSize is how many conversations fit in height rows, at two rows each.
func listPageSize(height int) int { return maxInt(0, height/2) }

// listPane renders the left message-list pane: two rows per conversation,
// initials block, senders and time on the first row, subject and attachment
// marker on the second. selected reports whether a conversation is
// bulk-selected, which turns its initials block into a reversed checkbox.
// top is the first visible row index. The block is exactly height rows of
// exactly width columns, and the regions cover each visible conversation as
// "conv:<index>" in pane-local coordinates.
func listPane(convs []mail.Conversation, cursor, top int, selected func(string) bool, width, height int) (string, []Region) {
	if width <= 0 || height <= 0 {
		return "", nil
	}

	blank := strings.Repeat(" ", width)
	lines := make([]string, 0, height)

	if len(convs) == 0 {
		msg := "No conversations."

		for y := range height {
			if y != height/2 {
				lines = append(lines, blank)
				continue
			}

			pad := maxInt(0, (width-heyui.DisplayWidth(msg))/2)
			lines = append(lines, fitLine(strings.Repeat(" ", pad)+dimStyle.Render(msg), width))
		}

		return strings.Join(lines, "\n"), nil
	}

	top = clamp(top, 0, maxInt(0, len(convs)-1))

	var regions []Region

	for i := top; i < len(convs) && i-top < listPageSize(height); i++ {
		st := paneStyler(plainRow)
		if i == cursor {
			st = cursorRow
		}

		checked := selected != nil && selected(convs[i].ID)
		r1, r2 := listRows(convs[i], checked, st, width)
		regions = append(regions, Region{ID: fmt.Sprintf("conv:%d", i), X: 0, Y: len(lines), W: width, H: 2})
		lines = append(lines, r1, r2)
	}

	for len(lines) < height {
		lines = append(lines, blank)
	}

	return strings.Join(lines[:height], "\n"), regions
}

// listRows draws one conversation's two rows. Unread state is a single
// signal -- bold senders, bright subject -- rather than a scatter of glyphs.
func listRows(c mail.Conversation, checked bool, st paneStyler, width int) (string, string) {
	var from mail.Address
	if len(c.Senders) > 0 {
		from = c.Senders[0]
	}

	block := paneInitialsStyle
	if checked {
		// The reversed block doubles as the bulk-selection checkbox.
		block = block.Reverse(true)
	}

	timeStr := relTime(c.Time)
	nameStyle := lipgloss.NewStyle()

	if c.Unread() {
		nameStyle = unreadStyle
	}

	nameW := width - 5 - heyui.DisplayWidth(timeStr) - 2

	row1 := st(lipgloss.NewStyle()).Render(" ") +
		st(block).Render(initials(from)) +
		st(lipgloss.NewStyle()).Render("  ") +
		st(nameStyle).Render(fitTo(joinDisplays(c.Senders), nameW)) +
		st(dimStyle).Render(" "+timeStr+" ")

	subj := terminal.SanitizeLine(c.Subject)
	if c.NumMessages > 1 {
		subj = fmt.Sprintf("[%d] %s", c.NumMessages, subj)
	}

	subjStyle := dimStyle
	if c.Unread() {
		subjStyle = paneBrightStyle
	}

	marker := " "
	if c.NumAttachments > 0 {
		marker = "∗"
	}

	subjW := width - 5 - heyui.DisplayWidth(marker) - 2

	row2 := st(lipgloss.NewStyle()).Render("     ") +
		st(subjStyle).Render(fitTo(subj, subjW)) +
		st(lipgloss.NewStyle()).Render(" ") +
		st(dimStyle).Render(marker) +
		st(lipgloss.NewStyle()).Render(" ")

	return fitLine(row1, width), fitLine(row2, width)
}

// threadBodyRows is how many rows of body are visible around the header
// cards, so the caller can clamp its body scroll. msgIdx is accepted for
// symmetry with threadPane, but every message costs the same today: one row
// collapsed, four rows of chrome expanded.
func threadBodyRows(th mail.Thread, msgIdx, height int) int {
	_ = msgIdx

	// Collapsed messages take a row each; the expanded one takes a header, a
	// meta row, a rule and an action row.
	chrome := len(th.Messages) + 3
	if len(th.Messages) == 0 {
		chrome = 4
	}

	return maxInt(0, height-chrome)
}

// The star has no backing field in the mail model yet, so every message wears
// the dim unstarred glyph; the region is still emitted so the click can be
// wired up the day the field exists.
const paneStar = "☆"

// threadPane renders the right conversation pane, Proton style: one card per
// message, collapsed messages as a single header row, the expanded message
// (msgIdx) with its header, meta row, rule, body scrolled to bodyTop, and an
// action row. Regions: "msg:<i>" on every header, "star" on the expanded
// header's star glyph, "reply"/"replyall"/"forward" on the action row.
func threadPane(th mail.Thread, msgIdx int, bodyLines []string, bodyTop int, width, height int) (string, []Region) {
	if width <= 0 || height <= 0 {
		return "", nil
	}

	blank := strings.Repeat(" ", width)
	lines := make([]string, 0, height)

	if len(th.Messages) == 0 {
		// Nothing fetched yet: the thread is still on its way from the provider.
		lines = append(lines, fitLine(" "+dimStyle.Render("loading…"), width))

		for len(lines) < height {
			lines = append(lines, blank)
		}

		return strings.Join(lines, "\n"), nil
	}

	msgIdx = clamp(msgIdx, 0, len(th.Messages)-1)
	bodyRows := threadBodyRows(th, msgIdx, height)

	var regions []Region

	starW := heyui.DisplayWidth(paneStar)

	for i, msg := range th.Messages {
		timeStr := relTime(msg.Time)
		suffix := paneStar + "  " + timeStr + " "
		suffixW := starW + 2 + heyui.DisplayWidth(timeStr) + 1
		flexW := width - 1 - suffixW

		regions = append(regions, Region{ID: fmt.Sprintf("msg:%d", i), X: 0, Y: len(lines), W: width, H: 1})

		if i != msgIdx {
			nameStyle := lipgloss.NewStyle()
			if msg.Unread {
				nameStyle = unreadStyle
			}

			lines = append(lines, fitLine(" "+
				nameStyle.Render(fitTo(terminal.SanitizeLine(msg.From.Display()), flexW))+
				dimStyle.Render(suffix), width))

			continue
		}

		// The expanded card. Header first: bold sender, dim address, then the
		// star and date shared with the collapsed rows so the columns line up.
		// Both halves are provider text, sanitized at this boundary.
		name := terminal.SanitizeLine(msg.From.Display())

		addr := ""
		if msg.From.Name != "" {
			addr = " <" + terminal.SanitizeLine(msg.From.Address) + ">"
		}

		if heyui.DisplayWidth(name) > flexW {
			name, addr = fitTo(name, flexW), ""
		}

		// The star is drawn after the header region, so a click on the glyph
		// resolves to it rather than to the whole header.
		regions = append(regions, Region{ID: "star", X: width - suffixW, Y: len(lines), W: starW, H: 1})

		lines = append(lines, fitLine(" "+
			unreadStyle.Render(name)+
			dimStyle.Render(fitTo(addr, flexW-heyui.DisplayWidth(name)))+
			dimStyle.Render(suffix), width))

		meta := " to " + joinDisplays(msg.To) + "  ·  " + msg.Time.Format("Mon, 2 Jan 2006 15:04")
		lines = append(lines, fitLine(dimStyle.Render(fitTo(meta, width)), width))

		lines = append(lines, fitLine(heyui.Rule(width, ""), width))

		for r := range bodyRows {
			idx := bodyTop + r

			switch {
			case len(bodyLines) == 0 && r == 0:
				lines = append(lines, fitLine(" "+dimStyle.Render("loading…"), width))
			case idx >= 0 && idx < len(bodyLines):
				lines = append(lines, fitLine(" "+bodyLines[idx], width))
			default:
				lines = append(lines, blank)
			}
		}

		// The action row, right-aligned like Proton's toolbar.
		actions := []struct{ glyph, label, id string }{
			{"↩", "reply", "reply"},
			{"↩↩", "reply all", "replyall"},
			{"↪", "forward", "forward"},
		}

		total := 0
		for _, a := range actions {
			total += heyui.DisplayWidth(" " + a.glyph + " " + a.label + " ")
		}

		x := maxInt(0, width-total)
		row := strings.Repeat(" ", x)

		for _, a := range actions {
			w := heyui.DisplayWidth(" " + a.glyph + " " + a.label + " ")
			regions = append(regions, Region{ID: a.id, X: x, Y: len(lines), W: w, H: 1})
			row += " " + dimStyle.Render(a.glyph) + " " + a.label + " "
			x += w
		}

		lines = append(lines, fitLine(row, width))
	}

	for len(lines) < height {
		lines = append(lines, blank)
	}

	// A pane too short for its chrome clips at the bottom, and the regions of
	// the rows that fell off go with it.
	kept := regions[:0]

	for _, r := range regions {
		if r.Y < height {
			kept = append(kept, r)
		}
	}

	return strings.Join(lines[:height], "\n"), kept
}
