package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	fcal "github.com/mhrsntrk/frankenstein-cli/internal/calendar"
	gcal "github.com/mhrsntrk/frankenstein-cli/internal/calendar/google"
	"github.com/mhrsntrk/frankenstein-cli/internal/config"
	"github.com/mhrsntrk/frankenstein-cli/internal/when"
)

// calendarProvider builds a Google calendar provider from the stored config
// and token.
func calendarProvider(ctx context.Context, app *App) (fcal.Provider, string, error) {
	cfg, err := app.Config()
	if err != nil {
		return nil, "", err
	}

	clientID, clientSecret := gcal.Credentials(cfg.Calendar.ClientID, cfg.Calendar.ClientSecret)
	if clientID == "" {
		return nil, "", fcal.ErrNotConfigured
	}

	tok, err := gcal.LoadToken()
	if err != nil {
		return nil, "", err
	}

	oc := gcal.OAuthConfig(clientID, clientSecret)

	p, err := gcal.New(ctx, oc, tok)
	if err != nil {
		return nil, "", err
	}

	return p, cfg.Calendar.CalendarID, nil
}

func newCalendarCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "calendar",
		Short:   "Google Calendar",
		Aliases: []string{"cal"},
		Long: "Proton Calendar has no CalDAV and no public API, so the calendar side\n" +
			"talks to Google.",
	}

	cmd.AddCommand(
		newCalendarSetupCmd(app),
		newCalendarsCmd(app),
		newEventsCmd(app),
		newEventAddCmd(app),
		newEventRemoveCmd(app),
	)

	return cmd
}

func newCalendarSetupCmd(app *App) *cobra.Command {
	var (
		clientID     string
		clientSecret string
		calendarID   string
	)

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Authorise Google Calendar",
		Long: "Runs the OAuth2 loopback flow and stores the token in the keyring.\n\n" +
			"Unless this build carries one, you need your own Google Cloud OAuth\n" +
			"client of type \"Desktop app\". No redirect URI needs registering: the\n" +
			"loopback flow picks a free local port. See docs/calendar-setup.md.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			cfg, err := app.Config()
			if err != nil {
				return err
			}

			if clientID != "" {
				cfg.Calendar.ClientID = clientID
			}

			if clientSecret != "" {
				cfg.Calendar.ClientSecret = clientSecret
			}

			if calendarID != "" {
				cfg.Calendar.CalendarID = calendarID
			}

			// A build may carry a client, in which case there is nothing to ask
			// for and the user goes straight to the consent screen.
			id, secret := gcal.Credentials(cfg.Calendar.ClientID, cfg.Calendar.ClientSecret)

			if id == "" {
				fmt.Fprint(app.Err, googleClientHelp)

				cfg.Calendar.ClientID, err = prompt(app.Err, "client id: ")
				if err != nil {
					return errNeedsTerminal(err)
				}

				entered, err := promptSecret(app.Err, "client secret: ")
				if err != nil {
					return errNeedsTerminal(err)
				}

				cfg.Calendar.ClientSecret = string(entered)

				id, secret = cfg.Calendar.ClientID, cfg.Calendar.ClientSecret
			}

			if id == "" {
				return fmt.Errorf("a client id is required")
			}

			oc := gcal.OAuthConfig(id, secret)

			tok, err := gcal.Authorize(ctx, oc, func(url string) {
				fmt.Fprintf(app.Err, "\nOpen this to authorise:\n\n  %s\n\nWaiting...\n", url)
			})
			if err != nil {
				return err
			}

			if err := gcal.SaveToken(tok); err != nil {
				return err
			}

			if err := saveConfig(cfg); err != nil {
				return err
			}

			return app.Emit(map[string]any{"ok": true, "calendar_id": cfg.Calendar.CalendarID},
				func(w io.Writer) {
					fmt.Fprintln(w, "Calendar authorised.")
				})
		},
	}

	cmd.Flags().StringVar(&clientID, "client-id", "", "google oauth client id")
	cmd.Flags().StringVar(&clientSecret, "client-secret", "", "google oauth client secret")
	cmd.Flags().StringVar(&calendarID, "calendar", "", "which calendar to use, default primary")

	return cmd
}

