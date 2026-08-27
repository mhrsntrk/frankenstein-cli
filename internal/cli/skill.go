package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	fcal "github.com/mhrsntrk/frankenstein-cli/internal/calendar"
	"github.com/mhrsntrk/frankenstein-cli/internal/config"
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

			// The calendar is optional: an unconfigured one just means no
			// agenda line, not a broken mail client.
			var cal fcal.Provider

			if p, _, cerr := calendarProvider(ctx, app); cerr == nil {
				cal = p
			}

			return tui.Run(tui.New(st, syncer, cal, cfg.Calendar.CalendarID))
		},
	}
}

// unusedConfig keeps the config import meaningful if the TUI stops needing it.
var _ = config.AppName
