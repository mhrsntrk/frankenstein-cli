// Package sync keeps the warm cache current: a first backfill, then an
// incremental loop against the provider's event stream.
//
// Everything that touches the network for mail lives here or in the provider.
// The TUI never calls either; it reads the cache this package fills.
package sync

import (
	"context"
	"fmt"
	"time"

	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
	"github.com/mhrsntrk/frankenstein-cli/internal/store"
)

// Syncer moves data from a provider into the cache.
type Syncer struct {
	provider mail.Provider
	store    *store.Store

	// BackfillBoxes are the boxes filled on a first sync. Backfilling every
	// box on a large account is thousands of requests for threads the user
	// will never open, so the default is the ones a person actually reads.
	BackfillBoxes []string

	// BackfillDepth is how many conversations per box to fetch initially.
	BackfillDepth int

	// BodyCacheSize caps cached bodies; older ones are evicted by last access.
	BodyCacheSize int

	// OnProgress, when set, is called with human-readable progress.
	OnProgress func(string)
}

// New returns a Syncer with sensible defaults.
func New(p mail.Provider, s *store.Store) *Syncer {
	return &Syncer{
		provider:      p,
		store:         s,
		BackfillDepth: 200,
		BodyCacheSize: 500,
	}
}

func (s *Syncer) progress(format string, args ...any) {
	if s.OnProgress != nil {
		s.OnProgress(fmt.Sprintf(format, args...))
	}
}

// Result summarises one sync pass.
type Result struct {
	Boxes         int    `json:"boxes"`
	Conversations int    `json:"conversations"`
	Messages      int    `json:"messages"`
	Newsletters   int    `json:"newsletters"`
	Evicted       int    `json:"evicted_bodies"`
	Senders       int    `json:"senders"`
	FullResync    bool   `json:"full_resync"`
	Cursor        string `json:"cursor"`
}

// Once runs a single sync pass: backfill if the cache is cold, otherwise apply
// whatever the event stream has waiting.
func (s *Syncer) Once(ctx context.Context) (Result, error) {
	cursor, err := s.store.Cursor(ctx)
	if err != nil {
		return Result{}, err
	}

	if cursor == "" {
		return s.Backfill(ctx)
	}

	res, err := s.Incremental(ctx, cursor)
	if err != nil {
		return res, err
	}

	// A Refresh flag means the cursor was too old to reconcile from.
	if res.FullResync {
		s.progress("provider asked for a full resync")

		return s.Backfill(ctx)
	}

	return res, nil
}

// Backfill rebuilds the cache from scratch and takes a fresh cursor.
//
// The cursor is taken *before* fetching so that anything changing mid-backfill
// is picked up by the next incremental pass rather than being missed.
func (s *Syncer) Backfill(ctx context.Context) (Result, error) {
	res := Result{FullResync: true}

	cursor, err := s.provider.Cursor(ctx)
	if err != nil {
		return res, err
	}

	res.Cursor = cursor

	boxes, err := s.provider.Boxes(ctx)
	if err != nil {
		return res, err
	}

	if err := s.store.PutBoxes(ctx, boxes); err != nil {
		return res, err
	}

	res.Boxes = len(boxes)
	s.progress("cached %d boxes", len(boxes))

	targets := s.BackfillBoxes
	if len(targets) == 0 {
		targets = defaultBackfillBoxes(boxes)
	}

	for _, boxID := range targets {
		n, m, err := s.backfillBox(ctx, boxID)
		if err != nil {
			return res, fmt.Errorf("backfill box %s: %w", boxID, err)
		}

		res.Conversations += n
		res.Messages += m
	}

	// Newsletters are cheap and the screener leans on them heavily.
	if ns, err := s.provider.Newsletters(ctx); err == nil {
		if err := s.store.PutNewsletters(ctx, ns); err != nil {
			return res, err
		}

		res.Newsletters = len(ns)
		s.progress("cached %d newsletter subscriptions", len(ns))
	} else if !isNotSupported(err) {
		return res, fmt.Errorf("newsletters: %w", err)
	}

	if err := s.store.SetCursor(ctx, cursor); err != nil {
		return res, err
	}

	return res, nil
}

// backfillBox pages through one box, caching conversation headers. Message
// headers come along for the threads that get opened, not up front: fetching
// every message of every thread is what makes a mirror rather than a cache.
func (s *Syncer) backfillBox(ctx context.Context, boxID string) (int, int, error) {
	const page = 150

	var convs int

	for offset := 0; offset < s.BackfillDepth; offset += page {
		limit := page
		if remaining := s.BackfillDepth - offset; remaining < limit {
			limit = remaining
		}

		batch, err := s.provider.Conversations(ctx, mail.ListOptions{
			BoxID:  boxID,
			Limit:  limit,
			Offset: offset,
			Desc:   true,
		})
		if err != nil {
			return convs, 0, err
		}

		if len(batch) == 0 {
			break
		}

		if err := s.store.PutConversations(ctx, batch); err != nil {
			return convs, 0, err
		}

		convs += len(batch)
		s.progress("cached %d conversations in box %s", convs, boxID)

		if len(batch) < limit {
			break
		}
	}

	return convs, 0, nil
}

