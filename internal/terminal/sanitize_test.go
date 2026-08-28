package terminal

import (
	"html"
	"strings"
	"testing"
	"unicode/utf8"
)

// sanitizeCase is one input to Sanitize. When exact is true the output is pinned
// to want; when it is false only the safety properties are asserted, because the
// exact bytes depend on how the underlying ANSI parser consumes a malformed or
// library-defined sequence, and over-stripping there is acceptable.
type sanitizeCase struct {
	name  string
	in    string
	want  string
	exact bool
}

var sanitizeCases = []sanitizeCase{
	// CSI sequences: the whole sequence goes, parameters and all.
	{name: "csi color", in: "\x1b[31mred\x1b[0m", want: "red", exact: true},
	{name: "csi cursor up", in: "\x1b[2Aup", want: "up", exact: true},
	{name: "csi cursor position", in: "\x1b[10;20Hmoved", want: "moved", exact: true},
	{name: "csi erase display", in: "\x1b[2Jclear", want: "clear", exact: true},
	{name: "csi erase line", in: "\x1b[2Kkept", want: "kept", exact: true},

	// OSC, with both terminators. The payload is part of the sequence.
	{name: "osc bel terminated", in: "\x1b]0;evil title\x07name", want: "name", exact: true},
	{name: "osc st terminated", in: "\x1b]8;;https://evil.example\x1b\\label", want: "label", exact: true},

	// DCS, APC and PM are string sequences like OSC.
	{name: "dcs", in: "\x1bP+q544e\x1b\\after", want: "after", exact: true},
	{name: "apc", in: "\x1b_payload\x1b\\after", want: "after", exact: true},
	{name: "pm", in: "\x1b^payload\x1b\\after", want: "after", exact: true},

	// Bare ESC. A lone one is dropped; ESC before a letter is consumed as a
	// two-byte escape sequence by the ANSI parser, which may take the letter with
	// it — either way no ESC survives, which is what matters.
	{name: "lone esc", in: "\x1b", want: "", exact: true},
	{name: "esc before letter", in: "a\x1bb"},
	{name: "esc before uppercase", in: "\x1bZ"},

	// An ESC that arrived as an HTML entity and was decoded upstream is a real
	// ESC by the time it reaches Sanitize, and is neutralized like any other.
	{name: "html entity decoded esc", in: html.UnescapeString("&#27;[31mURGENT&#27;[0m"), want: "URGENT", exact: true},

	// C1 controls, raw (invalid UTF-8) and as UTF-8-encoded runes. A raw 0x9B is
	// an 8-bit CSI to the parser, so what follows it may be consumed as a
	// sequence; the property is that neither the C1 nor any control survives.
	{name: "raw c1 byte", in: "\x80abc", want: "abc", exact: true},
	{name: "raw c1 csi with payload", in: "\x9b31mabc"},
	{name: "utf8 c1 nel", in: "a\u0085b", want: "ab", exact: true},
	{name: "utf8 c1 csi", in: "a\u009b31mb"},
	{name: "utf8 c1 osc", in: "a\u009d0;tb"},

	// Raw control characters.
	{name: "bel", in: "a\x07b", want: "ab", exact: true},
	{name: "backspace", in: "a\x08b", want: "ab", exact: true},
	{name: "vertical tab", in: "a\x0bb", want: "ab", exact: true},
	{name: "form feed", in: "a\x0cb", want: "ab", exact: true},
	{name: "del", in: "a\x7fb", want: "ab", exact: true},
	{name: "nul", in: "a\x00b", want: "ab", exact: true},

	// Newlines and tabs are text and survive Sanitize; a carriage return is a
	// control and does not, whether paired with a newline or alone.
	{name: "newline and tab kept", in: "a\tb\nc", want: "a\tb\nc", exact: true},
	{name: "crlf keeps only newline", in: "line1\r\nline2", want: "line1\nline2", exact: true},
	{name: "lone cr dropped", in: "abc\rdef", want: "abcdef", exact: true},

	// Bidirectional controls.
	{name: "rlo extension spoof", in: "invoice\u202efdp.exe", want: "invoicefdp.exe", exact: true},
	{name: "lrm and rlm", in: "a\u200eb\u200fc", want: "abc", exact: true},
	{name: "isolates", in: "\u2066a\u2067b\u2068c\u2069", want: "abc", exact: true},
	{name: "alm embeddings overrides", in: "x\u061c\u202a\u202b\u202c\u202dy", want: "xy", exact: true},

	// Invisible format characters.
	{name: "zero width space", in: "pay\u200bpal", want: "paypal", exact: true},
	{name: "soft hyphen", in: "invoice\u00adpdf.exe", want: "invoicepdf.exe", exact: true},
	{name: "word joiner and bom", in: "a\u2060b\ufeffc", want: "abc", exact: true},
	{name: "combining grapheme joiner", in: "a\u034fb", want: "ab", exact: true},

	// The zero width joiner and non-joiner: dropped between ASCII, kept where
	// they can join.
	{name: "zwj inside ascii", in: "pay\u200dpal", want: "paypal", exact: true},
	{name: "zwnj inside ascii", in: "pay\u200cpal", want: "paypal", exact: true},
	{
		name:  "zwj emoji family survives",
		in:    "\U0001f468\u200d\U0001f469\u200d\U0001f467\u200d\U0001f466", // man, woman, girl, boy joined by ZWJ
		want:  "\U0001f468\u200d\U0001f469\u200d\U0001f467\u200d\U0001f466",
		exact: true,
	},
	{name: "zwj run collapses to one", in: "\U0001f468\u200d\u200d\U0001f469", want: "\U0001f468\u200d\U0001f469", exact: true},
	{name: "leading zwj dropped", in: "\u200d\U0001f468", want: "\U0001f468", exact: true},
	{name: "trailing zwj dropped", in: "\U0001f468\u200d", want: "\U0001f468", exact: true},
	{name: "persian zwnj kept", in: "۱۰\u200cها", want: "۱۰\u200cها", exact: true},
	// A joiner's fate is decided on what is kept, so one held across a dropped
	// bidi control still joins the bases on either side.
	{name: "zwj across dropped control", in: "\U0001f468\u200d\u202e\U0001f469", want: "\U0001f468\u200d\U0001f469", exact: true},

	// Combining marks: at most eight per base, the counter resetting on each new
	// base. Variation selectors are marks and legitimate stacks fit under the cap.
	{name: "zalgo trimmed to cap", in: "e" + strings.Repeat("\u0301", 20), want: "e" + strings.Repeat("\u0301", 8), exact: true},
	{name: "mark cap resets per base", in: "a" + strings.Repeat("\u0301", 10) + "b\u0301\u0302", want: "a" + strings.Repeat("\u0301", 8) + "b\u0301\u0302", exact: true},
	{name: "keycap emoji", in: "1\ufe0f\u20e3", want: "1\ufe0f\u20e3", exact: true},
	{name: "red heart vs16", in: "❤\ufe0f", want: "❤\ufe0f", exact: true},
	{name: "decomposed vietnamese", in: "Vie\u0323\u0302t", want: "Vie\u0323\u0302t", exact: true},

	// Invalid UTF-8 is dropped byte by byte; a replacement character the input
	// genuinely carried is kept.
	{name: "invalid utf8 bytes", in: "\xff\xfeab", want: "ab", exact: true},
	{name: "truncated multibyte", in: "caf\xc3 e", want: "caf e", exact: true},
	{name: "surrogate half", in: "ab\xed\xa0\x80cd", want: "abcd", exact: true},
	{name: "carried replacement char kept", in: "a\ufffdb", want: "a\ufffdb", exact: true},

	// Ordinary text passes through untouched.
	{name: "empty", in: "", want: "", exact: true},
	{name: "plain ascii", in: "Hello, world. 100% [ok] <fine>", want: "Hello, world. 100% [ok] <fine>", exact: true},
	{name: "turkish", in: "Şükrü öğle İzmir'de ışık çğüşöı", want: "Şükrü öğle İzmir'de ışık çğüşöı", exact: true},
	{name: "cjk", in: "メール 電子郵件 이메일", want: "メール 電子郵件 이메일", exact: true},
	{name: "emoji with skin tone", in: "thumbs \U0001f44d\U0001f3fd up", want: "thumbs \U0001f44d\U0001f3fd up", exact: true},
}

