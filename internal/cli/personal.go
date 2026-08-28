package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/api/option"
	"google.golang.org/api/tasks/v1"

	gcal "github.com/mhrsntrk/frankenstein-cli/internal/calendar/google"
	"github.com/mhrsntrk/frankenstein-cli/internal/config"
	"github.com/mhrsntrk/frankenstein-cli/internal/personal"
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

// tasksService reuses the Google OAuth token the calendar already holds.
//
// Todos go to Google Tasks rather than staying local so they sync with the
// phone, which is the whole reason to have them somewhere other than a text
// file.
func tasksService(ctx context.Context, app *App) (*tasks.Service, error) {
	cfg, err := app.Config()
	if err != nil {
		return nil, err
	}

	clientID, clientSecret := gcal.Credentials(cfg.Calendar.ClientID, cfg.Calendar.ClientSecret)
	if clientID == "" {
		return nil, fmt.Errorf("todos use the Google account; run `frankenstein calendar setup` first")
	}

	tok, err := gcal.LoadToken()
	if err != nil {
		return nil, err
	}

	oc := gcal.OAuthConfig(clientID, clientSecret)

	return tasks.NewService(ctx, option.WithTokenSource(oc.TokenSource(ctx, tok)))
}

type todo struct {
	ID     string     `json:"id"`
	Title  string     `json:"title"`
	Notes  string     `json:"notes,omitempty"`
	Due    *time.Time `json:"due,omitempty"`
	Done   bool       `json:"done"`
	ListID string     `json:"list_id"`
}

func newTodoCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "todo",
		Short:   "Todos, backed by Google Tasks",
		Aliases: []string{"todos"},
		Long: "Todos live in Google Tasks so they sync with the phone.\n\n" +
			"They reuse the Google authorisation from `frankenstein calendar setup`;\n" +
			"the Tasks scope is requested at the same time.",
	}

	cmd.AddCommand(newTodoListCmd(app), newTodoAddCmd(app), newTodoDoneCmd(app))

	return cmd
}

// defaultTaskList returns the account's primary task list.
func defaultTaskList(ctx context.Context, svc *tasks.Service) (string, error) {
	lists, err := svc.Tasklists.List().MaxResults(20).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("list task lists: %w", err)
	}

	if len(lists.Items) == 0 {
		return "", fmt.Errorf("the Google account has no task lists")
	}

	return lists.Items[0].Id, nil
}

func newTodoListCmd(app *App) *cobra.Command {
	var all bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List todos",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			svc, err := tasksService(ctx, app)
			if err != nil {
				return err
			}

			listID, err := defaultTaskList(ctx, svc)
			if err != nil {
				return err
			}

			call := svc.Tasks.List(listID).MaxResults(100)
			if all {
				call = call.ShowCompleted(true).ShowHidden(true)
			}

			res, err := call.Context(ctx).Do()
			if err != nil {
				return fmt.Errorf("list todos: %w", err)
			}

			out := make([]todo, 0, len(res.Items))

			for _, t := range res.Items {
				item := todo{
					ID:     t.Id,
					Title:  t.Title,
					Notes:  t.Notes,
					Done:   t.Status == "completed",
					ListID: listID,
				}

				if t.Due != "" {
					if d, err := time.Parse(time.RFC3339, t.Due); err == nil {
						item.Due = &d
					}
				}

				out = append(out, item)
			}

			return app.Emit(out, func(w io.Writer) {
				if len(out) == 0 {
					fmt.Fprintln(w, "Nothing to do.")

					return
				}

				t := Table(w)
				fmt.Fprintln(t, "ID\tDONE\tDUE\tTITLE")

				for _, item := range out {
					mark := " "
					if item.Done {
						mark = "x"
					}

					due := ""
					if item.Due != nil {
						due = item.Due.Format("2 Jan")
					}

					fmt.Fprintf(t, "%s\t%s\t%s\t%s\n",
						shortID(item.ID), mark, due, truncate(item.Title, 60))
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
			ctx := cmd.Context()

			svc, err := tasksService(ctx, app)
			if err != nil {
				return err
			}

			listID, err := defaultTaskList(ctx, svc)
			if err != nil {
				return err
			}

			t := &tasks.Task{Title: strings.Join(args, " "), Notes: notes}

			if due != "" {
				when, err := parseWhen(due)
				if err != nil {
					return err
				}

				t.Due = when.Format(time.RFC3339)
			}

			created, err := svc.Tasks.Insert(listID, t).Context(ctx).Do()
			if err != nil {
				return fmt.Errorf("add todo: %w", err)
			}

			return app.Emit(todo{ID: created.Id, Title: created.Title, ListID: listID},
				func(w io.Writer) {
					fmt.Fprintf(w, "Added: %s\n", created.Title)
				})
		},
	}

	cmd.Flags().StringVar(&notes, "notes", "", "extra detail")
	cmd.Flags().StringVar(&due, "due", "", "due date")

	return cmd
}

func newTodoDoneCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "done <id>",
		Short: "Mark a todo done",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			svc, err := tasksService(ctx, app)
			if err != nil {
				return err
			}

			listID, err := defaultTaskList(ctx, svc)
			if err != nil {
				return err
			}

			// Accept the short prefix the listing prints.
			id := args[0]

			res, err := svc.Tasks.List(listID).MaxResults(100).Context(ctx).Do()
			if err != nil {
				return err
			}

			var matches []string

			for _, t := range res.Items {
				if t.Id == id || strings.HasPrefix(t.Id, id) {
					matches = append(matches, t.Id)
				}
			}

			switch len(matches) {
			case 0:
				return fmt.Errorf("no todo matches %q", id)
			case 1:
				id = matches[0]
			default:
				return fmt.Errorf("%q matches %d todos; use a longer prefix", id, len(matches))
			}

			t, err := svc.Tasks.Get(listID, id).Context(ctx).Do()
			if err != nil {
				return err
			}

			t.Status = "completed"

			if _, err := svc.Tasks.Update(listID, id, t).Context(ctx).Do(); err != nil {
				return fmt.Errorf("complete todo: %w", err)
			}

			return app.Emit(map[string]any{"ok": true, "id": id},
				func(w io.Writer) {
					fmt.Fprintf(w, "Done: %s\n", t.Title)
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

func formatDuration(d time.Duration) string {
	d = d.Round(time.Minute)

	h := int(d.Hours())
	m := int(d.Minutes()) % 60

	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}

	return fmt.Sprintf("%dh %02dm", h, m)
}

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
