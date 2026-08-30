// Package store is the warm cache: a SQLite index of boxes, conversations and
// message headers, with bodies fetched lazily and evicted by age.
//
// The TUI reads only from here. Nothing in this package touches the network.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
)

//go:embed schema.sql
var schema string

// Store is a handle on the cache database.
type Store struct {
	db *sql.DB
}

// Open opens (and migrates) the cache at path.
func Open(path string) (*Store, error) {
	// _time_format=sqlite keeps timestamps readable if anyone opens the file
	// by hand; busy_timeout stops the sync loop and a CLI command colliding.
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open cache %s: %w", path, err)
	}

	// modernc's driver is not safe for unlimited concurrent writers.
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		db.Close()

		return nil, err
	}

	return &Store{db: db}, nil
}

// migrations are the steps an existing cache may need on top of the base
// schema, in order. PRAGMA user_version records how many have run, so each
// step runs exactly once; a fresh database is created by schema.sql already in
// its final shape and is stamped current without running any of them.
var migrations = []func(*sql.Tx) error{
	// v1: the snippet column predates versioned migrations, so an old cache
	// may or may not already have it.
	func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('conversations') WHERE name = 'snippet'`).Scan(&n); err != nil {
			return err
		}

		if n > 0 {
			return nil
		}

		_, err := tx.Exec(`ALTER TABLE conversations ADD COLUMN snippet TEXT NOT NULL DEFAULT ''`)

		return err
	},
	// v2: conversation deletes used to leave the thread's messages behind.
	// Messages with an empty conversation_id (upstream drafts) are kept.
	func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM messages
			WHERE conversation_id != ''
			  AND conversation_id NOT IN (SELECT id FROM conversations)`)

		return err
	},
	// v3: conversations cached from a listing before the Context* fallback
	// existed have no time and no box membership, which puts them in no box
	// and below every dated row. They are unreachable rather than merely
	// wrong, so they are dropped and left for the next sync to refetch.
	func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM conversations
			WHERE time = 0
			  AND id NOT IN (SELECT conversation_id FROM conversation_boxes)`)

		return err
	},
}

// migrate brings the database to the current schema: the base schema first
// (CREATE IF NOT EXISTS, so it is safe to repeat), then whichever versioned
// steps this file has not yet seen.
func migrate(db *sql.DB) error {
	// A database without the conversations table is brand new: schema.sql
	// creates it in its final shape, so no migration has anything to do.
	var tables int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'conversations'`).Scan(&tables); err != nil {
		return fmt.Errorf("inspect cache: %w", err)
	}

	fresh := tables == 0

	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("migrate cache: %w", err)
	}

	if fresh {
		if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, len(migrations))); err != nil {
			return fmt.Errorf("stamp cache version: %w", err)
		}

		return nil
	}

	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read cache version: %w", err)
	}

	for ; version < len(migrations); version++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}

		if err := migrations[version](tx); err != nil {
			tx.Rollback()

			return fmt.Errorf("migrate cache to v%d: %w", version+1, err)
		}

		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, version+1)); err != nil {
			tx.Rollback()

			return fmt.Errorf("stamp cache version: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) Close() error { return s.db.Close() }

// DB exposes the handle for packages that own their own tables (habits, time
// tracking, journal index).
func (s *Store) DB() *sql.DB { return s.db }

// --- meta -------------------------------------------------------------------

// Meta reads a metadata value, returning "" when unset.
func (s *Store) Meta(ctx context.Context, key string) (string, error) {
	var v string

	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("read meta %s: %w", key, err)
	}

	return v, nil
}

// SetMeta writes a metadata value.
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("write meta %s: %w", key, err)
	}

	return nil
}

// Cursor and SetCursor track the provider's event stream position.
func (s *Store) Cursor(ctx context.Context) (string, error) { return s.Meta(ctx, "sync_cursor") }
func (s *Store) SetCursor(ctx context.Context, c string) error {
	return s.SetMeta(ctx, "sync_cursor", c)
}

// --- boxes ------------------------------------------------------------------

