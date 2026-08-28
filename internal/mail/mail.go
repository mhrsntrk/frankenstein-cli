// Package mail defines the provider-neutral mail model and the interface a
// backend must satisfy.
//
// Nothing outside internal/mail/... may import a Proton type. The TUI and the
// command layer speak only the types declared here, so a second backend (or a
// fake, for tests) is a matter of implementing Provider.
package mail

import (
	"context"
	"errors"
	"time"
)

// Errors a Provider may return that callers are expected to distinguish.
var (
	ErrNotFound               = errors.New("not found")
	ErrNotSupported           = errors.New("not supported by this provider")
	ErrNeedsHumanVerification = errors.New("human verification required")
)

// Box is a mailbox: a system folder, a user folder, a label, or one of the
// HEY-style boxes the screener maintains.
type Box struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	// Path is the display hierarchy, e.g. ["Work", "Clients"].
	Path []string `json:"path,omitempty"`

	Kind  BoxKind `json:"kind"`
	Color string  `json:"color,omitempty"`

	Total  int `json:"total"`
	Unread int `json:"unread"`
}

// BoxKind separates the boxes a user made from the ones the server or this
// tool maintains, because they behave differently on write.
type BoxKind string

const (
	BoxSystem   BoxKind = "system"
	BoxFolder   BoxKind = "folder"
	BoxLabel    BoxKind = "label"
	BoxCategory BoxKind = "category"
	BoxScreener BoxKind = "screener"
)

// Address is a participant. Provenance flags are kept because the screener
// uses them, but they are optional for any provider that lacks them.
type Address struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address"`

	IsProton      bool `json:"is_proton,omitempty"`
	IsSimpleLogin bool `json:"is_simple_login,omitempty"`
}

// String renders "Name <addr>", or the bare address when there is no name.
func (a Address) String() string {
	if a.Name == "" {
		return a.Address
	}

	return a.Name + " <" + a.Address + ">"
}

// Display is the shortest useful rendering for a list view.
func (a Address) Display() string {
	if a.Name != "" {
		return a.Name
	}

	return a.Address
}

// Conversation is a thread. Counts are scoped to the box it was listed under
// when the provider supports that, which is what a list view wants.
type Conversation struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`

	Senders    []Address `json:"senders,omitempty"`
	Recipients []Address `json:"recipients,omitempty"`

	NumMessages    int `json:"num_messages"`
	NumUnread      int `json:"num_unread"`
	NumAttachments int `json:"num_attachments"`

	Time time.Time `json:"time"`
	Size int64     `json:"size"`

	BoxIDs []string `json:"box_ids,omitempty"`

	// CategoryID is the provider's own server-side classification, empty when
	// the provider has none.
	CategoryID string `json:"category_id,omitempty"`

	// Snippet is the first line of the newest message, for a preview row. It is
	// empty until something has fetched and decrypted a body, which is why the
	// interface renders without it and fills it in later.
	Snippet string `json:"snippet,omitempty"`

	// Order is the provider's sort key, used for stable pagination.
	Order int64 `json:"-"`
}

// Unread reports whether anything in the thread is unread.
func (c Conversation) Unread() bool { return c.NumUnread > 0 }

// Message is one message's metadata. The body is fetched separately and
// deliberately: the cache holds headers eagerly and bodies lazily.
type Message struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversation_id"`

	Subject string    `json:"subject"`
	From    Address   `json:"from"`
	To      []Address `json:"to,omitempty"`
	CC      []Address `json:"cc,omitempty"`
	BCC     []Address `json:"bcc,omitempty"`
	ReplyTo []Address `json:"reply_to,omitempty"`

	Time   time.Time `json:"time"`
	Size   int64     `json:"size"`
	Unread bool      `json:"unread"`

	BoxIDs []string `json:"box_ids,omitempty"`

	CategoryID string `json:"category_id,omitempty"`

	// NewsletterID links the message to a tracked mailing list, empty when it
	// is not from one.
	NewsletterID string `json:"newsletter_id,omitempty"`

	NumAttachments int        `json:"num_attachments"`
	SpamScore      int        `json:"spam_score,omitempty"`
	IsDraft        bool       `json:"is_draft,omitempty"`
	SnoozedUntil   *time.Time `json:"snoozed_until,omitempty"`

	// ExternalID and References are the RFC 5322 threading headers, kept so a
	// provider without a native thread model can still be supported.
	ExternalID string   `json:"external_id,omitempty"`
	References []string `json:"references,omitempty"`

	Order int64 `json:"-"`
}

// Body is a decrypted message body.
type Body struct {
	MessageID string `json:"message_id"`
	MIMEType  string `json:"mime_type"`
	Content   string `json:"content"`

	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment describes a part without carrying its bytes.
type Attachment struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	MIMEType string `json:"mime_type"`
}

// Thread is a conversation together with its messages, newest last.
type Thread struct {
	Conversation Conversation `json:"conversation"`
	Messages     []Message    `json:"messages"`
}