// assertSafe fails if out still holds anything Sanitize exists to remove: an
// escape or control character (newline and tab excepted), a C1 control, a bidi
// control, an invisible format character, or an invalid byte.
func assertSafe(t *testing.T, out string) {
	t.Helper()
	if !utf8.ValidString(out) {
		t.Errorf("output %q is not valid UTF-8", out)
	}
	for _, r := range out {
		switch {
		case r == '\n' || r == '\t':
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			t.Errorf("output %q contains control character %U", out, r)
		case isBidiControl(r):
			t.Errorf("output %q contains bidi control %U", out, r)
		case isInvisible(r):
			t.Errorf("output %q contains invisible format character %U", out, r)
		}
	}
}

func TestSanitize(t *testing.T) {
	for _, tc := range sanitizeCases {
		t.Run(tc.name, func(t *testing.T) {
			out := Sanitize(tc.in)
			if tc.exact && out != tc.want {
				t.Errorf("Sanitize(%q) = %q, want %q", tc.in, out, tc.want)
			}
			assertSafe(t, out)
		})
	}
}

// TestSanitizeIdempotent holds Sanitize to its own claim: sanitizing twice is
// the same as once, for every case in the table.
func TestSanitizeIdempotent(t *testing.T) {
	for _, tc := range sanitizeCases {
		t.Run(tc.name, func(t *testing.T) {
			once := Sanitize(tc.in)
			if twice := Sanitize(once); twice != once {
				t.Errorf("Sanitize(Sanitize(%q)): got %q, want %q", tc.in, twice, once)
			}
		})
	}
}

func TestSanitizeLine(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "newline and tab become spaces", in: "a\tb\nc", want: "a b c"},
		{name: "crlf becomes one space", in: "line1\r\nline2", want: "line1 line2"},
		{name: "lone cr dropped", in: "abc\rdef", want: "abcdef"},
		{name: "escape and newline together", in: "\x1b[31mtwo\nlines\x1b[0m", want: "two lines"},
		{name: "single line unchanged", in: "Şükrü / İzmir", want: "Şükrü / İzmir"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := SanitizeLine(tc.in)
			if out != tc.want {
				t.Errorf("SanitizeLine(%q) = %q, want %q", tc.in, out, tc.want)
			}
			assertSafe(t, out)
		})
	}
}

// TestSanitizeLineSingleLine runs the whole Sanitize table through SanitizeLine:
// whatever the input, the result fits one line, stays safe, and sanitizing it
// again changes nothing.
func TestSanitizeLineSingleLine(t *testing.T) {
	for _, tc := range sanitizeCases {
		t.Run(tc.name, func(t *testing.T) {
			out := SanitizeLine(tc.in)
			if strings.ContainsAny(out, "\n\t\r") {
				t.Errorf("SanitizeLine(%q) = %q still contains a line break or tab", tc.in, out)
			}
			assertSafe(t, out)
			if again := SanitizeLine(out); again != out {
				t.Errorf("SanitizeLine(SanitizeLine(%q)): got %q, want %q", tc.in, again, out)
			}
		})
	}
}
