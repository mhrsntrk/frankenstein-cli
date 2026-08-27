// Package protonmail implements the mail.Provider interface against Proton's
// REST API, using a fork of go-proton-api that adds the conversation,
// newsletter and category surface upstream does not model.
package protonmail

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"sort"
	"strings"

	"github.com/ProtonMail/gluon/rfc822"
	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"

	fmail "github.com/mhrsntrk/frankenstein-cli/internal/mail"
	"github.com/mhrsntrk/frankenstein-cli/internal/protonapi"
)

// Provider is a Proton-backed mail.Provider.
//
// It uses two clients against one session. go-proton-api handles auth, keys,
// decryption, drafts and sending. internal/protonapi handles conversations,
// newsletters and the event deltas, which upstream does not model and cannot
// be added to from outside because its request methods are unexported.
type Provider struct {
	client  *proton.Client
	manager *proton.Manager

	// api covers the endpoints upstream leaves out.
	api *protonapi.Client

	// addrKRs maps address ID to its unlocked key ring. Decryption picks the
	// ring by the message's AddressID.
	addrKRs map[string]*crypto.KeyRing
	userKR  *crypto.KeyRing

	addresses []proton.Address

	// closeManager is true when we own the manager and must close it.
	closeManager bool
}

// New wraps an authenticated client and its unlocked key rings.
func New(m *proton.Manager, c *proton.Client, api *protonapi.Client, userKR *crypto.KeyRing, addrKRs map[string]*crypto.KeyRing, addresses []proton.Address, ownsManager bool) *Provider {
	return &Provider{
		client:       c,
		manager:      m,
		api:          api,
		userKR:       userKR,
		addrKRs:      addrKRs,
		addresses:    addresses,
		closeManager: ownsManager,
	}
}

func (p *Provider) Name() string { return "proton" }

func (p *Provider) Close() error {
	if p.client != nil {
		p.client.Close()
	}

	if p.userKR != nil {
		p.userKR.ClearPrivateParams()
	}

	for _, kr := range p.addrKRs {
		kr.ClearPrivateParams()
	}

	if p.closeManager && p.manager != nil {
		p.manager.Close()
	}

	return nil
}

func (p *Provider) Addresses(_ context.Context) ([]fmail.Address, error) {
	out := make([]fmail.Address, 0, len(p.addresses))

	for _, a := range p.addresses {
		out = append(out, fmail.Address{Name: a.DisplayName, Address: a.Email, IsProton: true})
	}

	return out, nil
}

// primaryAddressID returns the address to compose from: the first one with an
// unlocked key ring, preferring Proton's own ordering.
func (p *Provider) primaryAddressID() (string, error) {
	for _, a := range p.addresses {
		if _, ok := p.addrKRs[a.ID]; ok {
			return a.ID, nil
		}
	}

	return "", errors.New("no address with an unlocked key ring")
}

func (p *Provider) Boxes(ctx context.Context) ([]fmail.Box, error) {
	labels, err := p.client.GetLabels(ctx,
		proton.LabelTypeSystem, proton.LabelTypeFolder, proton.LabelTypeLabel)
	if err != nil {
		return nil, fmt.Errorf("get labels: %w", err)
	}

	boxes := make([]fmail.Box, 0, len(labels)+len(protonapi.CategoryLabels))
	seen := make(map[string]bool, len(labels))

	for _, l := range labels {
		boxes = append(boxes, toBox(l))
		seen[l.ID] = true
	}

	// Categories behave as labels but are never listed, so add them by hand.
	for _, b := range categoryBoxes() {
		if !seen[b.ID] {
			boxes = append(boxes, b)
		}
	}

	// One request fills in the counts for every box at once.
	counts, err := p.api.ConversationCounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("get conversation counts: %w", err)
	}

	byID := make(map[string]protonapi.Count, len(counts))
	for _, c := range counts {
		byID[c.LabelID] = c
	}

	for i := range boxes {
		if c, ok := byID[boxes[i].ID]; ok {
			boxes[i].Total = c.Total
			boxes[i].Unread = c.Unread
		}
	}

	// Stable output: system boxes in Proton's own order, then the rest by name.
	sort.SliceStable(boxes, func(i, j int) bool {
		ri, rj := boxRank(boxes[i]), boxRank(boxes[j])
		if ri != rj {
			return ri < rj
		}

		return strings.ToLower(boxes[i].Name) < strings.ToLower(boxes[j].Name)
	})

	return boxes, nil
}

