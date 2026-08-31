package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
)

func newSyncCmd(app *App) *cobra.Command {
	var (
		full  bool
		depth int
		watch bool
	)

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Bring the local cache up to date",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			// Watch mode never returns and never emits a document, so there is
			// no JSON to promise. Refusing is the honest contract.
			if watch && app.JSON {
				return fmt.Errorf("--watch runs forever and emits no JSON; drop one of the two")
			}

			syncer, err := app.Syncer(ctx)
			if err != nil {
				return err
			}

			if depth > 0 {
				syncer.BackfillDepth = depth
			}

			if !app.JSON {
				syncer.OnProgress = func(msg string) {
					fmt.Fprintf(app.Err, "  %s\n", msg)
				}
			}

			if watch {
				cfg, err := app.Config()
				if err != nil {
					return err
				}

				interval := time.Duration(cfg.SyncInterval) * time.Second

				fmt.Fprintf(app.Err, "Watching for changes every %s. Ctrl-C to stop.\n", interval)

				syncer.Run(ctx, interval, func(err error) {
					fmt.Fprintf(app.Err, "sync error: %v\n", err)
				})

				return nil
			}

			if full {
				st, err := app.Store()
				if err != nil {
					return err
				}

				// Clearing the cursor is what makes the next pass a backfill.
				if err := st.SetCursor(ctx, ""); err != nil {
					return err
				}
			}

			res, err := syncer.Once(ctx)
			if err != nil {
				return err
			}

			return app.Emit(res, func(w io.Writer) {
				if res.FullResync {
					fmt.Fprintln(w, "Full resync.")
				}

				if res.Boxes == 0 && res.Conversations == 0 && res.Messages == 0 {
					fmt.Fprintln(w, "Already up to date.")

					return
				}

				fmt.Fprintf(w, "%d boxes, %d conversations, %d messages",
					res.Boxes, res.Conversations, res.Messages)

				if res.Newsletters > 0 {
					fmt.Fprintf(w, ", %d newsletters", res.Newsletters)
				}

				fmt.Fprintln(w)
			})
		},
	}

	cmd.Flags().BoolVar(&full, "full", false, "discard the cursor and rebuild the cache")
	cmd.Flags().IntVar(&depth, "depth", 0, "conversations per box on a backfill")
	cmd.Flags().BoolVar(&watch, "watch", false, "keep syncing until interrupted")

	return cmd
}

func newNewslettersCmd(app *App) *cobra.Command {
	var refresh bool

	cmd := &cobra.Command{
		Use:     "newsletters",
		Short:   "List the mailing lists Proton tracks",
		Aliases: []string{"lists"},
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			st, err := app.Store()
			if err != nil {
				return err
			}

			if refresh {
				p, err := app.Provider(ctx)
				if err != nil {
					return err
				}

				ns, err := p.Newsletters(ctx)
				if err != nil {
					return err
				}

				if err := st.PutNewsletters(ctx, ns); err != nil {
					return err
				}
			}

			ns, err := st.Newsletters(ctx)
			if err != nil {
				return err
			}

			return app.Emit(ns, func(w io.Writer) {
				if len(ns) == 0 {
					fmt.Fprintln(w, "No newsletters cached. Run `frankenstein sync`.")

					return
				}

				t := Table(w)
				fmt.Fprintln(t, "NAME\tSENDER\t30D\tTOTAL\tUNREAD\tROUTED")

				for _, n := range ns {
					routed := ""
					if n.MoveToBoxID != "" {
						routed = "yes"
					}

					fmt.Fprintf(t, "%s\t%s\t%d\t%d\t%d\t%s\n",
						truncate(n.Name, 28), truncate(n.Sender.Address, 34),
						n.ReceivedLast30Days, n.ReceivedTotal, n.Unread, routed)
				}

				t.Flush()
			})
		},
	}

	cmd.Flags().BoolVar(&refresh, "refresh", false, "fetch from the provider before listing")

	return cmd
}
