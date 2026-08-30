package tui

import (
	"fmt"
	"net/mail"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	fcal "github.com/mhrsntrk/frankenstein-cli/internal/calendar"
	fmail "github.com/mhrsntrk/frankenstein-cli/internal/mail"
	"github.com/mhrsntrk/frankenstein-cli/internal/screener"
	"github.com/mhrsntrk/frankenstein-cli/internal/terminal"
	"github.com/mhrsntrk/frankenstein-cli/internal/tui/heyui"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

		m.sizeCompose()

		if m.noteEd != nil {
			m.noteEd.body.SetWidth(maxInt(20, m.contentWidth()-2))
			m.noteEd.body.SetHeight(maxInt(3, m.pageSize()-2))
		}

		if m.view == viewNoteRead {
			m.rewrapNote(m.noteRaw)
		}

		// The split view wraps the body to the thread pane, so a resize has to
		// re-flow it whenever a body is on screen, not only in viewMessage.
		if m.view == viewMessage || (m.splitMail() && len(m.bodyLines) > 0) {
			m.rewrapBody()
		}

		return m, nil

	case boxesMsg:
		m.boxes = msg
		m.quickBoxes = pickQuickBoxes(msg)

		return m, nil

	case chromeMsg:
		m.pending, m.account = msg.pending, msg.account

		return m, nil

	case convsMsg:
		// A response overtaken by a newer request describes a listing that is
		// no longer wanted; applying it would put the old one back.
		if msg.gen != m.convsGen {
			return m, nil
		}

		// The cursor survives a reload by clamping to the new length: an
		// action-triggered refresh must not throw the reader back to the top.
		// A box or filter switch resets it explicitly before loading.
		cursor := m.list.Cursor()
		m.setPostings(msg.convs)
		m.list.SetCursor(clamp(cursor, 0, maxInt(0, m.list.Len()-1)))
		m.list.ClearSelection()
		m.paneTop = scrollTo(clamp(m.paneTop, 0, maxInt(0, m.list.Len()-1)),
			m.list.Cursor(), maxInt(1, listPageSize(m.pageSize())))
		m.loading = false

		// The excerpt line is empty until a body has been decrypted, so ask for
		// the ones now on screen.
		return m, m.prefetchSnippets()

	case sendersMsg:
		m.senders = msg

		// Clamp rather than reset: deciding mid-queue reloads the list, and a
		// cursor thrown back to the top loses the reader's place in it.
		m.senderIdx = clamp(m.senderIdx, 0, maxInt(0, len(m.senders)-1))
		m.loading = false

		return m, nil

	case notesMsg:
		m.notes = msg
		m.extraIdx = clamp(m.extraIdx, 0, maxInt(0, len(m.notes)-1))
		m.loading = false

		noun := "notes"
		if len(msg) == 1 {
			noun = "note"
		}

		m.status = fmt.Sprintf("%d %s", len(msg), noun)

		return m, nil

	case noteEditMsg:
		m.loading = false

		return m.startNoteEdit(msg.name, msg.content)

	case noteMsg:
		m.openNote = msg.note
		m.noteRaw = msg.content
		m.noteTop = 0
		m.rewrapNote(msg.content)
		m.loading = false

		if m.view != viewNoteRead {
			m.push(viewNoteRead)
		}

		return m, nil

	case threadMsg:
		if msg.gen != m.threadGen {
			return m, nil
		}

		m.thread = msg.thread
		m.msgIdx = maxInt(0, len(m.thread.Messages)-1)
		m.loading = false

		// A thread that arrives after the user left the mail section must not
		// yank them back to it: the data is kept, the view is not adopted.
		if !m.inMailContext() {
			return m, nil
		}

		if m.view != viewCompose {
			m.view = viewThread
		}

		// The split view opens a conversation the way Proton does, with the
		// newest message already expanded, so the body comes along at once
		// rather than behind another keypress.
		if m.splitMail() && len(m.thread.Messages) > 0 {
			m.loading = true

			return m, m.loadBody(m.thread.Messages[m.msgIdx].ID)
		}

		return m, nil

	case bodyMsg:
		if msg.gen != m.bodyGen {
			return m, nil
		}

		m.body = msg.body
		m.bodyTop = 0
		m.loading = false
		m.rewrapBody()

		// Same rule as threadMsg: a slow body must not switch the screen the
		// user has already navigated away from.
		if m.inMailContext() && m.view != viewCompose {
			m.view = viewMessage
		}

		return m, nil

	case eventsMsg:
		// A response overtaken by a newer request describes a window no
		// longer on screen; applying it would put the old window back.
		if msg.gen != m.eventsGen {
			return m, nil
		}

		// Remember what the cursor was on before the slice changes hands.
		// extraIdx is shared with the notes list, so it is only touched when
		// the calendar is what it currently indexes.
		var prev fcal.Event

		onCalendar := m.view == viewCalendar
		if onCalendar {
			prev, _ = m.selectedEvent()
		}

		m.events, m.calErr = msg.events, msg.err

		if onCalendar {
			// Re-anchor on the event that was selected, so enter, e and D
			// keep acting on what the user is looking at; an event that went
			// away leaves the cursor clamped into the new list.
			m.extraIdx = clamp(m.extraIdx, 0, maxInt(0, len(m.events)-1))

			for i, e := range m.events {
				if e.ID == prev.ID && e.CalendarID == prev.CalendarID && prev.ID != "" {
					m.extraIdx = i

					break
				}
			}
		}

		return m, nil

	case calendarsMsg:
		if m.picker != nil {
			m.picker.calendars = msg

			// "primary" is an alias the list answers to with a real address,
			// so the configured set is resolved against it rather than
			// compared literally.
			m.picker.shown = fcal.ResolveShown(m.calendarIDs, msg)

			if len(m.picker.shown) == 0 {
				for _, c := range msg {
					if c.Primary {
						m.picker.shown[c.ID] = true
					}
				}
			}
		}

		for _, c := range msg {
			m.calColours[c.ID] = heyui.ColourFor(c.Color)
			m.calNames[c.ID] = c.Name
		}

		return m, nil

	case bandsMsg:
		m.calHabits, m.calTodos = msg.habits, msg.todos

		// The band cursor indexes whichever list the open manager shows, and
		// a reload can shrink it: completing the last todo would leave the
		// cursor past the end, and the next space would index out of range.
		if m.band != nil {
			count := len(m.calTodos)
			if m.view == viewHabits {
				count = len(m.calHabits)
			}

			m.band.idx = clamp(m.band.idx, 0, maxInt(0, count-1))
		}

		return m, nil

	case snippetsMsg:
		// Fold the fetched previews into the rows already on screen, keeping
		// the cursor and the selection where they were. SetPostings clears the
		// selection, so it is captured first and re-applied, like the cursor.
		for i := range m.convs {
			if s, ok := msg[m.convs[i].ID]; ok {
				m.convs[i].Snippet = s
			}
		}

		var selected []string

		for _, c := range m.convs {
			if m.list.Selected(heyui.PostingID(c.ID)) {
				selected = append(selected, c.ID)
			}
		}

		cursor := m.list.Cursor()
		m.setPostings(m.convs)
		m.list.SetCursor(cursor)

		for _, id := range selected {
			m.list.ToggleSelected(heyui.PostingID(id))
		}

		return m, nil

	case syncedMsg:
		m.loading = false
		m.status = fmt.Sprintf("synced %d conversations", msg.Conversations)

		cmds := []tea.Cmd{m.loadBoxes(), m.loadChrome()}
		if m.view == viewThreads {
			cmds = append(cmds, m.loadConvs(m.listBoxID(), m.filter.Value()))
		}

		// A screening decision is about a person, so mail that arrived after
		// the decision has to be routed too. Reapply is idempotent and makes
		// no provider calls when there is nothing to do.
		if m.screener != nil {
			cmds = append(cmds, m.reapplyScreener())
		}

		return m, tea.Batch(cmds...)

	case actionMsg:
		m.loading = false
		m.notify(msg.note)
		m.list.ClearSelection()

		var cmds []tea.Cmd

		if msg.reload {
			cmds = append(cmds, m.loadConvs(m.listBoxID(), m.filter.Value()), m.loadBoxes(), m.loadChrome())
		}

		if msg.rescreen {
			cmds = append(cmds, m.loadSenders())
		}

		if msg.reloadBands {
			cmds = append(cmds, m.loadBands())
		}

		if msg.reloadNotes {
			// A save closes the editor; a delete closes the reader too, since
			// what it was reading is gone. A reader left open over an edited
			// note re-reads it, so the text on screen is the text on disk.
			if m.view == viewNoteEdit {
				m.noteEd = nil
				m.pop()
			}

			if strings.HasPrefix(msg.note, "deleted ") {
				if m.view == viewNoteRead {
					m.view = viewNotes
				}
			} else if m.view == viewNoteRead && m.openNote.Path != "" {
				cmds = append(cmds, m.openNoteCmd(m.openNote))
			}

			cmds = append(cmds, m.loadNotes())
		}

		if msg.reloadCalendar {
			m.eventForm = nil

			if m.view == viewEventForm {
				m.pop()
			}

			cmds = append(cmds, m.loadEvents(), m.loadBands())
		}

		// A compose that was sent closes itself, even one parked as a minimized
		// bar, which is no longer the view on top. A saved draft only flashes:
		// the writer is still writing, and the composer stays open remembering
		// the draft's ID so the next save updates it.
		if m.compose != nil {
			if msg.note == "sent" {
				m.compose = nil
				m.composerMin, m.composerMax = false, false

				if m.view == viewCompose {
					m.pop()
				}
			} else if msg.draftID != "" {
				m.compose.draftID = msg.draftID
			}
		}

		return m, tea.Batch(cmds...)

	case errMsg:
		m.err = msg.err
		m.loading = false

		return m, nil

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Text entry owns the keyboard while it is open, or typing a subject would
	// trigger every single-letter action.
	if m.compose != nil && m.view == viewCompose {
		return m.handleComposeKey(msg)
	}

	if m.eventForm != nil && m.view == viewEventForm {
		return m.handleEventFormKey(msg)
	}

	if m.noteEd != nil && m.view == viewNoteEdit {
		return m.handleNoteEditKey(msg)
	}

	if m.band != nil && (m.view == viewHabits || m.view == viewTodos) {
		return m.handleBandKey(msg)
	}

	if m.picker != nil && m.view == viewCalendars {
		return m.handleCalendarPickerKey(msg)
	}

	if m.view == viewMovePicker {
		return m.handleMoveKey(msg)
	}

	if m.filtering {
		switch msg.Type {
		case tea.KeyEnter:
			m.filtering = false
			m.filter.Blur()
			m.loading = true
			m.resetMailContext()

			return m, m.loadConvs(m.box.ID, m.filter.Value())

		case tea.KeyEsc:
			m.filtering = false
			m.filter.SetValue("")
			m.filter.Blur()
			m.resetMailContext()

			return m, m.loadConvs(m.box.ID, "")
		}

		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)

		return m, cmd
	}

	// Any keypress clears the last flash, so notices do not linger.
	m.flash = ""
	m.err = nil

	if m.help {
		m.help = false

		return m, nil
	}

	// A section's own keys are read before the mail actions, or the shared
	// letters never reach it: p is paper-trail and t is trash in the mail
	// views, and both were swallowing the calendar's own meaning for them.
	if m.view == viewCalendar {
		if model, cmd, handled := m.calendarKey(msg); handled {
			return model, cmd
		}
	}

	if m.view == viewEventDetail {
		if model, cmd, handled := m.eventDetailKey(msg); handled {
			return model, cmd
		}
	}

	// The notes list and reader have their own verbs, read before the shared
	// mail letters for the same reason the calendar's are: e is edit here, not
	// mark-seen, and r reloads the folder rather than syncing mail.
	if m.view == viewNotes {
		if model, cmd, handled := m.notesKey(msg); handled {
			return model, cmd
		}
	}

	if m.view == viewNoteRead {
		if model, cmd, handled := m.noteReadKey(msg); handled {
			return model, cmd
		}
	}

	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true

		return m, tea.Quit

	case "?":
		m.help = true

		return m, nil

	case "tab":
		m.section = (m.section + 1) % 3

		return m, m.enterSection()

	case "shift+tab":
		m.section = (m.section + 2) % 3

		return m, m.enterSection()

	case "esc", "backspace", "h", "left":
		return m.goBack()

	case "enter", "l", "right":
		return m.drillIn()

	case "j", "down":
		return m.moveCursorBy(1)

	case "k", "up":
		return m.moveCursorBy(-1)

	case "g", "home":
		m.setCursor(0)

		return m, nil

	case "G", "end":
		m.setCursor(m.itemCount() - 1)

		return m, nil

	case "ctrl+d", "pgdown":
		m.moveCursor(m.pageSize() / 2)

		return m, nil

	case "ctrl+u", "pgup":
		m.moveCursor(-m.pageSize() / 2)

		return m, nil

	case "/":
		if m.view == viewThreads {
			m.filtering = true
			m.filter.Focus()

			return m, textinput.Blink
		}

		return m, nil

	case "r", "ctrl+r":
		// r replies in a thread and reloads in a list, matching hey-cli where
		// the same key means the obvious thing for what is on screen.
		if m.view == viewThread || m.view == viewMessage {
			return m.startCompose(composeReply)
		}

		m.loading = true
		m.status = "syncing"

		return m, m.syncOnce()

	case "R":
		if m.view == viewThread || m.view == viewMessage {
			return m.startCompose(composeReplyAll)
		}

		return m, nil

	case "f":
		if m.view == viewThread || m.view == viewMessage {
			return m.startCompose(composeForward)
		}

		return m, nil

	case "c":
		return m.startCompose(composeNew)

	case " ":
		if m.view == viewThreads {
			if p, ok := m.list.At(m.list.Cursor()); ok {
				m.list.ToggleSelected(p.ID)
				m.list.Move(1)
			}
		}

		return m, nil

	case "ctrl+a":
		if m.view == viewThreads {
			if m.list.SelectionCount() == m.list.Len() {
				m.list.ClearSelection()
			} else {
				m.list.SelectAll()
			}
		}

		return m, nil

	case "ctrl+s":
		m.push(viewScreener)
		m.loading = true

		return m, m.loadSenders()

	case "e":
		return m.applyMark(true)

	case "u":
		return m.applyMark(false)

	case "i":
		return m.applyScreen(screener.Imbox)

	case "d":
		return m.applyScreen(screener.Feed)

	case "p":
		return m.applyScreen(screener.PaperTrail)

	case "x":
		return m.applyScreen(screener.ScreenedOut)

	case "a":
		return m.applyMove("Archive", "archived")

	case "t":
		return m.applyMove("Trash", "trashed")

	case "!":
		return m.applyMove("Spam", "marked spam")

	case "s":
		return m.applyLabel("Starred", "starred")

	case "v":
		if m.view == viewThreads || m.view == viewThread {
			return m.openMovePicker()
		}

		return m, nil

	case "]":
		return m.cycleCategory(1)

	case "[":
		return m.cycleCategory(-1)
	}

	// 1-9 jump straight to a box, which is what keeps every box one keystroke
	// away instead of a walk back up the stack.
	if k := msg.String(); len(k) == 1 && k[0] >= '1' && k[0] <= '9' {
		i := int(k[0] - '1')

		if i < len(m.quickBoxes) {
			m.section = sectionMail
			m.box = m.quickBoxes[i]
			m.view = viewThreads
			m.loading = true
			m.list.ClearSelection()
			m.filter.SetValue("")
			m.resetMailContext()

			return m, m.loadConvs(m.box.ID, "")
		}
	}

	return m, nil
}

