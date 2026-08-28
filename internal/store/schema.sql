-- Warm cache, not a mirror.
--
-- Headers and conversation metadata are kept eagerly so the TUI can render
-- without touching the network. Bodies are fetched lazily and stored here only
-- once read, with an eviction pass, so the file does not grow into a full copy
-- of the mailbox.

PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS boxes (
    id     TEXT PRIMARY KEY,
    name   TEXT NOT NULL,
    path   TEXT NOT NULL DEFAULT '',
    kind   TEXT NOT NULL,
    color  TEXT NOT NULL DEFAULT '',
    total  INTEGER NOT NULL DEFAULT 0,
    unread INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS conversations (
    id              TEXT PRIMARY KEY,
    subject         TEXT NOT NULL DEFAULT '',
    senders         TEXT NOT NULL DEFAULT '[]',
    recipients      TEXT NOT NULL DEFAULT '[]',
    num_messages    INTEGER NOT NULL DEFAULT 0,
    num_unread      INTEGER NOT NULL DEFAULT 0,
    num_attachments INTEGER NOT NULL DEFAULT 0,
    time            INTEGER NOT NULL DEFAULT 0,
    size            INTEGER NOT NULL DEFAULT 0,
    category_id     TEXT NOT NULL DEFAULT '',
    sort_order      INTEGER NOT NULL DEFAULT 0,
    -- Proton returns no excerpt with conversation metadata, so the preview
    -- line is derived from a decrypted body and kept here. Empty means "not
    -- fetched yet", which is what the background prefetch looks for.
    snippet         TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_conversations_time ON conversations(time DESC);

-- Box membership is many-to-many and changes constantly, so it gets its own
-- table rather than a JSON column that would have to be rewritten wholesale.
CREATE TABLE IF NOT EXISTS conversation_boxes (
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    box_id          TEXT NOT NULL,
    PRIMARY KEY (conversation_id, box_id)
);

CREATE INDEX IF NOT EXISTS idx_conversation_boxes_box ON conversation_boxes(box_id);

CREATE TABLE IF NOT EXISTS messages (
    id              TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL DEFAULT '',
    subject         TEXT NOT NULL DEFAULT '',
    from_addr       TEXT NOT NULL DEFAULT '{}',
    to_addrs        TEXT NOT NULL DEFAULT '[]',
    cc_addrs        TEXT NOT NULL DEFAULT '[]',
    bcc_addrs       TEXT NOT NULL DEFAULT '[]',
    reply_to_addrs  TEXT NOT NULL DEFAULT '[]',
    time            INTEGER NOT NULL DEFAULT 0,
    size            INTEGER NOT NULL DEFAULT 0,
    unread          INTEGER NOT NULL DEFAULT 0,
    category_id     TEXT NOT NULL DEFAULT '',
    newsletter_id   TEXT NOT NULL DEFAULT '',
    num_attachments INTEGER NOT NULL DEFAULT 0,
    spam_score      INTEGER NOT NULL DEFAULT 0,
    is_draft        INTEGER NOT NULL DEFAULT 0,
    snoozed_until   INTEGER,
    external_id     TEXT NOT NULL DEFAULT '',
    box_ids         TEXT NOT NULL DEFAULT '[]',
    sort_order      INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_messages_conversation ON messages(conversation_id, time);
CREATE INDEX IF NOT EXISTS idx_messages_time ON messages(time DESC);
CREATE INDEX IF NOT EXISTS idx_messages_newsletter ON messages(newsletter_id);

-- Bodies are the lazy half of the cache. accessed_at drives eviction.
CREATE TABLE IF NOT EXISTS bodies (
    message_id  TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
    mime_type   TEXT NOT NULL DEFAULT '',
    content     TEXT NOT NULL DEFAULT '',
    attachments TEXT NOT NULL DEFAULT '[]',
    fetched_at  INTEGER NOT NULL DEFAULT 0,
    accessed_at INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_bodies_accessed ON bodies(accessed_at);

CREATE TABLE IF NOT EXISTS newsletters (
    id             TEXT PRIMARY KEY,
    list_id        TEXT NOT NULL DEFAULT '',
    name           TEXT NOT NULL DEFAULT '',
    sender_name    TEXT NOT NULL DEFAULT '',
    sender_address TEXT NOT NULL DEFAULT '',
    received_total INTEGER NOT NULL DEFAULT 0,
    received_30d   INTEGER NOT NULL DEFAULT 0,
    received_90d   INTEGER NOT NULL DEFAULT 0,
    unread         INTEGER NOT NULL DEFAULT 0,
    trackers       INTEGER NOT NULL DEFAULT 0,
    first_received INTEGER NOT NULL DEFAULT 0,
    last_received  INTEGER NOT NULL DEFAULT 0,
    last_read      INTEGER,
    unsubscribed   INTEGER NOT NULL DEFAULT 0,
    spam           INTEGER NOT NULL DEFAULT 0,
    can_unsub      INTEGER NOT NULL DEFAULT 0,
    mark_as_read   INTEGER NOT NULL DEFAULT 0,
    move_to_box_id TEXT NOT NULL DEFAULT ''
);

-- The screener's own state: one row per sender address, holding the decision
-- the user made. This is local; the *effect* of a decision is written to the
-- provider as a real label so it follows the user to other clients.
CREATE TABLE IF NOT EXISTS senders (
    address     TEXT PRIMARY KEY,
    name        TEXT NOT NULL DEFAULT '',
    decision    TEXT NOT NULL DEFAULT 'pending',
    decided_at  INTEGER,
    first_seen  INTEGER NOT NULL DEFAULT 0,
    last_seen   INTEGER NOT NULL DEFAULT 0,
    message_count INTEGER NOT NULL DEFAULT 0,
    newsletter_id TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_senders_decision ON senders(decision);

-- Phase 5 domains. Local only; Proton has no equivalent.
CREATE TABLE IF NOT EXISTS habits (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    name     TEXT NOT NULL UNIQUE,
    created  INTEGER NOT NULL DEFAULT 0,
    archived INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS habit_entries (
    habit_id INTEGER NOT NULL REFERENCES habits(id) ON DELETE CASCADE,
    day      TEXT NOT NULL,
    done     INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (habit_id, day)
);

CREATE TABLE IF NOT EXISTS time_entries (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    project  TEXT NOT NULL DEFAULT '',
    note     TEXT NOT NULL DEFAULT '',
    started  INTEGER NOT NULL,
    stopped  INTEGER
);

CREATE INDEX IF NOT EXISTS idx_time_entries_started ON time_entries(started DESC);

-- Journal entries live as markdown on disk; this table is only the index.
CREATE TABLE IF NOT EXISTS journal (
    day     TEXT PRIMARY KEY,
    path    TEXT NOT NULL,
    title   TEXT NOT NULL DEFAULT '',
    updated INTEGER NOT NULL DEFAULT 0
);
