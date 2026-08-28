package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/mhrsntrk/frankenstein-cli/internal/personal"
)

// The Notes section: quick markdown notes in a local folder. The list, the
// reader and the editor live here together, so replacing or extending the
// section touches one file.

// noteEditor is the open editor. A single textarea holds the whole markdown,
// title included: a quick note is one piece of text, not a form.
type noteEditor struct {
	body textarea.Model

	// name is the file being edited, empty for a note not yet saved: the name
	// is derived from the first heading on the first save.
	name string
}

// notesMsg is the refreshed list.
type notesMsg []personal.Note

// noteMsg is one note's content, ready to read.
type noteMsg struct {
	note    personal.Note
	content string
}

// noteEditMsg is one note's content, ready to edit.
type noteEditMsg struct {
	name    string
	content string
}

// loadNotes reads the folder.
func (m *Model) loadNotes() tea.Cmd {
	dir := m.notesDir

	return bg(5*time.Second, func(ctx context.Context) tea.Msg {
		if dir == "" {
			// The only way here is a machine with no home directory; saying so
			// beats writing into whatever the working directory happens to be.
			return errMsg{fmt.Errorf("no home directory for notes")}
		}

		notes, err := personal.ListNotes(dir)
		if err != nil {
			return errMsg{err}
		}

		return notesMsg(notes)
	})
}

// openNoteCmd reads a note off the render path.
func (m *Model) openNoteCmd(n personal.Note) tea.Cmd {
	return bg(5*time.Second, func(ctx context.Context) tea.Msg {
		content, err := personal.ReadNote(n.Path)
		if err != nil {
			return errMsg{err}
		}

		return noteMsg{note: n, content: content}
	})
}

// editNoteCmd reads a note for the editor.
func (m *Model) editNoteCmd(n personal.Note) tea.Cmd {
	return bg(5*time.Second, func(ctx context.Context) tea.Msg {
		content, err := personal.ReadNote(n.Path)
		if err != nil {
			return errMsg{err}
		}

		return noteEditMsg{name: n.Name, content: content}
	})
}

// saveNote writes the editor's text and refreshes the list. The name for a
// new note comes from its first heading, the way the file would be titled by
// hand.
func (m *Model) saveNote(name, content string) tea.Cmd {
	dir := m.notesDir

	return bg(5*time.Second, func(ctx context.Context) tea.Msg {
		if dir == "" {
			return errMsg{fmt.Errorf("no home directory for notes")}
		}

		if strings.TrimSpace(content) == "" {
			return errMsg{fmt.Errorf("nothing to save")}
		}

		if name == "" {
			name = personal.NewNoteName(dir, headingOf(content))
		}

		if _, err := personal.WriteNote(dir, name, content); err != nil {
			return errMsg{err}
		}

		return actionMsg{note: "note saved", reloadNotes: true}
	})
}

// deleteNote removes the note under the cursor.
func (m *Model) deleteNote(n personal.Note) tea.Cmd {
	return bg(5*time.Second, func(ctx context.Context) tea.Msg {
		if err := personal.DeleteNote(n.Path); err != nil {
			return errMsg{err}
		}

		return actionMsg{note: "deleted " + n.Name, reloadNotes: true}
	})
}

// headingOf is the first markdown heading of a draft, or a stand-in when the
// note starts without one.
func headingOf(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if t := strings.TrimLeft(line, "# "); strings.HasPrefix(line, "#") && t != "" {
			return t
		}

		if line != "" {
			// The first line names an untitled note well enough.
			return line
		}
	}

	return "note"
}

// selectedNote is the note under the cursor.
func (m *Model) selectedNote() (personal.Note, bool) {
	if m.extraIdx < 0 || m.extraIdx >= len(m.notes) {
		return personal.Note{}, false
	}

	return m.notes[m.extraIdx], true
}

// startNoteEdit opens the editor over a note's content, or over a fresh
// template when there is nothing to load.
func (m *Model) startNoteEdit(name, content string) (tea.Model, tea.Cmd) {
	body := textarea.New()
	body.ShowLineNumbers = false
	body.CharLimit = 0
	body.SetWidth(maxInt(20, m.contentWidth()-2))
	body.SetHeight(maxInt(3, m.pageSize()-2))
	body.SetValue(content)
	body.Focus()

	if content == "" {
		body.SetValue("# ")
		body.CursorEnd()
	} else {
		body.CursorStart()
	}

	m.noteEd = &noteEditor{body: body, name: name}
	m.push(viewNoteEdit)

	return m, textarea.Blink
}

