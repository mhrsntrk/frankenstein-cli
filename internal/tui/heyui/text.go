package heyui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// The text helpers hey-cli's list renderer shared with its chrome. The list
// itself is gone -- this project draws its own, in the order the store gave --
// but the calendar grids and the theme's Truncate still measure and cut text
// through these, so they outlive the renderer they were copied with.

// sectionHeader renders a section label with a rule filling the rest of the
// width: "All day ──────────".
func sectionHeader(label string, width int) string {
	s := lipgloss.NewStyle().Foreground(colorChrome).Bold(true).Render(label)
	if fill := width - lipgloss.Width(label) - 3; fill > 0 {
		s += " " + lipgloss.NewStyle().Foreground(colorChrome).Render(strings.Repeat("─", fill))
	}

	return s
}

// hintedSectionHeader is a section label with a hint on its right, where the
// HEY web app puts a section's buttons: "Habits ──── b to manage".
func hintedSectionHeader(label, hint string, width int) string {
	rule := lipgloss.NewStyle().Foreground(colorChrome)

	fill := width - lipgloss.Width(label) - lipgloss.Width(hint) - 4
	if fill < 1 {
		return sectionHeader(label, width)
	}

	return rule.Bold(true).Render(label) + " " +
		rule.Render(strings.Repeat("─", fill)) + " " +
		styleMuted.Render(hint)
}

// truncateToWidth trims s so its rendered width fits in w cells, appending
// "..." when anything was cut. Returns "" when w cannot hold the ellipsis.
func truncateToWidth(s string, w int) string {
	if displayWidth(s) <= w {
		return s
	}

	if w <= 3 {
		return ""
	}

	return fitGraphemes(s, w-3) + "..."
}

// fitGraphemes keeps whole grapheme clusters from the front of s until the next
// one would not fit in width cells, so a cut never lands inside an emoji
// sequence or between a base letter and its combining marks.
func fitGraphemes(s string, width int) string {
	var b strings.Builder

	for s != "" {
		cluster, clusterWidth := firstCluster(s)
		if cluster == "" || clusterWidth > width {
			break
		}

		b.WriteString(cluster)
		width -= clusterWidth
		s = s[len(cluster):]
	}

	return b.String()
}
