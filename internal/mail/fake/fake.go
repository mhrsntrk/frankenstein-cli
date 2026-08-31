// Package fake is an in-memory mail.Provider for tests.
//
// It exists so the store, the sync loop and the command layer can be tested
// without a Proton account, which is the whole point of the provider
// interface.
package fake

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
)

// Provider is an in-memory backend.
type Provider struct {
	mu sync.Mutex

	Boxen   []mail.Box
	Convs   []mail.Conversation
	Msgs    map[string][]mail.Message
	Bodies  map[string]mail.Body
	Newsies []mail.Newsletter
	Own     []mail.Address

	// Deltas are returned by Poll in order, one per call.
	Deltas []mail.Delta

	// SupportsNewsletters is false to simulate a provider without them.
	SupportsNewsletters bool

	// Calls records what was asked of the provider, so a test can assert that
	// a code path did not hit the network more than it should.
	Calls []string

	// Labelled records Label calls as "convID:boxID".
	Labelled   []string
	Unlabelled []string

	cursor string
}

// New returns an empty provider.
func New() *Provider {
	return &Provider{
		Msgs:                map[string][]mail.Message{},
		Bodies:              map[string]mail.Body{},
		SupportsNewsletters: true,
		cursor:              "cursor-0",
	}
}

func (p *Provider) record(name string) {
	p.Calls = append(p.Calls, name)
}

func (p *Provider) Name() string { return "fake" }

func (p *Provider) Close() error { return nil }

func (p *Provider) Addresses(context.Context) ([]mail.Address, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.record("Addresses")

	return append([]mail.Address(nil), p.Own...), nil
}

func (p *Provider) Boxes(context.Context) ([]mail.Box, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.record("Boxes")

	return append([]mail.Box(nil), p.Boxen...), nil
}

func (p *Provider) Conversations(_ context.Context, opts mail.ListOptions) ([]mail.Conversation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.record("Conversations")

	var out []mail.Conversation

	for _, c := range p.Convs {
		if opts.BoxID != "" && !contains(c.BoxIDs, opts.BoxID) {
			continue
		}

		if opts.UnreadOnly && !c.Unread() {
			continue
		}

		if opts.Search != "" && !strings.Contains(strings.ToLower(c.Subject), strings.ToLower(opts.Search)) {
			continue
		}

		out = append(out, c)
	}

	// Same order contract as the store: Desc is newest first, unset is oldest
	// first, ID breaks timestamp ties.
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Time.Equal(out[j].Time) {
			if opts.Desc {
				return out[i].Time.After(out[j].Time)
			}

			return out[i].Time.Before(out[j].Time)
		}

		if opts.Desc {
			return out[i].ID > out[j].ID
		}

		return out[i].ID < out[j].ID
	})

	if opts.Offset > 0 {
		if opts.Offset >= len(out) {
			return nil, nil
		}

		out = out[opts.Offset:]
	}

	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}

	return out, nil
}

func (p *Provider) Thread(_ context.Context, id string) (mail.Thread, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.record("Thread")

	for _, c := range p.Convs {
		if c.ID == id {
			return mail.Thread{Conversation: c, Messages: p.Msgs[id]}, nil
		}
	}

	return mail.Thread{}, mail.ErrNotFound
}

func (p *Provider) Body(_ context.Context, id string) (mail.Body, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.record("Body")

	b, ok := p.Bodies[id]
	if !ok {
		return mail.Body{}, mail.ErrNotFound
	}

	return b, nil
}

func (p *Provider) Attachment(context.Context, string, string) ([]byte, error) {
	return nil, mail.ErrNotFound
}

// Label records the call and mutates box membership, so a move flow can be
// tested end to end: label, unlabel, then resync and observe the new state.
func (p *Provider) Label(_ context.Context, ids []string, boxID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.record("Label")

	for _, id := range ids {
		p.Labelled = append(p.Labelled, id+":"+boxID)

		for i := range p.Convs {
			if p.Convs[i].ID == id && !contains(p.Convs[i].BoxIDs, boxID) {
				p.Convs[i].BoxIDs = append(p.Convs[i].BoxIDs, boxID)
			}
		}
	}

	return nil
}

// Unlabel is Label's inverse, and mutates membership the same way.
func (p *Provider) Unlabel(_ context.Context, ids []string, boxID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.record("Unlabel")

	for _, id := range ids {
		p.Unlabelled = append(p.Unlabelled, id+":"+boxID)

		for i := range p.Convs {
			if p.Convs[i].ID != id {
				continue
			}

			var kept []string

			for _, b := range p.Convs[i].BoxIDs {
				if b != boxID {
					kept = append(kept, b)
				}
			}

			p.Convs[i].BoxIDs = kept
		}
	}

	return nil
}

func (p *Provider) CreateBox(_ context.Context, name string, kind mail.BoxKind, color string) (mail.Box, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	b := mail.Box{
		ID:    fmt.Sprintf("box-%d", len(p.Boxen)+1),
		Name:  name,
		Path:  []string{name},
		Kind:  kind,
		Color: color,
	}

	p.Boxen = append(p.Boxen, b)

	return b, nil
}

func (p *Provider) MarkRead(context.Context, []string) error { return nil }

func (p *Provider) MarkUnread(context.Context, []string, string) error { return nil }

func (p *Provider) Draft(_ context.Context, d mail.Draft) (mail.Draft, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.record("Draft")

	if d.ID == "" {
		d.ID = "draft-1"
	}

	return d, nil
}

func (p *Provider) Send(_ context.Context, draftID string) (mail.Message, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.record("Send")

	return mail.Message{ID: draftID}, nil
}

func (p *Provider) Drafts(context.Context) ([]mail.Message, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.record("Drafts")

	return nil, nil
}

func (p *Provider) Newsletters(context.Context) ([]mail.Newsletter, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.record("Newsletters")

	if !p.SupportsNewsletters {
		return nil, mail.ErrNotSupported
	}

	return append([]mail.Newsletter(nil), p.Newsies...), nil
}

func (p *Provider) Cursor(context.Context) (string, error) { return p.cursor, nil }

// Poll returns the first queued delta after the given cursor, or an empty
// delta once the caller has caught up. Position comes from the cursor rather
// than a call counter, so a caller that re-polls an old cursor gets the same
// delta again — the semantics a real event stream has.
func (p *Provider) Poll(_ context.Context, cursor string) (mail.Delta, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.record("Poll")

	next := 0

	for i, d := range p.Deltas {
		if d.Cursor == cursor {
			next = i + 1
		}
	}

	if next >= len(p.Deltas) {
		return mail.Delta{Cursor: cursor}, nil
	}

	return p.Deltas[next], nil
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}

	return false
}

var _ mail.Provider = (*Provider)(nil)