// notesKey handles the keys that only mean something in the notes list. The
// third return says whether it took the key; anything else falls through to
// the shared bindings.
func (m *Model) notesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "enter", "l", "right":
		n, ok := m.selectedNote()
		if !ok {
			return m, nil, true
		}

		m.loading = true

		return m, m.openNoteCmd(n), true

	case "n", "c":
		model, cmd := m.startNoteEdit("", "")

		return model, cmd, true

	case "e":
		n, ok := m.selectedNote()
		if !ok {
			return m, nil, true
		}

		// The read goes through a command like every other file load, so a
		// slow disk cannot stall a keystroke.
		m.loading = true

		return m, m.editNoteCmd(n), true

	case "D":
		n, ok := m.selectedNote()
		if !ok {
			return m, nil, true
		}

		m.loading = true

		return m, m.deleteNote(n), true

	case "r":
		m.loading = true

		return m, m.loadNotes(), true
	}

	return m, nil, false
}

// noteReadKey handles the reader's own keys.
func (m *Model) noteReadKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "e":
		// The reader already holds the text it is showing; editing what is on
		// screen needs no second trip to disk.
		model, cmd := m.startNoteEdit(m.openNote.Name, m.noteRaw)

		return model, cmd, true

	case "D":
		m.loading = true

		return m, m.deleteNote(m.openNote), true
	}

	return m, nil, false
}

// handleNoteEditKey owns the keyboard while the editor is open, the same way
// the composer does: typing a note must not trigger single-letter actions.
func (m *Model) handleNoteEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ed := m.noteEd

	switch msg.String() {
	case "esc":
		m.noteEd = nil
		m.pop()

		return m, nil

	case "ctrl+d":
		m.loading = true

		return m, m.saveNote(ed.name, ed.body.Value())
	}

	var cmd tea.Cmd
	ed.body, cmd = ed.body.Update(msg)

	return m, cmd
}

// rewrapNote re-flows an open note to the current width, once on load and on
// resize, keeping the render path cheap.
func (m *Model) rewrapNote(content string) {
	width := maxInt(20, m.contentWidth()-2)

	var lines []string

	for _, para := range strings.Split(content, "\n") {
		lines = append(lines, wrap(para, width)...)
	}

	m.noteLines = lines
}

// --- views ------------------------------------------------------------------

func (m *Model) notesView() string {
	if m.loading && len(m.notes) == 0 {
		return dimStyle.Render("  loading…")
	}

	if len(m.notes) == 0 {
		return dimStyle.Render("  No notes yet. Press n to write one.")
	}

	var b strings.Builder

	visible := maxInt(1, m.pageSize())
	start := m.notesWindowStart()
	end := minInt(start+visible, len(m.notes))

	for i := start; i < end; i++ {
		n := m.notes[i]

		line := fmt.Sprintf("  %-40s %-12s %s",
			truncateStr(n.Title, 40), relTime(n.Updated), dimStyle.Render(n.Name+".md"))

		if i == m.extraIdx {
			line = selectedStyle.Render(padTo(line, m.contentWidth()))
		}

		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String()
}

// notesWindowStart is the first note on screen. The list keeps the cursor
// centred, and the mouse handler needs the same number to map a clicked row
// back to a note, so the formula lives once.
func (m *Model) notesWindowStart() int {
	visible := maxInt(1, m.pageSize())

	return clamp(m.extraIdx-visible/2, 0, maxInt(0, len(m.notes)-visible))
}

// noteReadView scrolls the wrapped markdown, with the cheap styling a
// terminal can afford: headings bright, the rest as written. Quick notes need
// legibility, not a renderer.
func (m *Model) noteReadView() string {
	if len(m.noteLines) == 0 {
		return dimStyle.Render("  (empty note)")
	}

	var b strings.Builder

	end := minInt(m.noteTop+m.pageSize(), len(m.noteLines))

	for i := m.noteTop; i < end; i++ {
		line := m.noteLines[i]

		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			line = titleStyle.Render(line)
		}

		b.WriteString("  " + line + "\n")
	}

	return b.String()
}

func (m *Model) noteEditView() string {
	if m.noteEd == nil {
		return ""
	}

	var b strings.Builder

	b.WriteString(dimStyle.Render("  Markdown; the first heading names the file."))
	b.WriteString("\n\n")
	b.WriteString(m.noteEd.body.View())
	b.WriteString("\n")

	return b.String()
}
