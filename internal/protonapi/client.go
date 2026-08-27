package protonapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// maxPageSize is the API's cap; asking for more silently returns fewer.
const maxPageSize = 150

// Client talks to the endpoints go-proton-api does not wrap.
//
// It carries the same session as the upstream client: the UID and access token
// come from the authenticated proton.Auth. Because Proton rotates tokens on
// refresh, SetAuth must be called from the upstream client's auth handler or
// this one will start returning 401 after the first rotation.
type Client struct {
	mu    sync.RWMutex
	uid   string
	token string

	host       string
	appVersion string
	hc         *http.Client
}

// New returns a client for host, identifying as appVersion.
func New(host, appVersion, uid, token string) *Client {
	return &Client{
		uid:        uid,
		token:      token,
		host:       strings.TrimSuffix(host, "/"),
		appVersion: appVersion,
		hc:         &http.Client{Timeout: 60 * time.Second},
	}
}

// SetAuth updates the session after a token refresh.
func (c *Client) SetAuth(uid, token string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.uid, c.token = uid, token
}

// APIError is a non-2xx response from Proton.
type APIError struct {
	Status  int
	Code    int    `json:"Code"`
	Message string `json:"Error"`
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("proton api: %s (code %d, status %d)", e.Message, e.Code, e.Status)
	}

	return fmt.Sprintf("proton api: status %d", e.Status)
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var reader io.Reader

	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}

		reader = bytes.NewReader(b)
	}

	target := c.host + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return err
	}

	c.mu.RLock()
	uid, token := c.uid, c.token
	c.mu.RUnlock()

	req.Header.Set("x-pm-uid", uid)
	req.Header.Set("x-pm-appversion", c.appVersion)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.protonmail.v1+json")

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("%s %s: read body: %w", method, path, err)
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		apiErr := &APIError{Status: res.StatusCode}
		_ = json.Unmarshal(raw, apiErr)

		return apiErr
	}

	if out == nil {
		return nil
	}

	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s %s: decode: %w", method, path, err)
	}

	return nil
}

// ConversationFilter selects which conversations to list.
type ConversationFilter struct {
	LabelID string
	Subject string

	// Desc lists newest first.
	Desc bool
}

func (f ConversationFilter) query(page, pageSize int) url.Values {
	q := url.Values{}

	if f.LabelID != "" {
		q.Set("LabelID", f.LabelID)
	}

	if f.Subject != "" {
		q.Set("Subject", f.Subject)
	}

	if f.Desc {
		q.Set("Desc", "1")
	} else {
		q.Set("Desc", "0")
	}

	q.Set("Page", strconv.Itoa(page))
	q.Set("PageSize", strconv.Itoa(pageSize))

	return q
}

// Conversations lists one page of threads.
func (c *Client) Conversations(ctx context.Context, page, pageSize int, filter ConversationFilter) ([]Conversation, error) {
	if pageSize <= 0 || pageSize > maxPageSize {
		pageSize = maxPageSize
	}

	var res struct {
		Total         int            `json:"Total"`
		Conversations []Conversation `json:"Conversations"`
	}

	if err := c.do(ctx, http.MethodGet, "/mail/v4/conversations",
		filter.query(page, pageSize), nil, &res); err != nil {
		return nil, err
	}

	return res.Conversations, nil
}

// Conversation returns one thread together with its messages.
//
// This is the endpoint that makes a separate /mail/v4/messages call
// unnecessary: the messages come back with every field, including the ones
// upstream's struct drops.
func (c *Client) Conversation(ctx context.Context, id string) (Conversation, []Message, error) {
	var res struct {
		Conversation Conversation `json:"Conversation"`
		Messages     []Message    `json:"Messages"`
	}

	if err := c.do(ctx, http.MethodGet,
		"/mail/v4/conversations/"+url.PathEscape(id), nil, nil, &res); err != nil {
		return Conversation{}, nil, err
	}

	return res.Conversation, res.Messages, nil
}

// ConversationCounts returns per-label totals and unread counts. One request
// covers every box, which is what makes the mailbox list cheap.
func (c *Client) ConversationCounts(ctx context.Context) ([]Count, error) {
	var res struct {
		Counts []Count `json:"Counts"`
	}

	if err := c.do(ctx, http.MethodGet, "/mail/v4/conversations/count", nil, nil, &res); err != nil {
		return nil, err
	}

	return res.Counts, nil
}

// LabelConversations adds a label to threads, which Proton applies to every
// message in them.
func (c *Client) LabelConversations(ctx context.Context, ids []string, labelID string) error {
	return c.conversationAction(ctx, "label", ids, labelID)
}

// UnlabelConversations removes a label from threads.
func (c *Client) UnlabelConversations(ctx context.Context, ids []string, labelID string) error {
	return c.conversationAction(ctx, "unlabel", ids, labelID)
}

// MarkConversationsRead marks every message in the threads read.
func (c *Client) MarkConversationsRead(ctx context.Context, ids []string) error {
	return c.conversationAction(ctx, "read", ids, "")
}

// MarkConversationsUnread marks threads unread. Unlike the others this is
// label-scoped: Proton needs to know which box the unread state applies to.
func (c *Client) MarkConversationsUnread(ctx context.Context, ids []string, labelID string) error {
	return c.conversationAction(ctx, "unread", ids, labelID)
}