// selectCategory narrows the listing to one category, or to all of it for an
// index outside the row.
//
// The box is left alone on purpose: a category is a label over the same
// conversations, so listing by its ID is the filter, and m.box staying the
// Inbox is what keeps the header and the bar naming where the reader is.
func (m *Model) selectCategory(i int) (tea.Model, tea.Cmd) {
	cats := m.categoryBoxes()

	id := ""
	if i >= 0 && i < len(cats) {
		id = cats[i].ID
	}

	m.view = viewThreads
	m.loading = true
	m.list.ClearSelection()

	// resetMailContext drops the category along with the rest of the previous
	// listing, so the new one is set after it rather than before.
	m.resetMailContext()
	m.categoryID = id

	return m, m.loadConvs(m.listBoxID(), m.filter.Value())
}

// cycleCategory steps along the category row and wraps, All included. The row
// is a sub-nav of the Inbox, so the keys are dead anywhere the row is not.
func (m *Model) cycleCategory(delta int) (tea.Model, tea.Cmd) {
	if !m.categoryRowShown() {
		return m, nil
	}

	cats := m.categoryBoxes()

	// Entry 0 is All, so category i sits at i+1.
	at := 0

	for i, b := range cats {
		if b.ID == m.categoryID {
			at = i + 1

			break
		}
	}

	n := len(cats) + 1
	next := ((at+delta)%n + n) % n

	return m.selectCategory(next - 1)
}

