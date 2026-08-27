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

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate cache: %w", err)
	}

	return &Store{db: db}, nil
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

// DeleteConversations removes conversations and, by cascade, their box rows.
func (s *Store) DeleteConversations(ctx context.Context, ids []string) error {
	return s.deleteByID(ctx, "conversations", ids)
}

// DeleteMessages removes messages and, by cascade, their cached bodies.
func (s *Store) DeleteMessages(ctx context.Context, ids []string) error {
	return s.deleteByID(ctx, "messages", ids)
}

func (s *Store) deleteByID(ctx context.Context, table string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `DELETE FROM `+table+` WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, id := range ids {
		if _, err := stmt.ExecContext(ctx, id); err != nil {
			return fmt.Errorf("delete from %s: %w", table, err)
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
	                 c.category_id, c.sort_order
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
		where = append(where, `c.subject LIKE ?`)
		args = append(args, "%"+opts.Search+"%")
	}

	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}

	query += ` ORDER BY c.time DESC`

	if opts.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, opts.Limit)

		if opts.Offset > 0 {
			query += ` OFFSET ?`
			args = append(args, opts.Offset)
		}
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

	// Box membership is only loaded for the rows actually returned.
	for i := range out {
		ids, err := s.conversationBoxes(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}

		out[i].BoxIDs = ids
	}

	return out, nil
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
		&c.NumUnread, &c.NumAttachments, &unix, &c.Size, &c.CategoryID, &c.Order); err != nil {
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
		        num_attachments, time, size, category_id, sort_order
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