// boxRank orders the kinds for display: system, category, folder, label.
func boxRank(b fmail.Box) int {
	switch b.Kind {
	case fmail.BoxSystem:
		return systemOrder(b.ID)
	case fmail.BoxCategory:
		return 100
	case fmail.BoxFolder:
		return 200
	default:
		return 300
	}
}

// systemOrder puts the boxes a person actually uses at the top, rather than
// Proton's numeric label IDs which interleave the "All *" aggregates.
func systemOrder(id string) int {
	order := []string{
		proton.InboxLabel,
		proton.StarredLabel,
		protonapi.SnoozedLabel,
		proton.DraftsLabel,
		proton.SentLabel,
		proton.ArchiveLabel,
		proton.SpamLabel,
		proton.TrashLabel,
		proton.AllMailLabel,
	}

	for i, o := range order {
		if o == id {
			return i
		}
	}

	return len(order)
}

func (p *Provider) Conversations(ctx context.Context, opts fmail.ListOptions) ([]fmail.Conversation, error) {
	if opts.Limit <= 0 {
		opts.Limit = 50
	}

	filter := protonapi.ConversationFilter{
		LabelID: opts.BoxID,
		Subject: opts.Search,
		Desc:    true,
	}

	if opts.BoxID == "" {
		filter.LabelID = proton.AllMailLabel
	}

	// The API pages by page number, not offset, so translate.
	pageSize := opts.Limit
	if pageSize > 150 {
		pageSize = 150
	}

	page := 0
	if pageSize > 0 {
		page = opts.Offset / pageSize
	}

	convs, err := p.api.Conversations(ctx, page, pageSize, filter)
	if err != nil {
		return nil, fmt.Errorf("get conversations: %w", err)
	}

	out := make([]fmail.Conversation, 0, len(convs))

	for _, c := range convs {
		conv := toConversation(c)
		if opts.UnreadOnly && !conv.Unread() {
			continue
		}

		out = append(out, conv)
	}

	return out, nil
}

func (p *Provider) Thread(ctx context.Context, conversationID string) (fmail.Thread, error) {
	conv, msgs, err := p.api.Conversation(ctx, conversationID)
	if err != nil {
		return fmail.Thread{}, fmt.Errorf("get conversation %s: %w", conversationID, err)
	}

	out := fmail.Thread{
		Conversation: toConversation(conv),
		Messages:     make([]fmail.Message, 0, len(msgs)),
	}

	for _, m := range msgs {
		out.Messages = append(out.Messages, toMessage(m))
	}

	sort.SliceStable(out.Messages, func(i, j int) bool {
		return out.Messages[i].Time.Before(out.Messages[j].Time)
	})

	return out, nil
}

func (p *Provider) Body(ctx context.Context, messageID string) (fmail.Body, error) {
	msg, err := p.client.GetMessage(ctx, messageID)
	if err != nil {
		return fmail.Body{}, fmt.Errorf("get message %s: %w", messageID, err)
	}

	kr, ok := p.addrKRs[msg.AddressID]
	if !ok {
		return fmail.Body{}, fmt.Errorf("no unlocked key ring for address %s", msg.AddressID)
	}

	dec, err := msg.Decrypt(kr)
	if err != nil {
		return fmail.Body{}, fmt.Errorf("decrypt message %s: %w", messageID, err)
	}

	body := fmail.Body{
		MessageID: messageID,
		MIMEType:  string(msg.MIMEType),
		Content:   string(dec),
	}

	for _, a := range msg.Attachments {
		body.Attachments = append(body.Attachments, fmail.Attachment{
			ID:       a.ID,
			Name:     a.Name,
			Size:     int64(a.Size),
			MIMEType: string(a.MIMEType),
		})
	}

	return body, nil
}

