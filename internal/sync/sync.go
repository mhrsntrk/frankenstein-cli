// Package sync keeps the warm cache current: a first backfill, then an
// incremental loop against the provider's event stream.
//
// Everything that touches the network for mail lives here or in the provider.
// The TUI never calls either; it reads the cache this package fills.
package sync

import (
	"context"
	"errors"
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

	// The TUI shows the account in its title bar and has no provider handle,
	// so record it here rather than making the render path ask.
	if addrs, err := s.provider.Addresses(ctx); err == nil && len(addrs) > 0 {
		_ = s.store.SetMeta(ctx, "account_email", addrs[0].Address)
	}

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
		n, err := s.backfillBox(ctx, boxID)
		if err != nil {
			return res, fmt.Errorf("backfill box %s: %w", boxID, err)
		}

		res.Conversations += n
	}

	// Newsletters are cheap, and the listing is the only place the mailing
	// list volume shows up, so they are refreshed on every pass.
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
//
// The backfill only ever adds. It used to also sweep away anything it did not
// see, so a resync would drop mail deleted server-side while the cursor was
// too old to say so -- but a sweep decides what to delete from a sample a few
// hundred rows deep, and one short page from the provider was enough to make
// it mistake a whole mailbox for that sample and delete the rest. A cache
// holding a thread the server no longer has is a stale row that the next
// event or refetch corrects; a cache that deletes live mail makes it
// invisible. The stale row is the better failure.
func (s *Syncer) backfillBox(ctx context.Context, boxID string) (int, error) {
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
			return convs, err
		}

		if len(batch) == 0 {
			break
		}

		if err := s.store.PutConversations(ctx, batch); err != nil {
			return convs, err
		}

		convs += len(batch)
		s.progress("cached %d conversations in box %s", convs, boxID)

		if len(batch) < limit {
			break
		}
	}

	return convs, nil
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

	// Changes are applied one by one in stream order. Batching by kind would
	// flatten the order and net a delete-then-recreate of the same ID out to
	// the wrong end state.
	for _, c := range delta.Conversations {
		if err := s.applyConversationChange(ctx, c); err != nil {
			return res, err
		}

		res.Conversations++
	}

	for _, m := range delta.Messages {
		if err := s.applyMessageChange(ctx, m); err != nil {
			return res, err
		}

		res.Messages++
	}

	// Box counts move with almost every delta — a message marked read changes
	// its box's unread count without a conversation event — so refresh them
	// whenever anything at all changed.
	if len(delta.Boxes) > 0 || res.Conversations > 0 || res.Messages > 0 {
		boxes, err := s.provider.Boxes(ctx)
		if err != nil {
			return res, err
		}

		if err := s.store.PutBoxes(ctx, boxes); err != nil {
			return res, err
		}

		res.Boxes = len(boxes)
	}

	n, err := s.store.EvictBodies(ctx, s.BodyCacheSize)
	if err != nil {
		return res, fmt.Errorf("evict bodies: %w", err)
	}

	res.Evicted = n

	if err := s.store.SetCursor(ctx, delta.Cursor); err != nil {
		return res, err
	}

	return res, nil
}

// applyConversationChange applies one conversation event.
func (s *Syncer) applyConversationChange(ctx context.Context, c mail.ConversationChange) error {
	if c.Kind == mail.ChangeDelete {
		return s.store.DeleteConversations(ctx, []string{c.ID})
	}

	conv := c.Conversation

	// Proton does not promise that an update event carries the full
	// conversation, so a zero field on an update is treated as "unchanged"
	// rather than allowed to blank a real cached value.
	if c.Kind == mail.ChangeUpdate {
		cached, err := s.store.Conversation(ctx, c.ID)

		switch {
		case err == nil:
			conv = mergeConversation(cached, conv)
		case !errors.Is(err, mail.ErrNotFound):
			return err
		}
	}

	return s.store.PutConversations(ctx, []mail.Conversation{conv})
}

