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
	"github.com/mhrsntrk/frankenstein-cli/internal/screener"
	"github.com/mhrsntrk/frankenstein-cli/internal/store"
	fsync "github.com/mhrsntrk/frankenstein-cli/internal/sync"
	"github.com/mhrsntrk/frankenstein-cli/internal/tui/heyui"
)

// How much chrome is shown, in the order it is given up on a short terminal.
// The title bar and the rule naming the box are never dropped: they are what
// say where you are.
const (
	chromeFull = iota
	chromeNoBanner
	chromeNoBoxBar
	chromeMinimal
)

// calendarView is which of hey-cli's grids the calendar section draws.
type calendarView int

const (
	calendarWeek calendarView = iota
	calendarDay
)

// view is which screen is on top.
type view int

const (
	viewBoxes view = iota
	viewThreads
	viewThread
	viewMessage
	viewScreener
	viewCompose
	viewCalendar
	viewJournal
	viewMovePicker
)

// section is the top-level area, switched with tab.
type section int

const (
	sectionMail section = iota
	sectionCalendar
	sectionJournal
)

// The chrome takes its colours from hey-cli's theme, so the parts this project
// draws sit in the same palette as the rows their renderer draws.
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
	subject textinput.Model
	body    textarea.Model

	// field is which input has focus: 0 to, 1 cc, 2 subject, 3 body.
	field int

	// inReplyTo is the message being answered, empty for a fresh compose.
	inReplyTo string
}

// Model is the whole application state.
type Model struct {
	store    *store.Store
	syncer   *fsync.Syncer
	provider mail.Provider
	screener *screener.Screener
	personal *personal.Store

	cal        fcal.Provider
	calendarID string

	cfg config.Config

	view    view
	section section

	// stack remembers where to return to when a transient view closes.
	stack []view

	boxes    []mail.Box
	boxIdx   int
	boxFirst int

	convs []mail.Conversation
	box   mail.Box

	// list is hey-cli's own row renderer. It owns the cursor, the scroll
	// position and the selection for the thread list.
	list *heyui.List

	thread mail.Thread
	msgIdx int

	body      mail.Body
	bodyLines []string
	bodyTop   int

	events []fcal.Event

	// calErr is why the calendar is empty, when it is empty for a reason.
	calErr error

	// calView is which grid is drawn, and calOffset how many days or weeks
	// away from today it is.
	calView   calendarView
	calOffset int

	journal  []personal.JournalEntry
	extraIdx int

	senders   []screener.Sender
	senderIdx int

	compose *composeState

	// moveTargets is the box picker's list, shown over the thread list.
	moveTargets []mail.Box
	moveIdx     int
	moveFilter  textinput.Model

	filter    textinput.Model
	filtering bool

	quickBoxes []mail.Box
	pending    int
	account    string

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
	sc *screener.Screener,
	ps *personal.Store,
	cal fcal.Provider,
	cfg config.Config,
) *Model {
	filter := textinput.New()
	filter.Placeholder = "filter"
	filter.CharLimit = 120

	move := textinput.New()
	move.Placeholder = "box"
	move.CharLimit = 60

	return &Model{
		store:      st,
		syncer:     syncer,
		provider:   p,
		screener:   sc,
		personal:   ps,
		cal:        cal,
		calendarID: cfg.Calendar.CalendarID,
		cfg:        cfg,
		filter:     filter,
		moveFilter: move,
		list:       heyui.NewList(),
		status:     "loading",
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
	if m.list.SelectionCount() > 0 {
		out := make([]string, 0, m.list.SelectionCount())

		for _, c := range m.convs {
			if m.list.Selected(heyui.PostingID(c.ID)) {
				out = append(out, c.ID)
			}
		}

		return out
	}

	if m.view == viewThreads {
		if c, ok := m.conversationAt(m.list.Cursor()); ok {
			return []string{c.ID}
		}
	}

	if (m.view == viewThread || m.view == viewMessage) && m.thread.Conversation.ID != "" {
		return []string{m.thread.Conversation.ID}
	}

	return nil
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

// bg runs work off the render path with a timeout.
func bg(d time.Duration, fn func(context.Context) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), d)
		defer cancel()

		return fn(ctx)
	}
}
