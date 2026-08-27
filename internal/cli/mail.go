package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mhrsntrk/frankenstein-cli/internal/config"
	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
)

func saveConfig(cfg config.Config) error { return config.Save(cfg) }
func cachePath() (string, error)         { return config.CachePath() }

// resolveBox turns a name or ID typed by a person into a box ID. Matching by
// name is case-insensitive and accepts a unique prefix, because typing a
// Proton label ID by hand is not a thing anyone will do.
func resolveBox(ctx context.Context, app *App, nameOrID string) (mail.Box, error) {
	st, err := app.Store()
	if err != nil {
		return mail.Box{}, err
	}

	boxes, err := st.Boxes(ctx)
	if err != nil {
		return mail.Box{}, err
	}

	if len(boxes) == 0 {
		return mail.Box{}, fmt.Errorf("no boxes cached; run `frankenstein sync` first")
	}

	needle := strings.ToLower(strings.TrimSpace(nameOrID))

	for _, b := range boxes {
		if b.ID == nameOrID || strings.ToLower(b.Name) == needle {
			return b, nil
		}
	}

	var matches []mail.Box

	for _, b := range boxes {
		if strings.HasPrefix(strings.ToLower(b.Name), needle) {
			matches = append(matches, b)
		}
	}

	switch len(matches) {
	case 0:
		return mail.Box{}, fmt.Errorf("no box matches %q", nameOrID)
	case 1:
		return matches[0], nil
	default:
		names := make([]string, 0, len(matches))
		for _, m := range matches {
			names = append(names, m.Name)
		}

		return mail.Box{}, fmt.Errorf("%q is ambiguous: %s", nameOrID, strings.Join(names, ", "))
	}
}

func newBoxesCmd(app *App) *cobra.Command {
	var all bool

	cmd := &cobra.Command{
		Use:     "boxes",
		Short:   "List mailboxes",
		Aliases: []string{"mailboxes"},
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := app.Store()
			if err != nil {
				return err
			}

			boxes, err := st.Boxes(cmd.Context())
			if err != nil {
				return err
			}

			if len(boxes) == 0 {
				return fmt.Errorf("no boxes cached; run `frankenstein sync` first")
			}

			// The screener's own boxes stay visible even when empty: they are
			// the point of the tool, and a fresh setup leaves all four at zero.
			pinned := make(map[string]bool, 4)

			if cfg, err := app.Config(); err == nil {
				for _, id := range []string{
					cfg.Screener.ImboxID, cfg.Screener.FeedID,
					cfg.Screener.PaperTrailID, cfg.Screener.ScreenedOutID,
				} {
					if id != "" {
						pinned[id] = true
					}
				}
			}

			shown := boxes[:0:0]

			for _, b := range boxes {
				// Empty aggregates and unused categories are noise unless asked for.
				if !all && b.Total == 0 && b.Kind != mail.BoxSystem && !pinned[b.ID] {
					continue
				}

				shown = append(shown, b)
			}

			return app.Emit(shown, func(w io.Writer) {
				t := Table(w)
				fmt.Fprintln(t, "NAME\tKIND\tTOTAL\tUNREAD")

				for _, b := range shown {
					unread := ""
					if b.Unread > 0 {
						unread = fmt.Sprintf("%d", b.Unread)
					}

					fmt.Fprintf(t, "%s\t%s\t%d\t%s\n", b.Name, b.Kind, b.Total, unread)
				}

				t.Flush()
			})
		},
	}

	cmd.Flags().BoolVar(&all, "all", false, "include empty boxes")

	return cmd
}

// listThreads is shared by `box` and `threads`.
func listThreads(cmd *cobra.Command, app *App, boxRef string, limit int, unreadOnly bool, search string) error {
	ctx := cmd.Context()

	st, err := app.Store()
	if err != nil {
		return err
	}

	opts := mail.ListOptions{Limit: limit, UnreadOnly: unreadOnly, Search: search, Desc: true}

	if boxRef != "" {
		box, err := resolveBox(ctx, app, boxRef)
		if err != nil {
			return err
		}

		opts.BoxID = box.ID
	}

	convs, err := st.Conversations(ctx, opts)
	if err != nil {
		return err
	}

	return app.Emit(convs, func(w io.Writer) {
		if len(convs) == 0 {
			fmt.Fprintln(w, "Nothing here.")

			return
		}

		t := Table(w)
		fmt.Fprintln(t, "ID\tWHEN\tFROM\tSUBJECT")

		for _, c := range convs {
			from := "(nobody)"
			if len(c.Senders) > 0 {
				from = c.Senders[0].Display()
			}

			marker := " "
			if c.Unread() {
				marker = "*"
			}

			count := ""
			if c.NumMessages > 1 {
				count = fmt.Sprintf(" (%d)", c.NumMessages)
			}

			fmt.Fprintf(t, "%s%s\t%s\t%s\t%s%s\n",
				marker, shortID(c.ID), relTime(c.Time), truncate(from, 24),
				truncate(c.Subject, 56), count)
		}

		t.Flush()
	})
}

