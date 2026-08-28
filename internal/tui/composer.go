package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhrsntrk/frankenstein-cli/internal/tui/heyui"
)

// The composer floats over the mail view the way Proton's web client floats
// its "New message" window over the message list: a bordered popup anchored
// bottom-right, with minimize, maximize and close buttons in the title bar.
// This file only draws the popup block and says where it wants to sit; the
// splicing over the base frame is overlay()'s job.

// composerLayout is where the popup wants to sit on a screen of w×h.
type composerLayout struct {
	x, y, w, h int
}

// Floors below which the popup stops shrinking. A composer squeezed thinner
// than this is unusable anyway, and overlay() clips at the screen edge rather
// than breaking, so overflowing a tiny terminal beats collapsing on it.
const (
	composerMinW = 24
	composerMinH = 10
)

// composerPlace computes the popup geometry: minimized is a single title bar
// anchored bottom-right; maximized is centred and nearly full screen with a
// two-cell margin; normal is anchored bottom-right at min(62, screenW-4) by
// min(18, screenH-4), one row clear of the bottom so the status line under it
// stays readable.
func composerPlace(screenW, screenH int, minimized, maximized bool) composerLayout {
	switch {
	case minimized:
		w := maxInt(minInt(34, screenW-4), 12)

		return composerLayout{x: maxInt(screenW-w-2, 0), y: maxInt(screenH-2, 0), w: w, h: 1}
	case maximized:
		w := maxInt(screenW-4, composerMinW)
		h := maxInt(screenH-4, composerMinH)

		return composerLayout{x: maxInt((screenW-w)/2, 0), y: maxInt((screenH-h)/2, 0), w: w, h: h}
	default:
		w := maxInt(minInt(62, screenW-4), composerMinW)
		h := maxInt(minInt(18, screenH-4), composerMinH)

		return composerLayout{x: maxInt(screenW-w-2, 0), y: maxInt(screenH-h-1, 0), w: w, h: h}
	}
}

// The title bar buttons are single-cell glyphs on purpose: an emoji's width
// varies by terminal, and a mis-measured button shears the border. The trash
// glyph in the footer is the one two-cell exception, and it is measured, not
// assumed.
const (
	glyphMinimize = "─"
	glyphMaximize = "□"
	glyphClose    = "✕"
	glyphRestore  = "▲"
	glyphDiscard  = "🗑"
)