// calendarKey handles the keys that only mean something in the calendar.
//
// The third return says whether it took the key. Anything it does not take
// falls through to the shared bindings, so j/k, tab and q still work there.
func (m *Model) calendarKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "1":
		m.calView, m.calOffset = calendarDay, 0

		return m, m.loadEvents(), true

	case "2":
		m.calView, m.calOffset = calendarWeek, 0

		return m, m.loadEvents(), true

	case "3":
		m.calView, m.calOffset = calendarYear, 0

		return m, m.loadEvents(), true

	case "n":
		m.calOffset++

		return m, m.loadEvents(), true

	case "p":
		m.calOffset--

		return m, m.loadEvents(), true

	case "t":
		m.calOffset = 0

		return m, m.loadEvents(), true

	case "c":
		// Creating, editing and deleting all need a provider, and m.cal is
		// legitimately nil on a machine that never ran the setup: without the
		// guard the save command would dereference it and panic.
		if m.cal == nil {
			m.err = fcal.ErrNotConfigured

			return m, nil, true
		}

		m.eventForm = newEventForm(nil, m.calAnchor())
		m.push(viewEventForm)

		return m, textinput.Blink, true

	case "enter":
		e, ok := m.selectedEvent()
		if !ok {
			return m, nil, true
		}

		m.detailID = e.ID
		m.push(viewEventDetail)

		return m, nil, true

	case "e":
		if m.cal == nil {
			m.err = fcal.ErrNotConfigured

			return m, nil, true
		}

		e, ok := m.selectedEvent()
		if !ok {
			return m, nil, true
		}

		m.eventForm = newEventForm(&e, m.calAnchor())
		m.push(viewEventForm)

		return m, textinput.Blink, true

	case "D":
		if m.cal == nil {
			m.err = fcal.ErrNotConfigured

			return m, nil, true
		}

		e, ok := m.selectedEvent()
		if !ok {
			return m, nil, true
		}

		m.loading = true

		return m, m.deleteEvent(e.CalendarID, e.ID, e.Title), true

	case "b":
		model, cmd := m.openHabits()

		return model, cmd, true

	case "s":
		model, cmd := m.openTodos()

		return model, cmd, true

	case "g":
		model, cmd := m.openCalendars()

		return model, cmd, true

	case "j", "down":
		m.extraIdx = clamp(m.extraIdx+1, 0, maxInt(0, len(m.events)-1))

		return m, nil, true

	case "k", "up":
		m.extraIdx = clamp(m.extraIdx-1, 0, maxInt(0, len(m.events)-1))

		return m, nil, true
	}

	return m, nil, false
}