// Incremental applies event deltas from the cursor forward.
func (s *Syncer) Incremental(ctx context.Context, cursor string) (Result, error) {
	res := Result{Cursor: cursor}

	delta, err := s.provider.Poll(ctx, cursor)
	if err != nil {
		return res, err
	}

	res.Cursor = delta.Cursor

	if delta.Resync {
		res.FullResync = true

		return res, nil
	}

	var (
		upConvs  []mail.Conversation
		delConvs []string
		upMsgs   []mail.Message
		delMsgs  []string
	)

	for _, c := range delta.Conversations {
		if c.Kind == mail.ChangeDelete {
			delConvs = append(delConvs, c.ID)
			continue
		}

		upConvs = append(upConvs, c.Conversation)
	}

	for _, m := range delta.Messages {
		if m.Kind == mail.ChangeDelete {
			delMsgs = append(delMsgs, m.ID)
			continue
		}

		upMsgs = append(upMsgs, m.Message)
	}

	if err := s.store.PutConversations(ctx, upConvs); err != nil {
		return res, err
	}

	if err := s.store.DeleteConversations(ctx, delConvs); err != nil {
		return res, err
	}

	if err := s.store.PutMessages(ctx, upMsgs); err != nil {
		return res, err
	}

	if err := s.store.DeleteMessages(ctx, delMsgs); err != nil {
		return res, err
	}

	res.Conversations = len(upConvs) + len(delConvs)
	res.Messages = len(upMsgs) + len(delMsgs)

	// Box counts move with almost every delta, so refresh them whenever
	// anything at all changed.
	if len(delta.Boxes) > 0 || res.Conversations > 0 {
		boxes, err := s.provider.Boxes(ctx)
		if err != nil {
			return res, err
		}

		if err := s.store.PutBoxes(ctx, boxes); err != nil {
			return res, err
		}

		res.Boxes = len(boxes)
	}

	if n, err := s.store.EvictBodies(ctx, s.BodyCacheSize); err == nil {
		res.Evicted = n
	}

	if err := s.store.SetCursor(ctx, delta.Cursor); err != nil {
		return res, err
	}

	return res, nil
}

// Run polls until the context is cancelled. Errors are reported through
// OnError, if set, rather than stopping the loop: a transient network failure
// should not kill a long-running TUI session.
func (s *Syncer) Run(ctx context.Context, interval time.Duration, onError func(error)) {
	if interval <= 0 {
		interval = 30 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if _, err := s.Once(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}

			if onError != nil {
				onError(err)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Thread fetches a thread's messages and caches them. This is the one read
// path that will reach the network on a cache miss, because a thread's message
// list is not backfilled.
func (s *Syncer) Thread(ctx context.Context, conversationID string) (mail.Thread, error) {
	t, err := s.provider.Thread(ctx, conversationID)
	if err != nil {
		return mail.Thread{}, err
	}

	if err := s.store.PutConversations(ctx, []mail.Conversation{t.Conversation}); err != nil {
		return t, err
	}

	if err := s.store.PutMessages(ctx, t.Messages); err != nil {
		return t, err
	}

	return t, nil
}

// Body returns a decrypted body, from the cache when possible.
func (s *Syncer) Body(ctx context.Context, messageID string) (mail.Body, error) {
	b, err := s.store.Body(ctx, messageID)
	if err == nil {
		return b, nil
	}

	if err != mail.ErrNotFound {
		return mail.Body{}, err
	}

	b, err = s.provider.Body(ctx, messageID)
	if err != nil {
		return mail.Body{}, err
	}

	if err := s.store.PutBody(ctx, b); err != nil {
		return b, err
	}

	return b, nil
}

// defaultBackfillBoxes picks the boxes worth filling on a cold start: the ones
// a person reads, not every label on the account.
func defaultBackfillBoxes(boxes []mail.Box) []string {
	want := map[string]bool{
		"Inbox":   true,
		"Starred": true,
		"Archive": true,
		"Sent":    true,
		"Drafts":  true,
	}

	var out []string

	for _, b := range boxes {
		if b.Kind == mail.BoxSystem && want[b.Name] {
			out = append(out, b.ID)
		}
	}

	if len(out) == 0 && len(boxes) > 0 {
		out = append(out, boxes[0].ID)
	}

	return out
}

func isNotSupported(err error) bool {
	return err == mail.ErrNotSupported
}