// applyMessageChange applies one message event, with the same partial-update
// guard as conversations.
func (s *Syncer) applyMessageChange(ctx context.Context, m mail.MessageChange) error {
	if m.Kind == mail.ChangeDelete {
		return s.store.DeleteMessages(ctx, []string{m.ID})
	}

	msg := m.Message

	if m.Kind == mail.ChangeUpdate {
		cached, err := s.store.Message(ctx, m.ID)

		switch {
		case err == nil:
			msg = mergeMessage(cached, msg)
		case !errors.Is(err, mail.ErrNotFound):
			return err
		}
	}

	return s.store.PutMessages(ctx, []mail.Message{msg})
}

// mergeConversation keeps cached values wherever the update left a zero. Only
// fields a real conversation cannot legitimately zero are guarded: NumUnread
// going to 0 is a thread being read, so it is written as-is.
func mergeConversation(cached, upd mail.Conversation) mail.Conversation {
	if upd.Time.Unix() <= 0 {
		upd.Time = cached.Time
	}

	if upd.Subject == "" {
		upd.Subject = cached.Subject
	}

	if len(upd.Senders) == 0 {
		upd.Senders = cached.Senders
	}

	if len(upd.Recipients) == 0 {
		upd.Recipients = cached.Recipients
	}

	if len(upd.BoxIDs) == 0 {
		upd.BoxIDs = cached.BoxIDs
	}

	if upd.NumMessages == 0 {
		upd.NumMessages = cached.NumMessages
	}

	if upd.Order == 0 {
		upd.Order = cached.Order
	}

	return upd
}

// mergeMessage is mergeConversation's counterpart for message events.
func mergeMessage(cached, upd mail.Message) mail.Message {
	if upd.Time.Unix() <= 0 {
		upd.Time = cached.Time
	}

	if upd.Subject == "" {
		upd.Subject = cached.Subject
	}

	if upd.From.Address == "" {
		upd.From = cached.From
	}

	if upd.ConversationID == "" {
		upd.ConversationID = cached.ConversationID
	}

	if len(upd.BoxIDs) == 0 {
		upd.BoxIDs = cached.BoxIDs
	}

	if upd.Order == 0 {
		upd.Order = cached.Order
	}

	return upd
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

	// A thread fetch races the incremental loop: by the time this snapshot
	// lands, a poll may already have applied newer events. A cached header
	// that outranks the snapshot wins and the write is skipped; the messages
	// are still cached, since they only ever accumulate.
	cached, err := s.store.Conversation(ctx, conversationID)

	switch {
	case err == nil && snapshotIsStale(cached, t.Conversation):
		// keep the cached header
	case err == nil || errors.Is(err, mail.ErrNotFound):
		if err := s.store.PutConversations(ctx, []mail.Conversation{t.Conversation}); err != nil {
			return t, err
		}
	default:
		return t, err
	}

	if err := s.store.PutMessages(ctx, t.Messages); err != nil {
		return t, err
	}

	return t, nil
}

// snapshotIsStale reports whether the cache already holds something newer
// than the fetched snapshot. Order is the provider's sort key and moves with
// every change, so it is the sharper comparison; Time is the fallback when
// either side lacks one.
func snapshotIsStale(cached, snap mail.Conversation) bool {
	if cached.Order > 0 && snap.Order > 0 {
		return snap.Order < cached.Order
	}

	return snap.Time.Before(cached.Time)
}

// ApplyLocalMove reflects a move in the cache immediately, so the list the
// user is looking at updates before the next poll confirms it. The caller
// still writes the move to the provider; this is only the optimistic half.
func (s *Syncer) ApplyLocalMove(ctx context.Context, ids []string, addBoxID, removeBoxID string) error {
	return s.store.ApplyMove(ctx, ids, addBoxID, removeBoxID)
}

// Body returns a decrypted body, from the cache when possible.
func (s *Syncer) Body(ctx context.Context, messageID string) (mail.Body, error) {
	b, err := s.store.Body(ctx, messageID)
	if err == nil {
		return b, nil
	}

	if !errors.Is(err, mail.ErrNotFound) {
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
	return errors.Is(err, mail.ErrNotSupported)
}