// selectedEvent is the event under the cursor in the calendar.
func (m *Model) selectedEvent() (fcal.Event, bool) {
	if m.extraIdx < 0 || m.extraIdx >= len(m.events) {
		return fcal.Event{}, false
	}

	return m.events[m.extraIdx], true
}

// enterSection switches between Mail, Calendar and Journal.
func (m *Model) enterSection() tea.Cmd {
	m.stack = nil

	switch m.section {
	case sectionCalendar:
		m.view = viewCalendar

		// The calendar list comes too, because it carries the colours and the
		// names. They used to arrive only when the picker was opened, so an
		// account with several calendars drew them all in one colour until
		// somebody happened to press g.
		return tea.Batch(m.loadEvents(), m.loadBands(), m.loadCalendars())
	case sectionNotes:
		m.view = viewNotes

		return m.loadNotes()
	default:
		m.view = viewBoxes

		return m.loadBoxes()
	}
}

// --- actions ----------------------------------------------------------------

func (m *Model) applyMark(read bool) (tea.Model, tea.Cmd) {
	ids := m.targets()
	if len(ids) == 0 {
		return m, nil
	}

	m.loading = true

	return m, m.markTargets(ids, read)
}

// applyScreen decides about the *senders* of the selected threads, which is
// the difference between filing one message and screening a person.
func (m *Model) applyScreen(d screener.Decision) (tea.Model, tea.Cmd) {
	if m.view == viewScreener {
		if m.senderIdx >= len(m.senders) {
			return m, nil
		}

		s := m.senders[m.senderIdx]
		m.loading = true

		return m, m.decideSender(s.Address, d)
	}

	ids := m.targets()
	if len(ids) == 0 {
		return m, nil
	}

	if !m.cfg.Screener.Configured() {
		m.err = fmt.Errorf("screener is not set up; run `frankenstein screener setup`")

		return m, nil
	}

	m.loading = true

	return m, m.screenTargets(ids, d)
}

