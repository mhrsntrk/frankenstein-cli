package protonmail

import (
	"net/mail"
	"time"

	"github.com/ProtonMail/go-proton-api"

	fmail "github.com/mhrsntrk/frankenstein-cli/internal/mail"
	"github.com/mhrsntrk/frankenstein-cli/internal/protonapi"
)

// This file is the only place Proton types are translated into the neutral
// model. Keeping it separate makes it obvious when something leaks.

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

func toAPIAddress(a protonapi.Address) fmail.Address {
	return fmail.Address{
		Name:          a.Name,
		Address:       a.Address,
		IsProton:      bool(a.IsProton),
		IsSimpleLogin: bool(a.IsSimpleLogin),
	}
}

func toAPIAddresses(in []protonapi.Address) []fmail.Address {
	if len(in) == 0 {
		return nil
	}

	out := make([]fmail.Address, 0, len(in))
	for _, a := range in {
		out = append(out, toAPIAddress(a))
	}

	return out
}

func toBoxKind(t proton.LabelType, id string) fmail.BoxKind {
	if protonapi.IsCategoryLabel(id) {
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
// labels with real counts, but /core/v4/labels rejects any Type outside 1-4,
// so without this they would be invisible.
func categoryBoxes() []fmail.Box {
	out := make([]fmail.Box, 0, len(protonapi.CategoryLabels))

	for _, id := range protonapi.CategoryLabels {
		name := protonapi.CategoryNames[id]
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

func toConversation(c protonapi.Conversation) fmail.Conversation {
	// Always the global rollups, never the Context* ones scoped to the label
	// a listing was made under. Everything this conversion feeds ends up in
	// the cache, which holds one row per thread: a box-scoped count written
	// there would show up in every other box the thread appears in, drifting
	// unread counts and times on any replied thread.
	return fmail.Conversation{
		ID:             c.ID,
		Subject:        c.Subject,
		Senders:        toAPIAddresses(c.Senders),
		Recipients:     toAPIAddresses(c.Recipients),
		NumMessages:    c.NumMessages,
		NumUnread:      c.NumUnread,
		NumAttachments: c.NumAttachments,
		Time:           time.Unix(c.Time, 0),
		Size:           c.Size,
		BoxIDs:         c.LabelIDs,
		CategoryID:     c.CategoryID,
		Order:          c.Order,
	}
}

func toMessage(m protonapi.Message) fmail.Message {
	msg := fmail.Message{
		ID:             m.ID,
		ConversationID: m.ConversationID,
		Subject:        m.Subject,
		To:             toAPIAddresses(m.ToList),
		CC:             toAPIAddresses(m.CCList),
		BCC:            toAPIAddresses(m.BCCList),
		Time:           time.Unix(m.Time, 0),
		Size:           m.Size,
		Unread:         bool(m.Unread),
		BoxIDs:         m.LabelIDs,
		CategoryID:     m.CategoryID,
		NumAttachments: m.NumAttachments,
		SpamScore:      m.SpamScore,
		IsDraft:        m.IsDraft(),
		ExternalID:     m.ExternalID,
		Order:          m.Order,
	}

	if m.Sender != nil {
		msg.From = toAPIAddress(*m.Sender)
	}

	// Provenance arrives on the message, not on the sender address.
	msg.From.IsProton = msg.From.IsProton || bool(m.IsProton)
	msg.From.IsSimpleLogin = msg.From.IsSimpleLogin || bool(m.IsSimpleLogin)

	if m.ReplyTo != nil {
		msg.ReplyTo = []fmail.Address{toAPIAddress(*m.ReplyTo)}
	}

	if m.IsNewsletter() {
		msg.NewsletterID = *m.NewsletterSubscriptionID
	}

	if m.IsSnoozed() {
		t := time.Unix(m.SnoozeTime, 0)
		msg.SnoozedUntil = &t
	}

	return msg
}

// toUpstreamMessage converts the metadata upstream returns. Used only for
// drafts, which the conversation endpoints do not cover.
func toUpstreamMessage(m proton.MessageMetadata) fmail.Message {
	return fmail.Message{
		ID:             m.ID,
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
		NumAttachments: m.NumAttachments,
		IsDraft:        m.IsDraft(),
		ExternalID:     m.ExternalID,
	}
}

func toNewsletter(n protonapi.NewsletterSubscription) fmail.Newsletter {
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

	if out.ReceivedTotal == 0 && n.ReceivedMessageCount > 0 {
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

func toChangeKind(a protonapi.EventAction) fmail.ChangeKind {
	switch a {
	case protonapi.EventCreate:
		return fmail.ChangeCreate
	case protonapi.EventDelete:
		return fmail.ChangeDelete
	default:
		return fmail.ChangeUpdate
	}
}
