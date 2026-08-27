// Package protonapi covers the parts of Proton's REST API that
// go-proton-api does not model.
//
// Upstream exists to serve Proton Bridge, which flattens everything down to
// IMAP. IMAP has no thread primitive, so the conversation surface was never
// needed there and the structs never declared it. The same goes for newsletter
// subscriptions and for a dozen fields on message metadata that arrive on the
// wire and are silently dropped by encoding/json.
//
// Extending proton.Client is not possible from outside: its do/doRes/exec
// methods are unexported. But the authenticated session it hands back carries
// a UID and an access token, which is all these endpoints need. So this is a
// small client alongside the upstream one rather than a fork of it.
//
// Everything here was verified against the live API on 2026-08-27.
package protonapi

import (
	"encoding/json"
	"strconv"
)

// Bool decodes Proton's 0/1 integers, which are not JSON booleans.
type Bool bool

func (b *Bool) UnmarshalJSON(data []byte) error {
	s := string(data)

	switch s {
	case "true", "1", `"1"`:
		*b = true

		return nil
	case "false", "0", `"0"`, "null":
		*b = false

		return nil
	}

	n, err := strconv.Atoi(s)
	if err != nil {
		return err
	}

	*b = n != 0

	return nil
}

func (b Bool) MarshalJSON() ([]byte, error) {
	if b {
		return []byte("1"), nil
	}

	return []byte("0"), nil
}

// Address is a participant as the conversation endpoints return it: a name and
// an address plus provenance flags a bare mail.Address cannot carry.
type Address struct {
	Name    string `json:"Name"`
	Address string `json:"Address"`

	IsProton           Bool    `json:"IsProton"`
	IsSimpleLogin      Bool    `json:"IsSimpleLogin"`
	DisplaySenderImage Bool    `json:"DisplaySenderImage"`
	BimiSelector       *string `json:"BimiSelector"`
}

// Conversation is Proton's native thread.
//
// The Context* fields are rollups scoped to the label the conversation was
// listed under: "3 messages, 1 unread, newest at T, in this box". That is what
// a mailbox list view needs, as opposed to the unscoped totals.
type Conversation struct {
	ID    string `json:"ID"`
	Order int64  `json:"Order"`

	Subject    string    `json:"Subject"`
	Senders    []Address `json:"Senders"`
	Recipients []Address `json:"Recipients"`

	NumMessages    int   `json:"NumMessages"`
	NumUnread      int   `json:"NumUnread"`
	NumAttachments int   `json:"NumAttachments"`
	Size           int64 `json:"Size"`
	Time           int64 `json:"Time"`

	ContextNumMessages    int   `json:"ContextNumMessages"`
	ContextNumUnread      int   `json:"ContextNumUnread"`
	ContextNumAttachments int   `json:"ContextNumAttachments"`
	ContextSize           int64 `json:"ContextSize"`
	ContextTime           int64 `json:"ContextTime"`
	ContextExpirationTime int64 `json:"ContextExpirationTime"`

	LabelIDs []string            `json:"LabelIDs"`
	Labels   []ConversationLabel `json:"Labels"`

	// CategoryID is Proton's own server-side classification. See the
	// Category* constants.
	CategoryID string `json:"CategoryID"`

	ExpirationTime      int64 `json:"ExpirationTime"`
	ExpiringByRetention bool  `json:"ExpiringByRetention"`

	IsProton               Bool `json:"IsProton"`
	DisplaySnoozedReminder bool `json:"DisplaySnoozedReminder"`
}

// Unread reports whether the thread has unread messages in the label it was
// listed under, falling back to the unscoped count.
func (c Conversation) Unread() bool {
	if c.ContextNumMessages > 0 {
		return c.ContextNumUnread > 0
	}

	return c.NumUnread > 0
}

// ConversationLabel is the per-label context attached to a conversation.
type ConversationLabel struct {
	ID string `json:"ID"`

	ContextNumMessages    int   `json:"ContextNumMessages"`
	ContextNumUnread      int   `json:"ContextNumUnread"`
	ContextNumAttachments int   `json:"ContextNumAttachments"`
	ContextSize           int64 `json:"ContextSize"`
	ContextTime           int64 `json:"ContextTime"`
}