// Snippet stores a conversation's preview line.
func (s *Store) SetSnippet(ctx context.Context, conversationID, snippet string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE conversations SET snippet = ? WHERE id = ?`, snippet, conversationID)
	if err != nil {
		return fmt.Errorf("store snippet: %w", err)
	}

	return nil
}

// PutBoxes replaces the box list wholesale. Boxes are few and change rarely,
// so a full swap is simpler than reconciling and cannot drift.
func (s *Store) PutBoxes(ctx context.Context, boxes []mail.Box) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM boxes`); err != nil {
		return fmt.Errorf("clear boxes: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO boxes (id, name, path, kind, color, total, unread) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, b := range boxes {
		if _, err := stmt.ExecContext(ctx,
			b.ID, b.Name, strings.Join(b.Path, "/"), string(b.Kind), b.Color, b.Total, b.Unread); err != nil {
			return fmt.Errorf("insert box %s: %w", b.ID, err)
		}
	}

	// A label deleted server-side vanishes from the new list but its
	// membership rows would linger, so clear anything now pointing at nothing.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM conversation_boxes WHERE box_id NOT IN (SELECT id FROM boxes)`); err != nil {
		return fmt.Errorf("clear orphaned box membership: %w", err)
	}

	return tx.Commit()
}

// Boxes returns the cached box list in insertion order, which the provider
// already sorted for display.
func (s *Store) Boxes(ctx context.Context) ([]mail.Box, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, path, kind, color, total, unread FROM boxes ORDER BY rowid`)
	if err != nil {
		return nil, fmt.Errorf("list boxes: %w", err)
	}
	defer rows.Close()

	var out []mail.Box

	for rows.Next() {
		var b mail.Box
		var path, kind string

		if err := rows.Scan(&b.ID, &b.Name, &path, &kind, &b.Color, &b.Total, &b.Unread); err != nil {
			return nil, err
		}

		if path != "" {
			b.Path = strings.Split(path, "/")
		}

		b.Kind = mail.BoxKind(kind)
		out = append(out, b)
	}

	return out, rows.Err()
}

// Box returns one cached box by ID.
func (s *Store) Box(ctx context.Context, id string) (mail.Box, error) {
	boxes, err := s.Boxes(ctx)
	if err != nil {
		return mail.Box{}, err
	}

	for _, b := range boxes {
		if b.ID == id {
			return b, nil
		}
	}

	return mail.Box{}, mail.ErrNotFound
}

// --- conversations ----------------------------------------------------------

// PutConversations upserts conversations and replaces their box membership.
func (s *Store) PutConversations(ctx context.Context, convs []mail.Conversation) error {
	if len(convs) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	upsert, err := tx.PrepareContext(ctx, `
		INSERT INTO conversations
			(id, subject, senders, recipients, num_messages, num_unread,
			 num_attachments, time, size, category_id, sort_order)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			subject = excluded.subject,
			senders = excluded.senders,
			recipients = excluded.recipients,
			num_messages = excluded.num_messages,
			num_unread = excluded.num_unread,
			num_attachments = excluded.num_attachments,
			time = excluded.time,
			size = excluded.size,
			category_id = excluded.category_id,
			sort_order = excluded.sort_order`)
	if err != nil {
		return err
	}
	defer upsert.Close()

	clearBoxes, err := tx.PrepareContext(ctx, `DELETE FROM conversation_boxes WHERE conversation_id = ?`)
	if err != nil {
		return err
	}
	defer clearBoxes.Close()

	addBox, err := tx.PrepareContext(ctx,
		`INSERT OR IGNORE INTO conversation_boxes (conversation_id, box_id) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer addBox.Close()

	for _, c := range convs {
		senders, err := json.Marshal(c.Senders)
		if err != nil {
			return err
		}

		recipients, err := json.Marshal(c.Recipients)
		if err != nil {
			return err
		}

		if _, err := upsert.ExecContext(ctx,
			c.ID, c.Subject, string(senders), string(recipients),
			c.NumMessages, c.NumUnread, c.NumAttachments,
			c.Time.Unix(), c.Size, c.CategoryID, c.Order); err != nil {
			return fmt.Errorf("upsert conversation %s: %w", c.ID, err)
		}

		if _, err := clearBoxes.ExecContext(ctx, c.ID); err != nil {
			return err
		}

		for _, b := range c.BoxIDs {
			if _, err := addBox.ExecContext(ctx, c.ID, b); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

// DeleteConversations removes conversations, their box rows (by cascade) and
// their messages.
func (s *Store) DeleteConversations(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := deleteConversationsTx(ctx, tx, ids); err != nil {
		return err
	}

	return tx.Commit()
}

// deleteConversationsTx does the actual work, shared with PruneConversations.
//
// Messages carry no foreign key to conversations — a message event can arrive
// before its conversation is cached — so their delete is explicit here rather
// than a cascade. Bodies do cascade off the message rows.
func deleteConversationsTx(ctx context.Context, tx *sql.Tx, ids []string) error {
	delMsgs, err := tx.PrepareContext(ctx, `DELETE FROM messages WHERE conversation_id = ?`)
	if err != nil {
		return err
	}
	defer delMsgs.Close()

	delConv, err := tx.PrepareContext(ctx, `DELETE FROM conversations WHERE id = ?`)
	if err != nil {
		return err
	}
	defer delConv.Close()

	for _, id := range ids {
		if _, err := delMsgs.ExecContext(ctx, id); err != nil {
			return fmt.Errorf("delete messages of %s: %w", id, err)
		}

		if _, err := delConv.ExecContext(ctx, id); err != nil {
			return fmt.Errorf("delete conversation %s: %w", id, err)
		}
	}

	return nil
}

// DeleteMessages removes messages and, by cascade, their cached bodies.
func (s *Store) DeleteMessages(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `DELETE FROM messages WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, id := range ids {
		if _, err := stmt.ExecContext(ctx, id); err != nil {
			return fmt.Errorf("delete message %s: %w", id, err)
		}
	}

	return tx.Commit()
}

// PruneConversations deletes cached conversations in one box that a backfill
// no longer saw: whatever the sweep finds was deleted or moved server-side
// while the cursor was too old to tell us.
//
// seen is the union of IDs the whole backfill pass fetched, across every box,
// so a thread that moved between two backfilled boxes is never purged. since
// bounds the sweep to the window the backfill actually covered — a pass that
// stopped at its depth cap has said nothing about older threads, so only rows
// strictly newer than the oldest one fetched are candidates. The zero time
// means the box was paged to exhaustion and the whole box is fair game.
// Boundary ties on since are left alone; the next pass gets them.
func (s *Store) PruneConversations(ctx context.Context, boxID string, seen map[string]bool, since time.Time) (int, error) {
	query := `SELECT c.id FROM conversations c
	          JOIN conversation_boxes cb ON cb.conversation_id = c.id
	          WHERE cb.box_id = ?`
	args := []any{boxID}

	if !since.IsZero() {
		query += ` AND c.time > ?`
		args = append(args, since.Unix())
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("list prune candidates in %s: %w", boxID, err)
	}

	var stale []string

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()

			return 0, err
		}

		if !seen[id] {
			stale = append(stale, id)
		}
	}

	if err := rows.Err(); err != nil {
		rows.Close()

		return 0, err
	}

	rows.Close()

	if len(stale) == 0 {
		return 0, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if err := deleteConversationsTx(ctx, tx, stale); err != nil {
		return 0, err
	}

	return len(stale), tx.Commit()
}

// ApplyMove records a move locally: the conversations gain one box and lose
// another, so the list the user is looking at changes now rather than after
// the next poll round-trips. Box counters are nudged to match; the next
// provider refresh replaces them with authoritative numbers.
//
// IDs not in the cache are skipped: there is nothing to move locally.
func (s *Store) ApplyMove(ctx context.Context, ids []string, addBoxID, removeBoxID string) error {
	if len(ids) == 0 || (addBoxID == "" && removeBoxID == "") {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	add, err := tx.PrepareContext(ctx,
		`INSERT OR IGNORE INTO conversation_boxes (conversation_id, box_id) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer add.Close()

	remove, err := tx.PrepareContext(ctx,
		`DELETE FROM conversation_boxes WHERE conversation_id = ? AND box_id = ?`)
	if err != nil {
		return err
	}
	defer remove.Close()

	// Counter deltas per box: total moves with every row, unread only with
	// the threads that have something unread.
	var addTotal, addUnread, rmTotal, rmUnread int

	for _, id := range ids {
		var unread int

		err := tx.QueryRowContext(ctx,
			`SELECT num_unread FROM conversations WHERE id = ?`, id).Scan(&unread)
		if err == sql.ErrNoRows {
			continue
		}

		if err != nil {
			return fmt.Errorf("apply move to %s: %w", id, err)
		}

		if addBoxID != "" {
			res, err := add.ExecContext(ctx, id, addBoxID)
			if err != nil {
				return fmt.Errorf("apply move to %s: %w", id, err)
			}

			if n, _ := res.RowsAffected(); n > 0 {
				addTotal++

				if unread > 0 {
					addUnread++
				}
			}
		}

		if removeBoxID != "" {
			res, err := remove.ExecContext(ctx, id, removeBoxID)
			if err != nil {
				return fmt.Errorf("apply move to %s: %w", id, err)
			}

			if n, _ := res.RowsAffected(); n > 0 {
				rmTotal++

				if unread > 0 {
					rmUnread++
				}
			}
		}
	}

	for _, adj := range []struct {
		boxID         string
		total, unread int
	}{
		{addBoxID, addTotal, addUnread},
		{removeBoxID, -rmTotal, -rmUnread},
	} {
		if adj.boxID == "" || (adj.total == 0 && adj.unread == 0) {
			continue
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE boxes SET total = MAX(total + ?, 0), unread = MAX(unread + ?, 0) WHERE id = ?`,
			adj.total, adj.unread, adj.boxID); err != nil {
			return fmt.Errorf("adjust counts for box %s: %w", adj.boxID, err)
		}
	}

	return tx.Commit()
}

// Conversations lists cached threads for a box, newest first.
func (s *Store) Conversations(ctx context.Context, opts mail.ListOptions) ([]mail.Conversation, error) {
	var (
		where []string
		args  []any
	)

	query := `SELECT c.id, c.subject, c.senders, c.recipients, c.num_messages,
	                 c.num_unread, c.num_attachments, c.time, c.size,
	                 c.category_id, c.sort_order, c.snippet
	          FROM conversations c`

	if opts.BoxID != "" {
		query += ` JOIN conversation_boxes cb ON cb.conversation_id = c.id`
		where = append(where, `cb.box_id = ?`)
		args = append(args, opts.BoxID)
	}

	if opts.UnreadOnly {
		where = append(where, `c.num_unread > 0`)
	}

	if opts.Search != "" {
		// % and _ are LIKE wildcards; a search for them should match them.
		where = append(where, `c.subject LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(opts.Search)+"%")
	}

	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}

	// sort_order and id break ties between threads sharing a timestamp, so
	// paging never shows a row twice or skips one.
	if opts.Desc {
		query += ` ORDER BY c.time DESC, c.sort_order DESC, c.id DESC`
	} else {
		query += ` ORDER BY c.time ASC, c.sort_order ASC, c.id ASC`
	}

	switch {
	case opts.Limit > 0:
		query += ` LIMIT ?`
		args = append(args, opts.Limit)

		if opts.Offset > 0 {
			query += ` OFFSET ?`
			args = append(args, opts.Offset)
		}
	case opts.Offset > 0:
		// SQLite requires a LIMIT before OFFSET; -1 means unbounded.
		query += ` LIMIT -1 OFFSET ?`
		args = append(args, opts.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()

	var out []mail.Conversation

	for rows.Next() {
		c, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, c)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Box membership is only loaded for the rows actually returned, and in
	// one query rather than one per row.
	ids := make([]string, len(out))
	for i := range out {
		ids[i] = out[i].ID
	}

	byConv, err := s.conversationBoxesBatch(ctx, ids)
	if err != nil {
		return nil, err
	}

	for i := range out {
		out[i].BoxIDs = byConv[out[i].ID]
	}

	return out, nil
}

// escapeLike neutralises LIKE wildcards in user input, paired with ESCAPE '\'.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

	return r.Replace(s)
}

type scanner interface {
	Scan(dest ...any) error
}

func scanConversation(row scanner) (mail.Conversation, error) {
	var (
		c                   mail.Conversation
		senders, recipients string
		unix                int64
	)

	if err := row.Scan(&c.ID, &c.Subject, &senders, &recipients, &c.NumMessages,
		&c.NumUnread, &c.NumAttachments, &unix, &c.Size, &c.CategoryID, &c.Order,
		&c.Snippet); err != nil {
		return mail.Conversation{}, err
	}

	if err := json.Unmarshal([]byte(senders), &c.Senders); err != nil {
		return mail.Conversation{}, fmt.Errorf("decode senders for %s: %w", c.ID, err)
	}

	if err := json.Unmarshal([]byte(recipients), &c.Recipients); err != nil {
		return mail.Conversation{}, fmt.Errorf("decode recipients for %s: %w", c.ID, err)
	}

	c.Time = time.Unix(unix, 0)

	return c, nil
}

// conversationBoxesBatch loads box membership for many conversations at once.
// The IN list is chunked to stay under SQLite's bound-parameter limit.
func (s *Store) conversationBoxesBatch(ctx context.Context, ids []string) (map[string][]string, error) {
	const chunk = 500

	out := make(map[string][]string, len(ids))

	for len(ids) > 0 {
		n := len(ids)
		if n > chunk {
			n = chunk
		}

		batch := ids[:n]
		ids = ids[n:]

		args := make([]any, n)
		for i, id := range batch {
			args[i] = id
		}

		query := `SELECT conversation_id, box_id FROM conversation_boxes WHERE conversation_id IN (?` +
			strings.Repeat(`, ?`, n-1) + `)`

		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("list box membership: %w", err)
		}

		for rows.Next() {
			var conv, box string
			if err := rows.Scan(&conv, &box); err != nil {
				rows.Close()

				return nil, err
			}

			out[conv] = append(out[conv], box)
		}

		if err := rows.Err(); err != nil {
			rows.Close()

			return nil, err
		}

		rows.Close()
	}

	return out, nil
}

func (s *Store) conversationBoxes(ctx context.Context, id string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT box_id FROM conversation_boxes WHERE conversation_id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string

	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}

		out = append(out, b)
	}

	return out, rows.Err()
}

