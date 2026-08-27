// Package tui is the Bubble Tea shell.
//
// The render path never touches the network. Every view reads the SQLite warm
// cache; anything that needs the provider goes through a command that returns
// a message, so the UI stays responsive while it happens.
package tui

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	fcal "github.com/mhrsntrk/frankenstein-cli/internal/calendar"
	"github.com/mhrsntrk/frankenstein-cli/internal/config"
	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
	"github.com/mhrsntrk/frankenstein-cli/internal/store"
	fsync "github.com/mhrsntrk/frankenstein-cli/internal/sync"
)

// view is which screen is on top of the stack.
type view int

const (
	viewBoxes view = iota
	viewThreads
	viewThread
	viewMessage
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true)
	headerStyle   = lipgloss.NewStyle().Bold(true).Underline(true)
	selectedStyle = lipgloss.NewStyle().Reverse(true)
	unreadStyle   = lipgloss.NewStyle().Bold(true)
	dimStyle      = lipgloss.NewStyle().Faint(true)
	errorStyle    = lipgloss.NewStyle().Bold(true)
	statusStyle   = lipgloss.NewStyle().Faint(true)

	// keyStyle marks the letter you actually press, so the footer reads as
	// bindings rather than prose.
	keyStyle = lipgloss.NewStyle().Bold(true)

	sectionStyle = lipgloss.NewStyle().Faint(true).Underline(true)

	// bannerStyle is the screener call to action.
	bannerStyle = lipgloss.NewStyle().Reverse(true).Bold(true)
)

// Model is the whole application state.
type Model struct {
	store  *store.Store
	syncer *fsync.Syncer

	// cal is optional; the agenda line is skipped when it is nil.
	cal        fcal.Provider
	calendarID string

	view view

	boxes    []mail.Box
	boxIdx   int
	boxFirst int

	convs     []mail.Conversation
	convIdx   int
	convFirst int
	box       mail.Box

	thread mail.Thread
	msgIdx int

	body      mail.Body
	bodyLines []string
	bodyTop   int

	events []fcal.Event

	// Chrome state. quickBoxes are the boxes bound to the number keys, in the
	// order they are shown; pending drives the screener banner.
	quickBoxes []mail.Box
	pending    int
	account    string

	filter    textinput.Model
	filtering bool

	screener config.ScreenerConfig

	width, height int

	status  string
	err     error
	loading bool

	quitting bool
}

// New builds the model. The provider is reached only through the syncer.
func New(st *store.Store, syncer *fsync.Syncer, cal fcal.Provider, calendarID string, sc config.ScreenerConfig) *Model {
	ti := textinput.New()
	ti.Placeholder = "filter"
	ti.CharLimit = 80

	return &Model{
		store:      st,
		syncer:     syncer,
		cal:        cal,
		calendarID: calendarID,
		filter:     ti,
		screener:   sc,
		status:     "loading",
	}
}

// --- messages ---------------------------------------------------------------

type boxesMsg []mail.Box
type convsMsg []mail.Conversation
type threadMsg mail.Thread
type bodyMsg mail.Body
type eventsMsg []fcal.Event
type chromeMsg struct {
	pending int
	account string
}
type syncedMsg fsync.Result
type errMsg struct{ err error }

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.loadBoxes(), m.loadEvents(), m.loadChrome(), m.syncOnce())
}

func (m *Model) loadBoxes() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		boxes, err := m.store.Boxes(ctx)
		if err != nil {
			return errMsg{err}
		}

		// Empty user labels are noise in a picker.
		kept := boxes[:0:0]

		for _, b := range boxes {
			if b.Total == 0 && b.Kind != mail.BoxSystem {
				continue
			}

			kept = append(kept, b)
		}

		return boxesMsg(kept)
	}
}

// loadChrome fetches the small facts the header and banner need.
func (m *Model) loadChrome() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		out := chromeMsg{}

		if n, err := m.store.PendingSenders(ctx); err == nil {
			out.pending = n
		}

		if v, err := m.store.Meta(ctx, "account_email"); err == nil {
			out.account = v
		}

		return out
	}
}

func (m *Model) loadConvs(boxID, search string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		convs, err := m.store.Conversations(ctx, mail.ListOptions{
			BoxID:  boxID,
			Limit:  500,
			Search: search,
			Desc:   true,
		})
		if err != nil {
			return errMsg{err}
		}

		return convsMsg(convs)
	}
}

func (m *Model) loadThread(id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		t, err := m.syncer.Thread(ctx, id)
		if err != nil {
			return errMsg{err}
		}

		return threadMsg(t)
	}
}

func (m *Model) loadBody(id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		b, err := m.syncer.Body(ctx, id)
		if err != nil {
			return errMsg{err}
		}

		return bodyMsg(b)
	}
}

func (m *Model) loadEvents() tea.Cmd {
	if m.cal == nil {
		return nil
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		now := time.Now()
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)

		events, err := m.cal.Events(ctx, m.calendarID, start, start.AddDate(0, 0, 1))
		if err != nil {
			// A calendar failure must not take the mail client down with it.
			return eventsMsg(nil)
		}

		return eventsMsg(events)
	}
}

func (m *Model) syncOnce() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		res, err := m.syncer.Once(ctx)
		if err != nil {
			return errMsg{err}
		}

		return syncedMsg(res)
	}
}

