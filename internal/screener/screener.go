// Package screener is the HEY layer: first-time senders are quarantined until
// the user says yes or no, and accepted mail is routed to Imbox, Feed or
// Paper Trail.
//
// The decision itself is local, but its effect is written to the provider as a
// real label, so the routing follows the user to their phone and to Proton's
// web client. Where the provider offers server-side routing (Proton does, for
// newsletters) that is used in preference, because it keeps working with this
// tool shut down.
package screener

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mhrsntrk/frankenstein-cli/internal/config"
	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
	"github.com/mhrsntrk/frankenstein-cli/internal/protonapi"
	"github.com/mhrsntrk/frankenstein-cli/internal/store"
)

// Decision is what the user said about a sender.
type Decision string

const (
	// Pending means the sender is in the screener awaiting a decision.
	Pending Decision = "pending"

	// Imbox is mail the user wants to see: real correspondence.
	Imbox Decision = "imbox"

	// Feed is mail to read at leisure: newsletters, announcements.
	Feed Decision = "feed"

	// PaperTrail is mail to keep but never read: receipts, confirmations.
	PaperTrail Decision = "paper_trail"

	// ScreenedOut is mail the user never wants to see again.
	ScreenedOut Decision = "screened_out"
)

// Valid reports whether d is a decision the screener understands.
func (d Decision) Valid() bool {
	switch d {
	case Pending, Imbox, Feed, PaperTrail, ScreenedOut:
		return true
	default:
		return false
	}
}

// BoxNames are the labels created on the provider, one per routed decision.
const (
	BoxImbox       = "Imbox"
	BoxFeed        = "Feed"
	BoxPaperTrail  = "Paper Trail"
	BoxScreenedOut = "Screened Out"
)

// Sender is one address the screener knows about.
type Sender struct {
	Address      string     `json:"address"`
	Name         string     `json:"name,omitempty"`
	Decision     Decision   `json:"decision"`
	DecidedAt    *time.Time `json:"decided_at,omitempty"`
	FirstSeen    time.Time  `json:"first_seen"`
	LastSeen     time.Time  `json:"last_seen"`
	MessageCount int        `json:"message_count"`
	NewsletterID string     `json:"newsletter_id,omitempty"`
}

// Screener applies decisions to a provider and records them in the cache.
type Screener struct {
	store    *store.Store
	provider mail.Provider
	cfg      config.ScreenerConfig
}

// New returns a Screener.
func New(s *store.Store, p mail.Provider, cfg config.ScreenerConfig) *Screener {
	return &Screener{store: s, provider: p, cfg: cfg}
}

// Proton only accepts colours from a fixed palette and answers anything else
// with 422 Code 2001 "Invalid color". These four are confirmed accepted; they
// are used as a fallback when the account has no existing labels to borrow
// from.
var fallbackPalette = []string{
	"#8080FF", // Imbox, Proton's own default
	"#F78400", // Feed
	"#a839a4", // Paper Trail
	"#ba1e55", // Screened Out
}

// Setup creates the four boxes on the provider and returns the config that
// records their IDs. Existing boxes with the right names are reused, so this
// is safe to run twice.
func Setup(ctx context.Context, p mail.Provider) (config.ScreenerConfig, error) {
	existing, err := p.Boxes(ctx)
	if err != nil {
		return config.ScreenerConfig{}, err
	}

	byName := make(map[string]string, len(existing))
	for _, b := range existing {
		byName[strings.ToLower(b.Name)] = b.ID
	}

	palette := paletteFrom(existing)

	var cfg config.ScreenerConfig

	wanted := []struct {
		name string
		dest *string
	}{
		{BoxImbox, &cfg.ImboxID},
		{BoxFeed, &cfg.FeedID},
		{BoxPaperTrail, &cfg.PaperTrailID},
		{BoxScreenedOut, &cfg.ScreenedOutID},
	}

	for i, w := range wanted {
		if id, ok := byName[strings.ToLower(w.name)]; ok {
			*w.dest = id

			continue
		}

		box, err := p.CreateBox(ctx, w.name, mail.BoxLabel, palette[i%len(palette)])
		if err != nil {
			return cfg, fmt.Errorf("create %q: %w", w.name, err)
		}

		*w.dest = box.ID
	}

	cfg.Enabled = true

	return cfg, nil
}

// paletteFrom returns colours to create the screener boxes with.
//
// The confirmed palette comes first so the four boxes get sensible colours in
// a sensible order. Colours already in use on the account are appended as
// extras: whatever the server accepted before is by definition inside its
// palette, so they are a safe backstop if the confirmed list ever stops being.
func paletteFrom(boxes []mail.Box) []string {
	seen := make(map[string]bool, len(boxes))

	out := make([]string, 0, len(fallbackPalette)+len(boxes))

	for _, c := range fallbackPalette {
		seen[c] = true
		out = append(out, c)
	}

	for _, b := range boxes {
		// System boxes all share one colour, so only user-made boxes add
		// anything.
		if b.Kind == mail.BoxSystem || b.Color == "" || seen[b.Color] {
			continue
		}

		seen[b.Color] = true
		out = append(out, b.Color)
	}

	return out
}

