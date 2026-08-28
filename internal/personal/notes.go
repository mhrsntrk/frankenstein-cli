package personal

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Notes are quick markdown files in a local directory, and the directory is
// the whole store: no index, no sync, nothing to migrate. Anything that can
// read a folder of .md files can read them without this tool.

// Note is one markdown file.
type Note struct {
	// Name is the filename without its extension, which is also how a note
	// is addressed from the CLI.
	Name string `json:"name"`

	Path    string    `json:"path"`
	Title   string    `json:"title"`
	Updated time.Time `json:"updated"`
}

// ListNotes reads the directory, newest first. A missing directory is an
// empty list, not an error: nothing has been written yet.
func ListNotes(dir string) ([]Note, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("list notes: %w", err)
	}

	var out []Note

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}

		info, err := e.Info()
		if err != nil {
			continue
		}

		path := filepath.Join(dir, e.Name())
		name := strings.TrimSuffix(e.Name(), ".md")

		out = append(out, Note{
			Name:    name,
			Path:    path,
			Title:   noteTitle(path, name),
			Updated: info.ModTime(),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })

	return out, nil
}

// noteTitle is the first markdown heading, or the filename when the note
// starts without one. Only the head of the file is read: the title is on the
// first few lines or it is not a title.
func noteTitle(path, fallback string) string {
	f, err := os.Open(path)
	if err != nil {
		return fallback
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, _ := f.Read(buf)

	for _, line := range strings.Split(string(buf[:n]), "\n") {
		line = strings.TrimSpace(line)
		if t := strings.TrimLeft(line, "# "); strings.HasPrefix(line, "#") && t != "" {
			return t
		}

		if line != "" {
			break
		}
	}

	return fallback
}

// ReadNote returns a note's markdown.
func ReadNote(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read note: %w", err)
	}

	return string(b), nil
}

// WriteNote saves a note's whole content under a name, creating the
// directory on first use. The editor holds the full text, so this replaces
// rather than appends.
func WriteNote(dir, name, content string) (Note, error) {
	if strings.TrimSpace(name) == "" {
		return Note{}, fmt.Errorf("a note needs a name")
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Note{}, fmt.Errorf("create notes dir: %w", err)
	}

	path := filepath.Join(dir, name+".md")

	content = strings.TrimRight(content, "\n") + "\n"

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return Note{}, fmt.Errorf("write note: %w", err)
	}

	return Note{Name: name, Path: path, Title: noteTitle(path, name), Updated: time.Now()}, nil
}

// DeleteNote removes the file outright. Notes are quick by design; anything
// worth an archive belongs somewhere with history.
func DeleteNote(path string) error {
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete note: %w", err)
	}

	return nil
}

// NewNoteName turns a title into an unused filename: lowercased, spaces and
// punctuation collapsed to dashes, suffixed with a counter when the obvious
// name is taken.
func NewNoteName(dir, title string) string {
	slug := slugify(title)
	if slug == "" {
		slug = time.Now().Format("2006-01-02-150405")
	}

	name := slug

	for i := 2; ; i++ {
		if _, err := os.Stat(filepath.Join(dir, name+".md")); os.IsNotExist(err) {
			return name
		}

		name = fmt.Sprintf("%s-%d", slug, i)
	}
}

func slugify(s string) string {
	var b strings.Builder

	dash := true // no leading dash

	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)

			dash = false
		case !dash:
			b.WriteRune('-')

			dash = true
		}
	}

	return strings.TrimRight(b.String(), "-")
}
