// Package tui is the Bubble Tea client.
//
// The render path never touches the network. Views read the SQLite warm cache;
// anything that needs the provider goes through a tea.Cmd so the interface
// stays responsive while it happens.
package tui

import (
	"context"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	fcal "github.com/mhrsntrk/frankenstein-cli/internal/calendar"
	"github.com/mhrsntrk/frankenstein-cli/internal/config"
	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
	"github.com/mhrsntrk/frankenstein-cli/internal/personal"
	"github.com/mhrsntrk/frankenstein-cli/internal/store"
	fsync "github.com/mhrsntrk/frankenstein-cli/internal/sync"
	"github.com/mhrsntrk/frankenstein-cli/internal/tui/heyui"
)

// How much chrome is shown, in the order it is given up on a short terminal:
// first the extras (today's agenda line, the footer's rule and key hints),
// then the box bar, then the section row. The title bar and the rule naming
// the box are never dropped: they are what say where you are.
const (
	chromeFull = iota
	chromeNoExtras
	chromeNoBoxBar
	chromeMinimal
)

// calendarView is which of hey-cli's grids the calendar section draws.
type calendarView int

// The order matches the sub-nav: 1 Day, 2 Week, 3 Year.
const (
	calendarDay calendarView = iota
	calendarWeek
	calendarYear
)

// TodoItem is one todo, as little of it as the ribbon needs. The ID is what
// completion goes by: titles are not unique, and completing by title marks
// the wrong one done when two match.
type TodoItem struct {
	ID    int64
	Title string
	Done  bool
}

// TodoLister reads the todo list.
type TodoLister func(context.Context) ([]TodoItem, error)

// Todos is how the command layer supplies todo access. Any nil field simply
// makes that operation unavailable rather than failing.
type Todos struct {
	List     TodoLister
	Add      func(context.Context, string) error
	Complete func(context.Context, int64) error
}

// view is which screen is on top.
type view int

const (
	viewBoxes view = iota
	viewThreads
	viewThread
	viewMessage
	viewCompose
	viewCalendar
	viewNotes
	viewMovePicker
	viewEventForm
	viewEventDetail
	viewHabits
	viewTodos
	viewCalendars
	viewNoteRead
	viewNoteEdit
)

// section is the top-level area, switched with tab.
type section int

const (
	sectionMail section = iota
	sectionCalendar
	sectionNotes
)

// The chrome takes its colours from hey-cli's theme, so the header, the lists
// and the calendar grids all sit in one palette.
var (
	titleStyle    = lipgloss.NewStyle().Foreground(heyui.Bright()).Bold(true)
	selectedStyle = lipgloss.NewStyle().Reverse(true)
	unreadStyle   = lipgloss.NewStyle().Bold(true)
	dimStyle      = heyui.MutedStyle()
	errorStyle    = lipgloss.NewStyle().Foreground(heyui.Alert()).Bold(true)
	statusStyle   = heyui.MutedStyle()
	keyStyle      = lipgloss.NewStyle().Foreground(heyui.Bright()).Bold(true)
	bannerStyle   = lipgloss.NewStyle().Reverse(true).Bold(true)
	okStyle       = lipgloss.NewStyle().Foreground(heyui.Primary()).Bold(true)
	markStyle     = lipgloss.NewStyle().Foreground(heyui.Primary()).Bold(true)
)

// composeKind distinguishes what a compose form is for, which decides the
// prefilled recipients, the subject prefix and the quoted body.
type composeKind int

const (
	composeNew composeKind = iota
	composeReply
	composeReplyAll
	composeForward
)

// composeState is the editor's own state.
type composeState struct {
	kind composeKind

	to      textinput.Model
	cc      textinput.Model
	bcc     textinput.Model
	subject textinput.Model
	body    textarea.Model

	// field is which input has focus: 0 to, 1 cc, 2 bcc, 3 subject, 4 body.
	field int

	// inReplyTo is the message being answered or forwarded, empty for a fresh
	// compose. It is the only threading field mail.Draft carries, so a forward
	// uses it too: the provider still learns which message the draft descends
	// from.
	inReplyTo string

	// draftID is the saved draft on the server, empty until the first ctrl+s.
	// Saving again with an ID updates that draft rather than creating a new
	// one, which is what Provider.Draft documents for a non-empty ID.
	draftID string

	// sending is true while a send or a save is in flight, so holding the
	// chord cannot deliver the same mail twice. It belongs to the composer
	// rather than the model: a background sync sets m.loading as well, and
	// that must not leave the composer refusing to send.
	sending bool
}

