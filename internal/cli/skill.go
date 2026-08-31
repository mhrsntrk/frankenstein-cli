package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	fcal "github.com/mhrsntrk/frankenstein-cli/internal/calendar"
	"github.com/mhrsntrk/frankenstein-cli/internal/skill"
	"github.com/mhrsntrk/frankenstein-cli/internal/tui"
)

func newSkillCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "The embedded agent skill",
		Long: "frankenstein ships a skill describing its command surface to a coding\n" +
			"agent. Installing it lets the agent drive your mail and calendar\n" +
			"through the --json interface.",
	}

	var (
		dir   string
		force bool
	)

	install := &cobra.Command{
		Use:   "install",
		Short: "Install the agent skill",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			path, err := skill.Install(dir, force)
			if err != nil {
				return err
			}

			return app.Emit(map[string]any{"ok": true, "path": path},
				func(w io.Writer) {
					fmt.Fprintf(w, "Installed to %s\n", path)
				})
		},
	}

	install.Flags().StringVar(&dir, "dir", "", "where to install, default ~/.claude/skills/frankenstein")
	install.Flags().BoolVar(&force, "force", false, "overwrite an existing skill")

	show := &cobra.Command{
		Use:   "show",
		Short: "Print the skill",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			content, err := skill.Content()
			if err != nil {
				return err
			}

			return app.Emit(map[string]string{"content": string(content)},
				func(w io.Writer) {
					fmt.Fprint(w, string(content))
				})
		},
	}

	cmd.AddCommand(install, show)

	return cmd
}

func newTUICmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:     "tui",
		Short:   "Open the full-screen interface",
		Aliases: []string{"ui"},
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			// A full-screen interface has no JSON to offer and nothing to
			// paint on a pipe; refuse both before touching the terminal.
			if app.JSON {
				return fmt.Errorf("the TUI is interactive and has no --json; use the subcommands instead")
			}

			if !term.IsTerminal(int(os.Stdout.Fd())) {
				return fmt.Errorf("the TUI needs a terminal; stdout is not one")
			}

			st, err := app.Store()
			if err != nil {
				return err
			}

			syncer, err := app.Syncer(ctx)
			if err != nil {
				return err
			}

			cfg, err := app.Config()
			if err != nil {
				return err
			}

			p, err := app.Provider(ctx)
			if err != nil {
				return err
			}

			ps, err := personalStore(app)
			if err != nil {
				return err
			}

			// The calendar is optional: an unconfigured one means no agenda,
			// not a broken mail client.
			var cal fcal.Provider

			if c, _, cerr := calendarProvider(ctx, app); cerr == nil {
				cal = c
			}

			// Todos are local, so unlike the calendar they are always
			// available: the ribbon works on a machine that has never seen a
			// Google account.
			todos := tui.Todos{
				List: func(ctx context.Context) ([]tui.TodoItem, error) {
					return listTodos(ctx, app)
				},
				Add: func(ctx context.Context, title string) error {
					return addTodo(ctx, app, title)
				},
				Complete: func(ctx context.Context, id int64) error {
					return completeTodoByID(ctx, app, id)
				},
			}

			// The picker writes its choice back to the config, so which
			// calendars you look at survives a restart.
			saveCalendars := func(ids []string) error {
				current, err := app.Config()
				if err != nil {
					return err
				}

				current.Calendar.CalendarIDs = ids

				return saveConfig(current)
			}

			return tui.Run(tui.New(st, syncer, p, ps, cal, todos, saveCalendars, cfg))
		},
	}
}
