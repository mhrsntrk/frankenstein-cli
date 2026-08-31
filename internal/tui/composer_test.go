package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/x/ansi"
)

// testComposeState builds a composer around real bubbles models, the same way
// the update path does, so the renderer is exercised against genuine View()
// output rather than stand-in strings.
func testComposeState() *composeState {
	return &composeState{
		to:      textinput.New(),
		cc:      textinput.New(),
		bcc:     textinput.New(),
		subject: textinput.New(),
		body:    textarea.New(),
	}
}

func TestComposerPlaceFitsOnScreen(t *testing.T) {
	screens := []struct{ w, h int }{{120, 40}, {60, 20}}

	modes := []struct {
		name                 string
		minimized, maximized bool
	}{
		{"normal", false, false},
		{"minimized", true, false},
		{"maximized", false, true},
	}

	for _, s := range screens {
		for _, m := range modes {
			lay := composerPlace(s.w, s.h, m.minimized, m.maximized)

			if lay.x < 0 || lay.y < 0 {
				t.Errorf("%s at %dx%d: origin (%d,%d) off screen", m.name, s.w, s.h, lay.x, lay.y)
			}

			if lay.x+lay.w > s.w || lay.y+lay.h > s.h {
				t.Errorf("%s at %dx%d: extent (%d,%d) past screen edge",
					m.name, s.w, s.h, lay.x+lay.w, lay.y+lay.h)
			}
		}
	}
}

func TestComposerPlaceDimensions(t *testing.T) {
	// A wide screen gets the capped size; a small one gives up four cells.
	if lay := composerPlace(120, 40, false, false); lay.w != 62 || lay.h != 18 {
		t.Errorf("normal at 120x40 = %dx%d, want 62x18", lay.w, lay.h)
	}

	if lay := composerPlace(60, 20, false, false); lay.w != 56 || lay.h != 16 {
		t.Errorf("normal at 60x20 = %dx%d, want 56x16", lay.w, lay.h)
	}

	if lay := composerPlace(120, 40, false, true); lay.w != 116 || lay.h != 36 || lay.x != 2 || lay.y != 2 {
		t.Errorf("maximized at 120x40 = %+v, want 116x36 at (2,2)", lay)
	}

	if lay := composerPlace(120, 40, true, false); lay.h != 1 {
		t.Errorf("minimized height = %d, want 1", lay.h)
	}
}

func TestComposerPopupExactGeometry(t *testing.T) {
	screens := []struct{ w, h int }{{120, 40}, {60, 20}}

	for _, s := range screens {
		for _, maximized := range []bool{false, true} {
			c := testComposeState()
			lay := composerPlace(s.w, s.h, false, maximized)

			block, _ := composerPopup(c, lay, false, "you@example.com")

			lines := strings.Split(block, "\n")
			if len(lines) != lay.h {
				t.Fatalf("at %dx%d max=%v: %d lines, want %d", s.w, s.h, maximized, len(lines), lay.h)
			}

			for i, l := range lines {
				if w := ansi.StringWidth(l); w != lay.w {
					t.Errorf("at %dx%d max=%v line %d: width %d, want %d", s.w, s.h, maximized, i, w, lay.w)
				}
			}
		}

		// Minimized is a single bar of exactly the laid-out width.
		c := testComposeState()
		lay := composerPlace(s.w, s.h, true, false)

		block, _ := composerPopup(c, lay, true, "you@example.com")

		if lines := strings.Split(block, "\n"); len(lines) != 1 {
			t.Fatalf("minimized at %dx%d: %d lines, want 1", s.w, s.h, len(lines))
		}

		if w := ansi.StringWidth(block); w != lay.w {
			t.Errorf("minimized at %dx%d: width %d, want %d", s.w, s.h, w, lay.w)
		}
	}
}

