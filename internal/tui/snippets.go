package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
)

// snippetsMsg reports previews that were fetched, keyed by conversation.
type snippetsMsg map[string]string

// snippetPrefetchLimit is how many rows are filled per pass. Each one costs a
// thread fetch plus a body fetch plus a decrypt, so this is deliberately small:
// the list is already usable without previews, and they arrive as they land.
const snippetPrefetchLimit = 12

// prefetchSnippets fills the excerpt line for rows that have none.
//
// Proton sends no excerpt with conversation metadata, so the only way to get
// one is to decrypt a body. That is far too slow for a render path, so it runs
// as a command and the rows redraw as the answers arrive. Once fetched a
// snippet is cached for good.
func (m *Model) prefetchSnippets() tea.Cmd {
	var want []mail.Conversation

	for _, c := range m.convs {
		if strings.TrimSpace(c.Snippet) != "" {
			continue
		}

		want = append(want, c)

		if len(want) == snippetPrefetchLimit {
			break
		}
	}

	if len(want) == 0 {
		return nil
	}

	syncer, store := m.syncer, m.store

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		out := make(snippetsMsg, len(want))

		for _, c := range want {
			if ctx.Err() != nil {
				break
			}

			thread, err := syncer.Thread(ctx, c.ID)
			if err != nil || len(thread.Messages) == 0 {
				continue
			}

			// The newest message is the one a preview should show.
			newest := thread.Messages[len(thread.Messages)-1]

			body, err := syncer.Body(ctx, newest.ID)
			if err != nil {
				continue
			}

			snippet := excerpt(renderBody(body))
			if snippet == "" {
				continue
			}

			out[c.ID] = snippet

			if err := store.SetSnippet(ctx, c.ID, snippet); err != nil {
				continue
			}
		}

		return out
	}
}

// excerptLength is roughly one row's worth of preview on a wide terminal.
const excerptLength = 160

// excerpt reduces a decrypted body to a single readable line.
//
// Quoted replies and signature blocks are dropped: a preview showing the reply
// someone quoted rather than what they wrote is worse than no preview.
func excerpt(body string) string {
	var kept []string

	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)

		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, ">"):
			continue
		case line == "--" || line == "-- ":
			// Everything below a signature marker is boilerplate.
			return join(kept)
		case strings.HasPrefix(line, "On ") && strings.HasSuffix(line, "wrote:"):
			return join(kept)
		}

		kept = append(kept, line)

		if len(strings.Join(kept, " ")) >= excerptLength {
			break
		}
	}

	return join(kept)
}

func join(lines []string) string {
	s := strings.Join(lines, " ")

	s = strings.Join(strings.Fields(s), " ")

	if len(s) > excerptLength {
		s = strings.TrimSpace(s[:excerptLength])
	}

	return s
}
