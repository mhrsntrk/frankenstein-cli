package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// overlay splices popup over base with popup's top-left at cell (x, y).
//
// Both arguments are full rendered blocks that may carry ANSI SGR sequences,
// so everything here is measured in terminal columns rather than bytes or
// runes: a styled cell is several bytes wide and an emoji is two columns, and
// either would shear the popup off its frame if the splice counted anything
// but cells. The heavy lifting is delegated to charmbracelet/x/ansi, which
// cuts styled strings without breaking escape sequences.
//
// Coordinates outside base are handled gracefully rather than rejected: the
// caller centres the popup against a window size that can change under it,
// and a resize race must never make the composer disappear. Negative offsets
// clamp to the edge, short base lines are padded with spaces, and a base with
// fewer lines than y plus the popup height grows blank lines to fit.
func overlay(base, popup string, x, y int) string {
	if popup == "" {
		return base
	}

	x = maxInt(x, 0)
	y = maxInt(y, 0)

	baseLines := strings.Split(base, "\n")
	popupLines := strings.Split(popup, "\n")

	// Extend rather than clip when the popup hangs below the last base line.
	// Split/Join round-trips exactly, so base keeps its precise line count
	// plus only this extension: no trailing newline appears or vanishes.
	for len(baseLines) < y+len(popupLines) {
		baseLines = append(baseLines, "")
	}

	for i, pl := range popupLines {
		baseLines[y+i] = spliceLine(baseLines[y+i], pl, x)
	}

	return strings.Join(baseLines, "\n")
}

// spliceLine lays one popup line over one base line starting at column x.
//
// The result is left part of base, then popup, then whatever of base remains
// past the popup's right edge. Styles are firewalled in both directions with
// explicit resets: an open SGR attribute in the base must not tint the popup,
// and an open attribute in the popup must not leak into the base remainder.
// The cost is that base styling after the popup can be lost to the reset,
// which is acceptable for a frame that is redrawn every keystroke anyway.
func spliceLine(baseLine, popupLine string, x int) string {
	popupWidth := ansi.StringWidth(popupLine)

	// A zero-width popup line covers no cells, so the base line stands. This
	// also keeps blank popup rows from erasing content they never covered.
	if popupWidth == 0 {
		return baseLine
	}

	var b strings.Builder

	// Keep the first x columns of the base, padding with spaces when the base
	// line is shorter than the popup's left edge: the popup has to land at
	// its column even over a base that never reached that far.
	left := ansi.Truncate(baseLine, x, "")
	b.WriteString(left)

	if leftWidth := ansi.StringWidth(left); leftWidth < x {
		b.WriteString(strings.Repeat(" ", x-leftWidth))
	}

	// Only a styled left part needs the firewall; resetting unconditionally
	// would litter plain frames with escape sequences the tests then have to
	// wade through.
	if strings.Contains(left, "\x1b") {
		b.WriteString("\x1b[0m")
	}

	b.WriteString(popupLine)

	if strings.Contains(popupLine, "\x1b") {
		b.WriteString("\x1b[0m")
	}

	// The remainder starts at the popup's right edge. TruncateLeft measures
	// in cells and keeps escape sequences intact, so a base styled beyond the
	// popup resumes with its own attributes where they survive.
	if cut := x + popupWidth; cut < ansi.StringWidth(baseLine) {
		b.WriteString(ansi.TruncateLeft(baseLine, cut, ""))
	}

	return b.String()
}
