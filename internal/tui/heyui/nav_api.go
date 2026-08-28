package heyui

// The chrome renderers, exported so this project's header can be drawn by
// hey-cli's code rather than an imitation of it.

// NavItem is one entry in a navigation row. Shortcut is the letter or digit
// underlined inside the label.
type NavItem struct {
	Label    string
	Shortcut string
}

// TopRule draws "──── title ────────── account ──" across the width.
//
// Upstream hardcodes its own wordmark; this takes the title as an argument,
// because HEY is 37signals' trademark.
func TopRule(w int, title, account string) string { return renderTopRule(w, title, account) }

// Rule draws "──── label ────" with the label centred, or a plain rule when
// the label is empty.
func Rule(w int, label string) string { return renderRule(w, label) }

// NavRow draws a row of navigation items, centred, with the selected one
// highlighted. Shortcut letters are emphasised the way upstream does it.
//
// Pass -1 for selected when nothing in the row is current. Upstream always has
// a selection and indexes its slice with it directly, so an out-of-range value
// panics; it is clamped here rather than by editing their file.
func NavRow(items []NavItem, selected int, focused bool, w int) string {
	if len(items) == 0 {
		return ""
	}

	converted := make([]navItem, 0, len(items))

	for _, i := range items {
		converted = append(converted, navItem{label: i.Label, shortcut: i.Shortcut})
	}

	// A row with nothing current still has to scroll around some anchor, and
	// the first item is the least surprising one. focused=false keeps it from
	// reading as a selection.
	if selected < 0 || selected >= len(converted) {
		return renderNavRow(converted, 0, false, w, true)
	}

	return renderNavRow(converted, selected, focused, w, true)
}

// Center places text in the middle of a width.
func Center(text string, w int) string { return centerText(text, w) }
