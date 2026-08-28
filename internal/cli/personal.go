package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mhrsntrk/frankenstein-cli/internal/config"
	"github.com/mhrsntrk/frankenstein-cli/internal/personal"
	"github.com/mhrsntrk/frankenstein-cli/internal/tui"
	"github.com/mhrsntrk/frankenstein-cli/internal/when"
)

func personalStore(app *App) (*personal.Store, error) {
	st, err := app.Store()
	if err != nil {
		return nil, err
	}

	dir, err := config.JournalDir()
	if err != nil {
		return nil, err
	}

	return personal.New(st.DB(), dir), nil
}

// --- todos ------------------------------------------------------------------

// listTodos reads the open todos for the calendar's "Sometime this week"
// ribbon.
func listTodos(ctx context.Context, app *App) ([]tui.TodoItem, error) {
	ps, err := personalStore(app)
	if err != nil {
		return nil, err
	}

	todos, err := ps.Todos(ctx, false)
	if err != nil {
		return nil, err
	}

	out := make([]tui.TodoItem, 0, len(todos))
	for _, t := range todos {
		out = append(out, tui.TodoItem{Title: t.Title, Done: t.Done()})
	}

	return out, nil
}

func addTodo(ctx context.Context, app *App, title string) error {
	ps, err := personalStore(app)
	if err != nil {
		return err
	}

	_, err = ps.AddTodo(ctx, title, nil, "")

	return err
}

// completeTodoByTitle matches on the title because that is what the calendar
// ribbon shows; the ribbon carries no IDs.
func completeTodoByTitle(ctx context.Context, app *App, title string) error {
	ps, err := personalStore(app)
	if err != nil {
		return err
	}

	todo, err := ps.TodoByTitle(ctx, title)
	if err != nil {
		return err
	}

	return ps.CompleteTodo(ctx, todo.ID, true)
}

func newTodoCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "todo",
		Short:   "Things to do",
		Aliases: []string{"todos"},
		Long: "Local, like the habits and the journal. They live in the same SQLite\n" +
			"file as the mail cache and never leave the machine.",
	}

	cmd.AddCommand(
		newTodoListCmd(app),
		newTodoAddCmd(app),
		newTodoDoneCmd(app),
		newTodoRemoveCmd(app),
	)

	return cmd
}

func newTodoListCmd(app *App) *cobra.Command {
	var all bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List todos",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ps, err := personalStore(app)
			if err != nil {
				return err
			}

			todos, err := ps.Todos(cmd.Context(), all)
			if err != nil {
				return err
			}

			return app.Emit(todos, func(w io.Writer) {
				if len(todos) == 0 {
					fmt.Fprintln(w, "Nothing to do.")

					return
				}

				t := Table(w)
				fmt.Fprintln(t, "ID\tDONE\tDUE\tTITLE")

				for _, item := range todos {
					mark := " "
					if item.Done() {
						mark = "x"
					}

					due := ""
					if item.Due != nil {
						due = item.Due.Format("2 Jan")

						if item.Overdue() {
							due += " (overdue)"
						}
					}

					fmt.Fprintf(t, "%d\t%s\t%s\t%s\n",
						item.ID, mark, due, truncate(item.Title, 60))
				}

				t.Flush()
			})
		},
	}

	cmd.Flags().BoolVar(&all, "all", false, "include completed todos")

	return cmd
}

func newTodoAddCmd(app *App) *cobra.Command {
	var (
		notes string
		due   string
	)

	cmd := &cobra.Command{
		Use:   "add <title>",
		Short: "Add a todo",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ps, err := personalStore(app)
			if err != nil {
				return err
			}

			var dueAt *time.Time

			if due != "" {
				parsed, err := when.Parse(due)
				if err != nil {
					return err
				}

				dueAt = &parsed
			}

			todo, err := ps.AddTodo(cmd.Context(), strings.Join(args, " "), dueAt, notes)
			if err != nil {
				return err
			}

			return app.Emit(todo, func(w io.Writer) {
				fmt.Fprintf(w, "Added: %s\n", todo.Title)
			})
		},
	}

	cmd.Flags().StringVar(&notes, "notes", "", "extra detail")
	cmd.Flags().StringVar(&due, "due", "", "due date")

	return cmd
}

func newTodoDoneCmd(app *App) *cobra.Command {
	var undo bool

	cmd := &cobra.Command{
		Use:   "done <id>",
		Short: "Mark a todo done",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ps, err := personalStore(app)
			if err != nil {
				return err
			}

			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("%q is not a todo id; `frankenstein todo list` shows them", args[0])
			}

			if err := ps.CompleteTodo(cmd.Context(), id, !undo); err != nil {
				return err
			}

			verb := "done"
			if undo {
				verb = "reopened"
			}

			return app.Emit(map[string]any{"ok": true, "id": id, "done": !undo},
				func(w io.Writer) {
					fmt.Fprintf(w, "%d %s\n", id, verb)
				})
		},
	}

	cmd.Flags().BoolVar(&undo, "undo", false, "reopen it instead")

	return cmd
}

func newTodoRemoveCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:     "remove <id>",
		Short:   "Delete a todo",
		Aliases: []string{"rm", "delete"},
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ps, err := personalStore(app)
			if err != nil {
				return err
			}

			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("%q is not a todo id", args[0])
			}

			if err := ps.DeleteTodo(cmd.Context(), id); err != nil {
				return err
			}

			return app.Emit(map[string]any{"ok": true, "id": id},
				func(w io.Writer) {
					fmt.Fprintf(w, "Deleted %d\n", id)
				})
		},
	}
}

// --- habits -----------------------------------------------------------------

func newHabitCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "habit",
		Short:   "Track daily habits",
		Aliases: []string{"habits"},
		Long:    "Local only. Proton has no equivalent and syncing this is not worth a service.",
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List habits and streaks",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				ps, err := personalStore(app)
				if err != nil {
					return err
				}

				habits, err := ps.Habits(cmd.Context(), false)
				if err != nil {
					return err
				}

				return app.Emit(habits, func(w io.Writer) {
					if len(habits) == 0 {
						fmt.Fprintln(w, "No habits yet. Add one with `frankenstein habit add <name>`.")

						return
					}

					t := Table(w)
					fmt.Fprintln(t, "HABIT\tTODAY\tSTREAK\tLAST 30")

					for _, h := range habits {
						mark := " "
						if h.DoneToday {
							mark = "x"
						}

						fmt.Fprintf(t, "%s\t%s\t%d\t%d/30\n", h.Name, mark, h.Streak, h.Last30)
					}

					t.Flush()
				})
			},
		},
		&cobra.Command{
			Use:   "add <name>",
			Short: "Start tracking a habit",
			Args:  cobra.MinimumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				ps, err := personalStore(app)
				if err != nil {
					return err
				}

				h, err := ps.AddHabit(cmd.Context(), strings.Join(args, " "))
				if err != nil {
					return err
				}

				return app.Emit(h, func(w io.Writer) {
					fmt.Fprintf(w, "Tracking %q\n", h.Name)
				})
			},
		},
		newHabitCheckCmd(app),
		&cobra.Command{
			Use:   "archive <name>",
			Short: "Stop tracking a habit, keeping its history",
			Args:  cobra.MinimumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				ps, err := personalStore(app)
				if err != nil {
					return err
				}

				name := strings.Join(args, " ")

				if err := ps.ArchiveHabit(cmd.Context(), name); err != nil {
					return err
				}

				return app.Emit(map[string]any{"ok": true, "habit": name},
					func(w io.Writer) {
						fmt.Fprintf(w, "Archived %q\n", name)
					})
			},
		},
	)

	return cmd
}

func newHabitCheckCmd(app *App) *cobra.Command {
	var (
		day  string
		undo bool
	)

	cmd := &cobra.Command{
		Use:   "check <name>",
		Short: "Mark a habit done today",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ps, err := personalStore(app)
			if err != nil {
				return err
			}

			when := time.Now()

			if day != "" {
				when, err = parseWhen(day)
				if err != nil {
					return err
				}
			}

			h, err := ps.CheckHabit(cmd.Context(), strings.Join(args, " "), when, !undo)
			if err != nil {
				return err
			}

			return app.Emit(h, func(w io.Writer) {
				if undo {
					fmt.Fprintf(w, "Cleared %q for %s\n", h.Name, when.Format("2 Jan"))

					return
				}

				fmt.Fprintf(w, "%s: %d day streak\n", h.Name, h.Streak)
			})
		},
	}

	cmd.Flags().StringVar(&day, "day", "", "which day, default today")
	cmd.Flags().BoolVar(&undo, "undo", false, "clear the day instead of marking it")

	return cmd
}

// --- time tracking ----------------------------------------------------------

func newTimeCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "time",
		Short: "Track time against projects",
	}

	start := &cobra.Command{
		Use:   "start <project>",
		Short: "Start the timer",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ps, err := personalStore(app)
			if err != nil {
				return err
			}

			note, _ := cmd.Flags().GetString("note")

			e, err := ps.StartTimer(cmd.Context(), strings.Join(args, " "), note)
			if err != nil {
				return err
			}

			return app.Emit(e, func(w io.Writer) {
				fmt.Fprintf(w, "Started %s at %s\n", e.Project, e.Started.Format("15:04"))
			})
		},
	}

	start.Flags().String("note", "", "what you are working on")

	cmd.AddCommand(
		start,
		&cobra.Command{
			Use:   "stop",
			Short: "Stop the timer",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				ps, err := personalStore(app)
				if err != nil {
					return err
				}

				e, err := ps.StopTimer(cmd.Context())
				if err != nil {
					return err
				}

				return app.Emit(e, func(w io.Writer) {
					fmt.Fprintf(w, "Stopped %s after %s\n", e.Project, formatDuration(e.Duration()))
				})
			},
		},
		newTimeReportCmd(app),
	)

	return cmd
}