func (c *Client) conversationAction(ctx context.Context, action string, ids []string, labelID string) error {
	for _, chunk := range chunkStrings(ids, maxPageSize) {
		body := struct {
			IDs     []string `json:"IDs"`
			LabelID string   `json:"LabelID,omitempty"`
		}{IDs: chunk, LabelID: labelID}

		if err := c.do(ctx, http.MethodPut,
			"/mail/v4/conversations/"+action, nil, body, nil); err != nil {
			return fmt.Errorf("%s conversations: %w", action, err)
		}
	}

	return nil
}

// LatestEventID returns the current position in the event stream.
func (c *Client) LatestEventID(ctx context.Context) (string, error) {
	var res struct {
		EventID string `json:"EventID"`
	}

	if err := c.do(ctx, http.MethodGet, "/core/v4/events/latest", nil, nil, &res); err != nil {
		return "", err
	}

	return res.EventID, nil
}

// Events returns the deltas from a cursor forward, following More until the
// stream is drained.
//
// A non-zero Refresh on any event means the cursor is too old to reconcile
// from and the caller must rebuild its cache.
func (c *Client) Events(ctx context.Context, cursor string) ([]Event, error) {
	var out []Event

	for i := 0; i < 100; i++ {
		var e Event

		if err := c.do(ctx, http.MethodGet,
			"/core/v4/events/"+url.PathEscape(cursor), nil, nil, &e); err != nil {
			return out, err
		}

		out = append(out, e)

		// A repeated event ID would loop forever; treat it as the end.
		if e.More == 0 || e.EventID == "" || e.EventID == cursor {
			return out, nil
		}

		cursor = e.EventID
	}

	return out, nil
}

// NewsletterFilter selects which subscriptions to list.
//
// There is deliberately no Sort field. The endpoint rejects a Sort parameter
// with 400 Code 2001 in every spelling tried against the live API, so ordering
// is done by the caller after the fact. PageSize and Active are accepted.
type NewsletterFilter struct {
	// Active restricts to lists not yet unsubscribed from.
	Active *bool

	SearchTerm string
}

// NewsletterSubscriptions walks every page and returns all mailing lists.
func (c *Client) NewsletterSubscriptions(ctx context.Context, filter NewsletterFilter) ([]NewsletterSubscription, error) {
	var (
		all    []NewsletterSubscription
		cursor string
	)

	for i := 0; i < 100; i++ {
		q := url.Values{}
		q.Set("PageSize", "100")

		if cursor != "" {
			q.Set("Cursor", cursor)
		}

		if filter.Active != nil {
			if *filter.Active {
				q.Set("Active", "1")
			} else {
				q.Set("Active", "0")
			}
		}

		if filter.SearchTerm != "" {
			q.Set("SearchTerm", filter.SearchTerm)
		}

		var res struct {
			NewsletterSubscriptions []NewsletterSubscription `json:"NewsletterSubscriptions"`

			PageInfo struct {
				NextCursor string `json:"NextCursor"`
				Total      int    `json:"Total"`
			} `json:"PageInfo"`
		}

		if err := c.do(ctx, http.MethodGet,
			"/mail/v4/newsletter-subscriptions", q, nil, &res); err != nil {
			return all, err
		}

		all = append(all, res.NewsletterSubscriptions...)

		next := res.PageInfo.NextCursor
		if next == "" || next == cursor || len(res.NewsletterSubscriptions) == 0 {
			return all, nil
		}

		cursor = next
	}

	return all, nil
}

// UpdateNewsletterSubscriptionReq changes a list's server-side handling.
// Fields left nil are not modified.
type UpdateNewsletterSubscriptionReq struct {
	MarkAsRead   *bool   `json:"MarkAsRead,omitempty"`
	MoveToFolder *string `json:"MoveToFolder,omitempty"`
	Spam         *bool   `json:"Spam,omitempty"`
	Hidden       *bool   `json:"Hidden,omitempty"`
}

// UpdateNewsletterSubscription sets the auto mark-as-read and move-to-folder
// rules Proton applies on its own servers.
//
// NOTE: the read path for subscriptions is verified against the live API; this
// write path is not. The request shape is inferred from the fields the read
// returns. Verify before relying on it.
func (c *Client) UpdateNewsletterSubscription(ctx context.Context, id string, req UpdateNewsletterSubscriptionReq) (NewsletterSubscription, error) {
	var res struct {
		NewsletterSubscription NewsletterSubscription `json:"NewsletterSubscription"`
	}

	if err := c.do(ctx, http.MethodPut,
		"/mail/v4/newsletter-subscriptions/"+url.PathEscape(id), nil, req, &res); err != nil {
		return NewsletterSubscription{}, err
	}

	return res.NewsletterSubscription, nil
}

func chunkStrings(in []string, size int) [][]string {
	if size <= 0 || len(in) == 0 {
		return nil
	}

	var out [][]string

	for i := 0; i < len(in); i += size {
		end := i + size
		if end > len(in) {
			end = len(in)
		}

		out = append(out, in[i:end])
	}

	return out
}
