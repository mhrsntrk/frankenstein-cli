package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestOverlayPlainSplice(t *testing.T) {
	base := "aaaaa\nbbbbb\nccccc"
	popup := "XX\nYY"

	got := overlay(base, popup, 1, 1)
	want := "aaaaa\nbXXbb\ncYYcc"

	if got != want {
		t.Fatalf("overlay = %q, want %q", got, want)
	}
}

func TestOverlayPadsShortBaseLines(t *testing.T) {
	// The second base line never reaches column 4, so the splice has to pad
	// with spaces or the popup would drift left on that row.
	base := "aaaaaaaa\nab"
	popup := "ZZ\nZZ"

	got := overlay(base, popup, 4, 0)
	want := "aaaaZZaa\nab  ZZ"

	if got != want {
		t.Fatalf("overlay = %q, want %q", got, want)
	}
}

func TestOverlayExtendsBaseDownward(t *testing.T) {
	// The popup hangs one row below the last base line; base must grow a
	// blank line rather than clip the popup's bottom edge.
	base := "aaaa\nbbbb"
	popup := "PP\nQQ"

	got := overlay(base, popup, 1, 1)
	want := "aaaa\nbPPb\n QQ"

	if got != want {
		t.Fatalf("overlay = %q, want %q", got, want)
	}

	if lines := strings.Count(got, "\n") + 1; lines != 3 {
		t.Fatalf("line count = %d, want 3", lines)
	}
}

func TestOverlayPopupWiderThanBase(t *testing.T) {
	base := "abc\ndef"
	popup := "WWWWW"

	got := overlay(base, popup, 1, 0)
	want := "aWWWWW\ndef"

	if got != want {
		t.Fatalf("overlay = %q, want %q", got, want)
	}
}

func TestOverlayNegativeCoordinatesClamp(t *testing.T) {
	base := "abcd\nefgh"
	popup := "XY"

	got := overlay(base, popup, -3, -2)
	want := "XYcd\nefgh"

	if got != want {
		t.Fatalf("overlay = %q, want %q", got, want)
	}
}

func TestOverlayEmptyPopupReturnsBase(t *testing.T) {
	base := "abcd\nefgh"

	if got := overlay(base, "", 2, 1); got != base {
		t.Fatalf("overlay with empty popup = %q, want base %q", got, base)
	}
}

func TestOverlayANSIStyleFirewall(t *testing.T) {
	// The whole base line is red with an open attribute at the cut point, and
	// the popup is green without a trailing reset. Neither side may bleed
	// into the other across the splice boundaries.
	base := "\x1b[31mredredred\x1b[0m"
	popup := "\x1b[32mPOP"

	got := overlay(base, popup, 2, 0)

	// Cell content and geometry survive the styling: same visible text, same
	// visible width.
	if plain := ansi.Strip(got); plain != "rePOPdred" {
		t.Fatalf("visible text = %q, want %q", plain, "rePOPdred")
	}

	if w := ansi.StringWidth(got); w != 9 {
		t.Fatalf("visible width = %d, want 9", w)
	}

	// The base cell before the popup was red, so a reset must sit between the
	// base's left part and the popup content or the red would tint the popup.
	popupAt := strings.Index(got, "\x1b[32mPOP")
	if popupAt < 0 {
		t.Fatalf("popup content missing from %q", got)
	}

	if !strings.HasSuffix(got[:popupAt], "\x1b[0m") {
		t.Fatalf("no reset before popup content in %q", got)
	}

	// The popup left green open, so a reset must follow it before the base
	// remainder resumes.
	after := got[popupAt+len("\x1b[32mPOP"):]
	if !strings.HasPrefix(after, "\x1b[0m") {
		t.Fatalf("no reset after popup content in %q", got)
	}
}
