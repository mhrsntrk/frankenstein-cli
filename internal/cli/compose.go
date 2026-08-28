package cli

import (
	"fmt"
	"io"
	"net/mail"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	fmail "github.com/mhrsntrk/frankenstein-cli/internal/mail"
	"github.com/mhrsntrk/frankenstein-cli/internal/terminal"
)

// editorName resolves which editor to run: $EDITOR, then $VISUAL, then
// nothing. There is no vi fallback on purpose; a guessed editor opening on a
// machine that never chose one is worse than an error saying what to set.
func editorName() string {
	if e := os.Getenv("EDITOR"); e != "" {
		return e
	}

	return os.Getenv("VISUAL")
}

// openEditor writes a temporary file, opens $EDITOR on it, and returns what
// the user wrote.
func openEditor() (string, error) {
	editor := editorName()
	if editor == "" {
		return "", fmt.Errorf("no body given and $EDITOR is unset; pass --body or pipe the text in")
	}

	f, err := os.CreateTemp("", "frankenstein-*.txt")
	if err != nil {
		return "", err
	}

	name := f.Name()
	f.Close()

	defer os.Remove(name)

	// #nosec G204 -- the editor comes from the user's own environment.
	cmd := exec.Command(editor, name)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("run %s: %w", editor, err)
	}

	b, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}

	body := strings.TrimSpace(string(b))
	if body == "" {
		return "", fmt.Errorf("empty body, nothing sent")
	}

	return body, nil
}

// parseAddressList accepts comma-separated addresses, with or without names.
// An empty list comes back as nil with no error: whether a recipient is
// required is the caller's question, and --cc left unset is not a typo.
func parseAddressList(in []string) ([]fmail.Address, error) {
	var out []fmail.Address

	for _, chunk := range in {
		// A proper parse first, so a quoted name may carry a comma:
		// `"Doe, John" <j@example.com>` is one address, not two.
		if addrs, err := mail.ParseAddressList(chunk); err == nil {
			for _, a := range addrs {
				out = append(out, fmail.Address{Name: a.Name, Address: a.Address})
			}

			continue
		}

		for _, part := range strings.Split(chunk, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}

			a, err := mail.ParseAddress(part)
			if err != nil {
				// Bare "user@host" without angle brackets is common enough to
				// accept, so only reject what has no @ at all.
				if !strings.Contains(part, "@") {
					return nil, fmt.Errorf("not an email address: %q", part)
				}

				out = append(out, fmail.Address{Address: part})

				continue
			}

			out = append(out, fmail.Address{Name: a.Name, Address: a.Address})
		}
	}

	return out, nil
}

func newComposeCmd(app *App) *cobra.Command {
	var (
		to, cc, bcc []string
		subject     string
		body        string
		send        bool
		html        bool
	)

	cmd := &cobra.Command{
		Use:   "compose",
		Short: "Write a new message",
		Long: "Writes a draft and, with --send, sends it.\n\n" +
			"The body comes from --body, from stdin, or from $EDITOR.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			toAddrs, err := parseAddressList(to)
			if err != nil {
				return err
			}

			if len(toAddrs) == 0 {
				return fmt.Errorf("no recipients")
			}

			// A typoed --cc must fail loudly, not silently drop the recipient.
			ccAddrs, err := parseAddressList(cc)
			if err != nil {
				return fmt.Errorf("--cc: %w", err)
			}

			bccAddrs, err := parseAddressList(bcc)
			if err != nil {
				return fmt.Errorf("--bcc: %w", err)
			}

			text, err := readStdinOrEditor(app, body)
			if err != nil {
				return err
			}

			p, err := app.Provider(ctx)
			if err != nil {
				return err
			}

			mime := "text/plain"
			if html {
				mime = "text/html"
			}

			draft, err := p.Draft(ctx, fmail.Draft{
				Subject:  subject,
				To:       toAddrs,
				CC:       ccAddrs,
				BCC:      bccAddrs,
				Body:     text,
				MIMEType: mime,
			})
			if err != nil {
				return err
			}

			if !send {
				return app.Emit(draft, func(w io.Writer) {
					fmt.Fprintf(w, "Draft saved: %s\n", draft.ID)
					fmt.Fprintf(w, "Send it with: frankenstein send %s\n", shortID(draft.ID))
				})
			}

			sent, err := p.Send(ctx, draft.ID)
			if err != nil {
				return err
			}

			return app.Emit(sent, func(w io.Writer) {
				fmt.Fprintf(w, "Sent to %s\n", joinAddrs(toAddrs))
			})
		},
	}

	cmd.Flags().StringArrayVar(&to, "to", nil, "recipient, repeatable or comma separated")
	cmd.Flags().StringArrayVar(&cc, "cc", nil, "carbon copy")
	cmd.Flags().StringArrayVar(&bcc, "bcc", nil, "blind carbon copy")
	cmd.Flags().StringVarP(&subject, "subject", "s", "", "subject line")
	cmd.Flags().StringVar(&body, "body", "", "message body")
	cmd.Flags().BoolVar(&send, "send", false, "send instead of saving a draft")
	cmd.Flags().BoolVar(&html, "html", false, "treat the body as HTML")

	_ = cmd.MarkFlagRequired("to")

	return cmd
}