// applyMove labels into a system box and unlabels the current one, so the
// thread actually leaves where it was.
func (m *Model) applyMove(boxName, note string) (tea.Model, tea.Cmd) {
	ids := m.targets()
	if len(ids) == 0 {
		return m, nil
	}

	box, ok := m.boxByName(boxName)
	if !ok {
		m.err = fmt.Errorf("no %s box in the cache; run a sync", boxName)

		return m, nil
	}

	m.loading = true

	return m, m.labelTargets(ids, box, true, note)
}

// applyLabel adds a label without removing the current box.
func (m *Model) applyLabel(boxName, note string) (tea.Model, tea.Cmd) {
	ids := m.targets()
	if len(ids) == 0 {
		return m, nil
	}

	box, ok := m.boxByName(boxName)
	if !ok {
		// Saying why beats a key that silently does nothing: the box being
		// absent from the cache is a sync problem the user can fix.
		m.err = fmt.Errorf("no %s box in the cache; run a sync", boxName)

		return m, nil
	}

	m.loading = true

	return m, m.labelTargets(ids, box, false, note)
}

func (m *Model) openMovePicker() (tea.Model, tea.Cmd) {
	if len(m.targets()) == 0 {
		return m, nil
	}

	m.moveTargets = nil

	for _, b := range m.boxes {
		// Aggregate views are not somewhere a thread can be moved to.
		if strings.HasPrefix(b.Name, "All ") || b.Kind == fmail.BoxCategory {
			continue
		}

		m.moveTargets = append(m.moveTargets, b)
	}

	m.moveIdx = 0
	m.moveFilter.SetValue("")
	m.moveFilter.Focus()
	m.push(viewMovePicker)

	return m, textinput.Blink
}

