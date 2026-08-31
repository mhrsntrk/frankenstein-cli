package habit

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestEveryHabitEmojiIsTwoCellsWide holds the invariant the Icon doc comment
// relies on. Habit icons are drawn into calendar and list rows whose columns are
// laid out from a measured width, so a one-cell or three-cell emoji does not
// just look wrong: the terminal advances by what it drew, and every cell to the
// right of it on that row shifts.
//
// The width is measured with ansi.StringWidth rather than heyui's displayWidth
// because heyui imports this package, so a test here cannot import heyui back.
// The two agree on these clusters anyway: none of them is a ZWJ sequence, a
// flag pair, a skin-toned emoji or a variation-selector base, which are the only
// cases where displayWidth departs from the table.
func TestEveryHabitEmojiIsTwoCellsWide(t *testing.T) {
	for _, icon := range Icons {
		if got := ansi.StringWidth(icon.Emoji); got != 2 {
			t.Errorf("icon %q: emoji %q is %d cells wide, want 2", icon.Name, icon.Emoji, got)
		}
	}
}

// TestEveryIconNameIsUnique guards the lookup in EmojiFor, which answers the
// first match and would silently hide a duplicate behind it.
func TestEveryIconNameIsUnique(t *testing.T) {
	seen := make(map[string]bool, len(Icons))
	for _, icon := range Icons {
		if seen[icon.Name] {
			t.Errorf("icon name %q appears twice", icon.Name)
		}
		seen[icon.Name] = true
	}
}