func newReplyCmd(app *App) *cobra.Command {
	var (
		body string
		send bool
		all  bool
	)

	cmd := &cobra.Command{
		Use:   "reply <message-id>",
		Short: "Reply to a message",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			st, err := app.Store()
			if err != nil {
				return err
			}

			msg, err := resolveMessage(ctx, st, args[0])
			if err != nil {
				return err
			}

			text, err := readStdinOrEditor(app, body)
			if err != nil {
				return err
			}

			p, err := app.Provider(ctx)
			if err != nil {
				return err
			}

			to := []fmail.Address{msg.From}
			if len(msg.ReplyTo) > 0 {
				to = msg.ReplyTo
			}

			var cc []fmail.Address

			if all {
				// Reply-all keeps the other recipients but must not add the
				// account's own addresses back in.
				own := make(map[string]bool)

				if addrs, err := p.Addresses(ctx); err == nil {
					for _, a := range addrs {
						own[strings.ToLower(a.Address)] = true
					}
				}

				for _, a := range append(append([]fmail.Address{}, msg.To...), msg.CC...) {
					if !own[strings.ToLower(a.Address)] {
						cc = append(cc, a)
					}
				}
			}

			subject := msg.Subject
			if !strings.HasPrefix(strings.ToLower(subject), "re:") {
				subject = "Re: " + subject
			}

			draft, err := p.Draft(ctx, fmail.Draft{
				Subject:   subject,
				To:        to,
				CC:        cc,
				Body:      text,
				MIMEType:  "text/plain",
				InReplyTo: msg.ID,
			})
			if err != nil {
				return err
			}

			if !send {
				return app.Emit(draft, func(w io.Writer) {
					fmt.Fprintf(w, "Reply drafted: %s\n", draft.ID)
				})
			}

			sent, err := p.Send(ctx, draft.ID)
			if err != nil {
				return err
			}

			return app.Emit(sent, func(w io.Writer) {
				fmt.Fprintf(w, "Replied to %s\n", terminal.SanitizeLine(msg.From.Display()))
			})
		},
	}

	cmd.Flags().StringVar(&body, "body", "", "reply text")
	cmd.Flags().BoolVar(&send, "send", false, "send instead of saving a draft")
	cmd.Flags().BoolVar(&all, "all", false, "reply to everyone")

	return cmd
}

func newDraftsCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "drafts",
		Short: "List saved drafts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := app.Provider(cmd.Context())
			if err != nil {
				return err
			}

			drafts, err := p.Drafts(cmd.Context())
			if err != nil {
				return err
			}

			return app.Emit(drafts, func(w io.Writer) {
				if len(drafts) == 0 {
					fmt.Fprintln(w, "No drafts.")

					return
				}

				t := Table(w)
				fmt.Fprintln(t, "ID\tWHEN\tTO\tSUBJECT")

				for _, d := range drafts {
					fmt.Fprintf(t, "%s\t%s\t%s\t%s\n",
						shortID(d.ID), relTime(d.Time),
						truncate(joinAddrs(d.To), 28), truncate(d.Subject, 48))
				}

				t.Flush()
			})
		},
	}
}

func newSendCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "send <draft-id>",
		Short: "Send a saved draft",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			p, err := app.Provider(ctx)
			if err != nil {
				return err
			}

			id := args[0]

			// Accept the short prefix the drafts listing prints.
			if len(id) <= 12 {
				drafts, err := p.Drafts(ctx)
				if err != nil {
					return err
				}

				var matches []string

				for _, d := range drafts {
					if strings.HasPrefix(d.ID, id) {
						matches = append(matches, d.ID)
					}
				}

				switch len(matches) {
				case 0:
					return fmt.Errorf("no draft matches %q", id)
				case 1:
					id = matches[0]
				default:
					return fmt.Errorf("%q matches %d drafts; use a longer prefix", id, len(matches))
				}
			}

			sent, err := p.Send(ctx, id)
			if err != nil {
				return err
			}

			return app.Emit(sent, func(w io.Writer) {
				fmt.Fprintf(w, "Sent: %s\n", terminal.SanitizeLine(sent.Subject))
			})
		},
	}
}