func (m *Model) handleMoveKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.moveFilter.Blur()
		m.pop()

		return m, nil

	case tea.KeyEnter:
		matches := m.moveMatches()
		if len(matches) == 0 {
			return m, nil
		}

		box := matches[minInt(m.moveIdx, len(matches)-1)]

		m.moveFilter.Blur()
		m.pop()

		ids := m.targets()
		if len(ids) == 0 {
			return m, nil
		}

		m.loading = true

		return m, m.labelTargets(ids, box, true, "moved to "+box.Name+":")

	case tea.KeyUp:
		m.moveIdx = maxInt(0, m.moveIdx-1)

		return m, nil

	case tea.KeyDown:
		m.moveIdx = minInt(len(m.moveMatches())-1, m.moveIdx+1)

		return m, nil
	}

	var cmd tea.Cmd
	m.moveFilter, cmd = m.moveFilter.Update(msg)
	m.moveIdx = 0

	return m, cmd
}

func (m *Model) moveMatches() []fmail.Box {
	q := strings.ToLower(strings.TrimSpace(m.moveFilter.Value()))
	if q == "" {
		return m.moveTargets
	}

	var out []fmail.Box

	for _, b := range m.moveTargets {
		if strings.Contains(strings.ToLower(b.Name), q) {
			out = append(out, b)
		}
	}

	return out
}

// --- compose ----------------------------------------------------------------

func (m *Model) startCompose(kind composeKind) (tea.Model, tea.Cmd) {
	// A half-written draft parked as a minimized bar is restored rather than
	// silently replaced by a fresh composer over it.
	if m.compose != nil {
		m.composerMin = false

		if m.view != viewCompose {
			m.push(viewCompose)
		}

		return m, textinput.Blink
	}

	// The popup draws its own field labels, so the inputs carry no prompt of
	// their own: a chevron or a gutter bar inside the bordered window is the
	// kind of double chrome the flat compose view could afford and this one
	// cannot.
	to := textinput.New()
	to.Placeholder = "someone@example.com"
	to.CharLimit = 500
	to.Prompt = ""

	cc := textinput.New()
	cc.CharLimit = 500
	cc.Prompt = ""

	bcc := textinput.New()
	bcc.CharLimit = 500
	bcc.Prompt = ""

	subject := textinput.New()
	subject.CharLimit = 300
	subject.Prompt = ""

	body := textarea.New()
	body.ShowLineNumbers = false
	body.Prompt = ""

	c := &composeState{kind: kind, to: to, cc: cc, bcc: bcc, subject: subject, body: body}

	if kind != composeNew {
		msg, ok := m.currentMessage()
		if !ok {
			m.err = fmt.Errorf("no message to reply to")

			return m, nil
		}

		// Quoting needs the decrypted body. Starting the compose without it
		// would silently send unquoted, so the reply waits for the load it
		// kicks off here instead.
		if m.body.MessageID != msg.ID {
			m.notify("body still loading")
			m.loading = true

			return m, m.loadBody(msg.ID)
		}

		// The subject came from the provider; sanitized here because it goes
		// back on screen through the composer's own input views.
		subj := terminal.SanitizeLine(msg.Subject)

		switch kind {
		case composeForward:
			if !strings.HasPrefix(strings.ToLower(subj), "fw:") &&
				!strings.HasPrefix(strings.ToLower(subj), "fwd:") {
				subj = "Fwd: " + subj
			}

			// mail.Draft's only threading field is InReplyTo, so a forward
			// records its parent there too: the provider still learns which
			// message the draft descends from.
			c.inReplyTo = msg.ID
		default:
			if !strings.HasPrefix(strings.ToLower(subj), "re:") {
				subj = "Re: " + subj
			}

			reply := msg.From
			if len(msg.ReplyTo) > 0 {
				reply = msg.ReplyTo[0]
			}

			c.to.SetValue(reply.Address)
			c.inReplyTo = msg.ID
		}

		if kind == composeReplyAll {
			// The reply target is already in To, so it is deduplicated out of
			// the Cc list along with our own address and repeats.
			replyTo := msg.From
			if len(msg.ReplyTo) > 0 {
				replyTo = msg.ReplyTo[0]
			}

			var others []string

			seen := map[string]bool{}

			for _, a := range append(append([]fmail.Address{}, msg.To...), msg.CC...) {
				key := strings.ToLower(a.Address)
				if a.Address == "" || seen[key] ||
					strings.EqualFold(a.Address, m.account) ||
					strings.EqualFold(a.Address, replyTo.Address) {
					continue
				}

				seen[key] = true
				others = append(others, a.Address)
			}

			c.cc.SetValue(strings.Join(others, ", "))
		}

		c.subject.SetValue(subj)

		c.body.SetValue("\n\n" + quote(terminal.SanitizeLine(msg.From.Display()),
			msg.Time.Format("2 Jan 2006 15:04"), terminal.Sanitize(renderBody(m.body))))
		c.body.CursorStart()
	}

	c.to.Focus()

	m.compose = c
	m.composerMin, m.composerMax = false, false
	m.sizeCompose()
	m.push(viewCompose)

	return m, textinput.Blink
}

