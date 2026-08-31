package render

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// navItem is one entry in a navigation row.
type navItem struct {
	shortcut string // Shift+letter shortcut, underlined inside the label
	label    string
}

// renderRule draws a horizontal rule with a centered label:
//
//	——————————————————— label ———————————————————
func renderRule(width int, label string) string {
	if width <= 0 {
		return ""
	}
	rule := lipgloss.NewStyle().Foreground(colorChrome)
	if label == "" || width < 3 {
		return rule.Render(strings.Repeat("─", width))
	}
	label = truncateStr(label, width-2)
	padded := " " + label + " "
	padLen := lipgloss.Width(padded)
	ruleLen := max(width-padLen, 0)
	left := ruleLen / 2
	right := ruleLen - left
	line := strings.Repeat("─", left) + padded + strings.Repeat("─", right)
	return rule.Render(line)
}

// renderNavLabel renders a nav label in the given style, underlining the
// first occurrence of the shortcut letter (the Windows menu convention).
// A shortcut absent from the label — a number — is shown as an underlined
// prefix instead: "1 Inbox".
func renderNavLabel(label, shortcut string, base lipgloss.Style) string {
	if shortcut == "" {
		return base.Render(label)
	}
	// A letter shortcut is a mnemonic and is underlined where it occurs in the
	// word; a digit is a positional accelerator and belongs in front of the
	// label. Underlining a digit in place finds it inside an unread count --
	// "1 Inbox (13)" loses its prefix and underlines the 1 of 13 instead.
	if isDigits(shortcut) {
		return base.Underline(true).Render(shortcut) + base.Render(" "+label)
	}
	idx := strings.Index(strings.ToLower(label), strings.ToLower(shortcut))
	if idx < 0 {
		return base.Underline(true).Render(shortcut) + base.Render(" "+label)
	}
	end := idx + len(shortcut)
	out := base.Underline(true).Render(label[idx:end])
	if idx > 0 {
		out = base.Render(label[:idx]) + out
	}
	if end < len(label) {
		out += base.Render(label[end:])
	}
	return out
}

// renderNavRow draws a row of nav items with the selected one bolded.
// If centered is true, the row is horizontally centered within width.
// When items overflow the available width, the row scrolls horizontally
// to keep the selected item visible and shows ‹/› indicators.
func renderNavRow(items []navItem, selected int, focused bool, width int, centered bool) string {
	const sep = "  "
	sepW := lipgloss.Width(sep)

	// Pre-render each item and measure its display width.
	type rendered struct {
		str string
		w   int
	}
	all := make([]rendered, len(items))
	totalW := 0
	for i, item := range items {
		// Tabs are always bold. The selected tab uses the active color when
		// its row has focus and the less prominent primary color otherwise.
		style := lipgloss.NewStyle().Foreground(colorChrome).Bold(true)
		if i == selected {
			if focused {
				style = style.Foreground(colorActive)
			} else {
				style = style.Foreground(colorPrimary)
			}
		}
		s := renderNavLabel(item.label, item.shortcut, style)
		w := lipgloss.Width(s)
		all[i] = rendered{s, w}
		totalW += w
	}
	totalW += sepW * max(len(items)-1, 0) // separators

	// If everything fits, no scrolling needed.
	if totalW <= width {
		parts := make([]string, len(all))
		for i, r := range all {
			parts[i] = r.str
		}
		row := strings.Join(parts, sep)
		if centered {
			return centerText(row, width)
		}
		return row
	}

	// Scrolling: find the largest window of items around `selected` that fits.
	leftArrow := lipgloss.NewStyle().Foreground(colorChrome).Render("‹ ")
	rightArrow := lipgloss.NewStyle().Foreground(colorChrome).Render(" ›")
	arrowW := lipgloss.Width(leftArrow) // both arrows have the same width

	// Start with the selected item and expand outward.
	lo, hi := selected, selected
	usedW := all[selected].w

	for {
		expandedLeft, expandedRight := false, false

		// Try expanding left.
		if lo > 0 {
			need := sepW + all[lo-1].w
			reserveR := 0
			if hi < len(items)-1 {
				reserveR = arrowW
			}
			reserveL := 0
			if lo-1 > 0 {
				reserveL = arrowW
			}
			if usedW+need+reserveL+reserveR <= width {
				lo--
				usedW += need
				expandedLeft = true
			}
		}

		// Try expanding right.
		if hi < len(items)-1 {
			need := sepW + all[hi+1].w
			reserveL := 0
			if lo > 0 {
				reserveL = arrowW
			}
			reserveR := 0
			if hi+1 < len(items)-1 {
				reserveR = arrowW
			}
			if usedW+need+reserveL+reserveR <= width {
				hi++
				usedW += need
				expandedRight = true
			}
		}

		if !expandedLeft && !expandedRight {
			break
		}
	}

	// Build the visible row.
	var b strings.Builder
	if lo > 0 {
		b.WriteString(leftArrow)
	}
	for i := lo; i <= hi; i++ {
		if i > lo {
			b.WriteString(sep)
		}
		b.WriteString(all[i].str)
	}
	if hi < len(items)-1 {
		b.WriteString(rightArrow)
	}

	row := b.String()
	if centered {
		return centerText(row, width)
	}
	return row
}

// centerText pads text so it sits in the middle of width.
func centerText(text string, width int) string {
	pad := max((width-lipgloss.Width(text))/2, 0)
	return strings.Repeat(" ", pad) + text
}

// renderTopRule draws the top rule with HEY centered and the account
// aligned to the right, both bold:
//
//	─────────── HEY ─────────── jz@example.com ──
func renderTopRule(width int, title, account string) string {
	ruleStyle := lipgloss.NewStyle().Foreground(colorChrome)
	labelStyle := lipgloss.NewStyle().Foreground(colorChrome).Bold(true)

	accountWidth := 0
	if account != "" {
		accountWidth = lipgloss.Width(account) + 2 // surrounding spaces
	}
	// Upstream hardcodes its own wordmark here. This takes the title as an
	// argument instead: HEY is 37signals' trademark and does not belong in
	// another client's title bar.
	titleWidth := lipgloss.Width(title) + 2 // surrounding spaces

	const tail = 2
	// Centre the title; when the account leaves no room, shift the title left
	// rather than giving up the right alignment.
	left := min((width-titleWidth)/2, width-titleWidth-accountWidth-tail-1)
	mid := width - left - titleWidth - accountWidth - tail

	if left < 1 || mid < 1 {
		return renderRule(width, strings.TrimSuffix(title+" · "+account, " · "))
	}

	var b strings.Builder
	b.WriteString(ruleStyle.Render(strings.Repeat("─", left)))
	b.WriteString(" " + labelStyle.Render(title) + " ")
	b.WriteString(ruleStyle.Render(strings.Repeat("─", mid)))
	if account != "" {
		b.WriteString(" " + labelStyle.Render(account) + " ")
	}
	b.WriteString(ruleStyle.Render(strings.Repeat("─", tail)))
	return b.String()
}

// renderHeader renders the full 3-row navigation header.

// isDigits reports whether every rune is a decimal digit, which is what
// separates a positional accelerator from a mnemonic letter.
func isDigits(s string) bool {
	if s == "" {
		return false
	}

	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}
