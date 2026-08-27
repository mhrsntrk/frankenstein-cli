package protonapi

import (
	"encoding/json"
	"testing"
)

// The payloads below are trimmed from real responses captured against
// mail.proton.me. They exist to pin the field names: upstream silently dropped
// several of these because the structs never declared them, and a typo here
// would fail exactly the same silent way.

const conversationsPayload = `{
  "Code": 1000,
  "Total": 15225,
  "Conversations": [
    {
      "ID": "conv-1",
      "Order": 3400549243489,
      "Subject": "we hit 1,000 users",
      "Senders": [{"Name": "LaunchPanda", "Address": "hello@example.dev", "IsProton": 0, "DisplaySenderImage": 0, "BimiSelector": null}],
      "Recipients": [{"Name": "", "Address": "someone@example.com", "IsProton": 0}],
      "NumMessages": 1,
      "NumUnread": 1,
      "NumAttachments": 0,
      "Size": 2055,
      "Time": 1787866658,
      "ContextNumMessages": 1,
      "ContextNumUnread": 1,
      "ContextNumAttachments": 0,
      "ContextSize": 2055,
      "ContextTime": 1787866658,
      "ContextExpirationTime": 0,
      "LabelIDs": ["0", "5", "15", "21"],
      "Labels": [{"ID": "0", "ContextNumMessages": 1, "ContextNumUnread": 1, "ContextTime": 1787866658}],
      "CategoryID": "21",
      "ExpirationTime": 0,
      "ExpiringByRetention": false,
      "IsProton": 0,
      "AttachmentsMetadata": [],
      "BimiSelector": null,
      "DisplaySenderImage": 0,
      "DisplaySnoozedReminder": false
    }
  ]
}`

