package cli

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
	"github.com/mhrsntrk/frankenstein-cli/internal/screener"
)

// newScreener wires a screener against the live provider and cache.
func newScreener(cmd *cobra.Command, app *App) (*screener.Screener, error) {
	ctx := cmd.Context()

	st, err := app.Store()
	if err != nil {
		return nil, err
	}

	p, err := app.Provider(ctx)
	if err != nil {
		return nil, err
	}

	cfg, err := app.Config()
	if err != nil {
		return nil, err
	}

	return screener.New(st, p, cfg.Screener), nil
}

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

			// Keep the screener's view of senders current after every sync.
			if sc, err := newScreener(cmd, app); err == nil {
				if _, err := sc.Observe(ctx); err != nil {
					fmt.Fprintf(app.Err, "warning: could not update senders: %v\n", err)
				}
			}

			return app.Emit(res, func(w io.Writer) {
				if res.FullResync {
					fmt.Fprintln(w, "Full resync.")
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

func newScreenerCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "screener",
		Short:   "Decide which senders reach you",
		Aliases: []string{"screen"},
		Long: "The screener quarantines first-time senders until you say yes or no.\n\n" +
			"Decisions are written to Proton as real labels, so they follow you to\n" +
			"the web and mobile apps. For mailing lists the routing is pushed down\n" +
			"to Proton's own server-side rules, which keep working with this tool\n" +
			"shut down.",
	}

	cmd.AddCommand(
		newScreenerSetupCmd(app),
		newScreenerListCmd(app),
		newScreenerDecideCmd(app),
		newScreenerRouteCmd(app),
		newScreenerStatusCmd(app),
	)

	return cmd
}

func newScreenerSetupCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Create the Imbox, Feed, Paper Trail and Screened Out labels",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			p, err := app.Provider(ctx)
			if err != nil {
				return err
			}

			sc, err := screener.Setup(ctx, p)
			if err != nil {
				return err
			}

			cfg, err := app.Config()
			if err != nil {
				return err
			}

			cfg.Screener = sc

			if err := saveConfig(cfg); err != nil {
				return err
			}

			return app.Emit(sc, func(w io.Writer) {
				fmt.Fprintln(w, "Screener ready. These labels now exist in Proton:")
				fmt.Fprintf(w, "  %s\n  %s\n  %s\n  %s\n",
					screener.BoxImbox, screener.BoxFeed,
					screener.BoxPaperTrail, screener.BoxScreenedOut)
				fmt.Fprintln(w, "\nRun `frankenstein screener list` to see who is waiting.")
			})
		},
	}
}

func newScreenerListCmd(app *App) *cobra.Command {
	var (
		which  string
		limit  int
		sugges bool
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List senders and their decisions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			sc, err := newScreener(cmd, app)
			if err != nil {
				return err
			}

			d := screener.Decision(which)
			if which != "" && !d.Valid() {
				return fmt.Errorf("unknown decision %q", which)
			}

			senders, err := sc.List(ctx, d, limit)
			if err != nil {
				return err
			}

			type row struct {
				screener.Sender

				Suggested screener.Decision `json:"suggested,omitempty"`
				Because   string            `json:"because,omitempty"`
			}

			rows := make([]row, 0, len(senders))

			for _, s := range senders {
				r := row{Sender: s}

				if sugges && s.Decision == screener.Pending {
					r.Suggested, r.Because = sc.Suggest(ctx, s)
				}

				rows = append(rows, r)
			}

			return app.Emit(rows, func(w io.Writer) {
				if len(rows) == 0 {
					fmt.Fprintln(w, "Nobody waiting.")

					return
				}

				t := Table(w)

				if sugges {
					fmt.Fprintln(t, "SENDER\tMSGS\tLAST\tDECISION\tSUGGESTED")
				} else {
					fmt.Fprintln(t, "SENDER\tMSGS\tLAST\tDECISION")
				}

				for _, r := range rows {
					name := r.Address
					if r.Name != "" {
						name = fmt.Sprintf("%s <%s>", r.Name, r.Address)
					}

					if sugges {
						fmt.Fprintf(t, "%s\t%d\t%s\t%s\t%s\n",
							truncate(name, 44), r.MessageCount, relTime(r.LastSeen),
							r.Decision, r.Suggested)
					} else {
						fmt.Fprintf(t, "%s\t%d\t%s\t%s\n",
							truncate(name, 44), r.MessageCount, relTime(r.LastSeen), r.Decision)
					}
				}

				t.Flush()
			})
		},
	}

	cmd.Flags().StringVar(&which, "decision", string(screener.Pending),
		"filter by decision: pending, imbox, feed, paper_trail, screened_out; empty for all")
	cmd.Flags().IntVarP(&limit, "limit", "n", 50, "how many senders to show")
	cmd.Flags().BoolVar(&sugges, "suggest", false, "suggest a decision using Proton's own signals")

	return cmd
}

func newScreenerDecideCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "decide <sender> <imbox|feed|paper_trail|screened_out>",
		Short: "Decide where a sender's mail goes",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			sc, err := newScreener(cmd, app)
			if err != nil {
				return err
			}

			d := screener.Decision(args[1])
			if !d.Valid() || d == screener.Pending {
				return fmt.Errorf("decision must be one of imbox, feed, paper_trail, screened_out")
			}

			n, err := sc.Decide(ctx, args[0], d)
			if err != nil {
				return err
			}

			return app.Emit(map[string]any{
				"ok":         true,
				"sender":     args[0],
				"decision":   d,
				"relabelled": n,
			}, func(w io.Writer) {
				fmt.Fprintf(w, "%s -> %s", args[0], d)

				if n > 0 {
					fmt.Fprintf(w, " (%d thread(s) relabelled)", n)
				}

				fmt.Fprintln(w)
			})
		},
	}

	return cmd
}

func newScreenerRouteCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "route",
		Short: "Push newsletter decisions down to Proton's server-side rules",
		Long: "Proton keeps per-subscription routing rules for mailing lists. Pushing\n" +
			"Feed and Paper Trail decisions into them means the routing keeps\n" +
			"working on your phone with this tool shut down.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			sc, err := newScreener(cmd, app)
			if err != nil {
				return err
			}

			routed, err := sc.RouteNewsletters(ctx)
			if errors.Is(err, mail.ErrNotSupported) {
				return app.Emit(map[string]any{"ok": false, "reason": "provider has no server-side routing"},
					func(w io.Writer) {
						fmt.Fprintln(w, "This provider has no server-side newsletter routing.")
					})
			}

			if err != nil {
				return err
			}

			return app.Emit(map[string]any{"ok": true, "routed": routed},
				func(w io.Writer) {
					if len(routed) == 0 {
						fmt.Fprintln(w, "Nothing to route.")

						return
					}

					fmt.Fprintf(w, "Routed %d list(s) server-side:\n", len(routed))

					for _, n := range routed {
						fmt.Fprintf(w, "  %s\n", n)
					}
				})
		},
	}
}

func newScreenerStatusCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show screener counts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			sc, err := newScreener(cmd, app)
			if err != nil {
				return err
			}

			stats, err := sc.Stats(ctx)
			if err != nil {
				return err
			}

			cfg, _ := app.Config()

			out := map[string]any{
				"configured": cfg.Screener.Configured(),
				"enabled":    cfg.Screener.Enabled,
				"counts":     stats,
			}

			return app.Emit(out, func(w io.Writer) {
				if !cfg.Screener.Configured() {
					fmt.Fprintln(w, "Screener is not set up. Run `frankenstein screener setup`.")

					return
				}

				t := Table(w)
				fmt.Fprintln(t, "DECISION\tSENDERS")

				for _, d := range []screener.Decision{
					screener.Pending, screener.Imbox, screener.Feed,
					screener.PaperTrail, screener.ScreenedOut,
				} {
					fmt.Fprintf(t, "%s\t%d\n", d, stats[d])
				}

				t.Flush()
			})
		},
	}
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
