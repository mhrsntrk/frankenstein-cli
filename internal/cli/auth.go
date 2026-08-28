package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/mhrsntrk/frankenstein-cli/internal/auth"
	gcal "github.com/mhrsntrk/frankenstein-cli/internal/calendar/google"
)

// stdin is shared so that a fresh reader per prompt cannot swallow buffered
// input intended for the next one.
var stdin = bufio.NewReader(os.Stdin)

func prompt(w io.Writer, label string) (string, error) {
	fmt.Fprint(w, label)

	line, err := stdin.ReadString('\n')
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(line), nil
}

// promptSecret hides input when stdin is a terminal, and falls back to a plain
// read otherwise so the tool still works in a pipeline.
func promptSecret(w io.Writer, label string) ([]byte, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintf(w, "%s(no terminal, input will echo) ", label)

		line, err := stdin.ReadString('\n')
		if err != nil {
			return nil, err
		}

		return []byte(strings.TrimSpace(line)), nil
	}

	fmt.Fprint(w, label)

	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(w)

	return b, err
}

type loginResult struct {
	OK       bool   `json:"ok"`
	Username string `json:"username,omitempty"`
	Email    string `json:"email,omitempty"`

	// NeedsHumanVerification and its URL are how the JSON caller learns that
	// login stalled on a captcha rather than failed.
	NeedsHumanVerification bool   `json:"needs_human_verification,omitempty"`
	VerificationURL        string `json:"verification_url,omitempty"`
	Message                string `json:"message,omitempty"`
}

func newLoginCmd(app *App) *cobra.Command {
	var (
		username string
		totp     string
		hvToken  string
		hvMethod string
	)

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to Proton",
		Long: "Logs in with SRP, handles two-factor, and unlocks the account keys.\n\n" +
			"Proton challenges a fresh login with a CAPTCHA. When that happens this\n" +
			"command prints a verify.proton.me URL; solve it in a browser and the\n" +
			"login continues. In --json mode it returns needs_human_verification\n" +
			"with the URL instead, and you re-run with --hv-token.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			// Login always asks for a password, so without a terminal on stdin
			// the prompt would read whatever a pipe happens to hold. Refuse
			// before asking rather than echo a secret into the wrong channel.
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return fmt.Errorf("login is interactive; run it in a terminal")
			}

			// The flags win, but an agent driving --json cannot type into a
			// prompt, so the environment is the second way in.
			if totp == "" {
				totp = os.Getenv("FRANKENSTEIN_TOTP")
			}

			if hvToken == "" {
				hvToken = os.Getenv("FRANKENSTEIN_HV_TOKEN")
			}

			if hvMethod == "" {
				hvMethod = os.Getenv("FRANKENSTEIN_HV_METHODS")
			}

			cfg, err := app.Config()
			if err != nil {
				return err
			}

			if username == "" {
				username = cfg.Username
			}

			if username == "" {
				username, err = prompt(app.Err, "proton username or email: ")
				if err != nil {
					return err
				}
			}

			password, err := promptSecret(app.Err, "login password: ")
			if err != nil {
				return err
			}

			mboxPass, err := promptSecret(app.Err, "mailbox password (blank if you use one password): ")
			if err != nil {
				return err
			}

			creds := auth.Credentials{
				Username:        username,
				Password:        password,
				MailboxPassword: mboxPass,
				TOTP:            totp,
			}

			if hvToken != "" {
				methods := []string{"captcha"}
				if hvMethod != "" {
					methods = strings.Split(hvMethod, ",")
				}

				creds.HV = &auth.HumanVerification{Token: hvToken, Methods: methods}
			}

			client, err := auth.Login(ctx, cfg, creds)

			var hv *auth.HumanVerification
			if errors.As(err, &hv) {
				if app.JSON {
					if err := app.Emit(loginResult{
						NeedsHumanVerification: true,
						VerificationURL:        hv.URL,
						Message:                "solve the CAPTCHA in a browser, then re-run with --hv-token",
					}, nil); err != nil {
						return err
					}

					// A stalled login is not a finished one: the caller keys
					// off the exit code, so exiting 0 here would read as
					// logged in.
					return fmt.Errorf("login stalled on human verification; solve the CAPTCHA and re-run with --hv-token")
				}

				fmt.Fprintf(app.Err, "\nProton wants a CAPTCHA. Open this, solve it, then press Enter:\n\n  %s\n\n", hv.URL)

				if _, perr := prompt(app.Err, "press Enter when solved: "); perr != nil {
					return perr
				}

				creds.HV = hv
				client, err = auth.Login(ctx, cfg, creds)
			}

			if err != nil {
				// The account may need a TOTP code we did not ask for up front.
				if strings.Contains(err.Error(), "needs a TOTP code") && totp == "" {
					code, perr := prompt(app.Err, "two-factor code: ")
					if perr != nil {
						return perr
					}

					creds.TOTP = code

					client, err = auth.Login(ctx, cfg, creds)
				}
			}

			if err != nil {
				return err
			}

			defer client.Client.Close()

			sessions, err := app.Sessions()
			if err != nil {
				return err
			}

			if err := sessions.Save(client.Session()); err != nil {
				return err
			}

			cfg.Username = client.User.Name
			if err := saveConfig(cfg); err != nil {
				return err
			}

			email := ""
			if len(client.Addresses) > 0 {
				email = client.Addresses[0].Email
			}

			return app.Emit(loginResult{OK: true, Username: client.User.Name, Email: email},
				func(w io.Writer) {
					fmt.Fprintf(w, "Logged in as %s", client.User.Name)

					if email != "" {
						fmt.Fprintf(w, " (%s)", email)
					}

					fmt.Fprintln(w)
					fmt.Fprintln(w, "Run `frankenstein sync` to fill the cache.")
				})
		},
	}

	cmd.Flags().StringVar(&username, "username", "", "proton username or email")
	cmd.Flags().StringVar(&totp, "totp", "", "two-factor code, or $FRANKENSTEIN_TOTP")
	cmd.Flags().StringVar(&hvToken, "hv-token", "", "solved human-verification token, or $FRANKENSTEIN_HV_TOKEN")
	cmd.Flags().StringVar(&hvMethod, "hv-methods", "", "human-verification methods, comma separated, or $FRANKENSTEIN_HV_METHODS")

	return cmd
}

func newLogoutCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Forget the stored session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sessions, err := app.Sessions()
			if err != nil {
				return err
			}

			cfg, err := app.Config()
			if err != nil {
				return err
			}

			// Logout revokes the session on Proton's side when it can and
			// clears the local copy regardless; only the clear can fail.
			if err := auth.Logout(cmd.Context(), cfg, sessions); err != nil {
				return err
			}

			// Logging out of Proton while leaving the Google token behind would
			// make "logout" a half-truth.
			if err := gcal.ClearToken(); err != nil {
				fmt.Fprintf(app.Err, "warning: could not clear the calendar token: %v\n", err)
			}

			return app.Emit(map[string]bool{"ok": true}, func(w io.Writer) {
				fmt.Fprintln(w, "Signed out of Proton and Google.")
				fmt.Fprintln(w, "The local cache is untouched; delete it by hand if you want it gone.")
			})
		},
	}
}

type whoami struct {
	LoggedIn    bool     `json:"logged_in"`
	Username    string   `json:"username,omitempty"`
	Addresses   []string `json:"addresses,omitempty"`
	CachePath   string   `json:"cache_path,omitempty"`
	Credentials string   `json:"credentials,omitempty"`
}

func newWhoamiCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the logged-in account",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := app.Provider(cmd.Context())
			if err != nil {
				if errors.Is(err, ErrNotLoggedIn) {
					return app.Emit(whoami{}, func(w io.Writer) {
						fmt.Fprintln(w, "Not logged in.")
					})
				}

				return err
			}

			addrs, err := p.Addresses(cmd.Context())
			if err != nil {
				return err
			}

			out := whoami{LoggedIn: true}

			for _, a := range addrs {
				out.Addresses = append(out.Addresses, a.Address)
			}

			if cfg, err := app.Config(); err == nil {
				out.Username = cfg.Username
			}

			if path, err := cachePath(); err == nil {
				out.CachePath = path
			}

			if s, err := app.Sessions(); err == nil {
				out.Credentials = s.Location()
			}

			return app.Emit(out, func(w io.Writer) {
				fmt.Fprintf(w, "Logged in as %s\n", out.Username)

				for _, a := range out.Addresses {
					fmt.Fprintf(w, "  %s\n", a)
				}

				fmt.Fprintf(w, "Cache:       %s\n", out.CachePath)
				fmt.Fprintf(w, "Credentials: %s\n", out.Credentials)
			})
		},
	}
}
