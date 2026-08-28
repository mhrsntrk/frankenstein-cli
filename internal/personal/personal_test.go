package personal_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mhrsntrk/frankenstein-cli/internal/personal"
	"github.com/mhrsntrk/frankenstein-cli/internal/store"
)

func setup(t *testing.T) (*personal.Store, *sql.DB) {
	t.Helper()

	dir := t.TempDir()

	st, err := store.Open(filepath.Join(dir, "cache.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() { st.Close() })

	return personal.New(st.DB(), filepath.Join(dir, "journal")), st.DB()
}

func TestHabitStreak(t *testing.T) {
	ctx := context.Background()
	ps, _ := setup(t)

	if _, err := ps.AddHabit(ctx, "read"); err != nil {
		t.Fatalf("add: %v", err)
	}

	now := time.Now()

	// Three consecutive days ending today.
	for i := 0; i < 3; i++ {
		if _, err := ps.CheckHabit(ctx, "read", now.AddDate(0, 0, -i), true); err != nil {
			t.Fatalf("check: %v", err)
		}
	}

	habits, err := ps.Habits(ctx, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(habits) != 1 {
		t.Fatalf("got %d habits, want 1", len(habits))
	}

	h := habits[0]

	if h.Streak != 3 {
		t.Errorf("streak = %d, want 3", h.Streak)
	}

	if !h.DoneToday {
		t.Error("today should be done")
	}

	if h.Last30 != 3 {
		t.Errorf("last 30 = %d, want 3", h.Last30)
	}
}

// A streak must survive today not being ticked yet: an unfinished morning is
// not a broken streak.
func TestHabitStreakCountsBackFromYesterday(t *testing.T) {
	ctx := context.Background()
	ps, _ := setup(t)

	if _, err := ps.AddHabit(ctx, "walk"); err != nil {
		t.Fatal(err)
	}

	now := time.Now()

	for i := 1; i <= 2; i++ {
		if _, err := ps.CheckHabit(ctx, "walk", now.AddDate(0, 0, -i), true); err != nil {
			t.Fatal(err)
		}
	}

	habits, _ := ps.Habits(ctx, false)
	h := habits[0]

	if h.DoneToday {
		t.Error("today should not be done")
	}

	if h.Streak != 2 {
		t.Errorf("streak = %d, want 2", h.Streak)
	}
}

// Days are calendar dates, not epoch seconds divided by 86400. That division
// breaks wherever the UTC offset crosses zero, which is exactly what London
// does at the end of October.
func TestHabitStreakAcrossDSTBoundary(t *testing.T) {
	london, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}

	ctx := context.Background()
	ps, _ := setup(t)

	if _, err := ps.AddHabit(ctx, "stretch"); err != nil {
		t.Fatal(err)
	}

	// 2026-10-25 is when London goes from BST (+1) back to GMT (0).
	days := []time.Time{
		time.Date(2026, 10, 24, 9, 0, 0, 0, london),
		time.Date(2026, 10, 25, 9, 0, 0, 0, london),
		time.Date(2026, 10, 26, 9, 0, 0, 0, london),
	}

	for _, d := range days {
		if _, err := ps.CheckHabit(ctx, "stretch", d, true); err != nil {
			t.Fatalf("check %s: %v", d, err)
		}
	}

	// Each day must be stored under a distinct key. Dividing by 86400 would
	// collapse two of these into one.
	var n int

	if err := ps.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM habit_entries`).Scan(&n); err != nil {
		t.Fatal(err)
	}

	if n != 3 {
		t.Errorf("stored %d day entries across the DST change, want 3", n)
	}
}

func TestHabitUncheck(t *testing.T) {
	ctx := context.Background()
	ps, _ := setup(t)

	if _, err := ps.AddHabit(ctx, "read"); err != nil {
		t.Fatal(err)
	}

	now := time.Now()

	if _, err := ps.CheckHabit(ctx, "read", now, true); err != nil {
		t.Fatal(err)
	}

	h, err := ps.CheckHabit(ctx, "read", now, false)
	if err != nil {
		t.Fatalf("uncheck: %v", err)
	}

	if h.DoneToday {
		t.Error("today should be cleared")
	}
}

func TestHabitUnknownName(t *testing.T) {
	ctx := context.Background()
	ps, _ := setup(t)

	if _, err := ps.CheckHabit(ctx, "nope", time.Now(), true); err == nil {
		t.Error("checking an unknown habit should fail")
	}
}

func TestTimerStartStopAndOverlap(t *testing.T) {
	ctx := context.Background()
	ps, _ := setup(t)

	if _, err := ps.StartTimer(ctx, "alpha", "first"); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Starting a second timer must close the first: two overlapping timers
	// would double-count the same wall clock.
	if _, err := ps.StartTimer(ctx, "beta", ""); err != nil {
		t.Fatalf("second start: %v", err)
	}

	entries, err := ps.TimeEntries(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}

	running := 0

	for _, e := range entries {
		if e.Running() {
			running++
		}
	}

	if running != 1 {
		t.Errorf("%d timers running, want 1", running)
	}

	if _, err := ps.StopTimer(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if _, err := ps.StopTimer(ctx); err == nil {
		t.Error("stopping with nothing running should fail")
	}
}

func TestTimeByProject(t *testing.T) {
	ctx := context.Background()
	ps, db := setup(t)

	now := time.Now()

	// Insert closed entries directly so the durations are deterministic.
	for _, row := range []struct {
		project string
		mins    int
	}{{"alpha", 30}, {"alpha", 15}, {"beta", 45}} {
		start := now.Add(-2 * time.Hour)
		stop := start.Add(time.Duration(row.mins) * time.Minute)

		if _, err := db.ExecContext(ctx,
			`INSERT INTO time_entries (project, note, started, stopped) VALUES (?, '', ?, ?)`,
			row.project, start.Unix(), stop.Unix()); err != nil {
			t.Fatal(err)
		}
	}

	totals, err := ps.TimeByProject(ctx, now.Add(-3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	if totals["alpha"] != 45*time.Minute {
		t.Errorf("alpha = %v, want 45m", totals["alpha"])
	}

	if totals["beta"] != 45*time.Minute {
		t.Errorf("beta = %v, want 45m", totals["beta"])
	}
}

func TestJournalWriteAppendsAndIndexes(t *testing.T) {
	ctx := context.Background()
	ps, _ := setup(t)

	day := time.Date(2026, 8, 28, 10, 0, 0, 0, time.Local)

	if _, err := ps.WriteJournal(ctx, day, "Morning", "first thought"); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := ps.WriteJournal(ctx, day, "Evening", "second thought"); err != nil {
		t.Fatalf("append: %v", err)
	}

	text, err := ps.ReadJournal(ctx, day)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Appending must keep what was already there.
	for _, want := range []string{"first thought", "second thought", "## Morning", "## Evening"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}

	// One day is one file, so the index must hold a single row.
	entries, err := ps.JournalEntries(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 {
		t.Fatalf("got %d index entries, want 1", len(entries))
	}

	if entries[0].Day != "2026-08-28" {
		t.Errorf("day key = %q", entries[0].Day)
	}

	hits, err := ps.SearchJournal(ctx, "SECOND")
	if err != nil {
		t.Fatal(err)
	}

	if len(hits) != 1 {
		t.Errorf("case-insensitive search found %d, want 1", len(hits))
	}

	if _, err := ps.ReadJournal(ctx, day.AddDate(0, 0, 1)); err == nil {
		t.Error("reading an unwritten day should fail")
	}
}

func TestTodoLifecycle(t *testing.T) {
	ctx := context.Background()
	ps, _ := setup(t)

	if _, err := ps.AddTodo(ctx, "  ", nil, ""); err == nil {
		t.Error("a blank title was accepted")
	}

	soon := time.Now().Add(24 * time.Hour)

	later, err := ps.AddTodo(ctx, "Renew the domain", &soon, "before it lapses")
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	undated, err := ps.AddTodo(ctx, "Read the Proton docs", nil, "")
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	open, err := ps.Todos(ctx, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(open) != 2 {
		t.Fatalf("open = %d, want 2", len(open))
	}

	// A deadline is the stronger claim, so a dated todo sorts first.
	if open[0].Title != "Renew the domain" {
		t.Errorf("first open todo is %q, want the dated one", open[0].Title)
	}

	if open[0].Notes != "before it lapses" {
		t.Errorf("notes = %q", open[0].Notes)
	}

	if open[0].Due == nil || open[0].Due.Unix() != soon.Unix() {
		t.Errorf("due = %v, want %v", open[0].Due, soon)
	}

	if err := ps.CompleteTodo(ctx, later.ID, true); err != nil {
		t.Fatalf("complete: %v", err)
	}

	open, _ = ps.Todos(ctx, false)
	if len(open) != 1 || open[0].ID != undated.ID {
		t.Errorf("after completing one, open = %+v", open)
	}

	all, _ := ps.Todos(ctx, true)
	if len(all) != 2 {
		t.Fatalf("all = %d, want 2", len(all))
	}

	// Done ones sort to the bottom.
	if !all[len(all)-1].Done() {
		t.Error("a completed todo did not sort last")
	}

	// Reopening puts it back.
	if err := ps.CompleteTodo(ctx, later.ID, false); err != nil {
		t.Fatalf("reopen: %v", err)
	}

	if open, _ = ps.Todos(ctx, false); len(open) != 2 {
		t.Errorf("after reopening, open = %d, want 2", len(open))
	}

	if err := ps.DeleteTodo(ctx, later.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if open, _ = ps.Todos(ctx, false); len(open) != 1 {
		t.Errorf("after deleting, open = %d, want 1", len(open))
	}

	// Acting on a todo that is not there is an error, not a silent no-op.
	if err := ps.CompleteTodo(ctx, 9999, true); err == nil {
		t.Error("completing a missing todo succeeded")
	}

	if err := ps.DeleteTodo(ctx, 9999); err == nil {
		t.Error("deleting a missing todo succeeded")
	}
}

func TestTodoOverdueAndByTitle(t *testing.T) {
	ctx := context.Background()
	ps, _ := setup(t)

	past := time.Now().Add(-48 * time.Hour)

	if _, err := ps.AddTodo(ctx, "File the tax return", &past, ""); err != nil {
		t.Fatalf("add: %v", err)
	}

	got, err := ps.TodoByTitle(ctx, "File the tax return")
	if err != nil {
		t.Fatalf("by title: %v", err)
	}

	if !got.Overdue() {
		t.Error("a todo two days past its date is not overdue")
	}

	if err := ps.CompleteTodo(ctx, got.ID, true); err != nil {
		t.Fatalf("complete: %v", err)
	}

	// TodoByTitle only looks at open todos, so a completed one is gone from it.
	if _, err := ps.TodoByTitle(ctx, "File the tax return"); err == nil {
		t.Error("a completed todo was still found by title")
	}
}