// Conversation returns one cached thread header.
func (s *Store) Conversation(ctx context.Context, id string) (mail.Conversation, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, subject, senders, recipients, num_messages, num_unread,
		        num_attachments, time, size, category_id, sort_order, snippet
		 FROM conversations WHERE id = ?`, id)

	c, err := scanConversation(row)
	if err == sql.ErrNoRows {
		return mail.Conversation{}, mail.ErrNotFound
	}

	if err != nil {
		return mail.Conversation{}, err
	}

	c.BoxIDs, err = s.conversationBoxes(ctx, id)

	return c, err
}

// --- messages ---------------------------------------------------------------

// PutMessages upserts message headers.
func (s *Store) PutMessages(ctx context.Context, msgs []mail.Message) error {
	if len(msgs) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO messages
			(id, conversation_id, subject, from_addr, to_addrs, cc_addrs, bcc_addrs,
			 reply_to_addrs, time, size, unread, category_id, newsletter_id,
			 num_attachments, spam_score, is_draft, snoozed_until, external_id,
			 box_ids, sort_order)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			conversation_id = excluded.conversation_id,
			subject = excluded.subject,
			from_addr = excluded.from_addr,
			to_addrs = excluded.to_addrs,
			cc_addrs = excluded.cc_addrs,
			bcc_addrs = excluded.bcc_addrs,
			reply_to_addrs = excluded.reply_to_addrs,
			time = excluded.time,
			size = excluded.size,
			unread = excluded.unread,
			category_id = excluded.category_id,
			newsletter_id = excluded.newsletter_id,
			num_attachments = excluded.num_attachments,
			spam_score = excluded.spam_score,
			is_draft = excluded.is_draft,
			snoozed_until = excluded.snoozed_until,
			external_id = excluded.external_id,
			box_ids = excluded.box_ids,
			sort_order = excluded.sort_order`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, m := range msgs {
		from, _ := json.Marshal(m.From)
		to, _ := json.Marshal(m.To)
		cc, _ := json.Marshal(m.CC)
		bcc, _ := json.Marshal(m.BCC)
		replyTo, _ := json.Marshal(m.ReplyTo)
		boxes, _ := json.Marshal(m.BoxIDs)

		var snoozed *int64
		if m.SnoozedUntil != nil {
			u := m.SnoozedUntil.Unix()
			snoozed = &u
		}

		if _, err := stmt.ExecContext(ctx,
			m.ID, m.ConversationID, m.Subject, string(from), string(to), string(cc),
			string(bcc), string(replyTo), m.Time.Unix(), m.Size, boolInt(m.Unread),
			m.CategoryID, m.NewsletterID, m.NumAttachments, m.SpamScore,
			boolInt(m.IsDraft), snoozed, m.ExternalID, string(boxes), m.Order); err != nil {
			return fmt.Errorf("upsert message %s: %w", m.ID, err)
		}
	}

	return tx.Commit()
}