// currentMessage is the message an action refers to: the one being read, or
// the one highlighted in a thread.
func (m *Model) currentMessage() (fmail.Message, bool) {
	if len(m.thread.Messages) == 0 {
		return fmail.Message{}, false
	}

	i := minInt(m.msgIdx, len(m.thread.Messages)-1)

	return m.thread.Messages[i], true
}

func (m *Model) handleComposeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := m.compose

	switch msg.String() {
	case "esc":
		// Esc minimizes rather than discards: a draft is work, and losing it
		// to the same key that closes every other popup was a trap. Discarding
		// stays on the ✕ and 🗑 buttons.
		m.composerMin = true

		if m.view == viewCompose {
			m.pop()
		}

		return m, nil

	case "ctrl+s":
		// A save already in flight owns the draft; a second one would race it
		// and create a duplicate.
		if m.loading {
			return m, nil
		}

		m.loading = true

		return m, m.sendCompose(false)

	case "ctrl+d":
		// Sending is deliberately a two-key chord rather than enter: enter is
		// a newline in the body, and an accidental send cannot be undone.
		// While a send is in flight the chord is inert, or holding it would
		// deliver the mail twice.
		if m.loading {
			return m, nil
		}

		m.loading = true

		return m, m.sendCompose(true)

	case "tab":
		c.field = (c.field + 1) % 5
		m.focusComposeField()
		m.sizeCompose()

		return m, nil

	case "shift+tab":
		c.field = (c.field + 4) % 5
		m.focusComposeField()
		m.sizeCompose()

		return m, nil
	}

	var cmd tea.Cmd

	switch c.field {
	case 0:
		c.to, cmd = c.to.Update(msg)
	case 1:
		c.cc, cmd = c.cc.Update(msg)
	case 2:
		c.bcc, cmd = c.bcc.Update(msg)
	case 3:
		c.subject, cmd = c.subject.Update(msg)
	default:
		c.body, cmd = c.body.Update(msg)
	}

	return m, cmd
}

func (m *Model) focusComposeField() {
	c := m.compose

	c.to.Blur()
	c.cc.Blur()
	c.bcc.Blur()
	c.subject.Blur()
	c.body.Blur()

	switch c.field {
	case 0:
		c.to.Focus()
	case 1:
		c.cc.Focus()
	case 2:
		c.bcc.Focus()
	case 3:
		c.subject.Focus()
	default:
		c.body.Focus()
	}
}

func quote(who, when, body string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "On %s, %s wrote:\n", when, who)

	for _, line := range strings.Split(body, "\n") {
		b.WriteString("> ")
		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String()
}

// parseAddresses accepts a comma-separated list, with or without display
// names. The whole string goes through the RFC 5322 parser first, because a
// quoted display name may itself carry a comma — `"Doe, John" <j@x>` — which a
// naive split would cut in half; the split path remains as the fallback for
// the bare, human forms the strict parser rejects.
func parseAddresses(in string) ([]fmail.Address, error) {
	if list, err := mail.ParseAddressList(in); err == nil && len(list) > 0 {
		out := make([]fmail.Address, 0, len(list))

		for _, a := range list {
			out = append(out, fmail.Address{Name: a.Name, Address: a.Address})
		}

		return out, nil
	}

	var out []fmail.Address

	for _, part := range strings.Split(in, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		a, err := mail.ParseAddress(part)
		if err != nil {
			if !strings.Contains(part, "@") {
				return nil, fmt.Errorf("not an email address: %q", part)
			}

			out = append(out, fmail.Address{Address: part})

			continue
		}

		out = append(out, fmail.Address{Name: a.Name, Address: a.Address})
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("no recipients")
	}

	return out, nil
}

// parseOptionalAddresses is parseAddresses for a field that may be empty: a
// blank Cc is not an error, but a Cc that fails to parse must be said rather
// than silently dropped.
func parseOptionalAddresses(in string) ([]fmail.Address, error) {
	if strings.TrimSpace(in) == "" {
		return nil, nil
	}

	return parseAddresses(in)
}

// --- navigation -------------------------------------------------------------

func (m *Model) goBack() (tea.Model, tea.Cmd) {
	switch m.view {
	case viewMessage:
		m.view = viewThread
	case viewThread:
		m.view = viewThreads
	case viewThreads:
		m.view = viewBoxes
		m.filter.SetValue("")
		m.list.ClearSelection()
	case viewNoteRead:
		m.view = viewNotes
	case viewEventDetail:
		// The detail view's own esc pops the stack; h and left arrive here
		// instead and have to do the same, or they are dead keys in it.
		m.pop()
	case viewScreener, viewCompose, viewMovePicker, viewEventForm,
		viewHabits, viewTodos, viewCalendars, viewNoteEdit:
		m.compose = nil
		m.eventForm = nil
		m.band = nil
		m.picker = nil
		m.noteEd = nil
		m.pop()
	}

	return m, nil
}