// Model is the whole application state.
type Model struct {
	store    *store.Store
	syncer   *fsync.Syncer
	provider mail.Provider
	personal *personal.Store

	cal        fcal.Provider
	calendarID string

	// calendarIDs are the calendars drawn, calColours what each draws in, and
	// calNames what to call one in the detail view.
	calendarIDs []string
	calColours  map[string]string
	calNames    map[string]string

	// detailID is the event the detail view is showing, held by ID so a
	// reload underneath it cannot swap in a different event.
	detailID string

	// saveCalendars persists the picker's choice. Supplied by the command
	// layer, which owns the config file.
	saveCalendars func([]string) error

	cfg config.Config

	view    view
	section section

	// stack remembers where to return to when a transient view closes.
	stack []view

	boxes    []mail.Box
	boxIdx   int
	boxFirst int

	box mail.Box

	// list is the thread list: the conversations, the cursor, the scroll
	// window and the selection. It draws them in the order the store gave.
	list mailList

	thread mail.Thread
	msgIdx int

	body      mail.Body
	bodyLines []string
	bodyTop   int

	events []fcal.Event

	// calErr is why the calendar is empty, when it is empty for a reason.
	calErr error

	// eventsGen tags each loadEvents with a generation, so a slow response
	// from an old window cannot overwrite the one now on screen.
	eventsGen int

	// convsGen, threadGen and bodyGen do for the mail loads what eventsGen
	// does for the calendar: each request carries the generation it was issued
	// under, and a response overtaken by a newer request is dropped rather
	// than applied over it.
	convsGen  int
	threadGen int
	bodyGen   int

	// calView is which grid is drawn, and calOffset how many days or weeks
	// away from today it is.
	calView   calendarView
	calOffset int

	// calHabits and calTodos fill the bands above and below the grid. Both
	// are local, and live in the same SQLite file as the mail cache.
	calHabits []heyui.Habit
	calTodos  []heyui.Todo

	// The todo functions are supplied by the command layer, so this package
	// needs no Google dependency. Nil means the ribbon and its manager are
	// simply unavailable.
	todos          TodoLister
	addTodoFn      func(context.Context, string) error
	completeTodoFn func(context.Context, int64) error

	// notesDir is the folder of markdown files the Notes section reads and
	// writes. The folder is the store: no index sits between it and the list.
	notesDir string

	notes []personal.Note

	// openNote is the note being read, noteRaw its markdown as loaded,
	// noteLines the wrapped text and noteTop the scroll position. The raw
	// text is kept so a resize can re-wrap without another disk read.
	openNote  personal.Note
	noteRaw   string
	noteLines []string
	noteTop   int

	noteEd *noteEditor

	extraIdx int

	compose *composeState

	// composerMin and composerMax are the popup's window state: parked as a
	// bar at the bottom right, or grown to nearly the whole screen. Both
	// survive the popup losing focus, the way Proton's window does.
	composerMin bool
	composerMax bool

	eventForm *eventForm
	band      *bandEditor
	picker    *calendarPicker

	// moveTargets is the box picker's list, shown over the thread list.
	moveTargets []mail.Box
	moveIdx     int
	moveFilter  textinput.Model

	filter    textinput.Model
	filtering bool

	quickBoxes []mail.Box

	// categoryID is the inbox category the listing is narrowed to, empty for
	// all of it. It is a filter rather than a location: m.box stays the Inbox
	// while one is picked, so the header and the box bar keep saying so.
	categoryID string

	account string

	width, height int

	// listRows is what View measured as left over for the list after the
	// header and footer were rendered.
	listRows int

	// chromeLevel is how much of the header and footer has been given up to
	// fit a short terminal. It is recomputed on every resize.
	chromeLevel int

	status   string
	flash    string
	err      error
	loading  bool
	help     bool
	quitting bool
}