// BoxIDFor maps a decision to the provider box it routes into. Pending and
// unknown decisions have no box.
func (s *Screener) BoxIDFor(d Decision) string {
	switch d {
	case Imbox:
		return s.cfg.ImboxID
	case Feed:
		return s.cfg.FeedID
	case PaperTrail:
		return s.cfg.PaperTrailID
	case ScreenedOut:
		return s.cfg.ScreenedOutID
	default:
		return ""
	}
}

// Observe records senders seen in the cache, creating pending entries for ones
// the screener has not met. It does not route anything; that is Decide's job.
//
// Senders come from conversations first and messages second. That matters: the
// cache is warm, not a mirror, so message rows only exist for threads that have
// been opened. Reading messages alone would leave the screener empty after a
// fresh sync, which is exactly when it is most useful.
//
// The account's own addresses are excluded, or every thread in Sent would put
// the user into their own screener.
func (s *Screener) Observe(ctx context.Context) (int, error) {
	own := make(map[string]bool)

	if addrs, err := s.provider.Addresses(ctx); err == nil {
		for _, a := range addrs {
			own[strings.ToLower(a.Address)] = true
		}
	}

	rows, err := s.store.DB().QueryContext(ctx, `
		SELECT addr,
		       MAX(name)          AS name,
		       MIN(t)             AS first_seen,
		       MAX(t)             AS last_seen,
		       SUM(n)             AS messages,
		       COALESCE(MAX(NULLIF(nl, '')), '') AS newsletter_id
		FROM (
			SELECT lower(json_extract(senders, '$[0].address'))    AS addr,
			       COALESCE(json_extract(senders, '$[0].name'), '') AS name,
			       time                                             AS t,
			       num_messages                                     AS n,
			       ''                                               AS nl
			FROM conversations
			WHERE json_extract(senders, '$[0].address') IS NOT NULL

			UNION ALL

			SELECT lower(json_extract(from_addr, '$.address')),
			       COALESCE(json_extract(from_addr, '$.name'), ''),
			       time,
			       1,
			       newsletter_id
			FROM messages
			WHERE is_draft = 0 AND json_extract(from_addr, '$.address') IS NOT NULL
		)
		WHERE addr IS NOT NULL AND addr <> ''
		GROUP BY addr`)
	if err != nil {
		return 0, fmt.Errorf("scan senders: %w", err)
	}
	defer rows.Close()

	type row struct {
		addr, name, newsletter string
		first, last            int64
		count                  int
	}

	var seen []row

	for rows.Next() {
		var (
			r    row
			name sql.NullString
		)

		if err := rows.Scan(&r.addr, &name, &r.first, &r.last, &r.count, &r.newsletter); err != nil {
			return 0, err
		}

		if own[r.addr] {
			continue
		}

		r.name = name.String
		seen = append(seen, r)
	}

	if err := rows.Err(); err != nil {
		return 0, err
	}

	tx, err := s.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// Only the counters move on an existing row: a decision already made must
	// never be reset by new mail arriving.
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO senders (address, name, decision, first_seen, last_seen, message_count, newsletter_id)
		VALUES (?, ?, 'pending', ?, ?, ?, ?)
		ON CONFLICT(address) DO UPDATE SET
			name = CASE WHEN excluded.name <> '' THEN excluded.name ELSE senders.name END,
			first_seen = MIN(senders.first_seen, excluded.first_seen),
			last_seen = MAX(senders.last_seen, excluded.last_seen),
			message_count = excluded.message_count,
			newsletter_id = CASE WHEN excluded.newsletter_id <> '' THEN excluded.newsletter_id ELSE senders.newsletter_id END`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	for _, r := range seen {
		if _, err := stmt.ExecContext(ctx, r.addr, r.name, r.first, r.last, r.count, r.newsletter); err != nil {
			return 0, fmt.Errorf("record sender %s: %w", r.addr, err)
		}
	}

	// Conversations carry no newsletter link, so fill it in from the cached
	// subscriptions by sender address. Without this the Feed suggestion would
	// only work for threads that had already been opened.
	if _, err := tx.ExecContext(ctx, `
		UPDATE senders
		SET newsletter_id = (
			SELECT n.id FROM newsletters n
			WHERE lower(n.sender_address) = senders.address
		)
		WHERE newsletter_id = ''
		  AND EXISTS (
			SELECT 1 FROM newsletters n WHERE lower(n.sender_address) = senders.address
		  )`); err != nil {
		return 0, fmt.Errorf("link newsletters: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	return len(seen), nil
}

// Pending lists senders awaiting a decision, most recent first.
func (s *Screener) Pending(ctx context.Context, limit int) ([]Sender, error) {
	return s.list(ctx, string(Pending), limit)
}

// List returns senders with a given decision. An empty decision returns all.
func (s *Screener) List(ctx context.Context, d Decision, limit int) ([]Sender, error) {
	return s.list(ctx, string(d), limit)
}

func (s *Screener) list(ctx context.Context, decision string, limit int) ([]Sender, error) {
	query := `SELECT address, name, decision, decided_at, first_seen, last_seen,
	                 message_count, newsletter_id
	          FROM senders`

	var args []any

	if decision != "" {
		query += ` WHERE decision = ?`
		args = append(args, decision)
	}

	query += ` ORDER BY last_seen DESC`

	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.store.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list senders: %w", err)
	}
	defer rows.Close()

	var out []Sender

	for rows.Next() {
		var (
			sn          Sender
			decided     *int64
			first, last int64
			dec         string
		)

		if err := rows.Scan(&sn.Address, &sn.Name, &dec, &decided, &first, &last,
			&sn.MessageCount, &sn.NewsletterID); err != nil {
			return nil, err
		}

		sn.Decision = Decision(dec)
		sn.FirstSeen = time.Unix(first, 0)
		sn.LastSeen = time.Unix(last, 0)

		if decided != nil {
			t := time.Unix(*decided, 0)
			sn.DecidedAt = &t
		}

		out = append(out, sn)
	}

	return out, rows.Err()
}

// Decide records a decision for a sender and applies it to their existing
// mail. Returns the number of conversations relabelled.
func (s *Screener) Decide(ctx context.Context, address string, d Decision) (int, error) {
	if !d.Valid() {
		return 0, fmt.Errorf("unknown decision %q", d)
	}

	address = strings.ToLower(strings.TrimSpace(address))
	if address == "" {
		return 0, fmt.Errorf("empty sender address")
	}

	now := time.Now().Unix()

	res, err := s.store.DB().ExecContext(ctx,
		`UPDATE senders SET decision = ?, decided_at = ? WHERE lower(address) = ?`,
		string(d), now, address)
	if err != nil {
		return 0, fmt.Errorf("record decision: %w", err)
	}

	if n, _ := res.RowsAffected(); n == 0 {
		// A decision can be made for a sender we have not seen yet, e.g. from
		// a rule the user is setting up in advance.
		if _, err := s.store.DB().ExecContext(ctx,
			`INSERT INTO senders (address, decision, decided_at, first_seen, last_seen)
			 VALUES (?, ?, ?, ?, ?)`, address, string(d), now, now, now); err != nil {
			return 0, fmt.Errorf("record decision: %w", err)
		}
	}

	if !s.cfg.Enabled || !s.cfg.Configured() {
		return 0, nil
	}

	return s.applyToSender(ctx, address, d)
}

// applyToSender relabels every cached conversation from an address.
//
// Conversations are matched directly as well as through messages: the cache is
// warm, not a mirror, so message rows exist only for threads that have been
// opened. Matching on messages alone would silently label nothing on a freshly
// synced mailbox.
func (s *Screener) applyToSender(ctx context.Context, address string, d Decision) (int, error) {
	boxID := s.BoxIDFor(d)
	if boxID == "" {
		return 0, nil
	}

	rows, err := s.store.DB().QueryContext(ctx, `
		SELECT DISTINCT id FROM (
			SELECT id FROM conversations
			WHERE lower(json_extract(senders, '$[0].address')) = ?

			UNION

			SELECT conversation_id AS id FROM messages
			WHERE lower(json_extract(from_addr, '$.address')) = ?
			  AND conversation_id <> ''
		)
		WHERE id IS NOT NULL AND id <> ''`, address, address)
	if err != nil {
		return 0, fmt.Errorf("find conversations for %s: %w", address, err)
	}
	defer rows.Close()

	var ids []string

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}

		ids = append(ids, id)
	}

	if err := rows.Err(); err != nil {
		return 0, err
	}

	if len(ids) == 0 {
		return 0, nil
	}

	if err := s.provider.Label(ctx, ids, boxID); err != nil {
		return 0, fmt.Errorf("apply %s label: %w", d, err)
	}

	// Remove the other screener labels so a re-decision does not leave a
	// conversation in two boxes at once.
	for _, other := range []Decision{Imbox, Feed, PaperTrail, ScreenedOut} {
		if other == d {
			continue
		}

		if id := s.BoxIDFor(other); id != "" {
			if err := s.provider.Unlabel(ctx, ids, id); err != nil {
				return len(ids), fmt.Errorf("clear %s label: %w", other, err)
			}
		}
	}

	return len(ids), nil
}

// Suggest proposes a decision for a sender that has none, using the signals
// the provider already computes. This is a suggestion, never applied on its
// own: the point of a screener is that a person decides.
func (s *Screener) Suggest(ctx context.Context, sn Sender) (Decision, string) {
	// A tracked mailing list is a Feed item by definition.
	if sn.NewsletterID != "" {
		return Feed, "Proton tracks this sender as a mailing list"
	}

	// Proton's own classification is the next best signal. Nothing here maps to
	// Paper Trail: no observed category corresponds to receipts, and guessing
	// one would file real correspondence somewhere the user never reads.
	category, err := s.senderCategory(ctx, sn.Address)
	if err == nil {
		switch category {
		case protonapi.CategoryDefaultLabel:
			return Imbox, "Proton files this sender under primary mail"
		case protonapi.CategoryPromotionsLabel:
			return Feed, "Proton categorises this sender as promotions"
		case protonapi.CategoryNewslettersLabel:
			return Feed, "Proton categorises this sender as a newsletter"
		case protonapi.CategoryUpdatesLabel:
			return Feed, "Proton categorises this sender as updates"
		case protonapi.CategorySocialLabel:
			return Feed, "Proton categorises this sender as social"
		case protonapi.CategoryForumsLabel:
			return Feed, "Proton categorises this sender as a forum or mailing list"
		}
	}

	return Pending, "no signal; decide by hand"
}

// senderCategory returns the category the provider most often assigns to a
// sender's mail.
//
// Like Observe, this reads conversations as well as messages: on a warm cache
// the message rows may not exist yet, and a suggestion that only worked after
// opening a thread would be useless.
func (s *Screener) senderCategory(ctx context.Context, address string) (string, error) {
	addr := strings.ToLower(address)

	rows, err := s.store.DB().QueryContext(ctx, `
		SELECT category_id, COUNT(*) AS n
		FROM (
			SELECT category_id FROM conversations
			WHERE lower(json_extract(senders, '$[0].address')) = ? AND category_id <> ''

			UNION ALL

			SELECT category_id FROM messages
			WHERE lower(json_extract(from_addr, '$.address')) = ? AND category_id <> ''
		)
		GROUP BY category_id ORDER BY n DESC LIMIT 1`, addr, addr)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	if !rows.Next() {
		return "", sql.ErrNoRows
	}

	var (
		cat string
		n   int
	)

	if err := rows.Scan(&cat, &n); err != nil {
		return "", err
	}

	return cat, nil
}

// RouteNewsletters pushes Feed and Paper Trail decisions for mailing lists
// down to the provider as server-side rules, so they keep applying with this
// tool shut down.
//
// Returns the lists routed. Providers without server-side routing return
// mail.ErrNotSupported, which is reported rather than treated as failure.
func (s *Screener) RouteNewsletters(ctx context.Context) ([]string, error) {
	if !s.cfg.Enabled || !s.cfg.Configured() {
		return nil, fmt.Errorf("screener is not set up; run `frankenstein screener setup`")
	}

	newsletters, err := s.store.Newsletters(ctx)
	if err != nil {
		return nil, err
	}

	byID := make(map[string]mail.Newsletter, len(newsletters))
	for _, n := range newsletters {
		byID[n.ID] = n
	}

	senders, err := s.List(ctx, "", 0)
	if err != nil {
		return nil, err
	}

	var routed []string

	for _, sn := range senders {
		if sn.NewsletterID == "" {
			continue
		}

		n, ok := byID[sn.NewsletterID]
		if !ok {
			continue
		}

		boxID := s.BoxIDFor(sn.Decision)
		if boxID == "" {
			continue
		}

		// Already routed where we want it.
		if n.MoveToBoxID == boxID {
			continue
		}

		// Paper Trail is read-and-forget, so mark it read on arrival.
		markRead := sn.Decision == PaperTrail

		if err := s.provider.RouteNewsletter(ctx, n.ID, boxID, markRead); err != nil {
			if err == mail.ErrNotSupported {
				return routed, err
			}

			return routed, fmt.Errorf("route %q: %w", n.Name, err)
		}

		routed = append(routed, n.Name)
	}

	sort.Strings(routed)

	return routed, nil
}

// Stats counts senders by decision.
func (s *Screener) Stats(ctx context.Context) (map[Decision]int, error) {
	rows, err := s.store.DB().QueryContext(ctx,
		`SELECT decision, COUNT(*) FROM senders GROUP BY decision`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[Decision]int)

	for rows.Next() {
		var (
			d string
			n int
		)

		if err := rows.Scan(&d, &n); err != nil {
			return nil, err
		}

		out[Decision(d)] = n
	}

	return out, rows.Err()
}