// Messages returns the cached headers of one thread, oldest first.
func (s *Store) Messages(ctx context.Context, conversationID string) ([]mail.Message, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, conversation_id, subject, from_addr, to_addrs, cc_addrs, bcc_addrs,
		       reply_to_addrs, time, size, unread, category_id, newsletter_id,
		       num_attachments, spam_score, is_draft, snoozed_until, external_id,
		       box_ids, sort_order
		FROM messages WHERE conversation_id = ? ORDER BY time ASC`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	var out []mail.Message

	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, m)
	}

	return out, rows.Err()
}

// Message returns one cached header.
func (s *Store) Message(ctx context.Context, id string) (mail.Message, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, conversation_id, subject, from_addr, to_addrs, cc_addrs, bcc_addrs,
		       reply_to_addrs, time, size, unread, category_id, newsletter_id,
		       num_attachments, spam_score, is_draft, snoozed_until, external_id,
		       box_ids, sort_order
		FROM messages WHERE id = ?`, id)

	m, err := scanMessage(row)
	if err == sql.ErrNoRows {
		return mail.Message{}, mail.ErrNotFound
	}

	return m, err
}

func scanMessage(row scanner) (mail.Message, error) {
	var (
		m                                 mail.Message
		from, to, cc, bcc, replyTo, boxes string
		unix                              int64
		unread, isDraft                   int
		snoozed                           *int64
	)

	if err := row.Scan(&m.ID, &m.ConversationID, &m.Subject, &from, &to, &cc, &bcc,
		&replyTo, &unix, &m.Size, &unread, &m.CategoryID, &m.NewsletterID,
		&m.NumAttachments, &m.SpamScore, &isDraft, &snoozed, &m.ExternalID,
		&boxes, &m.Order); err != nil {
		return mail.Message{}, err
	}

	for _, pair := range []struct {
		raw  string
		dest any
	}{
		{from, &m.From}, {to, &m.To}, {cc, &m.CC},
		{bcc, &m.BCC}, {replyTo, &m.ReplyTo}, {boxes, &m.BoxIDs},
	} {
		if pair.raw == "" {
			continue
		}

		if err := json.Unmarshal([]byte(pair.raw), pair.dest); err != nil {
			return mail.Message{}, fmt.Errorf("decode addresses for %s: %w", m.ID, err)
		}
	}

	m.Time = time.Unix(unix, 0)
	m.Unread = unread != 0
	m.IsDraft = isDraft != 0

	if snoozed != nil {
		t := time.Unix(*snoozed, 0)
		m.SnoozedUntil = &t
	}

	return m, nil
}