// Newsletter is a tracked mailing list. Providers without the concept return
// ErrNotSupported from the newsletter methods.
type Newsletter struct {
	ID     string `json:"id"`
	ListID string `json:"list_id"`

	Name   string  `json:"name"`
	Sender Address `json:"sender"`

	ReceivedTotal      int `json:"received_total"`
	ReceivedLast30Days int `json:"received_last_30_days"`
	ReceivedLast90Days int `json:"received_last_90_days"`
	Unread             int `json:"unread"`
	Trackers           int `json:"trackers"`

	FirstReceived time.Time  `json:"first_received"`
	LastReceived  time.Time  `json:"last_received"`
	LastRead      *time.Time `json:"last_read,omitempty"`

	Unsubscribed   bool `json:"unsubscribed"`
	Spam           bool `json:"spam"`
	CanUnsubscribe bool `json:"can_unsubscribe"`

	// MarkAsRead and MoveToBoxID are server-side rules that keep working with
	// this tool shut down. MoveToBoxID is empty when no rule is set.
	MarkAsRead  bool   `json:"mark_as_read"`
	MoveToBoxID string `json:"move_to_box_id,omitempty"`
}

// ListOptions controls a conversation or message listing.
type ListOptions struct {
	BoxID  string
	Limit  int
	Offset int

	// Search matches the subject when the provider supports it.
	Search string

	// UnreadOnly restricts to threads with unread messages.
	UnreadOnly bool

	// Desc lists newest first. This is the default.
	Desc bool
}

// Draft is a message being composed. ID is empty for a new draft.
type Draft struct {
	ID string `json:"id,omitempty"`

	Subject string    `json:"subject"`
	To      []Address `json:"to"`
	CC      []Address `json:"cc,omitempty"`
	BCC     []Address `json:"bcc,omitempty"`

	Body     string `json:"body"`
	MIMEType string `json:"mime_type,omitempty"`

	// InReplyTo is the message being replied to, empty for a fresh compose.
	InReplyTo string `json:"in_reply_to,omitempty"`
}

// Delta is one incremental change from the provider's event stream.
type Delta struct {
	// Cursor is the provider's position after this delta. Persist it.
	Cursor string

	// Resync means the cursor was too old and the cache must be rebuilt.
	Resync bool

	Conversations []ConversationChange
	Messages      []MessageChange
	Boxes         []BoxChange
}

// ChangeKind is what happened to an item.
type ChangeKind string

const (
	ChangeCreate ChangeKind = "create"
	ChangeUpdate ChangeKind = "update"
	ChangeDelete ChangeKind = "delete"
)

type ConversationChange struct {
	Kind         ChangeKind
	ID           string
	Conversation Conversation
}

type MessageChange struct {
	Kind    ChangeKind
	ID      string
	Message Message
}

type BoxChange struct {
	Kind ChangeKind
	ID   string
	Box  Box
}

// Provider is a mail backend.
//
// Implementations talk to the network. Nothing here is called from a TUI
// render path; the sync loop and the command layer call it, and the TUI reads
// the resulting cache.
type Provider interface {
	// Name identifies the backend, e.g. "proton".
	Name() string

	// Addresses returns the account's own addresses, used to tell outbound
	// from inbound and to pick a From address.
	Addresses(ctx context.Context) ([]Address, error)

	// Boxes lists every mailbox with its counts.
	Boxes(ctx context.Context) ([]Box, error)

	// Conversations lists threads in a box.
	Conversations(ctx context.Context, opts ListOptions) ([]Conversation, error)

	// Thread returns one conversation with all of its messages.
	Thread(ctx context.Context, conversationID string) (Thread, error)

	// Body fetches and decrypts one message body.
	Body(ctx context.Context, messageID string) (Body, error)

	// Attachment fetches and decrypts one attachment's bytes.
	Attachment(ctx context.Context, messageID, attachmentID string) ([]byte, error)

	// Label adds a box to conversations. Unlabel removes it.
	Label(ctx context.Context, conversationIDs []string, boxID string) error
	Unlabel(ctx context.Context, conversationIDs []string, boxID string) error

	// CreateBox makes a new user folder or label and returns it.
	CreateBox(ctx context.Context, name string, kind BoxKind, color string) (Box, error)

	// MarkRead and MarkUnread change read state for whole conversations.
	MarkRead(ctx context.Context, conversationIDs []string) error
	MarkUnread(ctx context.Context, conversationIDs []string, boxID string) error

	// Draft creates or updates a draft and returns it with its ID set.
	Draft(ctx context.Context, d Draft) (Draft, error)

	// Send sends a draft. The draft must already exist.
	Send(ctx context.Context, draftID string) (Message, error)

	// Drafts lists saved drafts.
	Drafts(ctx context.Context) ([]Message, error)

	// Newsletters lists tracked mailing lists. Returns ErrNotSupported when
	// the provider has no such concept.
	Newsletters(ctx context.Context) ([]Newsletter, error)

	// RouteNewsletter sets a server-side rule for a list, so it keeps applying
	// with this tool shut down. Returns ErrNotSupported if unavailable.
	RouteNewsletter(ctx context.Context, newsletterID, moveToBoxID string, markAsRead bool) error

	// Cursor returns the current position in the event stream, for a first
	// sync.
	Cursor(ctx context.Context) (string, error)

	// Poll returns changes since the cursor.
	Poll(ctx context.Context, cursor string) (Delta, error)

	// Close releases the provider's resources.
	Close() error
}
