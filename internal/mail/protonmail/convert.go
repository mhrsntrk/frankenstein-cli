package protonmail

import (
	"net/mail"
	"strings"
	"time"

	"github.com/ProtonMail/go-proton-api"

	fmail "github.com/mhrsntrk/frankenstein-cli/internal/mail"
)

// This file is the only place Proton types are translated into the neutral
// model. Keeping it separate makes it obvious when something leaks.

// systemBoxNames maps Proton's system label IDs to display names. The API
// returns names for most of these, but not for categories, which are never
// listed even though they behave as labels.
var categoryNames = map[string]string{
	proton.CategoryDefaultLabel:      "Primary",
	proton.CategoryPromotionsLabel:   "Promotions",
	proton.CategorySocialLabel:       "Social",
	proton.CategoryNewslettersLabel:  "Newsletters",
	proton.CategoryTransactionsLabel: "Transactions",
	proton.CategoryUpdatesLabel:      "Updates",
	proton.CategoryForumsLabel:       "Forums",
}

func toAddress(a *mail.Address) fmail.Address {
	if a == nil {
		return fmail.Address{}
	}

	return fmail.Address{Name: a.Name, Address: a.Address}
}

func toAddresses(in []*mail.Address) []fmail.Address {
	if len(in) == 0 {
		return nil
	}

	out := make([]fmail.Address, 0, len(in))
	for _, a := range in {
		out = append(out, toAddress(a))
	}

	return out
}

func toConvAddresses(in []proton.ConversationAddress) []fmail.Address {
	if len(in) == 0 {
		return nil
	}

	out := make([]fmail.Address, 0, len(in))
	for _, a := range in {
		out = append(out, fmail.Address{
			Name:          a.Name,
			Address:       a.Address,
			IsProton:      bool(a.IsProton),
			IsSimpleLogin: bool(a.IsSimpleLogin),
		})
	}

	return out
}

func toBoxKind(t proton.LabelType, id string) fmail.BoxKind {
	if proton.IsCategoryLabel(id) {
		return fmail.BoxCategory
	}

	switch t {
	case proton.LabelTypeSystem:
		return fmail.BoxSystem
	case proton.LabelTypeFolder:
		return fmail.BoxFolder
	default:
		return fmail.BoxLabel
	}
}

func toBox(l proton.Label) fmail.Box {
	return fmail.Box{
		ID:    l.ID,
		Name:  l.Name,
		Path:  l.Path,
		Kind:  toBoxKind(l.Type, l.ID),
		Color: l.Color,
	}
}

// categoryBoxes synthesises Box entries for Proton's categories. They are real
// labels with real counts, but /core/v4/labels never returns them, so without
// this they would be invisible.
func categoryBoxes() []fmail.Box {
	out := make([]fmail.Box, 0, len(proton.CategoryLabels))

	for _, id := range proton.CategoryLabels {
		name := categoryNames[id]
		if name == "" {
			name = "Category " + id
		}

		out = append(out, fmail.Box{
			ID:   id,
			Name: name,
			Path: []string{name},
			Kind: fmail.BoxCategory,
		})
	}

	return out
}

func toConversation(c proton.Conversation) fmail.Conversation {
	// Prefer the label-scoped rollups: they describe the thread as it appears
	// in the box being listed, which is what the caller asked for.
	num, unread, atts := c.NumMessages, c.NumUnread, c.NumAttachments
	size, when := c.Size, c.Time

	if c.ContextNumMessages > 0 {
		num, unread, atts = c.ContextNumMessages, c.ContextNumUnread, c.ContextNumAttachments
		size, when = c.ContextSize, c.ContextTime
	}

	return fmail.Conversation{
		ID:             c.ID,
		Subject:        c.Subject,
		Senders:        toConvAddresses(c.Senders),
		Recipients:     toConvAddresses(c.Recipients),
		NumMessages:    num,
		NumUnread:      unread,
		NumAttachments: atts,
		Time:           time.Unix(when, 0),
		Size:           size,
		BoxIDs:         c.LabelIDs,
		CategoryID:     c.CategoryID,
		Order:          c.Order,
	}
}

func toMessage(m proton.MessageMetadata) fmail.Message {
	msg := fmail.Message{
		ID:             m.ID,
		ConversationID: m.ConversationID,
		Subject:        m.Subject,
		From:           toAddress(m.Sender),
		To:             toAddresses(m.ToList),
		CC:             toAddresses(m.CCList),
		BCC:            toAddresses(m.BCCList),
		ReplyTo:        toAddresses(m.ReplyTos),
		Time:           time.Unix(m.Time, 0),
		Size:           int64(m.Size),
		Unread:         !m.Seen(),
		BoxIDs:         m.LabelIDs,
		CategoryID:     m.CategoryID,
		NumAttachments: m.NumAttachments,
		SpamScore:      m.SpamScore,
		IsDraft:        m.IsDraft(),
		ExternalID:     m.ExternalID,
		Order:          m.Order,
	}

	if m.IsNewsletter() {
		msg.NewsletterID = *m.NewsletterSubscriptionID
	}

	if m.IsSnoozed() {
		t := time.Unix(m.SnoozeTime, 0)
		msg.SnoozedUntil = &t
	}

	// Sender provenance lives on the message metadata, not on the address.
	msg.From.IsProton = bool(m.IsProton)
	msg.From.IsSimpleLogin = bool(m.IsSimpleLogin)

	return msg
}

func toNewsletter(n proton.NewsletterSubscription) fmail.Newsletter {
	out := fmail.Newsletter{
		ID:     n.ID,
		ListID: n.ListID,
		Name:   n.Name,
		Sender: fmail.Address{
			Name:    n.Name,
			Address: n.SenderAddress,
		},
		ReceivedTotal:      n.ReceivedMessages.Total,
		ReceivedLast30Days: n.ReceivedMessages.Last30Days,
		ReceivedLast90Days: n.ReceivedMessages.Last90Days,
		Unread:             n.UnreadMessageCount,
		Trackers:           n.TrackersCount,
		FirstReceived:      time.Unix(n.FirstReceivedTime, 0),
		LastReceived:       time.Unix(n.LastReceivedTime, 0),
		Unsubscribed:       n.Unsubscribed,
		Spam:               n.Spam,
		CanUnsubscribe:     n.CanUnsubscribe(),
		MarkAsRead:         n.MarkAsRead,
	}

	if n.ReceivedMessages.Total == 0 && n.ReceivedMessageCount > 0 {
		out.ReceivedTotal = n.ReceivedMessageCount
	}

	if n.LastReadTime > 0 {
		t := time.Unix(n.LastReadTime, 0)
		out.LastRead = &t
	}

	if n.MoveToFolder != nil {
		out.MoveToBoxID = *n.MoveToFolder
	}

	return out
}

func toChangeKind(a proton.EventAction) fmail.ChangeKind {
	switch a {
	case proton.EventCreate:
		return fmail.ChangeCreate
	case proton.EventDelete:
		return fmail.ChangeDelete
	default:
		return fmail.ChangeUpdate
	}
}

// parseReferences splits a References header into individual message IDs.
func parseReferences(values []string) []string {
	var out []string

	for _, v := range values {
		for _, f := range strings.Fields(v) {
			f = strings.TrimSpace(f)
			if f != "" {
				out = append(out, f)
			}
		}
	}

	return out
}
