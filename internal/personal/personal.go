// Package personal holds the domains Proton has no equivalent for: habits,
// time tracking and a journal.
//
// These are local by design. Journal entries are markdown files on disk with
// only their index in SQLite, so they stay readable and greppable without this
// tool.
package personal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Store wraps the shared cache database.
type Store struct {
	db         *sql.DB
	journalDir string
}

// New returns a Store over an already-open database.
func New(db *sql.DB, journalDir string) *Store {
	return &Store{db: db, journalDir: journalDir}
}

// --- habits -----------------------------------------------------------------

// Habit is something tracked daily.
type Habit struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Archived bool   `json:"archived"`

	// Streak is consecutive days up to and including today.
	Streak int `json:"streak"`

	// DoneToday reports today's entry.
	DoneToday bool `json:"done_today"`

	// Last30 is how many of the last 30 days were done.
	Last30 int `json:"last_30"`

	// DoneDays are the days it was kept, most recent first. The calendar's
	// habit band needs the days themselves, not just a count.
	DoneDays []time.Time `json:"-"`
}

// AddHabit creates a habit.
func (s *Store) AddHabit(ctx context.Context, name string) (Habit, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Habit{}, fmt.Errorf("a habit needs a name")
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO habits (name, created) VALUES (?, ?)`, name, time.Now().Unix())
	if err != nil {
		return Habit{}, fmt.Errorf("add habit: %w", err)
	}

	id, _ := res.LastInsertId()

	return Habit{ID: id, Name: name}, nil
}

// Habits lists habits with their streaks.
func (s *Store) Habits(ctx context.Context, includeArchived bool) ([]Habit, error) {
	query := `SELECT id, name, archived FROM habits`
	if !includeArchived {
		query += ` WHERE archived = 0`
	}

	query += ` ORDER BY name`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list habits: %w", err)
	}
	defer rows.Close()

	var out []Habit

	for rows.Next() {
		var (
			h        Habit
			archived int
		)

		if err := rows.Scan(&h.ID, &h.Name, &archived); err != nil {
			return nil, err
		}

		h.Archived = archived != 0
		out = append(out, h)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range out {
		if err := s.fillHabitStats(ctx, &out[i]); err != nil {
			return nil, err
		}
	}

	return out, nil
}

// fillHabitStats computes the streak by walking back a day at a time.
//
// Days are compared as local calendar dates, never as an epoch division: a
// day is not always 86400 seconds, and dividing breaks wherever the UTC offset
// crosses zero.
func (s *Store) fillHabitStats(ctx context.Context, h *Habit) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT day FROM habit_entries WHERE habit_id = ? AND done = 1`, h.ID)
	if err != nil {
		return err
	}
	defer rows.Close()

	done := make(map[string]bool)

	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return err
		}

		done[d] = true
	}

	if err := rows.Err(); err != nil {
		return err
	}

	today := time.Now()
	h.DoneToday = done[dayKey(today)]

	// The days themselves, for anything drawing a calendar band. A year is
	// enough for any view and keeps the slice small.
	for i := range 365 {
		day := today.AddDate(0, 0, -i)
		if done[dayKey(day)] {
			h.DoneDays = append(h.DoneDays, day)
		}
	}

	// A streak survives today not being done yet: it counts back from
	// yesterday in that case, so an unfinished morning does not read as zero.
	start := 0
	if !h.DoneToday {
		start = 1
	}

	for i := start; ; i++ {
		if !done[dayKey(today.AddDate(0, 0, -i))] {
			break
		}

		h.Streak++
	}

	for i := 0; i < 30; i++ {
		if done[dayKey(today.AddDate(0, 0, -i))] {
			h.Last30++
		}
	}

	return nil
}

func dayKey(t time.Time) string { return t.Format("2006-01-02") }

// CheckHabit marks a habit done (or not) on a day.
func (s *Store) CheckHabit(ctx context.Context, name string, day time.Time, done bool) (Habit, error) {
	h, err := s.habitByName(ctx, name)
	if err != nil {
		return Habit{}, err
	}

	if done {
		_, err = s.db.ExecContext(ctx,
			`INSERT INTO habit_entries (habit_id, day, done) VALUES (?, ?, 1)
			 ON CONFLICT(habit_id, day) DO UPDATE SET done = 1`, h.ID, dayKey(day))
	} else {
		_, err = s.db.ExecContext(ctx,
			`DELETE FROM habit_entries WHERE habit_id = ? AND day = ?`, h.ID, dayKey(day))
	}

	if err != nil {
		return Habit{}, fmt.Errorf("record habit: %w", err)
	}

	if err := s.fillHabitStats(ctx, &h); err != nil {
		return h, err
	}

	return h, nil
}