func (p *Provider) Attachment(ctx context.Context, messageID, attachmentID string) ([]byte, error) {
	msg, err := p.client.GetMessage(ctx, messageID)
	if err != nil {
		return nil, fmt.Errorf("get message %s: %w", messageID, err)
	}

	kr, ok := p.addrKRs[msg.AddressID]
	if !ok {
		return nil, fmt.Errorf("no unlocked key ring for address %s", msg.AddressID)
	}

	var att *proton.Attachment

	for i := range msg.Attachments {
		if msg.Attachments[i].ID == attachmentID {
			att = &msg.Attachments[i]
			break
		}
	}

	if att == nil {
		return nil, fmt.Errorf("attachment %s: %w", attachmentID, fmail.ErrNotFound)
	}

	raw, err := p.client.GetAttachment(ctx, attachmentID)
	if err != nil {
		return nil, fmt.Errorf("get attachment %s: %w", attachmentID, err)
	}

	// KeyPackets is base64 on the wire; the session key for this attachment
	// lives there, and the bytes fetched above are the data packet.
	keyPacket, err := base64.StdEncoding.DecodeString(att.KeyPackets)
	if err != nil {
		return nil, fmt.Errorf("decode attachment key packets: %w", err)
	}

	split := crypto.NewPGPSplitMessage(keyPacket, raw)

	plain, err := kr.Decrypt(split.GetPGPMessage(), nil, crypto.GetUnixTime())
	if err != nil {
		return nil, fmt.Errorf("decrypt attachment %s: %w", attachmentID, err)
	}

	return plain.GetBinary(), nil
}

func (p *Provider) Label(ctx context.Context, conversationIDs []string, boxID string) error {
	if len(conversationIDs) == 0 {
		return nil
	}

	return p.api.LabelConversations(ctx, conversationIDs, boxID)
}

func (p *Provider) Unlabel(ctx context.Context, conversationIDs []string, boxID string) error {
	if len(conversationIDs) == 0 {
		return nil
	}

	return p.api.UnlabelConversations(ctx, conversationIDs, boxID)
}

func (p *Provider) CreateBox(ctx context.Context, name string, kind fmail.BoxKind, color string) (fmail.Box, error) {
	t := proton.LabelTypeLabel
	if kind == fmail.BoxFolder {
		t = proton.LabelTypeFolder
	}

	if color == "" {
		color = "#8080FF"
	}

	l, err := p.client.CreateLabel(ctx, proton.CreateLabelReq{
		Name:  name,
		Color: color,
		Type:  t,
	})
	if err != nil {
		return fmail.Box{}, fmt.Errorf("create label %q: %w", name, err)
	}

	return toBox(l), nil
}

func (p *Provider) MarkRead(ctx context.Context, conversationIDs []string) error {
	if len(conversationIDs) == 0 {
		return nil
	}

	return p.api.MarkConversationsRead(ctx, conversationIDs)
}

func (p *Provider) MarkUnread(ctx context.Context, conversationIDs []string, boxID string) error {
	if len(conversationIDs) == 0 {
		return nil
	}

	if boxID == "" {
		boxID = protonapi.InboxLabel
	}

	return p.api.MarkConversationsUnread(ctx, conversationIDs, boxID)
}

func (p *Provider) Draft(ctx context.Context, d fmail.Draft) (fmail.Draft, error) {
	addrID, err := p.primaryAddressID()
	if err != nil {
		return fmail.Draft{}, err
	}

	sender := p.addressByID(addrID)
	if sender == nil {
		return fmail.Draft{}, errors.New("primary address vanished")
	}

	tpl := proton.DraftTemplate{
		Subject:  d.Subject,
		Sender:   &mail.Address{Name: sender.DisplayName, Address: sender.Email},
		ToList:   toProtonAddresses(d.To),
		CCList:   toProtonAddresses(d.CC),
		BCCList:  toProtonAddresses(d.BCC),
		Body:     d.Body,
		MIMEType: toMIMEType(d.MIMEType),
	}

	kr := p.addrKRs[addrID]

	if d.ID == "" {
		req := proton.CreateDraftReq{Message: tpl}

		// ParentID and Action only apply to a reply; a fresh compose must not
		// set them or the API rejects the draft.
		if d.InReplyTo != "" {
			req.ParentID = d.InReplyTo
			req.Action = proton.ReplyAction
		}

		msg, err := p.client.CreateDraft(ctx, kr, req)
		if err != nil {
			return fmail.Draft{}, fmt.Errorf("create draft: %w", err)
		}

		d.ID = msg.ID

		return d, nil
	}

	if _, err := p.client.UpdateDraft(ctx, d.ID, kr, proton.UpdateDraftReq{Message: tpl}); err != nil {
		return fmail.Draft{}, fmt.Errorf("update draft %s: %w", d.ID, err)
	}

	return d, nil
}