// Message is message metadata with the fields upstream discards.
//
// Only the ones this project uses are declared; the response carries more.
type Message struct {
	ID        string `json:"ID"`
	AddressID string `json:"AddressID"`
	Order     int64  `json:"Order"`

	// ConversationID is the thread this belongs to. Upstream never declared
	// it, which is why threads had to be rebuilt from headers there.
	ConversationID string `json:"ConversationID"`

	Subject string    `json:"Subject"`
	Sender  *Address  `json:"Sender"`
	ToList  []Address `json:"ToList"`
	CCList  []Address `json:"CCList"`
	BCCList []Address `json:"BCCList"`
	ReplyTo *Address  `json:"ReplyTo"`

	Time   int64 `json:"Time"`
	Size   int64 `json:"Size"`
	Unread Bool  `json:"Unread"`

	LabelIDs []string `json:"LabelIDs"`

	// CategoryID is the server-side classification, also present in LabelIDs.
	CategoryID string `json:"CategoryID"`

	// NewsletterSubscriptionID links to a tracked mailing list, null for
	// ordinary mail.
	NewsletterSubscriptionID *string `json:"NewsletterSubscriptionID"`

	NumAttachments int   `json:"NumAttachments"`
	SpamScore      int   `json:"SpamScore"`
	Flags          int64 `json:"Flags"`

	IsProton      Bool `json:"IsProton"`
	IsSimpleLogin Bool `json:"IsSimpleLogin"`

	SnoozeTime     int64 `json:"SnoozeTime"`
	ExpirationTime int64 `json:"ExpirationTime"`

	ExternalID string `json:"ExternalID"`
}

// IsNewsletter reports whether Proton associates this with a mailing list.
func (m Message) IsNewsletter() bool {
	return m.NewsletterSubscriptionID != nil && *m.NewsletterSubscriptionID != ""
}

// IsSnoozed reports whether the message is currently snoozed.
func (m Message) IsSnoozed() bool { return m.SnoozeTime > 0 }

// Message flag bits, matching upstream's MessageFlag.
const (
	flagReceived = 1 << 0
	flagSent     = 1 << 1
)

// IsDraft reports whether the message has neither been received nor sent.
func (m Message) IsDraft() bool { return m.Flags&(flagReceived|flagSent) == 0 }

// Count is a per-label rollup from the count endpoints.
type Count struct {
	LabelID string `json:"LabelID"`
	Total   int    `json:"Total"`
	Unread  int    `json:"Unread"`
}

// NewsletterSubscription is Proton's record of a mailing list.
//
// MarkAsRead and MoveToFolder are server-side routing rules: set them once and
// the routing keeps applying with this client shut down. There is no general
// filter API, but this covers the newsletter case.
type NewsletterSubscription struct {
	ID        string `json:"ID"`
	UserID    string `json:"UserID"`
	AddressID string `json:"AddressID"`

	// ListID is the List-Id header, the stable identity of the list.
	ListID        string `json:"ListID"`
	Name          string `json:"Name"`
	SenderAddress string `json:"SenderAddress"`

	ReceivedMessageCount int `json:"ReceivedMessageCount"`
	UnreadMessageCount   int `json:"UnreadMessageCount"`

	ReceivedMessages struct {
		Total      int `json:"Total"`
		Last30Days int `json:"Last30Days"`
		Last90Days int `json:"Last90Days"`
	} `json:"ReceivedMessages"`

	FirstReceivedTime int64 `json:"FirstReceivedTime"`
	LastReceivedTime  int64 `json:"LastReceivedTime"`
	LastReadTime      int64 `json:"LastReadTime"`

	// TrackersCount is how many tracking pixels Proton stripped from this
	// list's mail.
	TrackersCount int `json:"TrackersCount"`

	Unsubscribed     bool   `json:"Unsubscribed"`
	UnsubscribedTime *int64 `json:"UnsubscribedTime"`
	Spam             bool   `json:"Spam"`
	Hidden           bool   `json:"Hidden"`
	DiscussionsGroup bool   `json:"DiscussionsGroup"`

	MarkAsRead   bool    `json:"MarkAsRead"`
	MoveToFolder *string `json:"MoveToFolder"`
	FilterID     *string `json:"FilterID"`

	UnsubscribeMethods json.RawMessage   `json:"UnsubscribeMethods"`
	Headers            map[string]string `json:"Headers"`
}