// New builds the model. Everything that writes goes through the provider;
// everything that reads comes from the store.
func New(
	st *store.Store,
	syncer *fsync.Syncer,
	p mail.Provider,
	ps *personal.Store,
	cal fcal.Provider,
	todos Todos,
	saveCalendars func([]string) error,
	cfg config.Config,
) *Model {
	filter := textinput.New()
	filter.Placeholder = "filter"
	filter.CharLimit = 120

	move := textinput.New()
	move.Placeholder = "box"
	move.CharLimit = 60

	// The notes folder comes from config rather than a constructor argument:
	// it is a local path, and every caller would pass the same one. An error
	// here only means no home directory, where nothing else works either.
	notesDir, _ := config.NotesDir()

	return &Model{
		notesDir:       notesDir,
		store:          st,
		syncer:         syncer,
		provider:       p,
		personal:       ps,
		cal:            cal,
		calendarIDs:    cfg.Calendar.Shown(),
		calColours:     map[string]string{},
		calNames:       map[string]string{},
		saveCalendars:  saveCalendars,
		todos:          todos.List,
		addTodoFn:      todos.Add,
		completeTodoFn: todos.Complete,
		calendarID:     cfg.Calendar.CalendarID,
		cfg:            cfg,
		filter:         filter,
		moveFilter:     move,
		calView:        calendarWeek,

		// Mail opens on the mail itself. The box list is a place to go when
		// the bar does not have the box you want, not the front door.
		view:    viewThreads,
		loading: true,
		status:  "loading",
	}
}

// push remembers the current view before opening a transient one.
func (m *Model) push(next view) {
	m.stack = append(m.stack, m.view)
	m.view = next
}

// pop returns to whatever was open before the transient view.
func (m *Model) pop() {
	if len(m.stack) == 0 {
		m.view = viewThreads

		return
	}

	m.view = m.stack[len(m.stack)-1]
	m.stack = m.stack[:len(m.stack)-1]
}

// notify shows a transient message in the status line.
func (m *Model) notify(s string) { m.flash = s }

// targets returns the conversations an action applies to: the selection if
// there is one, otherwise the row under the cursor. This is what makes every
// action work in bulk without a separate set of bulk commands.
func (m *Model) targets() []string {
	if ids := m.list.selectedIDs(); len(ids) > 0 {
		return ids
	}

	if m.view == viewThreads {
		if c, ok := m.list.at(m.list.cursor); ok {
			return []string{c.ID}
		}
	}

	if (m.view == viewThread || m.view == viewMessage) && m.thread.Conversation.ID != "" {
		return []string{m.thread.Conversation.ID}
	}

	return nil
}

// defaultBox is the box the mail section opens on: the Inbox, or the first
// box on the bar for an account that somehow has no box by that name.
func (m *Model) defaultBox() (mail.Box, bool) {
	if box, ok := m.boxByName("Inbox"); ok {
		return box, true
	}

	if len(m.quickBoxes) > 0 {
		return m.quickBoxes[0], true
	}

	return mail.Box{}, false
}

// boxByName finds a cached box by exact name, which is how the TUI refers to
// system boxes without importing a provider's label IDs.
func (m *Model) boxByName(name string) (mail.Box, bool) {
	for _, b := range m.boxes {
		if b.Name == name {
			return b, true
		}
	}

	return mail.Box{}, false
}

// categoryAllLabel names the entry that clears the category filter. It leads
// the row, and is what the click map looks for, so it is written once.
const categoryAllLabel = "All"

// categoryOrder is the order the inbox categories are offered in.
//
// They are matched by name because the build forbids this package importing
// the provider's label IDs, so the ordering cannot be written as a list of
// them; the provider stamps mail.BoxCategory on each one on the way through.
var categoryOrder = []string{
	"Primary", "Social", "Promotions", "Newsletters", "Updates", "Forums",
}

// categoryBoxes are the account's inbox categories, in categoryOrder. One this
// list does not name is appended rather than dropped, so a category the
// provider starts sending still reaches the row.
func (m *Model) categoryBoxes() []mail.Box {
	out := make([]mail.Box, 0, len(categoryOrder))
	taken := make(map[string]bool, len(categoryOrder))

	for _, name := range categoryOrder {
		for _, b := range m.boxes {
			if b.Kind == mail.BoxCategory && b.Name == name {
				out = append(out, b)
				taken[b.ID] = true

				break
			}
		}
	}

	for _, b := range m.boxes {
		if b.Kind == mail.BoxCategory && !taken[b.ID] {
			out = append(out, b)
		}
	}

	return out
}