// googleClientHelp is printed when the user has to make their own OAuth
// client, which is most of the time: shipping one is a decision with
// consequences, and this build did not make it.
// Each step carries its own URL. Google renames the console's sections often
// enough that directions by menu name go stale; the URLs have not moved.
const googleClientHelp = `
The calendar uses Google, and Google needs an OAuth client to let anything in.
This tool ships without one on purpose, so it uses yours. Five minutes, once.
(Todos are local and need none of this.)

  1. Make a project
     https://console.cloud.google.com/projectcreate

  2. Enable the API it calls
     https://console.cloud.google.com/apis/library/calendar-json.googleapis.com

  3. Set up the consent screen, choosing "External"
     https://console.cloud.google.com/auth/overview
     Add your own address under Audience > Test users. Leave it in Testing;
     this is your client, used by you.

  4. Create credentials of type "Desktop app"
     https://console.cloud.google.com/auth/clients/create
     No redirect URI is needed: the sign-in binds a free port on this machine.

  5. Paste the two values below.

You will be asked to grant read and write on your events and read on your
calendar list. Nothing else.

Google will say the app is not verified. It is yours and you made it a minute
ago, so "Advanced" then "Go to ... (unsafe)" is the way through.

The client id and secret are stored in ~/.config/frankenstein/config.json.
The token goes to your system keyring, and "frankenstein logout" clears it.

`

// errNeedsTerminal turns the bare EOF from a closed stdin into something that
// says what to do instead.
func errNeedsTerminal(err error) error {
	if errors.Is(err, io.EOF) {
		// No trailing ellipsis: an error string is a lower-case phrase with no
		// closing punctuation, and "..." counts.
		return fmt.Errorf("this needs a terminal to type into; "+
			"without one, pass --client-id and --client-secret to %s calendar setup",
			config.AppName)
	}

	return err
}

func newCalendarsCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List calendars",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, _, err := calendarProvider(cmd.Context(), app)
			if err != nil {
				return err
			}

			cals, err := p.Calendars(cmd.Context())
			if err != nil {
				return err
			}

			cfg, _ := app.Config()

			shown := fcal.ResolveShown(cfg.Calendar.Shown(), cals)

			return app.Emit(cals, func(w io.Writer) {
				t := Table(w)
				fmt.Fprintln(t, "SHOWN\tNAME\tCOLOUR\tID")

				for _, c := range cals {
					mark := ""
					if shown[c.ID] {
						mark = "x"
					}

					name := c.Name
					if c.Primary {
						name += " (default)"
					}

					fmt.Fprintf(t, "%s\t%s\t%s\t%s\n",
						mark, truncate(name, 34), c.Color, truncate(c.ID, 44))
				}

				t.Flush()
				fmt.Fprintln(w, "\nPress g in the TUI to choose which are shown.")
			})
		},
	}
}

func newEventsCmd(app *App) *cobra.Command {
	var (
		days int
		from string
	)

	cmd := &cobra.Command{
		Use:     "events",
		Short:   "List upcoming events",
		Aliases: []string{"agenda"},
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			p, calID, err := calendarProvider(ctx, app)
			if err != nil {
				return err
			}

			start := time.Now()

			if from != "" {
				start, err = parseWhen(from)
				if err != nil {
					return err
				}
			}

			cfg, _ := app.Config()

			ids := cfg.Calendar.Shown()
			if calID != "" && len(cfg.Calendar.CalendarIDs) == 0 {
				ids = []string{calID}
			}

			events, err := p.EventsFrom(ctx, ids, start, start.AddDate(0, 0, days))
			if err != nil {
				return err
			}

			// A calendar that failed while the others answered, or an event
			// dropped for an unreadable time, does not fail the listing; the
			// provider reports it as a warning. It goes to stderr so JSON
			// output stays clean.
			if warner, ok := p.(interface{ Warnings() []string }); ok {
				for _, warning := range warner.Warnings() {
					fmt.Fprintf(app.Err, "warning: %s\n", warning)
				}
			}

			return app.Emit(events, func(w io.Writer) {
				if len(events) == 0 {
					fmt.Fprintln(w, "Nothing scheduled.")

					return
				}

				var day string

				for _, e := range events {
					d := e.Start.Format("Monday 2 January")
					if d != day {
						if day != "" {
							fmt.Fprintln(w)
						}

						fmt.Fprintf(w, "%s\n", d)
						day = d
					}

					when := fmt.Sprintf("%s-%s",
						e.Start.Format("15:04"), e.End.Format("15:04"))
					if e.AllDay {
						when = "all day"
					}

					fmt.Fprintf(w, "  %-13s %s", when, e.Title)

					if e.Location != "" {
						fmt.Fprintf(w, "  (%s)", truncate(e.Location, 30))
					}

					fmt.Fprintln(w)
				}
			})
		},
	}

	cmd.Flags().IntVarP(&days, "days", "d", 7, "how many days ahead to look")
	cmd.Flags().StringVar(&from, "from", "", "start date, YYYY-MM-DD or 'today'")

	return cmd
}

