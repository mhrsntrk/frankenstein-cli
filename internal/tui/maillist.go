package tui

import "github.com/mhrsntrk/frankenstein-cli/internal/mail"

// mailList is the thread list's state: the conversations, where the cursor and
// the scroll window sit, and which rows are bulk-selected.
//
// It keeps the order it is handed and never re-sorts. The store lists newest
// first, so that is what the reader sees; the renderer this replaced grouped
// unread mail into a section above read mail, which is why a week-old unread
// newsletter used to sit above this morning's reply.
//
// Selection is keyed by the provider's own conversation ID, so a reload that
// replaces the rows keeps the marks on the same conversations.
type mailList struct {
	convs    []mail.Conversation
	cursor   int
	top      int
	selected map[string]bool
}

// setConvs replaces the rows, leaving the selection alone: a background
// refresh must not silently unmark what the reader picked. The cursor and the
// window are clamped, because the new listing may be shorter.
func (l *mailList) setConvs(convs []mail.Conversation) {
	l.convs = convs
	l.clamp()
}

// clamp keeps the cursor and the window inside the list.
func (l *mailList) clamp() {
	l.cursor = clamp(l.cursor, 0, maxInt(0, len(l.convs)-1))
	l.top = clamp(l.top, 0, maxInt(0, len(l.convs)-1))
}

func (l *mailList) len() int { return len(l.convs) }

// at maps a row index back to its conversation.
func (l *mailList) at(i int) (mail.Conversation, bool) {
	if i < 0 || i >= len(l.convs) {
		return mail.Conversation{}, false
	}

	return l.convs[i], true
}

func (l *mailList) setCursor(i int) {
	l.cursor = clamp(i, 0, maxInt(0, len(l.convs)-1))
}

func (l *mailList) move(delta int) { l.setCursor(l.cursor + delta) }

// scrollBy moves the window without moving the cursor, for a mouse wheel.
func (l *mailList) scrollBy(delta int) {
	l.top = clamp(l.top+delta, 0, maxInt(0, len(l.convs)-1))
}

// scrollInto pulls the window far enough that the cursor is drawn, given how
// many conversations fit on screen.
func (l *mailList) scrollInto(rows int) {
	l.top = scrollTo(l.top, l.cursor, maxInt(1, rows))
}

func (l *mailList) isSelected(id string) bool { return l.selected[id] }

func (l *mailList) toggleSelected(id string) {
	if l.selected == nil {
		l.selected = map[string]bool{}
	}

	if l.selected[id] {
		delete(l.selected, id)

		return
	}

	l.selected[id] = true
}

func (l *mailList) clearSelection() { l.selected = nil }

func (l *mailList) selectionCount() int { return len(l.selected) }

func (l *mailList) selectAll() {
	l.selected = make(map[string]bool, len(l.convs))
	for _, c := range l.convs {
		l.selected[c.ID] = true
	}
}

// selectedIDs is the marked conversations in the order they are drawn, so a
// bulk action reports them the way the reader sees them.
func (l *mailList) selectedIDs() []string {
	if len(l.selected) == 0 {
		return nil
	}

	out := make([]string, 0, len(l.selected))

	for _, c := range l.convs {
		if l.selected[c.ID] {
			out = append(out, c.ID)
		}
	}

	return out
}

// indexAtLine maps a line offset inside the rendered list to the conversation
// drawn there, so a click can find its row. listPane spends exactly two lines
// on every conversation, with no headings between them.
func (l *mailList) indexAtLine(line int) (int, bool) {
	if line < 0 {
		return 0, false
	}

	i := l.top + line/2
	if i >= len(l.convs) {
		return 0, false
	}

	return i, true
}
