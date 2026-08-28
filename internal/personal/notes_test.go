package personal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListNotesMissingDir(t *testing.T) {
	notes, err := ListNotes(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}

	if notes != nil {
		t.Errorf("got %v, want nil", notes)
	}
}

func TestWriteNoteCreatesDirAndNormalizes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")

	n, err := WriteNote(dir, "scratch", "a\n\n\n")
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dir, "scratch.md"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	if string(b) != "a\n" {
		t.Errorf("content = %q, want %q", b, "a\n")
	}

	if n.Name != "scratch" {
		t.Errorf("name = %q, want %q", n.Name, "scratch")
	}

	if n.Path != filepath.Join(dir, "scratch.md") {
		t.Errorf("path = %q, want %q", n.Path, filepath.Join(dir, "scratch.md"))
	}

	if n.Title == "" {
		t.Error("title should be set")
	}

	if n.Updated.IsZero() {
		t.Error("updated should be set")
	}
}

func TestNoteTitle(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"h1", "# Hello World\n\nbody\n", "Hello World"},
		{"h2", "## Sub\n", "Sub"},
		{"blank-lines-first", "\n\n# Late Heading\n", "Late Heading"},
		{"no-heading", "just prose\n# not a title\n", "no-heading"},
		{"empty", "", "empty"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := WriteNote(dir, tc.name, tc.content); err != nil {
				t.Fatalf("write: %v", err)
			}

			got := noteTitle(filepath.Join(dir, tc.name+".md"), tc.name)
			if got != tc.want {
				t.Errorf("title = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestListNotesNewestFirstAndFiltered(t *testing.T) {
	dir := t.TempDir()

	for _, name := range []string{"old", "new"} {
		if _, err := WriteNote(dir, name, "x"); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// Force distinct mtimes; a same-second write order proves nothing.
	base := time.Now().Add(-time.Hour)

	if err := os.Chtimes(filepath.Join(dir, "old.md"), base, base); err != nil {
		t.Fatal(err)
	}

	if err := os.Chtimes(filepath.Join(dir, "new.md"), base.Add(time.Minute), base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	// Neither of these is a note.
	if err := os.WriteFile(filepath.Join(dir, "ignore.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir(filepath.Join(dir, "sub.md"), 0o700); err != nil {
		t.Fatal(err)
	}

	notes, err := ListNotes(dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(notes) != 2 {
		t.Fatalf("got %d notes, want 2", len(notes))
	}

	if notes[0].Name != "new" || notes[1].Name != "old" {
		t.Errorf("order = %s, %s; want new, old", notes[0].Name, notes[1].Name)
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Hello, World!", "hello-world"},
		{"Çok önemli not", "çok-önemli-not"},
		{"  spaced  out  ", "spaced-out"},
		{"!!!", ""},
		{"", ""},
	}

	for _, tc := range cases {
		if got := slugify(tc.in); got != tc.want {
			t.Errorf("slugify(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNewNoteName(t *testing.T) {
	dir := t.TempDir()

	if got := NewNoteName(dir, "Hello, World!"); got != "hello-world" {
		t.Errorf("fresh = %q, want %q", got, "hello-world")
	}

	// Each collision bumps the counter.
	for _, want := range []string{"hello-world", "hello-world-2", "hello-world-3"} {
		name := NewNoteName(dir, "Hello, World!")
		if name != want {
			t.Errorf("got %q, want %q", name, want)
		}

		if _, err := WriteNote(dir, name, "x"); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestNewNoteNameFallback(t *testing.T) {
	dir := t.TempDir()

	name := NewNoteName(dir, "!!!")
	if name == "" {
		t.Fatal("fallback name should not be empty")
	}

	if strings.Contains(name, ".md") {
		t.Errorf("name %q should not carry the extension", name)
	}

	if _, err := os.Stat(filepath.Join(dir, name+".md")); !os.IsNotExist(err) {
		t.Errorf("fallback %q should be an unused name", name)
	}
}

func TestWriteNoteBlankName(t *testing.T) {
	if _, err := WriteNote(t.TempDir(), "  ", "x"); err == nil {
		t.Error("blank name should error")
	}
}

func TestDeleteNote(t *testing.T) {
	dir := t.TempDir()

	if err := DeleteNote(filepath.Join(dir, "missing.md")); err == nil {
		t.Error("deleting a missing note should error")
	}

	n, err := WriteNote(dir, "gone", "x")
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := DeleteNote(n.Path); err != nil {
		t.Fatalf("delete: %v", err)
	}

	notes, err := ListNotes(dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(notes) != 0 {
		t.Errorf("got %d notes after delete, want 0", len(notes))
	}
}

func TestReadNoteRoundTrip(t *testing.T) {
	dir := t.TempDir()

	n, err := WriteNote(dir, "trip", "# Trip\n\nbody\n")
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := ReadNote(n.Path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if got != "# Trip\n\nbody\n" {
		t.Errorf("content = %q, want %q", got, "# Trip\n\nbody\n")
	}
}