// --- bodies -----------------------------------------------------------------

// Body returns a cached body and bumps its access time. ErrNotFound means the
// caller should fetch it from the provider and call PutBody.
func (s *Store) Body(ctx context.Context, messageID string) (mail.Body, error) {
	var (
		b    mail.Body
		atts string
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT message_id, mime_type, content, attachments FROM bodies WHERE message_id = ?`,
		messageID).Scan(&b.MessageID, &b.MIMEType, &b.Content, &atts)
	if err == sql.ErrNoRows {
		return mail.Body{}, mail.ErrNotFound
	}

	if err != nil {
		return mail.Body{}, fmt.Errorf("read body %s: %w", messageID, err)
	}

	if atts != "" {
		if err := json.Unmarshal([]byte(atts), &b.Attachments); err != nil {
			return mail.Body{}, err
		}
	}

	if _, err := s.db.ExecContext(ctx,
		`UPDATE bodies SET accessed_at = ? WHERE message_id = ?`,
		time.Now().Unix(), messageID); err != nil {
		return mail.Body{}, err
	}

	return b, nil
}

// PutBody caches a decrypted body.
func (s *Store) PutBody(ctx context.Context, b mail.Body) error {
	atts, err := json.Marshal(b.Attachments)
	if err != nil {
		return err
	}

	now := time.Now().Unix()

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO bodies (message_id, mime_type, content, attachments, fetched_at, accessed_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(message_id) DO UPDATE SET
			mime_type = excluded.mime_type,
			content = excluded.content,
			attachments = excluded.attachments,
			fetched_at = excluded.fetched_at,
			accessed_at = excluded.accessed_at`,
		b.MessageID, b.MIMEType, b.Content, string(atts), now, now)
	if err != nil {
		return fmt.Errorf("cache body %s: %w", b.MessageID, err)
	}

	return nil
}