func TestComposerPopupCcRowAppears(t *testing.T) {
	lay := composerPlace(120, 40, false, false)

	c := testComposeState()

	block, regions := composerPopup(c, lay, false, "you@example.com")

	if _, ok := regionByID(regions, "togglecc"); !ok {
		t.Error("no togglecc region while the Cc row is hidden")
	}

	if _, ok := regionByID(regions, "field:cc"); ok {
		t.Error("field:cc region present while the Cc row is hidden")
	}

	// Focusing the cc field summons the Cc and Bcc rows and retires the toggle.
	c.field = 1

	block2, regions2 := composerPopup(c, lay, false, "you@example.com")

	if _, ok := regionByID(regions2, "field:cc"); !ok {
		t.Error("no field:cc region while the cc field has focus")
	}

	if _, ok := regionByID(regions2, "field:bcc"); !ok {
		t.Error("no field:bcc region while the cc field has focus")
	}

	if _, ok := regionByID(regions2, "togglecc"); ok {
		t.Error("togglecc region present alongside the Cc row")
	}

	// Both shapes still fill the block exactly.
	for _, b := range []string{block, block2} {
		if n := len(strings.Split(b, "\n")); n != lay.h {
			t.Errorf("block is %d lines, want %d", n, lay.h)
		}
	}
}

func regionByID(regions []Region, id string) (Region, bool) {
	for _, r := range regions {
		if r.ID == id {
			return r, true
		}
	}

	return Region{}, false
}

func TestComposerPopupHitButtons(t *testing.T) {
	c := testComposeState()
	lay := composerPlace(120, 40, false, false)

	_, regions := composerPopup(c, lay, false, "you@example.com")

	// The ✕ sits three cells in from the right edge of the title bar.
	if id, ok := hit(regions, lay.w-3, 0); !ok || id != "close" {
		t.Errorf("hit(%d, 0) = %q, %v; want close", lay.w-3, id, ok)
	}

	// Send is the six-cell button flush right in the footer row, one above
	// the bottom border.
	if id, ok := hit(regions, lay.w-6, lay.h-2); !ok || id != "send" {
		t.Errorf("hit(%d, %d) = %q, %v; want send", lay.w-6, lay.h-2, id, ok)
	}

	// And the cell just outside either button belongs to nothing.
	if id, ok := hit(regions, lay.w-2, 0); ok {
		t.Errorf("hit(%d, 0) = %q; want no region on the border", lay.w-2, id)
	}
}

func TestComposerBarHitButtons(t *testing.T) {
	c := testComposeState()
	lay := composerPlace(120, 40, true, false)

	_, regions := composerPopup(c, lay, true, "you@example.com")

	if id, ok := hit(regions, lay.w-1, 0); !ok || id != "close" {
		t.Errorf("hit(%d, 0) = %q, %v; want close", lay.w-1, id, ok)
	}

	if id, ok := hit(regions, lay.w-3, 0); !ok || id != "maximize" {
		t.Errorf("hit(%d, 0) = %q, %v; want maximize", lay.w-3, id, ok)
	}

	if id, ok := hit(regions, 1, 0); !ok || id != "restore" {
		t.Errorf("hit(1, 0) = %q, %v; want restore", id, ok)
	}
}

func TestComposerPopupFocusStyling(t *testing.T) {
	lay := composerPlace(120, 40, false, false)

	focusTo := testComposeState()
	focusTo.field = 0

	focusSubject := testComposeState()
	focusSubject.field = 3

	a, _ := composerPopup(focusTo, lay, false, "you@example.com")
	b, _ := composerPopup(focusSubject, lay, false, "you@example.com")

	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")

	// Row 2 is To, row 3 Subject (no Cc row in either state). Moving focus
	// must restyle both rows, or the popup never shows where typing lands.
	if al[2] == bl[2] {
		t.Error("To row renders identically focused and unfocused")
	}

	if al[3] == bl[3] {
		t.Error("Subject row renders identically focused and unfocused")
	}

	// Styling must not disturb geometry.
	for i, l := range al {
		if ansi.StringWidth(l) != ansi.StringWidth(bl[i]) {
			t.Errorf("line %d changes width with focus", i)
		}
	}
}
