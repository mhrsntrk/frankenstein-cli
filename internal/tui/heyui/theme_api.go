package heyui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// The chrome outside the list — the title bar, the nav row, the box switcher,
// the footer — is this project's own, but it has to sit in the same palette as
// the rows hey-cli draws. These expose the theme so it does, rather than a
// second set of colours being invented alongside it.

// Primary is the accent: titles, the cursor, sender names.
func Primary() color.Color { return colorPrimary }

// Muted is decorative filler.
func Muted() color.Color { return colorMuted }

// Bright is emphasised text.
func Bright() color.Color { return colorBright }

// Alert is the unread dot and anything needing attention.
func Alert() color.Color { return colorAlert }

// Link is the colour subjects and hyperlinks take.
func Link() color.Color { return colorLink }

// MutedStyle dims text with the faint attribute, which is what upstream uses
// for excerpts rather than a fixed grey.
func MutedStyle() lipgloss.Style { return styleMuted }

// SelectionStyle applies the highlight upstream uses for the cursor row.
func SelectionStyle(s lipgloss.Style) lipgloss.Style { return selectionStyle(s) }

// DisplayWidth is the printable width of a string in terminal columns,
// counting grapheme clusters rather than runes so an emoji is the two columns
// it actually occupies.
func DisplayWidth(s string) int { return displayWidth(s) }

// Truncate shortens a string to n columns.
func Truncate(s string, n int) string { return truncateToWidth(s, n) }