// composerPopup renders the popup block for the given geometry. account is
// the From address. Regions come back in the popup's own coordinates: (0,0)
// is the popup's top-left cell, and the mouse handler adds lay.x, lay.y
// before matching.
//
// This is a pure renderer: it never resizes the embedded models, it only
// calls View() on them. The caller must size them to the geometry first:
// the single-line inputs to a width of lay.w-13 (the border and padding take
// 4 columns, the label another 9), and the body to a width of lay.w-4 and a
// height of lay.h-8 (two less when the Cc and Bcc rows are showing).
func composerPopup(c *composeState, lay composerLayout, minimized bool, account string) (string, []Region) {
	if c == nil || lay.w < 4 || lay.h < 1 {
		return "", nil
	}

	if minimized {
		return composerBar(c, lay)
	}

	inner := lay.w - 4

	var (
		lines   []string
		regions []Region
	)

	// row wraps content in the border with a one-cell pad, clipped and padded
	// to exactly the inner width so every line of the block measures lay.w.
	row := func(content string) {
		lines = append(lines, "│ "+fitCells(content, inner)+" │")
	}

	// label9 is the nine-column label gutter the flat compose view used. The
	// focused field's label takes markStyle where the flat view drew "> ", so
	// focus reads the same way without spending a marker column.
	label9 := func(label string, focused bool) string {
		cell := fmt.Sprintf("%-9s", label)
		if focused {
			return markStyle.Render(cell)
		}

		return dimStyle.Render(cell)
	}

	// Title bar: ┌ title ──── ─ □ ✕ ┐. The buttons are the only clickable
	// cells; the rest of the bar drags nothing, so it gets no region.
	title := heyui.Truncate(composeTitle(c), maxInt(lay.w-11, 1))
	fill := maxInt(lay.w-11-heyui.DisplayWidth(title), 0)

	lines = append(lines, "┌ "+titleStyle.Render(title)+" "+
		dimStyle.Render(strings.Repeat("─", fill))+" "+
		dimStyle.Render(glyphMinimize+" "+glyphMaximize+" "+glyphClose)+" ┐")

	regions = append(regions,
		Region{ID: "minimize", X: lay.w - 7, Y: 0, W: 1, H: 1},
		Region{ID: "maximize", X: lay.w - 5, Y: 0, W: 1, H: 1},
		Region{ID: "close", X: lay.w - 3, Y: 0, W: 1, H: 1},
	)

	// From is not a field, only a statement of which account is writing.
	row(dimStyle.Render(fmt.Sprintf("%-9s", "From")) + dimStyle.Render(account))

	// The Cc and Bcc rows appear together once either has content or focus;
	// until then the To row carries the toggle that summons them, the way
	// Proton's popup does.
	showCC := composeShowCC(c)

	toY := len(lines)

	if showCC {
		row(label9("To", c.field == 0) + c.to.View())
	} else {
		toggle := dimStyle.Render("CC BCC")
		left := fitCells(label9("To", c.field == 0)+c.to.View(), inner-7)

		row(left + " " + toggle)
	}

	regions = append(regions, Region{ID: "field:to", X: 2, Y: toY, W: inner, H: 1})

	if !showCC {
		// After the field region on purpose: hit() searches from the end, so
		// the toggle wins the cells the two share.
		regions = append(regions, Region{ID: "togglecc", X: lay.w - 8, Y: toY, W: 6, H: 1})
	}

	if showCC {
		regions = append(regions, Region{ID: "field:cc", X: 2, Y: len(lines), W: inner, H: 1})
		row(label9("Cc", c.field == 1) + c.cc.View())

		regions = append(regions, Region{ID: "field:bcc", X: 2, Y: len(lines), W: inner, H: 1})
		row(label9("Bcc", c.field == 2) + c.bcc.View())
	}

	regions = append(regions, Region{ID: "field:subject", X: 2, Y: len(lines), W: inner, H: 1})
	row(label9("Subject", c.field == 3) + c.subject.View())

	rule := dimStyle.Render(strings.Repeat("─", inner))
	row(rule)

	// The body takes every row the chrome leaves: eight fixed rows, ten with
	// the Cc and Bcc rows. On a screen too short even for that the body keeps
	// one row and the block is trimmed to lay.h below, losing its bottom edge
	// rather than its text.
	bodyRows := lay.h - 8
	if showCC {
		bodyRows -= 2
	}

	bodyRows = maxInt(bodyRows, 1)

	regions = append(regions, Region{ID: "field:body", X: 2, Y: len(lines), W: inner, H: bodyRows})

	bodyLines := strings.Split(c.body.View(), "\n")

	for i := 0; i < bodyRows; i++ {
		line := ""
		if i < len(bodyLines) {
			line = bodyLines[i]
		}

		row(line)
	}

	row(rule)

	// Footer: 🗑  Not saved …………… [ Send ]. The glyph is measured because an
	// emoji is one or two cells depending on the terminal, and the padding
	// absorbs the difference so Send stays flush right either way.
	footY := len(lines)
	gw := heyui.DisplayWidth(glyphDiscard)
	send := bannerStyle.Render(" Send ")

	pad := inner - gw - 2 - 9 - 6
	if pad >= 1 {
		row(glyphDiscard + "  " + dimStyle.Render("Not saved") + strings.Repeat(" ", pad) + send)
	} else {
		// Too narrow for the save note; the actions matter more.
		row(glyphDiscard + strings.Repeat(" ", maxInt(inner-gw-6, 0)) + send)
	}

	regions = append(regions,
		Region{ID: "discard", X: 2, Y: footY, W: gw, H: 1},
		Region{ID: "send", X: lay.w - 8, Y: footY, W: 6, H: 1},
	)

	lines = append(lines, "└"+strings.Repeat("─", lay.w-2)+"┘")

	if len(lines) > lay.h {
		lines = lines[:lay.h]
	}

	return strings.Join(lines, "\n"), regions
}

// composeShowCC reports whether the Cc and Bcc rows are drawn: once either
// field has content or focus. The renderer and the sizing both ask, so the
// answer lives once.
func composeShowCC(c *composeState) bool {
	return c.field == 1 || c.field == 2 ||
		strings.TrimSpace(c.cc.Value()) != "" || strings.TrimSpace(c.bcc.Value()) != ""
}

// composerBar is the minimized composer: one title bar parked bottom-right,
// still there so a half-written mail is never out of sight. Clicking the bar
// restores it; the buttons keep their own meanings.
func composerBar(c *composeState, lay composerLayout) (string, []Region) {
	title := heyui.Truncate(composeTitle(c), maxInt(lay.w-7, 1))
	fill := maxInt(lay.w-7-heyui.DisplayWidth(title), 0)

	bar := glyphRestore + " " + titleStyle.Render(title) + " " +
		dimStyle.Render(strings.Repeat("─", fill)) + " " +
		dimStyle.Render(glyphMaximize+" "+glyphClose)

	regions := []Region{
		{ID: "restore", X: 0, Y: 0, W: maxInt(lay.w-4, 1), H: 1},
		{ID: "maximize", X: lay.w - 3, Y: 0, W: 1, H: 1},
		{ID: "close", X: lay.w - 1, Y: 0, W: 1, H: 1},
	}

	return bar, regions
}

// fitCells clips a possibly styled string to exactly w printable columns,
// padding with spaces. Cells, not runes: the embedded views carry escape
// sequences and the odd wide glyph, and either would push the popup's right
// border out of line if this counted anything else.
func fitCells(s string, w int) string {
	if w <= 0 {
		return ""
	}

	s = ansi.Truncate(s, w, "")

	if n := ansi.StringWidth(s); n < w {
		s += strings.Repeat(" ", w-n)
	}

	return s
}