func newBoxCmd(app *App) *cobra.Command {
	var (
		limit  int
		unread bool
	)

	cmd := &cobra.Command{
		Use:   "box <name>",
		Short: "List threads in a mailbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return listThreads(cmd, app, args[0], limit, unread, "")
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "n", 30, "how many threads to show")
	cmd.Flags().BoolVar(&unread, "unread", false, "only threads with unread messages")

	return cmd
}

func newThreadsCmd(app *App) *cobra.Command {
	var (
		box    string
		limit  int
		unread bool
		search string
	)

	cmd := &cobra.Command{
		Use:   "threads",
		Short: "List threads",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return listThreads(cmd, app, box, limit, unread, search)
		},
	}

	cmd.Flags().StringVarP(&box, "box", "b", "Inbox", "mailbox to list")
	cmd.Flags().IntVarP(&limit, "limit", "n", 30, "how many threads to show")
	cmd.Flags().BoolVar(&unread, "unread", false, "only threads with unread messages")
	cmd.Flags().StringVar(&search, "search", "", "match the subject")

	return cmd
}

// resolveConversation accepts a full ID or the short prefix the list prints.
func resolveConversation(ctx context.Context, app *App, ref string) (mail.Conversation, error) {
	st, err := app.Store()
	if err != nil {
		return mail.Conversation{}, err
	}

	if c, err := st.Conversation(ctx, ref); err == nil {
		return c, nil
	}

	convs, err := st.Conversations(ctx, mail.ListOptions{Limit: 5000})
	if err != nil {
		return mail.Conversation{}, err
	}

	var matches []mail.Conversation

	for _, c := range convs {
		if strings.HasPrefix(c.ID, ref) || shortID(c.ID) == ref {
			matches = append(matches, c)
		}
	}

	switch len(matches) {
	case 0:
		return mail.Conversation{}, fmt.Errorf("no thread matches %q", ref)
	case 1:
		return matches[0], nil
	default:
		return mail.Conversation{}, fmt.Errorf("%q matches %d threads; use a longer prefix", ref, len(matches))
	}
}

func newThreadCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:     "thread <id>",
		Short:   "Show a thread's messages",
		Aliases: []string{"show"},
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			conv, err := resolveConversation(ctx, app, args[0])
			if err != nil {
				return err
			}

			syncer, err := app.Syncer(ctx)
			if err != nil {
				return err
			}

			thread, err := syncer.Thread(ctx, conv.ID)
			if err != nil {
				return err
			}

			return app.Emit(thread, func(w io.Writer) {
				fmt.Fprintf(w, "%s\n", thread.Conversation.Subject)
				fmt.Fprintf(w, "%d message(s)\n\n", len(thread.Messages))

				t := Table(w)
				fmt.Fprintln(t, "ID\tWHEN\tFROM\tUNREAD")

				for _, m := range thread.Messages {
					unread := ""
					if m.Unread {
						unread = "*"
					}

					fmt.Fprintf(t, "%s\t%s\t%s\t%s\n",
						shortID(m.ID), m.Time.Format("2006-01-02 15:04"),
						truncate(m.From.Display(), 30), unread)
				}

				t.Flush()
				fmt.Fprintf(w, "\nRead one with: %s read <message-id>\n", config.AppName)
			})
		},
	}
}

type readOutput struct {
	Message mail.Message `json:"message"`
	Body    mail.Body    `json:"body"`
}

func newReadCmd(app *App) *cobra.Command {
	var markRead bool

	cmd := &cobra.Command{
		Use:   "read <message-id>",
		Short: "Read one message",
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

			syncer, err := app.Syncer(ctx)
			if err != nil {
				return err
			}

			body, err := syncer.Body(ctx, msg.ID)
			if err != nil {
				return err
			}

			if markRead && msg.Unread && msg.ConversationID != "" {
				p, err := app.Provider(ctx)
				if err != nil {
					return err
				}

				if err := p.MarkRead(ctx, []string{msg.ConversationID}); err != nil {
					fmt.Fprintf(app.Err, "warning: could not mark read: %v\n", err)
				}
			}

			return app.Emit(readOutput{Message: msg, Body: body}, func(w io.Writer) {
				fmt.Fprintf(w, "From:    %s\n", msg.From)
				fmt.Fprintf(w, "To:      %s\n", joinAddrs(msg.To))

				if len(msg.CC) > 0 {
					fmt.Fprintf(w, "Cc:      %s\n", joinAddrs(msg.CC))
				}

				fmt.Fprintf(w, "Date:    %s\n", msg.Time.Format(time.RFC1123))
				fmt.Fprintf(w, "Subject: %s\n", msg.Subject)

				if len(body.Attachments) > 0 {
					names := make([]string, 0, len(body.Attachments))
					for _, a := range body.Attachments {
						names = append(names, a.Name)
					}

					fmt.Fprintf(w, "Files:   %s\n", strings.Join(names, ", "))
				}

				fmt.Fprintf(w, "\n%s\n", renderBody(body))
			})
		},
	}

	cmd.Flags().BoolVar(&markRead, "mark-read", true, "mark the thread read")

	return cmd
}