func (p *Provider) Drafts(ctx context.Context) ([]fmail.Message, error) {
	meta, err := p.client.GetMessageMetadataPage(ctx, 0, 150, proton.MessageFilter{
		LabelID: proton.DraftsLabel,
		Desc:    proton.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("get drafts: %w", err)
	}

	out := make([]fmail.Message, 0, len(meta))
	for _, m := range meta {
		out = append(out, toUpstreamMessage(m))
	}

	return out, nil
}

func (p *Provider) Newsletters(ctx context.Context) ([]fmail.Newsletter, error) {
	subs, err := p.api.NewsletterSubscriptions(ctx, protonapi.NewsletterFilter{
		Sort: protonapi.NewsletterSortRecentlyReceived,
	})
	if err != nil {
		return nil, fmt.Errorf("get newsletter subscriptions: %w", err)
	}

	out := make([]fmail.Newsletter, 0, len(subs))
	for _, s := range subs {
		out = append(out, toNewsletter(s))
	}

	return out, nil
}

func (p *Provider) RouteNewsletter(ctx context.Context, newsletterID, moveToBoxID string, markAsRead bool) error {
	req := protonapi.UpdateNewsletterSubscriptionReq{
		MarkAsRead: &markAsRead,
	}

	if moveToBoxID != "" {
		req.MoveToFolder = &moveToBoxID
	}

	if _, err := p.api.UpdateNewsletterSubscription(ctx, newsletterID, req); err != nil {
		return fmt.Errorf("route newsletter %s: %w", newsletterID, err)
	}

	return nil
}

func (p *Provider) Cursor(ctx context.Context) (string, error) {
	id, err := p.api.LatestEventID(ctx)
	if err != nil {
		return "", fmt.Errorf("get latest event id: %w", err)
	}

	return id, nil
}

func (p *Provider) Poll(ctx context.Context, cursor string) (fmail.Delta, error) {
	if cursor == "" {
		id, err := p.Cursor(ctx)
		if err != nil {
			return fmail.Delta{}, err
		}

		return fmail.Delta{Cursor: id, Resync: true}, nil
	}

	events, err := p.api.Events(ctx, cursor)
	if err != nil {
		return fmail.Delta{}, fmt.Errorf("get event: %w", err)
	}

	out := fmail.Delta{Cursor: cursor}

	for _, e := range events {
		out.Cursor = e.EventID

		// A Refresh flag means our cursor is too old to reconcile from and the
		// cache has to be rebuilt from scratch.
		if e.Refresh != 0 {
			out.Resync = true
		}

		for _, ce := range e.Conversations {
			out.Conversations = append(out.Conversations, fmail.ConversationChange{
				Kind:         toChangeKind(ce.Action),
				ID:           ce.ID,
				Conversation: toConversation(ce.Conversation),
			})
		}

		for _, me := range e.Messages {
			out.Messages = append(out.Messages, fmail.MessageChange{
				Kind:    toChangeKind(me.Action),
				ID:      me.ID,
				Message: toMessage(me.Message),
			})
		}

		for _, le := range e.Labels {
			out.Boxes = append(out.Boxes, fmail.BoxChange{
				Kind: toChangeKind(le.Action),
				ID:   le.ID,
				Box: fmail.Box{
					ID:    le.Label.ID,
					Name:  le.Label.Name,
					Kind:  toBoxKind(proton.LabelType(le.Label.Type), le.Label.ID),
					Color: le.Label.Color,
				},
			})
		}
	}

	return out, nil
}

// Send sends an existing draft.
//
// Proton does not accept a plain "send this": every recipient gets their own
// encryption package, built from that recipient's public key and the send
// preferences implied by whether they are a Proton user.
func (p *Provider) Send(ctx context.Context, draftID string) (fmail.Message, error) {
	draft, err := p.client.GetMessage(ctx, draftID)
	if err != nil {
		return fmail.Message{}, fmt.Errorf("get draft %s: %w", draftID, err)
	}

	kr, ok := p.addrKRs[draft.AddressID]
	if !ok {
		return fmail.Message{}, fmt.Errorf("no unlocked key ring for address %s", draft.AddressID)
	}

	body, err := draft.Decrypt(kr)
	if err != nil {
		return fmail.Message{}, fmt.Errorf("decrypt draft %s: %w", draftID, err)
	}

	recipients := allRecipients(draft)
	if len(recipients) == 0 {
		return fmail.Message{}, errors.New("draft has no recipients")
	}

	prefs := make(map[string]proton.SendPreferences, len(recipients))

	for _, addr := range recipients {
		pubKeys, recType, err := p.client.GetPublicKeys(ctx, addr)
		if err != nil {
			return fmail.Message{}, fmt.Errorf("get public keys for %s: %w", addr, err)
		}

		pref := proton.SendPreferences{MIMEType: draft.MIMEType}

		if recType == proton.RecipientTypeInternal && len(pubKeys) > 0 {
			pubKR, err := pubKeys.GetKeyRing()
			if err != nil {
				return fmail.Message{}, fmt.Errorf("key ring for %s: %w", addr, err)
			}

			pref.Encrypt = true
			pref.PubKey = pubKR
			pref.SignatureType = proton.DetachedSignature
			pref.EncryptionScheme = proton.InternalScheme
		} else {
			// No usable key: send in the clear. Proton's own clients do the
			// same, and refusing here would make the tool unable to mail
			// anyone outside Proton.
			pref.Encrypt = false
			pref.SignatureType = proton.NoSignature
			pref.EncryptionScheme = proton.ClearScheme
			pref.MIMEType = rfc822.TextPlain
		}

		prefs[addr] = pref
	}

	req := &proton.SendDraftReq{}

	if err := req.AddTextPackage(kr, string(body), draft.MIMEType, prefs, nil); err != nil {
		return fmail.Message{}, fmt.Errorf("build send package: %w", err)
	}

	sent, err := p.client.SendDraft(ctx, draftID, *req)
	if err != nil {
		return fmail.Message{}, fmt.Errorf("send draft %s: %w", draftID, err)
	}

	return toUpstreamMessage(sent.MessageMetadata), nil
}

// addressByID finds one of the account's own addresses.
func (p *Provider) addressByID(id string) *proton.Address {
	for i := range p.addresses {
		if p.addresses[i].ID == id {
			return &p.addresses[i]
		}
	}

	return nil
}

// allRecipients collects To, CC and BCC without duplicates.
func allRecipients(m proton.Message) []string {
	var out []string

	seen := make(map[string]bool)

	for _, list := range [][]*mail.Address{m.ToList, m.CCList, m.BCCList} {
		for _, a := range list {
			if a == nil || a.Address == "" || seen[a.Address] {
				continue
			}

			seen[a.Address] = true
			out = append(out, a.Address)
		}
	}

	return out
}

func toProtonAddresses(in []fmail.Address) []*mail.Address {
	if len(in) == 0 {
		return nil
	}

	out := make([]*mail.Address, 0, len(in))
	for _, a := range in {
		out = append(out, &mail.Address{Name: a.Name, Address: a.Address})
	}

	return out
}

func toMIMEType(s string) rfc822.MIMEType {
	switch s {
	case "", "text/plain":
		return rfc822.TextPlain
	case "text/html":
		return rfc822.TextHTML
	default:
		return rfc822.MIMEType(s)
	}
}

var _ fmail.Provider = (*Provider)(nil)
