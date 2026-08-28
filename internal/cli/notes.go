package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/mhrsntrk/frankenstein-cli/internal/config"
	"github.com/mhrsntrk/frankenstein-cli/internal/personal"
)

// --- notes ------------------------------------------------------------------

// resolveNote finds a note by its name, the filename without .md.
func resolveNote(dir, name string) (personal.Note, error) {
	notes, err := personal.ListNotes(dir)
	if err != nil {
		return personal.Note{}, err
	}

	for _, n := range notes {
		if n.Name == name {
			return n, nil
		}
	}

	return personal.Note{}, fmt.Errorf("no note called %q", name)
}

func newNotesCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "notes",
		Short:   "Quick markdown notes",
		Aliases: []string{"note"},
		Long: "Notes are markdown files in a local folder, and the folder is the whole\n" +
			"store. They stay readable and greppable without this tool.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runNotesList(app)
		},
	}

	cmd.AddCommand(
		newNotesListCmd(app),
		newNotesShowCmd(app),
		newNotesNewCmd(app),
		newNotesEditCmd(app),
		newNotesRemoveCmd(app),
	)

	return cmd
}

func runNotesList(app *App) error {
	dir, err := config.NotesDir()
	if err != nil {
		return err
	}

	notes, err := personal.ListNotes(dir)
	if err != nil {
		return err
	}

	return app.Emit(notes, func(w io.Writer) {
		if len(notes) == 0 {
			fmt.Fprintln(w, "No notes yet.")

			return
		}

		t := Table(w)
		fmt.Fprintln(t, "TITLE\tNAME\tUPDATED")

		for _, n := range notes {
			fmt.Fprintf(t, "%s\t%s\t%s\n", n.Title, n.Name, relTime(n.Updated))
		}

		t.Flush()
	})
}

func newNotesListCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List notes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runNotesList(app)
		},
	}
}

func newNotesShowCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Print a note",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := config.NotesDir()
			if err != nil {
				return err
			}

			n, err := resolveNote(dir, args[0])
			if err != nil {
				return err
			}

			text, err := personal.ReadNote(n.Path)
			if err != nil {
				return err
			}

			return app.Emit(map[string]string{"name": n.Name, "content": text},
				func(w io.Writer) {
					fmt.Fprintln(w, strings.TrimRight(text, "\n"))
				})
		},
	}
}

func newNotesNewCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "new <title...>",
		Short: "Create a note",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := config.NotesDir()
			if err != nil {
				return err
			}

			title := strings.Join(args, " ")
			content := "# " + title + "\n\n"

			if fi, err := os.Stdin.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) == 0 {
				b, err := io.ReadAll(os.Stdin)
				if err != nil {
					return err
				}

				if body := strings.TrimSpace(string(b)); body != "" {
					content += body + "\n"
				}
			}

			n, err := personal.WriteNote(dir, personal.NewNoteName(dir, title), content)
			if err != nil {
				return err
			}

			return app.Emit(n, func(w io.Writer) {
				fmt.Fprintf(w, "Created %s\n", n.Path)
			})
		},
	}
}

func newNotesEditCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "edit <name>",
		Short: "Open a note in $EDITOR",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := config.NotesDir()
			if err != nil {
				return err
			}

			// The editor is for a person at a keyboard: an agent or a pipe gets
			// an error, not vi waiting on input that will never come.
			if app.JSON {
				return fmt.Errorf("edit is interactive and has no --json; read with `notes show`, write the file directly")
			}

			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return fmt.Errorf("edit needs a terminal; stdin is not one")
			}

			n, err := resolveNote(dir, args[0])
			if err != nil {
				return err
			}

			editor := editorName()
			if editor == "" {
				return fmt.Errorf("$EDITOR is unset; set it, or edit %s directly", n.Path)
			}

			// #nosec G204 -- the editor comes from the user's own environment.
			ed := exec.Command(editor, n.Path)
			ed.Stdin = os.Stdin
			ed.Stdout = os.Stdout
			ed.Stderr = os.Stderr

			if err := ed.Run(); err != nil {
				return fmt.Errorf("run %s: %w", editor, err)
			}

			return nil
		},
	}
}

func newNotesRemoveCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:     "rm <name>",
		Short:   "Delete a note",
		Aliases: []string{"remove"},
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := config.NotesDir()
			if err != nil {
				return err
			}

			n, err := resolveNote(dir, args[0])
			if err != nil {
				return err
			}

			if err := personal.DeleteNote(n.Path); err != nil {
				return err
			}

			return app.Emit(n, func(w io.Writer) {
				fmt.Fprintf(w, "Deleted %s\n", n.Path)
			})
		},
	}
}