func (m *Model) drillIn() (tea.Model, tea.Cmd) {
	switch m.view {
	case viewBoxes:
		if len(m.boxes) == 0 {
			return m, nil
		}

		m.box = m.boxes[m.boxIdx]
		m.view = viewThreads
		m.loading = true
		m.resetMailContext()

		return m, m.loadConvs(m.box.ID, "")

	case viewThreads:
		c, ok := m.conversationAt(m.list.Cursor())
		if !ok {
			return m, nil
		}

		m.loading = true

		return m, m.loadThread(c.ID)

	case viewThread:
		if len(m.thread.Messages) == 0 {
			return m, nil
		}

		m.loading = true

		return m, m.loadBody(m.thread.Messages[m.msgIdx].ID)

	}

	return m, nil
}

func (m *Model) itemCount() int {
	switch m.view {
	case viewBoxes:
		return len(m.boxes)
	case viewThreads:
		return m.list.Len()
	case viewThread:
		return len(m.thread.Messages)
	case viewMessage:
		return len(m.bodyLines)
	case viewScreener:
		return len(m.senders)
	case viewCalendar:
		return len(m.events)
	case viewNotes:
		return len(m.notes)
	case viewNoteRead:
		return len(m.noteLines)
	}

	return 0
}

// pageSize is how many rows the list may draw.
//
// View measures the rendered header and footer and leaves the rest; this is
// only the fallback for the first frame, before anything has been measured.
func (m *Model) pageSize() int {
	if m.listRows > 0 {
		return m.listRows
	}

	return maxInt(1, m.height-14)
}

// moveCursorBy moves the cursor and, when the split view's thread pane is what
// j/k walk, loads the body of the newly selected card the way a click on it
// does: without this the highlight moved but the pane kept the old message.
func (m *Model) moveCursorBy(delta int) (tea.Model, tea.Cmd) {
	prev := m.msgIdx

	m.moveCursor(delta)

	if m.splitMail() && m.view == viewThread && m.msgIdx != prev &&
		m.msgIdx >= 0 && m.msgIdx < len(m.thread.Messages) {
		m.bodyTop = 0
		m.loading = true

		return m, m.loadBody(m.thread.Messages[m.msgIdx].ID)
	}

	return m, nil
}

func (m *Model) moveCursor(delta int) {
	if m.view == viewMessage {
		m.bodyTop = clamp(m.bodyTop+delta, 0, maxInt(0, len(m.bodyLines)-m.bodyViewRows()))

		return
	}

	if m.view == viewNoteRead {
		m.noteTop = clamp(m.noteTop+delta, 0, maxInt(0, len(m.noteLines)-m.pageSize()))

		return
	}

	m.setCursor(m.cursor() + delta)
}

func (m *Model) cursor() int {
	switch m.view {
	case viewBoxes:
		return m.boxIdx
	case viewThreads:
		return m.list.Cursor()
	case viewThread:
		return m.msgIdx
	case viewScreener:
		return m.senderIdx
	case viewCalendar, viewNotes:
		return m.extraIdx
	}

	return 0
}

func (m *Model) setCursor(i int) {
	n := m.itemCount()
	if n == 0 {
		return
	}

	i = clamp(i, 0, n-1)

	switch m.view {
	case viewBoxes:
		m.boxIdx = i
		m.boxFirst = scrollTo(m.boxFirst, i, m.pageSize())
	case viewThreads:
		m.list.SetCursor(i)

		// The split view's list pane scrolls itself: it keeps the cursor in
		// sight the way the single-pane list does through its own renderer.
		if m.splitMail() {
			m.paneTop = scrollTo(m.paneTop, i, maxInt(1, listPageSize(m.pageSize())))
		}
	case viewThread:
		m.msgIdx = i
	case viewScreener:
		m.senderIdx = i
	case viewCalendar, viewNotes:
		m.extraIdx = i
	}
}

// pickQuickBoxes chooses what the number keys bind to: the system boxes, in
// the order every other mail client puts them in.
//
// The screener's own boxes are deliberately absent. The bar says where mail
// lives, and the Imbox, the Feed and the Paper Trail are not places mail rests
// in this account: they are a queue you visit to decide about a sender. They
// stay one keystroke away through ctrl+s, and one screen away in the full box
// list, which is where a box that is not a destination belongs.
func pickQuickBoxes(boxes []fmail.Box) []fmail.Box {
	var out []fmail.Box

	for _, name := range []string{"Inbox", "Starred", "Archive", "Sent", "Drafts", "Spam", "Trash"} {
		for _, b := range boxes {
			if b.Kind == fmail.BoxSystem && b.Name == name {
				out = append(out, b)

				break
			}
		}
	}

	if len(out) > 9 {
		out = out[:9]
	}

	return out
}