// ArchiveHabit hides a habit without deleting its history.
func (s *Store) ArchiveHabit(ctx context.Context, name string) error {
	h, err := s.habitByName(ctx, name)
	if err != nil {
		return err
	}

	_, err = s.db.ExecContext(ctx, `UPDATE habits SET archived = 1 WHERE id = ?`, h.ID)

	return err
}

func (s *Store) habitByName(ctx context.Context, name string) (Habit, error) {
	var h Habit

	err := s.db.QueryRowContext(ctx,
		`SELECT id, name FROM habits WHERE lower(name) = lower(?)`, name).Scan(&h.ID, &h.Name)
	if err == sql.ErrNoRows {
		return Habit{}, fmt.Errorf("no habit called %q", name)
	}

	return h, err
}

// --- time tracking ----------------------------------------------------------

// TimeEntry is one tracked interval. Stopped is nil while running.
type TimeEntry struct {
	ID      int64      `json:"id"`
	Project string     `json:"project"`
	Note    string     `json:"note,omitempty"`
	Started time.Time  `json:"started"`
	Stopped *time.Time `json:"stopped,omitempty"`
}

// Running reports whether the entry is still open.
func (e TimeEntry) Running() bool { return e.Stopped == nil }

// Duration is how long the entry ran, or has been running.
func (e TimeEntry) Duration() time.Duration {
	if e.Stopped == nil {
		return time.Since(e.Started)
	}

	return e.Stopped.Sub(e.Started)
}

// ErrNothingRunning is returned by StopTimer when no entry is open.
var ErrNothingRunning = errors.New("nothing running")

// StartTimer opens a new entry, closing any that was already running so two
// timers can never overlap.
func (s *Store) StartTimer(ctx context.Context, project, note string) (TimeEntry, error) {
	// One transaction covers the stop and the insert: two starts racing each
	// other would otherwise both see nothing running and leave two open rows,
	// which StopTimer can only close one at a time.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TimeEntry{}, fmt.Errorf("start timer: %w", err)
	}
	defer tx.Rollback()

	if _, err := stopRunning(ctx, tx); err != nil && !errors.Is(err, ErrNothingRunning) {
		return TimeEntry{}, err
	}

	now := time.Now()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO time_entries (project, note, started) VALUES (?, ?, ?)`,
		project, note, now.Unix())
	if err != nil {
		return TimeEntry{}, fmt.Errorf("start timer: %w", err)
	}

	id, _ := res.LastInsertId()

	if err := tx.Commit(); err != nil {
		return TimeEntry{}, fmt.Errorf("start timer: %w", err)
	}

	return TimeEntry{ID: id, Project: project, Note: note, Started: now}, nil
}

// StopTimer closes the running entry. It returns ErrNothingRunning when there
// is none.
func (s *Store) StopTimer(ctx context.Context) (TimeEntry, error) {
	return stopRunning(ctx, s.db)
}

// querier is the slice of *sql.DB and *sql.Tx that stopRunning needs, so
// StopTimer and StartTimer's transaction can share one implementation.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func stopRunning(ctx context.Context, db querier) (TimeEntry, error) {
	var (
		e       TimeEntry
		started int64
	)

	err := db.QueryRowContext(ctx,
		`SELECT id, project, note, started FROM time_entries
		 WHERE stopped IS NULL ORDER BY started DESC LIMIT 1`).
		Scan(&e.ID, &e.Project, &e.Note, &started)
	if err == sql.ErrNoRows {
		return TimeEntry{}, ErrNothingRunning
	}

	if err != nil {
		return TimeEntry{}, err
	}

	now := time.Now()
	e.Started = time.Unix(started, 0)
	e.Stopped = &now

	if _, err := db.ExecContext(ctx,
		`UPDATE time_entries SET stopped = ? WHERE id = ?`, now.Unix(), e.ID); err != nil {
		return e, fmt.Errorf("stop timer: %w", err)
	}

	return e, nil
}

// TimeEntries lists entries started on or after since.
func (s *Store) TimeEntries(ctx context.Context, since time.Time) ([]TimeEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project, note, started, stopped FROM time_entries
		 WHERE started >= ? ORDER BY started DESC`, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("list time entries: %w", err)
	}
	defer rows.Close()

	var out []TimeEntry

	for rows.Next() {
		var (
			e       TimeEntry
			started int64
			stopped *int64
		)

		if err := rows.Scan(&e.ID, &e.Project, &e.Note, &started, &stopped); err != nil {
			return nil, err
		}

		e.Started = time.Unix(started, 0)

		if stopped != nil {
			t := time.Unix(*stopped, 0)
			e.Stopped = &t
		}

		out = append(out, e)
	}

	return out, rows.Err()
}