func newTimeReportCmd(app *App) *cobra.Command {
	var since string

	cmd := &cobra.Command{
		Use:     "report",
		Short:   "Total tracked time by project",
		Aliases: []string{"status"},
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ps, err := personalStore(app)
			if err != nil {
				return err
			}

			from := time.Now().AddDate(0, 0, -7)

			if since != "" {
				from, err = parseWhen(since)
				if err != nil {
					return err
				}
			}

			totals, err := ps.TimeByProject(cmd.Context(), from)
			if err != nil {
				return err
			}

			type line struct {
				Project string `json:"project"`
				Seconds int    `json:"seconds"`
				Human   string `json:"human"`
			}

			lines := make([]line, 0, len(totals))

			for p, d := range totals {
				lines = append(lines, line{Project: p, Seconds: int(d.Seconds()), Human: formatDuration(d)})
			}

			sort.Slice(lines, func(i, j int) bool { return lines[i].Seconds > lines[j].Seconds })

			return app.Emit(lines, func(w io.Writer) {
				if len(lines) == 0 {
					fmt.Fprintln(w, "Nothing tracked.")

					return
				}

				t := Table(w)
				fmt.Fprintln(t, "PROJECT\tTIME")

				for _, l := range lines {
					fmt.Fprintf(t, "%s\t%s\n", l.Project, l.Human)
				}

				t.Flush()
			})
		},
	}

	cmd.Flags().StringVar(&since, "since", "", "start of the report period")

	return cmd
}

func formatDuration(d time.Duration) string { return when.FormatDuration(d) }

// --- journal ----------------------------------------------------------------

func newJournalCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "journal",
		Short:   "Write and read a journal",
		Aliases: []string{"j"},
		Long: "Entries are markdown files on disk, one per day, with only the index in\n" +
			"SQLite. They stay readable and greppable without this tool.",
	}

	cmd.AddCommand(
		newJournalWriteCmd(app),
		newJournalReadCmd(app),
		newJournalListCmd(app),
		newJournalSearchCmd(app),
	)

	return cmd
}

func newJournalWriteCmd(app *App) *cobra.Command {
	var (
		title string
		day   string
		body  string
	)

	cmd := &cobra.Command{
		Use:     "write [text]",
		Short:   "Append to a day's entry",
		Aliases: []string{"add"},
		RunE: func(cmd *cobra.Command, args []string) error {
			ps, err := personalStore(app)
			if err != nil {
				return err
			}

			text := strings.Join(args, " ")
			if text == "" {
				text, err = readStdinOrEditor(body)
				if err != nil {
					return err
				}
			}

			when := time.Now()

			if day != "" {
				when, err = parseWhen(day)
				if err != nil {
					return err
				}
			}

			e, err := ps.WriteJournal(cmd.Context(), when, title, text)
			if err != nil {
				return err
			}

			return app.Emit(e, func(w io.Writer) {
				fmt.Fprintf(w, "Written to %s\n", e.Path)
			})
		},
	}

	cmd.Flags().StringVar(&title, "title", "", "section heading")
	cmd.Flags().StringVar(&day, "day", "", "which day, default today")
	cmd.Flags().StringVar(&body, "body", "", "entry text")

	return cmd
}

func newJournalReadCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "read [day]",
		Short: "Read a day's entry",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ps, err := personalStore(app)
			if err != nil {
				return err
			}

			when := time.Now()

			if len(args) == 1 {
				when, err = parseWhen(args[0])
				if err != nil {
					return err
				}
			}

			text, err := ps.ReadJournal(cmd.Context(), when)
			if err != nil {
				return err
			}

			return app.Emit(map[string]string{"day": when.Format("2006-01-02"), "content": text},
				func(w io.Writer) {
					fmt.Fprintln(w, text)
				})
		},
	}
}

func newJournalListCmd(app *App) *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List journal entries",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ps, err := personalStore(app)
			if err != nil {
				return err
			}

			entries, err := ps.JournalEntries(cmd.Context(), limit)
			if err != nil {
				return err
			}

			return app.Emit(entries, func(w io.Writer) {
				if len(entries) == 0 {
					fmt.Fprintln(w, "Nothing written yet.")

					return
				}

				t := Table(w)
				fmt.Fprintln(t, "DAY\tTITLE")

				for _, e := range entries {
					fmt.Fprintf(t, "%s\t%s\n", e.Day, e.Title)
				}

				t.Flush()
			})
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "n", 30, "how many entries to show")

	return cmd
}

func newJournalSearchCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "search <term>",
		Short: "Find entries containing a term",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ps, err := personalStore(app)
			if err != nil {
				return err
			}

			hits, err := ps.SearchJournal(cmd.Context(), strings.Join(args, " "))
			if err != nil {
				return err
			}

			return app.Emit(hits, func(w io.Writer) {
				if len(hits) == 0 {
					fmt.Fprintln(w, "No matches.")

					return
				}

				for _, e := range hits {
					fmt.Fprintf(w, "%s  %s\n", e.Day, e.Path)
				}
			})
		},
	}
}