func resolveMessage(ctx context.Context, st interface {
	Message(context.Context, string) (mail.Message, error)
	Messages(context.Context, string) ([]mail.Message, error)
}, ref string) (mail.Message, error) {
	if m, err := st.Message(ctx, ref); err == nil {
		return m, nil
	}

	return mail.Message{}, fmt.Errorf("no cached message %q; open its thread first", ref)
}

func joinAddrs(in []mail.Address) string {
	out := make([]string, 0, len(in))
	for _, a := range in {
		out = append(out, a.String())
	}

	return strings.Join(out, ", ")
}

// renderBody strips HTML down to something readable in a terminal. This is
// deliberately crude: a full HTML renderer is not the job here.
func renderBody(b mail.Body) string {
	if !strings.Contains(strings.ToLower(b.MIMEType), "html") {
		return b.Content
	}

	s := b.Content

	for _, tag := range []string{"script", "style"} {
		s = stripElement(s, tag)
	}

	s = strings.NewReplacer(
		"<br>", "\n", "<br/>", "\n", "<br />", "\n",
		"</p>", "\n\n", "</div>", "\n", "</tr>", "\n",
		"&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'",
	).Replace(s)

	var out strings.Builder

	depth := 0

	for _, r := range s {
		switch {
		case r == '<':
			depth++
		case r == '>' && depth > 0:
			depth--
		case depth == 0:
			out.WriteRune(r)
		}
	}

	// Collapse the runs of blank lines that tag stripping leaves behind.
	lines := strings.Split(out.String(), "\n")
	kept := make([]string, 0, len(lines))
	blank := 0

	for _, l := range lines {
		l = strings.TrimRight(l, " \t")

		if strings.TrimSpace(l) == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}

		kept = append(kept, l)
	}

	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func stripElement(s, tag string) string {
	lower := strings.ToLower(s)
	open := "<" + tag
	closeTag := "</" + tag + ">"

	for {
		i := strings.Index(lower, open)
		if i < 0 {
			return s
		}

		j := strings.Index(lower[i:], closeTag)
		if j < 0 {
			return s[:i]
		}

		end := i + j + len(closeTag)
		s = s[:i] + s[end:]
		lower = strings.ToLower(s)
	}
}

// shortID is the prefix shown in listings, long enough to stay unique on a
// real mailbox but short enough to retype.
func shortID(id string) string {
	if len(id) <= 10 {
		return id
	}

	return id[:10]
}

// relTime renders a timestamp the way a mail client does: time today, weekday
// this week, date beyond that.
func relTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	now := time.Now()

	switch {
	case t.YearDay() == now.YearDay() && t.Year() == now.Year():
		return t.Format("15:04")
	case now.Sub(t) < 7*24*time.Hour:
		return t.Format("Mon 15:04")
	case t.Year() == now.Year():
		return t.Format("2 Jan")
	default:
		return t.Format("2 Jan 2006")
	}
}

func newLabelCmd(app *App) *cobra.Command {
	var remove bool

	cmd := &cobra.Command{
		Use:   "label <thread-id> <box>",
		Short: "Add or remove a label on a thread",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			conv, err := resolveConversation(ctx, app, args[0])
			if err != nil {
				return err
			}

			box, err := resolveBox(ctx, app, args[1])
			if err != nil {
				return err
			}

			p, err := app.Provider(ctx)
			if err != nil {
				return err
			}

			if remove {
				err = p.Unlabel(ctx, []string{conv.ID}, box.ID)
			} else {
				err = p.Label(ctx, []string{conv.ID}, box.ID)
			}

			if err != nil {
				return err
			}

			verb := "labelled"
			if remove {
				verb = "unlabelled"
			}

			return app.Emit(map[string]any{"ok": true, "thread": conv.ID, "box": box.Name, "removed": remove},
				func(w io.Writer) {
					fmt.Fprintf(w, "%s %s %s\n", verb, shortID(conv.ID), box.Name)
				})
		},
	}

	cmd.Flags().BoolVar(&remove, "remove", false, "remove the label instead of adding it")

	return cmd
}

// readStdinOrEditor gets a message body: from the flag, from a pipe, or by
// opening $EDITOR.
func readStdinOrEditor(bodyFlag string) (string, error) {
	if bodyFlag != "" {
		return bodyFlag, nil
	}

	fi, err := os.Stdin.Stat()
	if err == nil && (fi.Mode()&os.ModeCharDevice) == 0 {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}

		if len(strings.TrimSpace(string(b))) > 0 {
			return string(b), nil
		}
	}

	return openEditor()
}