// EvictBodies drops cached bodies beyond the newest keep, by access time. This
// is what keeps the warm cache from becoming a full mirror.
func (s *Store) EvictBodies(ctx context.Context, keep int) (int, error) {
	if keep < 0 {
		return 0, nil
	}

	res, err := s.db.ExecContext(ctx, `
		DELETE FROM bodies WHERE message_id IN (
			SELECT message_id FROM bodies ORDER BY accessed_at DESC LIMIT -1 OFFSET ?
		)`, keep)
	if err != nil {
		return 0, fmt.Errorf("evict bodies: %w", err)
	}

	n, err := res.RowsAffected()

	return int(n), err
}

// --- newsletters ------------------------------------------------------------

// PutNewsletters replaces the cached newsletter list.
func (s *Store) PutNewsletters(ctx context.Context, ns []mail.Newsletter) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM newsletters`); err != nil {
		return err
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO newsletters
			(id, list_id, name, sender_name, sender_address, received_total,
			 received_30d, received_90d, unread, trackers, first_received,
			 last_received, last_read, unsubscribed, spam, can_unsub,
			 mark_as_read, move_to_box_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, n := range ns {
		var lastRead *int64
		if n.LastRead != nil {
			u := n.LastRead.Unix()
			lastRead = &u
		}

		if _, err := stmt.ExecContext(ctx,
			n.ID, n.ListID, n.Name, n.Sender.Name, n.Sender.Address,
			n.ReceivedTotal, n.ReceivedLast30Days, n.ReceivedLast90Days,
			n.Unread, n.Trackers, n.FirstReceived.Unix(), n.LastReceived.Unix(),
			lastRead, boolInt(n.Unsubscribed), boolInt(n.Spam),
			boolInt(n.CanUnsubscribe), boolInt(n.MarkAsRead), n.MoveToBoxID); err != nil {
			return fmt.Errorf("insert newsletter %s: %w", n.ID, err)
		}
	}

	return tx.Commit()
}

// Newsletters returns cached mailing lists, most recently received first.
func (s *Store) Newsletters(ctx context.Context) ([]mail.Newsletter, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, list_id, name, sender_name, sender_address, received_total,
		       received_30d, received_90d, unread, trackers, first_received,
		       last_received, last_read, unsubscribed, spam, can_unsub,
		       mark_as_read, move_to_box_id
		FROM newsletters ORDER BY last_received DESC`)
	if err != nil {
		return nil, fmt.Errorf("list newsletters: %w", err)
	}
	defer rows.Close()

	var out []mail.Newsletter

	for rows.Next() {
		var (
			n                               mail.Newsletter
			first, last                     int64
			lastRead                        *int64
			unsub, spam, canUnsub, markRead int
		)

		if err := rows.Scan(&n.ID, &n.ListID, &n.Name, &n.Sender.Name, &n.Sender.Address,
			&n.ReceivedTotal, &n.ReceivedLast30Days, &n.ReceivedLast90Days,
			&n.Unread, &n.Trackers, &first, &last, &lastRead,
			&unsub, &spam, &canUnsub, &markRead, &n.MoveToBoxID); err != nil {
			return nil, err
		}

		n.FirstReceived = time.Unix(first, 0)
		n.LastReceived = time.Unix(last, 0)
		n.Unsubscribed = unsub != 0
		n.Spam = spam != 0
		n.CanUnsubscribe = canUnsub != 0
		n.MarkAsRead = markRead != 0

		if lastRead != nil {
			t := time.Unix(*lastRead, 0)
			n.LastRead = &t
		}

		out = append(out, n)
	}

	return out, rows.Err()
}

func boolInt(b bool) int {
	if b {
		return 1
	}

	return 0
}

// PendingSenders counts senders awaiting a screener decision. The TUI shows it
// in a banner, so it needs to be one cheap query rather than a full listing.
func (s *Store) PendingSenders(ctx context.Context) (int, error) {
	var n int

	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM senders WHERE decision = 'pending'`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count pending senders: %w", err)
	}

	return n, nil
}