// parseWhen is the shared parser, so the command layer and the TUI accept the
// same input.
func parseWhen(s string) (time.Time, error) { return when.Parse(s) }

// draftEnd turns the end a person typed into the end the draft carries. The
// date given at --end is the last day an all-day event covers, but the draft
// hands Google an exclusive end date, so handing it over untouched cut the
// last day off every multi-day all-day event.
func draftEnd(endAt time.Time, allDay bool) time.Time {
	if !allDay {
		return endAt
	}

	return endAt.AddDate(0, 0, 1)
}

func newEventAddCmd(app *App) *cobra.Command {
	var (
		start     string
		duration  time.Duration
		end       string
		location  string
		notes     string
		allDay    bool
		attendees []string
	)

	cmd := &cobra.Command{
		Use:   "add <title>",
		Short: "Create an event",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			p, calID, err := calendarProvider(ctx, app)
			if err != nil {
				return err
			}

			startAt, err := parseWhen(start)
			if err != nil {
				return err
			}

			endAt := startAt.Add(duration)

			if end != "" {
				endAt, err = parseWhen(end)
				if err != nil {
					return err
				}

				endAt = draftEnd(endAt, allDay)
			}

			if !endAt.After(startAt) && !allDay {
				return fmt.Errorf("the event ends before it starts")
			}

			ev, err := p.CreateEvent(ctx, calID, fcal.EventDraft{
				Title:     strings.Join(args, " "),
				Location:  location,
				Notes:     notes,
				Start:     startAt,
				End:       endAt,
				AllDay:    allDay,
				Attendees: attendees,
			})
			if err != nil {
				return err
			}

			return app.Emit(ev, func(w io.Writer) {
				fmt.Fprintf(w, "Added %q on %s\n", ev.Title, ev.Start.Format("Mon 2 Jan 15:04"))
			})
		},
	}

	cmd.Flags().StringVar(&start, "start", "now", "when it starts")
	cmd.Flags().DurationVar(&duration, "for", time.Hour, "how long it runs")
	cmd.Flags().StringVar(&end, "end", "", "when it ends, overrides --for")
	cmd.Flags().StringVar(&location, "location", "", "where")
	cmd.Flags().StringVar(&notes, "notes", "", "description")
	cmd.Flags().BoolVar(&allDay, "all-day", false, "an all-day event")
	cmd.Flags().StringArrayVar(&attendees, "invite", nil, "attendee email, repeatable")

	return cmd
}

func newEventRemoveCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:     "remove <event-id>",
		Short:   "Delete an event",
		Aliases: []string{"rm", "delete"},
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			p, calID, err := calendarProvider(ctx, app)
			if err != nil {
				return err
			}

			if err := p.DeleteEvent(ctx, calID, args[0]); err != nil {
				return err
			}

			return app.Emit(map[string]any{"ok": true, "event": args[0]},
				func(w io.Writer) {
					fmt.Fprintf(w, "Deleted %s\n", args[0])
				})
		},
	}
}