// CanUnsubscribe reports whether Proton advertises any way to leave the list.
func (n NewsletterSubscription) CanUnsubscribe() bool {
	if len(n.UnsubscribeMethods) == 0 {
		return false
	}

	var methods map[string]json.RawMessage
	if err := json.Unmarshal(n.UnsubscribeMethods, &methods); err != nil {
		return false
	}

	return len(methods) > 0
}

// EventAction is what happened to an item in a delta.
type EventAction int

const (
	EventDelete EventAction = iota
	EventCreate
	EventUpdate
	EventUpdateFlags
)

// Event is one delta from /core/v4/events.
//
// Upstream models EventID, Refresh, Messages, Labels, Addresses and
// Notifications, and drops Conversations, which is the one this client is here
// for.
type Event struct {
	EventID string `json:"EventID"`
	Refresh int    `json:"Refresh"`
	More    int    `json:"More"`

	Conversations []ConversationEvent `json:"Conversations"`
	Messages      []MessageEvent      `json:"Messages"`
	Labels        []LabelEvent        `json:"Labels"`
}

type ConversationEvent struct {
	ID     string      `json:"ID"`
	Action EventAction `json:"Action"`

	Conversation Conversation `json:"Conversation"`
}

type MessageEvent struct {
	ID     string      `json:"ID"`
	Action EventAction `json:"Action"`

	Message Message `json:"Message"`
}

type LabelEvent struct {
	ID     string      `json:"ID"`
	Action EventAction `json:"Action"`

	Label struct {
		ID    string `json:"ID"`
		Name  string `json:"Name"`
		Path  string `json:"Path"`
		Color string `json:"Color"`
		Type  int    `json:"Type"`
	} `json:"Label"`
}

// Label IDs. Upstream stops at 12 and models neither of the last two, nor the
// categories below.
const (
	InboxLabel        = "0"
	AllDraftsLabel    = "1"
	AllSentLabel      = "2"
	TrashLabel        = "3"
	SpamLabel         = "4"
	AllMailLabel      = "5"
	ArchiveLabel      = "6"
	SentLabel         = "7"
	DraftsLabel       = "8"
	OutboxLabel       = "9"
	StarredLabel      = "10"
	AllScheduledLabel = "12"

	// AllMailNoSpamTrashLabel is a second "All Mail" that excludes Spam and
	// Trash.
	AllMailNoSpamTrashLabel = "15"
	SnoozedLabel            = "16"
)

// Proton classifies inbound mail server-side into categories, which behave as
// system labels: they appear in LabelIDs and in the conversation counts. But
// /core/v4/labels rejects any Type outside 1-4, so they are never listed and
// carry no server-provided names at all.
//
// The IDs are confirmed. The names are inferred from what actually lands in
// each one on a real mailbox, not from any Proton documentation:
//
//	20  small, community and social senders
//	21  retailers and product marketing
//	22  service and developer notifications
//	23  never observed with any mail in it
//	24  the bulk of the mailbox: personal correspondence, banking, invoices
//	25  Substack and similar subscription writing
//	26  mailing lists and working groups
//
// 24 being the default matters most, because it is the one the screener maps
// to the Imbox. An earlier guess had 24 as "Transactions", which suggested
// filing personal mail into the Paper Trail.
const (
	CategorySocialLabel      = "20"
	CategoryPromotionsLabel  = "21"
	CategoryUpdatesLabel     = "22"
	CategoryUnknownLabel     = "23"
	CategoryDefaultLabel     = "24"
	CategoryNewslettersLabel = "25"
	CategoryForumsLabel      = "26"
)

// CategoryLabels lists every category label ID in order.
var CategoryLabels = []string{
	CategoryDefaultLabel,
	CategorySocialLabel,
	CategoryPromotionsLabel,
	CategoryUpdatesLabel,
	CategoryNewslettersLabel,
	CategoryForumsLabel,
	CategoryUnknownLabel,
}

// CategoryNames maps a category label ID to its display name.
var CategoryNames = map[string]string{
	CategoryDefaultLabel:     "Primary",
	CategorySocialLabel:      "Social",
	CategoryPromotionsLabel:  "Promotions",
	CategoryUpdatesLabel:     "Updates",
	CategoryNewslettersLabel: "Newsletters",
	CategoryForumsLabel:      "Forums",
	CategoryUnknownLabel:     "Category 23",
}

// IsCategoryLabel reports whether a label ID is one of Proton's categories.
func IsCategoryLabel(id string) bool {
	for _, c := range CategoryLabels {
		if c == id {
			return true
		}
	}

	return false
}
