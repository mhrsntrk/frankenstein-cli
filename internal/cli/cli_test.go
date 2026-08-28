package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
)

// --- parseAddressList ---------------------------------------------------

func TestParseAddressList(t *testing.T) {
	cases := []struct {
		name    string
		in      []string
		want    []mail.Address
		wantErr string
	}{
		{
			name: "bare address",
			in:   []string{"j@example.com"},
			want: []mail.Address{{Address: "j@example.com"}},
		},
		{
			name: "named address",
			in:   []string{"Jane <j@example.com>"},
			want: []mail.Address{{Name: "Jane", Address: "j@example.com"}},
		},
		{
			// The comma inside the quotes is part of the name, not a
			// separator; splitting on it made two broken halves.
			name: "quoted name carrying a comma",
			in:   []string{`"Doe, John" <j@example.com>`},
			want: []mail.Address{{Name: "Doe, John", Address: "j@example.com"}},
		},
		{
			name: "comma separated pair",
			in:   []string{"a@example.com, b@example.com"},
			want: []mail.Address{{Address: "a@example.com"}, {Address: "b@example.com"}},
		},
		{
			name: "repeated flag",
			in:   []string{"a@example.com", "Jane <b@example.com>"},
			want: []mail.Address{{Address: "a@example.com"}, {Name: "Jane", Address: "b@example.com"}},
		},
		{
			name:    "typo with no at sign",
			in:      []string{"janeexample.com"},
			wantErr: "not an email address",
		},
		{
			// The good half must not paper over the bad one.
			name:    "typo hiding behind a valid address",
			in:      []string{"a@example.com, janeexample.com"},
			wantErr: "not an email address",
		},
		{
			name: "empty input is not an error",
			in:   nil,
			want: nil,
		},
		{
			name: "blank string is not an error",
			in:   []string{" "},
			want: nil,
		},
		{
			name: "trailing comma",
			in:   []string{"a@example.com,"},
			want: []mail.Address{{Address: "a@example.com"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAddressList(tc.in)

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("err = %v", err)
			}

			if len(got) != len(tc.want) {
				t.Fatalf("got %d addresses %v, want %d", len(got), got, len(tc.want))
			}

			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("addr[%d] = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// --- Emit ---------------------------------------------------------------

// An empty listing is an empty list. A nil slice encoding as null would make
// every JSON consumer carry a null-check that exists only because Go
// distinguishes nil from empty.
func TestEmitEncodesNilSliceAsEmptyList(t *testing.T) {
	var buf bytes.Buffer

	app := &App{JSON: true, Out: &buf}

	var none []mail.Conversation

	if err := app.Emit(none, nil); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	if got := strings.TrimSpace(buf.String()); got != "[]" {
		t.Errorf("nil slice encoded as %q, want []", got)
	}
}

func TestEmitLeavesRealValuesAlone(t *testing.T) {
	var buf bytes.Buffer

	app := &App{JSON: true, Out: &buf}

	if err := app.Emit([]string{"a"}, nil); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	if got := strings.TrimSpace(buf.String()); got != "[\n  \"a\"\n]" {
		t.Errorf("slice encoded as %q", got)
	}

	buf.Reset()

	if err := app.Emit(map[string]bool{"ok": true}, nil); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	if got := strings.TrimSpace(buf.String()); !strings.Contains(got, `"ok": true`) {
		t.Errorf("map encoded as %q", got)
	}
}

func TestEmitCallsHumanWithoutJSON(t *testing.T) {
	var buf bytes.Buffer

	app := &App{JSON: false, Out: &buf}

	err := app.Emit([]string{"ignored"}, func(w io.Writer) {
		_, _ = io.WriteString(w, "human")
	})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}

	if buf.String() != "human" {
		t.Errorf("human output = %q", buf.String())
	}
}

// --- bodyFrom -----------------------------------------------------------

func TestBodyFrom(t *testing.T) {
	content := func(s string) func() (string, error) {
		return func() (string, error) { return s, nil }
	}

	refuse := func(name string, t *testing.T) func() (string, error) {
		return func() (string, error) {
			t.Errorf("%s ran and should not have", name)

			return "", nil
		}
	}

	t.Run("the flag wins", func(t *testing.T) {
		got, err := bodyFrom("from the flag", true, true, refuse("stdin", t), refuse("editor", t))
		if err != nil || got != "from the flag" {
			t.Errorf("got %q, %v", got, err)
		}
	})

	t.Run("a pipe with text is the body", func(t *testing.T) {
		got, err := bodyFrom("", true, false, content("piped text\n"), refuse("editor", t))
		if err != nil || got != "piped text\n" {
			t.Errorf("got %q, %v", got, err)
		}
	})

	t.Run("an empty pipe is an error, not an editor", func(t *testing.T) {
		_, err := bodyFrom("", true, false, content(" \n"), refuse("editor", t))
		if err == nil || !strings.Contains(err.Error(), "empty body on stdin") {
			t.Errorf("err = %v, want empty body on stdin", err)
		}
	})

	t.Run("json mode never opens the editor", func(t *testing.T) {
		_, err := bodyFrom("", false, true, refuse("stdin", t), refuse("editor", t))
		if err == nil || !strings.Contains(err.Error(), "--body") {
			t.Errorf("err = %v, want a pointer at --body", err)
		}
	})

	t.Run("a person with no flag and no pipe gets the editor", func(t *testing.T) {
		got, err := bodyFrom("", false, false, refuse("stdin", t), content("typed in the editor"))
		if err != nil || got != "typed in the editor" {
			t.Errorf("got %q, %v", got, err)
		}
	})
}

// --- renderBody and stripElement ----------------------------------------

func TestStripElementAdversarial(t *testing.T) {
	// An unclosed <style> swallows to the end rather than leaking CSS as
	// prose.
	if got := stripElement("before<style>p { color: red }", "style"); got != "before" {
		t.Errorf("unclosed style: got %q", got)
	}

	if got := stripElement("<STYLE>x</STYLE>after", "style"); got != "after" {
		t.Errorf("case-insensitive: got %q", got)
	}

	if got := stripElement("a<style>x</style>b<style>y</style>c", "style"); got != "abc" {
		t.Errorf("two elements: got %q", got)
	}
}

func TestRenderBodyNeutralizesEscapes(t *testing.T) {
	plain := mail.Body{
		MIMEType: "text/plain",
		Content:  "hello \x1b[2J\x1b]0;owned\x07world",
	}

	if got := renderBody(plain); strings.ContainsRune(got, 0x1b) {
		t.Errorf("plain body kept an escape: %q", got)
	} else if !strings.Contains(got, "hello") || !strings.Contains(got, "world") {
		t.Errorf("plain body lost its text: %q", got)
	}

	// The escape rides inside the markup and would survive tag stripping,
	// which only eats what sits between angle brackets.
	html := mail.Body{
		MIMEType: "text/html",
		Content:  "<p>click&nbsp;here \x1b[31mnow</p><style>p { color: red }</style>",
	}

	got := renderBody(html)

	if strings.ContainsRune(got, 0x1b) {
		t.Errorf("html body kept an escape: %q", got)
	}

	if !strings.Contains(got, "click here now") {
		t.Errorf("html body lost its text: %q", got)
	}

	if strings.Contains(got, "color") {
		t.Errorf("style leaked: %q", got)
	}
}

func TestTruncateSanitizes(t *testing.T) {
	if got := truncate("a\x1b[31mb\nc", 20); got != "ab c" {
		t.Errorf("truncate = %q, want %q", got, "ab c")
	}
}

// --- relTime ------------------------------------------------------------

func TestRelTimeFuture(t *testing.T) {
	now := time.Now()

	// A timestamp days ahead -- a scheduled send, a wrong sender clock --
	// must not wear this week's weekday form, which reads as the past.
	future := now.AddDate(0, 0, 3)

	want := future.Format("2 Jan")
	if future.Year() != now.Year() {
		want = future.Format("2 Jan 2006")
	}

	if got := relTime(future); got != want {
		t.Errorf("relTime(+3d) = %q, want %q", got, want)
	}

	nextYear := now.AddDate(1, 0, 0)
	if got, want := relTime(nextYear), nextYear.Format("2 Jan 2006"); got != want {
		t.Errorf("relTime(+1y) = %q, want %q", got, want)
	}

	if got := relTime(time.Time{}); got != "" {
		t.Errorf("relTime(zero) = %q, want empty", got)
	}
}