// TimeByProject totals tracked time per project since a point.
func (s *Store) TimeByProject(ctx context.Context, since time.Time) (map[string]time.Duration, error) {
	entries, err := s.TimeEntries(ctx, since)
	if err != nil {
		return nil, err
	}

	out := make(map[string]time.Duration)

	for _, e := range entries {
		name := e.Project
		if name == "" {
			name = "(unlabelled)"
		}

		out[name] += e.Duration()
	}

	return out, nil
}

// --- journal ----------------------------------------------------------------

// JournalEntry is one day's markdown file.
type JournalEntry struct {
	Day     string    `json:"day"`
	Path    string    `json:"path"`
	Title   string    `json:"title,omitempty"`
	Updated time.Time `json:"updated"`
}

// WriteJournal appends to (or creates) a day's entry and reindexes it.
func (s *Store) WriteJournal(ctx context.Context, day time.Time, title, body string) (JournalEntry, error) {
	if err := os.MkdirAll(s.journalDir, 0o700); err != nil {
		return JournalEntry{}, fmt.Errorf("create journal dir: %w", err)
	}

	key := dayKey(day)
	path := filepath.Join(s.journalDir, key+".md")

	var sb strings.Builder

	existing, err := os.ReadFile(path)
	switch {
	case err == nil:
		sb.Write(existing)

		if !strings.HasSuffix(string(existing), "\n") {
			sb.WriteString("\n")
		}

		sb.WriteString("\n")
	case os.IsNotExist(err):
		fmt.Fprintf(&sb, "# %s\n\n", day.Format("Monday, 2 January 2006"))
	default:
		return JournalEntry{}, fmt.Errorf("read journal entry: %w", err)
	}

	if title != "" {
		fmt.Fprintf(&sb, "## %s\n\n", title)
	}

	fmt.Fprintf(&sb, "%s\n", strings.TrimSpace(body))

	// The write goes to a temp file beside the entry and is renamed into
	// place, which is atomic on POSIX. Writing the target directly would
	// truncate it first, so a crash mid-write could eat the whole day.
	if err := writeFileAtomic(path, []byte(sb.String())); err != nil {
		return JournalEntry{}, fmt.Errorf("write journal entry: %w", err)
	}

	now := time.Now()

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO journal (day, path, title, updated) VALUES (?, ?, ?, ?)
		ON CONFLICT(day) DO UPDATE SET
			path = excluded.path,
			title = CASE WHEN excluded.title <> '' THEN excluded.title ELSE journal.title END,
			updated = excluded.updated`,
		key, path, title, now.Unix()); err != nil {
		return JournalEntry{}, fmt.Errorf("index journal entry: %w", err)
	}

	return JournalEntry{Day: key, Path: path, Title: title, Updated: now}, nil
}

// writeFileAtomic writes data to a temp file in path's directory and renames
// it over path, so the entry on disk is always either the old content or the
// new, never a truncated half.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}

	_, err = tmp.Write(data)
	if err == nil {
		// CreateTemp already opened it 0600, but keep that explicit: the
		// journal is private and the mode survives the rename.
		err = tmp.Chmod(0o600)
	}

	if cerr := tmp.Close(); err == nil {
		err = cerr
	}

	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}

	if err != nil {
		os.Remove(tmp.Name())

		return err
	}

	return nil
}

// JournalEntries lists indexed entries, newest first.
func (s *Store) JournalEntries(ctx context.Context, limit int) ([]JournalEntry, error) {
	query := `SELECT day, path, title, updated FROM journal ORDER BY day DESC`

	var args []any

	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list journal: %w", err)
	}
	defer rows.Close()

	var out []JournalEntry

	for rows.Next() {
		var (
			e       JournalEntry
			updated int64
		)

		if err := rows.Scan(&e.Day, &e.Path, &e.Title, &updated); err != nil {
			return nil, err
		}

		e.Updated = time.Unix(updated, 0)
		out = append(out, e)
	}

	return out, rows.Err()
}

// ReadJournal returns one day's markdown.
func (s *Store) ReadJournal(ctx context.Context, day time.Time) (string, error) {
	path := filepath.Join(s.journalDir, dayKey(day)+".md")

	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", fmt.Errorf("nothing written on %s", dayKey(day))
	}

	if err != nil {
		return "", err
	}

	return string(b), nil
}

// SearchJournal greps the entries for a term, newest first.
//
// The index holds only paths, so the search reads the files. On a personal
// journal that is a few hundred kilobytes and not worth a full-text index.
func (s *Store) SearchJournal(ctx context.Context, term string) ([]JournalEntry, error) {
	entries, err := s.JournalEntries(ctx, 0)
	if err != nil {
		return nil, err
	}

	needle := strings.ToLower(term)

	var hits []JournalEntry

	for _, e := range entries {
		b, err := os.ReadFile(e.Path)
		if err != nil {
			continue
		}

		if strings.Contains(strings.ToLower(string(b)), needle) {
			hits = append(hits, e)
		}
	}

	sort.Slice(hits, func(i, j int) bool { return hits[i].Day > hits[j].Day })

	return hits, nil
}

// DB exposes the handle for tests and for callers that need a raw query.
func (s *Store) DB() *sql.DB { return s.db }

// --- todos ------------------------------------------------------------------

// Todo is one thing to do. Local, like the habits and the journal: a list of
// personal reminders has no reason to leave the machine, and making the
// calendar's todo ribbon wait on an authorised Google account meant it showed
// nothing until one existed.
type Todo struct {
	ID      int64      `json:"id"`
	Title   string     `json:"title"`
	Notes   string     `json:"notes,omitempty"`
	Due     *time.Time `json:"due,omitempty"`
	DoneAt  *time.Time `json:"done_at,omitempty"`
	Created time.Time  `json:"created"`
}

// Done reports whether it has been completed.
func (t Todo) Done() bool { return t.DoneAt != nil }

// Overdue reports whether it is past its due date and still open.
func (t Todo) Overdue() bool {
	return t.Due != nil && !t.Done() && t.Due.Before(time.Now())
}

// AddTodo creates one.
func (s *Store) AddTodo(ctx context.Context, title string, due *time.Time, notes string) (Todo, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Todo{}, fmt.Errorf("a todo needs a title")
	}

	now := time.Now()

	var dueUnix *int64

	if due != nil {
		u := due.Unix()
		dueUnix = &u
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO todos (title, notes, due, created) VALUES (?, ?, ?, ?)`,
		title, notes, dueUnix, now.Unix())
	if err != nil {
		return Todo{}, fmt.Errorf("add todo: %w", err)
	}

	id, _ := res.LastInsertId()

	return Todo{ID: id, Title: title, Notes: notes, Due: due, Created: now}, nil
}

