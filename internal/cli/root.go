// Package cli is the cobra command tree.
//
// Every command supports --json. That is not a convenience: the agent surface
// depends on it being universal rather than selective, so a command without it
// is a bug.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/mhrsntrk/frankenstein-cli/internal/auth"
	"github.com/mhrsntrk/frankenstein-cli/internal/config"
	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
	"github.com/mhrsntrk/frankenstein-cli/internal/store"
	fsync "github.com/mhrsntrk/frankenstein-cli/internal/sync"
	"github.com/mhrsntrk/frankenstein-cli/internal/terminal"
)

// Version is set at build time with -ldflags.
var Version = "dev"

// App carries everything a command might need, opened lazily so that commands
// which do not touch the network or the cache stay fast.
type App struct {
	JSON bool

	Out io.Writer
	Err io.Writer

	cfg      config.Config
	cfgReady bool

	store *store.Store

	client   *auth.Client
	provider mail.Provider

	sessions *auth.Store
}

// Config loads and caches the configuration.
func (a *App) Config() (config.Config, error) {
	if a.cfgReady {
		return a.cfg, nil
	}

	cfg, err := config.Load()
	if err != nil {
		return cfg, err
	}

	a.cfg = cfg
	a.cfgReady = true

	return cfg, nil
}

// Sessions returns the credential store.
func (a *App) Sessions() (*auth.Store, error) {
	if a.sessions != nil {
		return a.sessions, nil
	}

	s, err := auth.NewStore()
	if err != nil {
		return nil, err
	}

	a.sessions = s

	return s, nil
}

// Store opens the cache.
func (a *App) Store() (*store.Store, error) {
	if a.store != nil {
		return a.store, nil
	}

	path, err := config.CachePath()
	if err != nil {
		return nil, err
	}

	s, err := store.Open(path)
	if err != nil {
		return nil, err
	}

	a.store = s

	return s, nil
}

// ErrNotLoggedIn is returned when no usable session exists.
var ErrNotLoggedIn = errors.New("not logged in; run `frankenstein login`")

// Provider resumes the stored session and returns a live mail provider.
func (a *App) Provider(ctx context.Context) (mail.Provider, error) {
	if a.provider != nil {
		return a.provider, nil
	}

	cfg, err := a.Config()
	if err != nil {
		return nil, err
	}

	sessions, err := a.Sessions()
	if err != nil {
		return nil, err
	}

	// Resume loads the session and writes the rotated token back itself.
	client, err := auth.Resume(ctx, cfg, sessions)
	if errors.Is(err, auth.ErrNoSession) {
		return nil, ErrNotLoggedIn
	}

	if err != nil {
		return nil, fmt.Errorf("%w\n\nThe stored session is no longer valid. Run `frankenstein login`", err)
	}

	a.client = client
	a.provider = client.Provider()

	return a.provider, nil
}

// Syncer returns a syncer wired to the provider and the cache.
func (a *App) Syncer(ctx context.Context) (*fsync.Syncer, error) {
	p, err := a.Provider(ctx)
	if err != nil {
		return nil, err
	}

	st, err := a.Store()
	if err != nil {
		return nil, err
	}

	cfg, err := a.Config()
	if err != nil {
		return nil, err
	}

	s := fsync.New(p, st)
	s.BodyCacheSize = cfg.BodyCacheSize

	return s, nil
}

// Close releases whatever was opened.
func (a *App) Close() {
	if a.provider != nil {
		_ = a.provider.Close()
	}

	if a.store != nil {
		_ = a.store.Close()
	}
}

// --- output -----------------------------------------------------------------

// Emit writes a value as JSON when --json is set, otherwise calls human.
//
// Every command routes its output through here, which is what makes the --json
// promise hold across the whole tree.
func (a *App) Emit(v any, human func(w io.Writer)) error {
	if a.JSON {
		// A nil slice encodes as null, and "no threads" is an empty list, not
		// the absence of one. Normalizing here keeps the contract without every
		// command remembering to allocate.
		if rv := reflect.ValueOf(v); rv.Kind() == reflect.Slice && rv.IsNil() {
			v = []any{}
		}

		enc := json.NewEncoder(a.Out)
		enc.SetIndent("", "  ")

		return enc.Encode(v)
	}

	human(a.Out)

	return nil
}

// Table returns a tabwriter that renders on Flush.
func Table(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
}

// truncate shortens s to n runes, adding an ellipsis when it had to cut. It
// also sanitizes on the way through: everything the listings truncate came
// from somewhere else, and a subject line is exactly where an escape sequence
// would arrive.
func truncate(s string, n int) string {
	s = terminal.SanitizeLine(s)

	r := []rune(s)
	if len(r) <= n {
		return s
	}

	if n <= 1 {
		return string(r[:n])
	}

	return string(r[:n-1]) + "…"
}

// Run builds the command tree and runs it under ctx.
func Run(ctx context.Context) int {
	app := &App{Out: os.Stdout, Err: os.Stderr}
	defer app.Close()

	root := &cobra.Command{
		Use:   config.AppName,
		Short: "A terminal client for Proton Mail and Google Calendar",
		Long: "frankenstein is a terminal client for a Proton mailbox, with a\n" +
			"Google-backed calendar and local notes, todos, habits, time tracking\n" +
			"and a journal alongside it.\n\n" +
			"Mail is read from a local cache that `frankenstein sync` fills, so\n" +
			"listing is instant and works offline.\n\n" +
			"Every command supports --json.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       Version,
	}

	root.PersistentFlags().BoolVar(&app.JSON, "json", false, "emit JSON instead of text")

	root.AddCommand(
		newLoginCmd(app),
		newLogoutCmd(app),
		newWhoamiCmd(app),
		newSyncCmd(app),
		newBoxesCmd(app),
		newBoxCmd(app),
		newThreadsCmd(app),
		newThreadCmd(app),
		newReadCmd(app),
		newComposeCmd(app),
		newReplyCmd(app),
		newDraftsCmd(app),
		newSendCmd(app),
		newLabelCmd(app),
		newNewslettersCmd(app),
		newCalendarCmd(app),
		newTodoCmd(app),
		newHabitCmd(app),
		newTimeCmd(app),
		newJournalCmd(app),
		newNotesCmd(app),
		newSkillCmd(app),
		newTUICmd(app),
	)

	root.AddCommand(newDocsCmd(app, root))

	if err := root.ExecuteContext(ctx); err != nil {
		if app.JSON {
			enc := json.NewEncoder(app.Out)
			enc.SetIndent("", "  ")
			_ = enc.Encode(map[string]string{"error": err.Error()})
		} else {
			fmt.Fprintf(app.Err, "error: %v\n", err)
		}

		return 1
	}

	return 0
}
