package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	"github.com/mhrsntrk/frankenstein-cli/internal/config"
)

// newDocsCmd generates man pages and shell completions.
//
// Hidden because it is packaging machinery, not something a user runs: the
// release build calls it so distro packages ship completions without anyone
// maintaining them by hand.
func newDocsCmd(app *App, root *cobra.Command) *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:    "docs",
		Short:  "Generate man pages and shell completions",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			manDir := filepath.Join(dir, "man")
			compDir := filepath.Join(dir, "completions")

			for _, d := range []string{manDir, compDir} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					return fmt.Errorf("create %s: %w", d, err)
				}
			}

			header := &doc.GenManHeader{
				Title:   "FRANKENSTEIN",
				Section: "1",
				Source:  "frankenstein " + Version,
				Manual:  "frankenstein manual",
			}

			if err := doc.GenManTree(root, header, manDir); err != nil {
				return fmt.Errorf("generate man pages: %w", err)
			}

			completions := map[string]func(io.Writer) error{
				config.AppName + ".bash": func(w io.Writer) error { return root.GenBashCompletionV2(w, true) },
				config.AppName + ".zsh":  root.GenZshCompletion,
				config.AppName + ".fish": func(w io.Writer) error { return root.GenFishCompletion(w, true) },
			}

			for name, gen := range completions {
				f, err := os.Create(filepath.Join(compDir, name))
				if err != nil {
					return err
				}

				if err := gen(f); err != nil {
					f.Close()

					return fmt.Errorf("generate %s: %w", name, err)
				}

				f.Close()
			}

			return app.Emit(map[string]any{"ok": true, "dir": dir},
				func(w io.Writer) {
					fmt.Fprintf(w, "Wrote man pages and completions to %s\n", dir)
				})
		},
	}

	cmd.Flags().StringVar(&dir, "dir", ".gen", "where to write")

	return cmd
}