// Todos lists todos, open ones first and then by due date.
func (s *Store) Todos(ctx context.Context, includeDone bool) ([]Todo, error) {
	query := `SELECT id, title, notes, due, done_at, created FROM todos`
	if !includeDone {
		query += ` WHERE done_at IS NULL`
	}

	// Open before done, then soonest due, then oldest. A todo with no date
	// sorts after one that has a date, since a deadline is the stronger claim.
	query += ` ORDER BY done_at IS NOT NULL, due IS NULL, due ASC, created ASC`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list todos: %w", err)
	}
	defer rows.Close()

	var out []Todo

	for rows.Next() {
		var (
			t           Todo
			due, doneAt *int64
			created     int64
		)

		if err := rows.Scan(&t.ID, &t.Title, &t.Notes, &due, &doneAt, &created); err != nil {
			return nil, err
		}

		t.Created = time.Unix(created, 0)

		if due != nil {
			d := time.Unix(*due, 0)
			t.Due = &d
		}

		if doneAt != nil {
			d := time.Unix(*doneAt, 0)
			t.DoneAt = &d
		}

		out = append(out, t)
	}

	return out, rows.Err()
}

// CompleteTodo marks one done, or reopens it.
func (s *Store) CompleteTodo(ctx context.Context, id int64, done bool) error {
	var doneAt *int64

	if done {
		u := time.Now().Unix()
		doneAt = &u
	}

	res, err := s.db.ExecContext(ctx, `UPDATE todos SET done_at = ? WHERE id = ?`, doneAt, id)
	if err != nil {
		return fmt.Errorf("complete todo: %w", err)
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no todo with id %d", id)
	}

	return nil
}

// DeleteTodo removes one outright.
func (s *Store) DeleteTodo(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM todos WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete todo: %w", err)
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no todo with id %d", id)
	}

	return nil
}

// TodoByTitle finds an open todo by exact title, for callers that only know
// what the user saw on screen.
func (s *Store) TodoByTitle(ctx context.Context, title string) (Todo, error) {
	todos, err := s.Todos(ctx, false)
	if err != nil {
		return Todo{}, err
	}

	for _, t := range todos {
		if t.Title == title {
			return t, nil
		}
	}

	return Todo{}, fmt.Errorf("no open todo called %q", title)
}
