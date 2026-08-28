package heyui

import (
	"hash/fnv"

	mail "github.com/mhrsntrk/frankenstein-cli/internal/tui/heymodel"
)

// List is the exported face of hey-cli's contentList.
//
// The renderer copied from upstream is unexported, and it is kept that way so
// the copied files stay diffable against hey-cli. Everything this project needs
// goes through here instead of editing their code.
type List struct {
	inner contentList
}

// NewList returns an empty list.
func NewList() *List {
	return &List{inner: contentList{selected: map[int64]struct{}{}}}
}

// SetSize sets the width in columns and the height in *rows*, where each
// posting occupies two of them.
func (l *List) SetSize(width, height int) {
	l.inner.width = width
	l.inner.height = height
}

// SetPostings replaces the rows.
func (l *List) SetPostings(postings []mail.Posting) {
	l.inner.setPostings(postings)
}

// HideSections turns off the "New for You" / "Previously Seen" headings.
//
// Upstream shows them only in the Imbox: every other box is a flat list, and a
// heading over a box that does not track seen state is noise.
func (l *List) HideSections(hide bool) {
	l.inner.hideSeenState = hide
}

// Cursor is the highlighted row.
func (l *List) Cursor() int { return l.inner.cursor }

// SetCursor moves the highlight, clamped to the list, and scrolls to keep it
// visible.
func (l *List) SetCursor(i int) {
	l.inner.cursor = i
	l.inner.clampCursor()
}

// Move shifts the cursor by delta.
func (l *List) Move(delta int) { l.SetCursor(l.inner.cursor + delta) }

// Len is how many rows there are.
func (l *List) Len() int { return len(l.inner.postings) }

// At returns the posting at an index, and whether there was one.
func (l *List) At(i int) (mail.Posting, bool) {
	if i < 0 || i >= len(l.inner.postings) {
		return mail.Posting{}, false
	}

	return l.inner.postings[i], true
}

// Selected reports whether a posting is marked.
func (l *List) Selected(id int64) bool {
	_, ok := l.inner.selected[id]

	return ok
}

// ToggleSelected marks or unmarks a posting.
//
// The map is created on demand because upstream's clearSelected nils it rather
// than emptying it, and setPostings calls that on every reload.
func (l *List) ToggleSelected(id int64) {
	if _, ok := l.inner.selected[id]; ok {
		delete(l.inner.selected, id)

		return
	}

	if l.inner.selected == nil {
		l.inner.selected = map[int64]struct{}{}
	}

	l.inner.selected[id] = struct{}{}
}

// ClearSelection drops every mark.
func (l *List) ClearSelection() {
	l.inner.clearSelected()
}

// SelectionCount is how many rows are marked.
func (l *List) SelectionCount() int { return len(l.inner.selected) }

// SelectAll marks every row.
func (l *List) SelectAll() {
	if l.inner.selected == nil {
		l.inner.selected = map[int64]struct{}{}
	}

	for _, p := range l.inner.postings {
		l.inner.selected[p.ID] = struct{}{}
	}
}

// View renders the list.
func (l *List) View() string { return l.inner.view() }

// SectionHeader draws hey-cli's "label ──────" heading, so headings outside the
// list match the ones inside it.
func SectionHeader(label string, width int) string { return sectionHeader(label, width) }

// PostingID turns a provider's string identifier into the int64 the renderer
// wants.
//
// Upstream's IDs are HEY's numeric ones. Proton's are long base64 strings, so
// they are hashed. FNV-64 over a mailbox's worth of conversations has a
// collision probability far below the point where it could matter, and the hash
// is only ever used to match a row to a selection within one render.
func PostingID(id string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))

	// Masked to stay positive: a negative ID would sort oddly if upstream ever
	// orders by it.
	return int64(h.Sum64() & 0x7fffffffffffffff)
}

// IndexAtLine maps a line offset inside the rendered list to the posting drawn
// there, so a click can find its row.
//
// The accounting mirrors view()'s loop exactly: an optional section heading,
// then two lines per posting. Getting it wrong by a line would select the row
// above or below the one clicked, so it is derived from the same helpers the
// renderer uses rather than assumed.
func (l *List) IndexAtLine(line int) (int, bool) {
	if line < 0 {
		return 0, false
	}

	c := &l.inner

	row := 0
	end := c.scrollOff + c.visibleItemsFrom(c.scrollOff)

	if end > c.itemCount() {
		end = c.itemCount()
	}

	for i := c.scrollOff; i < end; i++ {
		if c.sectionLabelAt(i) != "" {
			if row == line {
				// The heading itself: not a row.
				return 0, false
			}

			row++
		}

		if line == row || line == row+1 {
			return i, true
		}

		row += 2
	}

	return 0, false
}

// ScrollBy moves the window without moving the cursor, for a mouse wheel.
func (l *List) ScrollBy(delta int) {
	c := &l.inner

	c.scrollOff += delta

	if max := c.itemCount() - 1; c.scrollOff > max {
		c.scrollOff = max
	}

	if c.scrollOff < 0 {
		c.scrollOff = 0
	}
}