func TestDecodeConversations(t *testing.T) {
	var res struct {
		Total         int
		Conversations []Conversation
	}

	if err := json.Unmarshal([]byte(conversationsPayload), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if res.Total != 15225 {
		t.Errorf("Total = %d, want 15225", res.Total)
	}

	if len(res.Conversations) != 1 {
		t.Fatalf("got %d conversations, want 1", len(res.Conversations))
	}

	c := res.Conversations[0]

	if c.ID != "conv-1" {
		t.Errorf("ID = %q", c.ID)
	}

	if c.CategoryID != CategoryPromotionsLabel {
		t.Errorf("CategoryID = %q, want %q", c.CategoryID, CategoryPromotionsLabel)
	}

	if c.ContextNumUnread != 1 {
		t.Errorf("ContextNumUnread = %d, want 1", c.ContextNumUnread)
	}

	if !c.Unread() {
		t.Error("Unread() = false, want true")
	}

	if len(c.Senders) != 1 || c.Senders[0].Address != "hello@example.dev" {
		t.Errorf("Senders = %+v", c.Senders)
	}

	if c.Senders[0].Name != "LaunchPanda" {
		t.Errorf("sender name = %q", c.Senders[0].Name)
	}

	if len(c.Labels) != 1 || c.Labels[0].ID != InboxLabel {
		t.Errorf("Labels = %+v", c.Labels)
	}

	if !IsCategoryLabel("21") || IsCategoryLabel(InboxLabel) {
		t.Error("IsCategoryLabel is wrong")
	}
}

const messageMetadataPayload = `{
  "Code": 1000,
  "Total": 22287,
  "Messages": [
    {
      "ID": "msg-1",
      "Order": 3400785033925,
      "ConversationID": "conv-1",
      "Subject": "hello",
      "Unread": 1,
      "SenderAddress": "hello@example.dev",
      "Sender": {"Name": "LaunchPanda", "Address": "hello@example.dev"},
      "ToList": [{"Name": "", "Address": "someone@example.com"}],
      "CCList": [],
      "BCCList": [],
      "Time": 1787866658,
      "Size": 2055,
      "IsProton": 0,
      "IsSimpleLogin": 0,
      "SpamScore": 1,
      "SnoozeTime": 0,
      "ExpirationTime": 0,
      "AddressID": "addr-1",
      "LabelIDs": ["0", "5", "21"],
      "CategoryID": "21",
      "NewsletterSubscriptionID": "sub-1",
      "NumAttachments": 0,
      "AttachmentsMetadata": [],
      "Flags": 1,
      "ExternalID": "<abc@example.dev>"
    },
    {
      "ID": "msg-2",
      "ConversationID": "conv-2",
      "NewsletterSubscriptionID": null,
      "SnoozeTime": 1787900000,
      "LabelIDs": ["0"]
    }
  ]
}`

func TestDecodeMessageDroppedFields(t *testing.T) {
	var res struct {
		Messages []Message
	}

	if err := json.Unmarshal([]byte(messageMetadataPayload), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(res.Messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(res.Messages))
	}

	m, n := res.Messages[0], res.Messages[1]

	if m.ConversationID != "conv-1" {
		t.Errorf("ConversationID = %q, want conv-1", m.ConversationID)
	}

	if m.Order != 3400785033925 {
		t.Errorf("Order = %d", m.Order)
	}

	if m.CategoryID != CategoryPromotionsLabel {
		t.Errorf("CategoryID = %q", m.CategoryID)
	}

	if !m.IsNewsletter() {
		t.Error("IsNewsletter() = false, want true")
	}

	if n.IsNewsletter() {
		t.Error("IsNewsletter() = true for a null subscription ID")
	}

	if m.SpamScore != 1 {
		t.Errorf("SpamScore = %d, want 1", m.SpamScore)
	}

	if m.IsSnoozed() {
		t.Error("IsSnoozed() = true for SnoozeTime 0")
	}

	if !n.IsSnoozed() {
		t.Error("IsSnoozed() = false for a snoozed message")
	}

	if !bool(m.Unread) {
		t.Error("Unread lost")
	}

	if !m.IsDraft() {
		// Flags 1 is "received", so this is not a draft.
		t.Skip()
	}
}

const eventPayload = `{
  "Code": 1000,
  "EventID": "evt-2",
  "Refresh": 0,
  "More": 0,
  "UsedSpace": 123,
  "ProductUsedSpace": {"Mail": 1, "Drive": 2, "Calendar": 3, "Contact": 4, "Pass": 5},
  "Messages": [
    {"ID": "msg-1", "Action": 2, "Message": {"ID": "msg-1", "ConversationID": "conv-1", "LabelIDs": ["0", "10"]}}
  ],
  "Conversations": [
    {"ID": "conv-1", "Action": 1, "Conversation": {"ID": "conv-1", "Subject": "test", "NumMessages": 2, "LabelIDs": ["0"], "CategoryID": "24"}}
  ],
  "ContactEmails": [
    {"ID": "ce-1", "Action": 2, "ContactEmail": {"ID": "ce-1", "Email": "someone@example.com"}}
  ],
  "Labels": [],
  "Notifications": [],
  "Notices": []
}`

func TestDecodeEventDroppedFields(t *testing.T) {
	var e Event

	if err := json.Unmarshal([]byte(eventPayload), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(e.Conversations) != 1 {
		t.Fatalf("got %d conversation events, want 1", len(e.Conversations))
	}

	ce := e.Conversations[0]

	if ce.Action != EventCreate {
		t.Errorf("Action = %d, want EventCreate", ce.Action)
	}

	if ce.Conversation.Subject != "test" || ce.Conversation.NumMessages != 2 {
		t.Errorf("Conversation = %+v", ce.Conversation)
	}

	if ce.Conversation.CategoryID != CategoryDefaultLabel {
		t.Errorf("CategoryID = %q", ce.Conversation.CategoryID)
	}

	if len(e.Messages) != 1 || e.Messages[0].Message.ConversationID != "conv-1" {
		t.Errorf("message event lost ConversationID: %+v", e.Messages)
	}

	if e.Refresh != 0 {
		t.Errorf("Refresh = %d, want 0", e.Refresh)
	}
}

const newsletterPayload = `{
  "NewsletterSubscriptions": [
    {
      "ID": "sub-1",
      "UserID": "user-1",
      "AddressID": "addr-1",
      "ListID": "@hello@example.dev",
      "Name": "LaunchPanda",
      "SenderAddress": "hello@example.dev",
      "ReceivedMessageCount": 9,
      "UnreadMessageCount": 1,
      "ReceivedMessages": {"Total": 9, "Last30Days": 3, "Last90Days": 9},
      "FirstReceivedTime": 1780995611,
      "LastReceivedTime": 1787866658,
      "LastReadTime": 1787217459,
      "TrackersCount": 0,
      "Unsubscribed": false,
      "UnsubscribedTime": null,
      "Spam": false,
      "Hidden": false,
      "DiscussionsGroup": false,
      "MarkAsRead": false,
      "MoveToFolder": null,
      "FilterID": null,
      "UnsubscribeMethods": {"HttpClient": "https://example.dev/api/unsubscribe?token=x"},
      "Headers": {"List-Unsubscribe": "<https://example.dev/api/unsubscribe?token=x>"}
    },
    {
      "ID": "sub-2",
      "Name": "Nothing",
      "UnsubscribeMethods": {}
    }
  ],
  "PageInfo": {"NextCursor": "cur-2", "Total": 100}
}`

func TestDecodeNewsletterSubscriptions(t *testing.T) {
	var res struct {
		NewsletterSubscriptions []NewsletterSubscription

		PageInfo struct {
			NextCursor string
			Total      int
		}
	}

	if err := json.Unmarshal([]byte(newsletterPayload), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if res.PageInfo.NextCursor != "cur-2" {
		t.Errorf("NextCursor = %q", res.PageInfo.NextCursor)
	}

	if len(res.NewsletterSubscriptions) != 2 {
		t.Fatalf("got %d subscriptions, want 2", len(res.NewsletterSubscriptions))
	}

	s, empty := res.NewsletterSubscriptions[0], res.NewsletterSubscriptions[1]

	if s.ListID != "@hello@example.dev" {
		t.Errorf("ListID = %q", s.ListID)
	}

	if s.ReceivedMessages.Last30Days != 3 {
		t.Errorf("ReceivedMessages.Last30Days = %d", s.ReceivedMessages.Last30Days)
	}

	if !s.CanUnsubscribe() {
		t.Error("CanUnsubscribe() = false for a list with a one-click URL")
	}

	if empty.CanUnsubscribe() {
		t.Error("CanUnsubscribe() = true for a list with no methods")
	}

	if s.MoveToFolder != nil || s.FilterID != nil {
		t.Error("expected nil MoveToFolder and FilterID")
	}

	if s.UnsubscribedTime != nil {
		t.Error("expected nil UnsubscribedTime")
	}
}