// --- update -----------------------------------------------------------------

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

		if m.view == viewMessage {
			m.rewrapBody()
		}

		return m, nil

	case boxesMsg:
		m.boxes = msg
		m.quickBoxes = pickQuickBoxes(msg, m.screener)
		m.status = fmt.Sprintf("%d boxes", len(msg))

		return m, nil

	case chromeMsg:
		m.pending = msg.pending
		m.account = msg.account

		return m, nil

	case convsMsg:
		m.convs = msg
		m.convIdx, m.convFirst = 0, 0
		m.loading = false

		return m, nil

	case threadMsg:
		m.thread = mail.Thread(msg)
		m.msgIdx = len(m.thread.Messages) - 1

		if m.msgIdx < 0 {
			m.msgIdx = 0
		}

		m.view = viewThread
		m.loading = false

		return m, nil

	case bodyMsg:
		m.body = mail.Body(msg)
		m.bodyTop = 0
		m.view = viewMessage
		m.loading = false
		m.rewrapBody()

		return m, nil

	case eventsMsg:
		m.events = msg

		return m, nil

	case syncedMsg:
		m.status = fmt.Sprintf("synced %d conversations", msg.Conversations)

		return m, tea.Batch(m.loadBoxes(), m.loadChrome())

	case errMsg:
		m.err = msg.err
		m.loading = false

		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// While the filter is open every key belongs to it except the two that
	// close it, otherwise typing "q" in a search would quit the program.
	if m.filtering {
		switch msg.Type {
		case tea.KeyEnter:
			m.filtering = false
			m.filter.Blur()
			m.loading = true

			return m, m.loadConvs(m.box.ID, m.filter.Value())

		case tea.KeyEsc:
			m.filtering = false
			m.filter.SetValue("")
			m.filter.Blur()

			return m, m.loadConvs(m.box.ID, "")
		}

		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)

		return m, cmd
	}

	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true

		return m, tea.Quit

	case "esc", "backspace", "h", "left":
		return m.goBack()

	case "enter", "l", "right":
		return m.drillIn()

	case "j", "down":
		m.moveCursor(1)

		return m, nil

	case "k", "up":
		m.moveCursor(-1)

		return m, nil

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

	case "r":
		m.loading = true
		m.status = "syncing"

		return m, m.syncOnce()
	}

	// 1-9 jump straight to a box from anywhere, which is the thing that makes
	// hey-cli feel fast: you never walk back up to a list to change box.
	if k := msg.String(); len(k) == 1 && k[0] >= '1' && k[0] <= '9' {
		i := int(k[0] - '1')

		if i < len(m.quickBoxes) {
			m.box = m.quickBoxes[i]
			m.view = viewThreads
			m.loading = true
			m.filter.SetValue("")

			return m, m.loadConvs(m.box.ID, "")
		}
	}

	return m, nil
}

// pickQuickBoxes chooses what the number keys bind to: the screener's own
// boxes first, then the system boxes a person actually reads.
func pickQuickBoxes(boxes []mail.Box, sc config.ScreenerConfig) []mail.Box {
	byID := make(map[string]mail.Box, len(boxes))
	for _, b := range boxes {
		byID[b.ID] = b
	}

	var out []mail.Box

	for _, id := range []string{sc.ImboxID, sc.FeedID, sc.PaperTrailID} {
		if b, ok := byID[id]; ok && id != "" {
			out = append(out, b)
		}
	}

	want := []string{"Inbox", "Starred", "Archive", "Sent", "Drafts"}

	for _, name := range want {
		for _, b := range boxes {
			if b.Kind == mail.BoxSystem && b.Name == name {
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

func (m *Model) goBack() (tea.Model, tea.Cmd) {
	switch m.view {
	case viewMessage:
		m.view = viewThread
	case viewThread:
		m.view = viewThreads
	case viewThreads:
		m.view = viewBoxes
		m.filter.SetValue("")
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

		return m, m.loadConvs(m.box.ID, "")

	case viewThreads:
		if len(m.convs) == 0 {
			return m, nil
		}

		m.loading = true

		return m, m.loadThread(m.convs[m.convIdx].ID)

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
		return len(m.convs)
	case viewThread:
		return len(m.thread.Messages)
	case viewMessage:
		return len(m.bodyLines)
	}

	return 0
}

func (m *Model) pageSize() int {
	// Two lines of chrome at the top, one status line at the bottom.
	n := m.height - 4
	if n < 1 {
		return 1
	}

	return n
}

func (m *Model) moveCursor(delta int) {
	if m.view == viewMessage {
		m.bodyTop = clamp(m.bodyTop+delta, 0, maxInt(0, len(m.bodyLines)-m.pageSize()))

		return
	}

	m.setCursor(m.cursor() + delta)
}

func (m *Model) cursor() int {
	switch m.view {
	case viewBoxes:
		return m.boxIdx
	case viewThreads:
		return m.convIdx
	case viewThread:
		return m.msgIdx
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
		m.convIdx = i
		m.convFirst = scrollTo(m.convFirst, i, m.pageSize())
	case viewThread:
		m.msgIdx = i
	}
}

// scrollTo keeps the cursor inside the visible window with minimal movement.
func scrollTo(first, cursor, size int) int {
	if size < 1 {
		return 0
	}

	if cursor < first {
		return cursor
	}

	if cursor >= first+size {
		return cursor - size + 1
	}

	return first
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}

	if v > hi {
		return hi
	}

	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}

	return b
}
