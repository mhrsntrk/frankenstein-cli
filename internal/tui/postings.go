package tui

import (
	"strings"

	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
	"github.com/mhrsntrk/frankenstein-cli/internal/terminal"
	"github.com/mhrsntrk/frankenstein-cli/internal/tui/heymodel"
	"github.com/mhrsntrk/frankenstein-cli/internal/tui/heyui"
)

// toPostings maps Proton conversations onto the row type hey-cli's renderer
// draws.
//
// This is the whole adapter. Everything visual comes from their code; this only
// decides what goes in each field.
func toPostings(convs []mail.Conversation) []heymodel.Posting {
	out := make([]heymodel.Posting, 0, len(convs))

	for _, c := range convs {
		var from mail.Address
		if len(c.Senders) > 0 {
			from = c.Senders[0]
		}

		p := heymodel.Posting{
			ID:        heyui.PostingID(c.ID),
			CreatedAt: c.Time,
			// Name is the subject line; Summary is the excerpt under it.
			Name:    terminal.SanitizeLine(c.Subject),
			Summary: terminal.SanitizeLine(c.Snippet),
			Seen:    !c.Unread(),
			Creator: heymodel.Contact{
				Name:         terminal.SanitizeLine(from.Display()),
				EmailAddress: from.Address,
			},
			VisibleEntryCount: int32(c.NumMessages),
		}

		// With no snippet yet the excerpt line would be blank, which reads as a
		// broken row. Falling back to the sender keeps the two-line shape until
		// the prefetch fills it in.
		if strings.TrimSpace(p.Summary) == "" {
			p.Summary = ""
		}

		out = append(out, p)
	}

	return out
}

// setPostings is the one way the conversation list changes hands: it keeps
// m.convs, the heyui list and the orderedConvs cache in step, so a renderer
// can never read an ordering the list no longer draws.
func (m *Model) setPostings(convs []mail.Conversation) {
	m.convs = convs
	m.list.SetPostings(toPostings(convs))
	m.ordered = nil
}

// orderedConvs is the conversations in the list's own order. The heyui list
// resorts its postings into sections, so m.convs' order cannot be trusted for
// the split pane's row math; the list is the authority on what row i is. The
// answer is cached until setPostings replaces the rows, because this used to
// be rebuilt on every frame.
func (m *Model) orderedConvs() []mail.Conversation {
	if m.ordered != nil {
		return m.ordered
	}

	byID := make(map[int64]mail.Conversation, len(m.convs))
	for _, c := range m.convs {
		byID[heyui.PostingID(c.ID)] = c
	}

	out := make([]mail.Conversation, 0, m.list.Len())

	for i := 0; i < m.list.Len(); i++ {
		p, ok := m.list.At(i)
		if !ok {
			continue
		}

		if c, ok := byID[p.ID]; ok {
			out = append(out, c)
		}
	}

	m.ordered = out

	return out
}

// conversationAt maps a rendered row back to the conversation behind it.
func (m *Model) conversationAt(i int) (mail.Conversation, bool) {
	p, ok := m.list.At(i)
	if !ok {
		return mail.Conversation{}, false
	}

	for _, c := range m.convs {
		if heyui.PostingID(c.ID) == p.ID {
			return c, true
		}
	}

	return mail.Conversation{}, false
}