// categoryRowShown reports whether the category sub-row belongs on screen. A
// category narrows one listing, so it is only offered where that listing is
// what is being read: the Inbox's own threads.
func (m *Model) categoryRowShown() bool {
	return m.mailChrome() && m.boxActive() &&
		m.box.Name == "Inbox" && len(m.categoryBoxes()) > 0
}

// listBoxID is what the thread list is listed by. Proton models the categories
// as labels on the same conversations, so narrowing to one is listing by its
// ID; the box itself is what is listed the rest of the time.
func (m *Model) listBoxID() string {
	if m.categoryID != "" {
		return m.categoryID
	}

	return m.box.ID
}

// inMailContext reports whether what is on screen is still the mail section:
// one of the mail views, or the composer floating over them. A thread or body
// that lands outside this context is stored but must not adopt the view, or a
// slow load would yank the user out of whatever they navigated to since.
func (m *Model) inMailContext() bool {
	return m.section == sectionMail && (mailSplitView(m.view) || m.view == viewCompose)
}

// resetMailContext clears what belongs to the previous box or filter: the open
// thread and body would otherwise linger in the split view's right pane, and
// the old cursor position is meaningless against a new listing.
func (m *Model) resetMailContext() {
	// The category narrows one box's listing, so it cannot outlive a move to
	// another box or a new search. Picking a category sets it again after.
	m.categoryID = ""

	m.thread = mail.Thread{}
	m.body = mail.Body{}
	m.bodyLines = nil
	m.msgIdx = 0
	m.bodyTop = 0
	m.list.cursor = 0
	m.list.top = 0
}

// splitMail reports whether the mail section draws the Proton-style two-pane
// layout. Below the threshold the panes would each be too narrow to read, so
// the single-pane drill-down remains the fallback.
func (m *Model) splitMail() bool { return m.width >= 100 }

// mailSplitViews are the screens the split layout replaces: the list, the
// thread and the open message all become one screen with two panes.
func mailSplitView(v view) bool {
	return v == viewThreads || v == viewThread || v == viewMessage
}

// paneGeom splits the content width between the list and the thread pane,
// with a three-cell gap for the separator.
func (m *Model) paneGeom() (listW, gap, threadW int) {
	w := m.contentWidth()
	listW = clamp(w*2/5, 30, 70)
	gap = 3

	return listW, gap, maxInt(20, w-listW-gap)
}

// backView is the screen the composer floats over. The composer is a popup,
// not a place, so everything that renders or hit-tests the screen underneath
// looks through it.
func (m *Model) backView() view {
	if m.view != viewCompose {
		return m.view
	}

	if len(m.stack) > 0 {
		return m.stack[len(m.stack)-1]
	}

	return viewThreads
}

// bodyViewRows is how many rows of message body are on screen: the whole page
// in the single-pane view, what the thread pane's chrome leaves in the split.
func (m *Model) bodyViewRows() int {
	if m.splitMail() {
		return maxInt(1, threadBodyRows(m.thread, m.msgIdx, maxInt(1, m.pageSize())))
	}

	return m.pageSize()
}

// sizeCompose fits the composer's embedded inputs to the popup geometry,
// following the formula composerPopup documents: single-line inputs get
// lay.w-13, the body lay.w-4 wide and the rows the fixed chrome leaves.
func (m *Model) sizeCompose() {
	c := m.compose
	if c == nil {
		return
	}

	lay := composerPlace(m.width, m.height, false, m.composerMax)

	inputW := maxInt(10, lay.w-13)
	c.to.Width = inputW
	c.cc.Width = inputW
	c.bcc.Width = inputW
	c.subject.Width = inputW

	bodyRows := lay.h - 8
	if composeShowCC(c) {
		// The Cc and Bcc rows appear together and cost a row each.
		bodyRows -= 2
	}

	c.body.SetWidth(maxInt(10, lay.w-4))
	c.body.SetHeight(maxInt(1, bodyRows))
}

// bg runs work off the render path with a timeout.
func bg(d time.Duration, fn func(context.Context) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), d)
		defer cancel()

		return fn(ctx)
	}
}
