package tui

import (
	"strings"
	"testing"

	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
)

func TestRenderBodyPlainTextIsUntouched(t *testing.T) {
	in := "line one\nline two"

	got := renderBody(mail.Body{MIMEType: "text/plain", Content: in})
	if got != in {
		t.Errorf("plain text was altered:\n%q", got)
	}
}

func TestRenderBodyStripsHTML(t *testing.T) {
	html := `<html><head><style>p{color:red}</style></head>
<body><script>alert('x')</script>
<p>Hello &amp; welcome</p>
<p>Second&nbsp;paragraph</p>
<div>Third</div></body></html>`

	got := renderBody(mail.Body{MIMEType: "text/html; charset=utf-8", Content: html})

	for _, want := range []string{"Hello & welcome", "Second paragraph", "Third"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}

	// Script and style content must not leak into the reading pane.
	for _, unwanted := range []string{"alert", "color:red", "<p>", "</div>"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("leaked %q in:\n%s", unwanted, got)
		}
	}

	// Tag stripping leaves long runs of blank lines; they should collapse.
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("blank lines not collapsed:\n%q", got)
	}
}

func TestRenderBodyHandlesUnclosedTag(t *testing.T) {
	// A truncated document must not hang or panic.
	got := renderBody(mail.Body{MIMEType: "text/html", Content: "<div>text<script>oops"})

	if strings.Contains(got, "oops") {
		t.Errorf("unclosed script leaked: %q", got)
	}
}

func TestWrapBreaksOnWords(t *testing.T) {
	lines := wrap("the quick brown fox jumps over the lazy dog", 12)

	for _, l := range lines {
		if len([]rune(l)) > 12 {
			t.Errorf("line %q is longer than 12", l)
		}
	}

	if strings.Join(lines, " ") != "the quick brown fox jumps over the lazy dog" {
		t.Errorf("wrapping lost or reordered words: %v", lines)
	}
}

func TestWrapEmptyParagraph(t *testing.T) {
	if got := wrap("", 20); len(got) != 1 || got[0] != "" {
		t.Errorf("wrap(\"\") = %v, want one empty line", got)
	}
}

func TestWrapWordLongerThanWidth(t *testing.T) {
	// An unbreakable token must still come out, not vanish.
	lines := wrap("short supercalifragilistic", 8)

	joined := strings.Join(lines, " ")
	if !strings.Contains(joined, "supercalifragilistic") {
		t.Errorf("long word lost: %v", lines)
	}
}

func TestTruncateStr(t *testing.T) {
	cases := []struct {
		in    string
		width int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello", 4, "hel…"},
		{"hello", 1, "…"},
		{"hello", 0, ""},
		{"a\nb", 5, "a b"},
	}

	for _, c := range cases {
		if got := truncateStr(c.in, c.width); got != c.want {
			t.Errorf("truncateStr(%q, %d) = %q, want %q", c.in, c.width, got, c.want)
		}
	}
}

func TestTruncateStrCountsRunesNotBytes(t *testing.T) {
	// Five runes, more than five bytes. Truncating by bytes would cut a rune
	// in half and print a replacement character.
	if got := truncateStr("günəş!", 6); got != "günəş!" {
		t.Errorf("multibyte string truncated early: %q", got)
	}
}

func TestPadToIgnoresEscapeSequences(t *testing.T) {
	// A styled string is longer in bytes than on screen; padding to the screen
	// width has to measure printable runes only or the selection bar overruns.
	styled := "\x1b[1mabc\x1b[0m"

	got := padTo(styled, 6)

	if !strings.HasPrefix(got, styled) {
		t.Fatalf("padding mangled the styled string: %q", got)
	}

	if trailing := strings.TrimPrefix(got, styled); trailing != "   " {
		t.Errorf("padded with %d spaces, want 3", len(trailing))
	}
}

func TestScrollToKeepsCursorVisible(t *testing.T) {
	cases := []struct {
		first, cursor, size, want int
	}{
		{0, 0, 10, 0},  // already visible
		{0, 5, 10, 0},  // still visible, do not move
		{0, 12, 10, 3}, // below the window, scroll down minimally
		{5, 2, 10, 2},  // above the window, scroll up to the cursor
		{0, 0, 0, 0},   // degenerate size
	}

	for _, c := range cases {
		if got := scrollTo(c.first, c.cursor, c.size); got != c.want {
			t.Errorf("scrollTo(%d, %d, %d) = %d, want %d",
				c.first, c.cursor, c.size, got, c.want)
		}
	}
}

func TestClamp(t *testing.T) {
	if got := clamp(5, 0, 3); got != 3 {
		t.Errorf("clamp above = %d, want 3", got)
	}

	if got := clamp(-1, 0, 3); got != 0 {
		t.Errorf("clamp below = %d, want 0", got)
	}

	if got := clamp(2, 0, 3); got != 2 {
		t.Errorf("clamp inside = %d, want 2", got)
	}
}
