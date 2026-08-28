// Package google implements calendar.Provider against Google Calendar.
//
// Proton Calendar has no CalDAV and no public API, so there was never a real
// choice here: the calendar lives in Google and this is how it is reached.
package google

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
	googleoauth "golang.org/x/oauth2/google"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
	"google.golang.org/api/tasks/v1"

	fcal "github.com/mhrsntrk/frankenstein-cli/internal/calendar"
)

const (
	keyringService = "frankenstein-cli"
	keyringUser    = "google-calendar-token"
)

// Scopes requested. Full read/write on events, read-only on the calendar list,
// and Tasks, because todos ride the same Google authorisation rather than
// making the user run a second consent flow.
var Scopes = []string{
	calendar.CalendarEventsScope,
	calendar.CalendarReadonlyScope,
	tasks.TasksScope,
}

// Provider is a Google-backed calendar.Provider.
type Provider struct {
	svc *calendar.Service
}

// OAuthConfig builds the OAuth2 config for a loopback flow.
//
// The redirect URL is filled in at authorisation time, once we know which
// port the local listener actually got.
func OAuthConfig(clientID, clientSecret string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     googleoauth.Endpoint,
		Scopes:       Scopes,
	}
}

// SaveToken persists an OAuth token in the same keyring the Proton session
// uses.
func SaveToken(tok *oauth2.Token) error {
	b, err := json.Marshal(tok)
	if err != nil {
		return err
	}

	return keyring.Set(keyringService, keyringUser, string(b))
}

// LoadToken reads the stored OAuth token.
func LoadToken() (*oauth2.Token, error) {
	raw, err := keyring.Get(keyringService, keyringUser)
	if err != nil {
		return nil, fcal.ErrNotConfigured
	}

	var tok oauth2.Token
	if err := json.Unmarshal([]byte(raw), &tok); err != nil {
		return nil, fmt.Errorf("parse stored calendar token: %w", err)
	}

	return &tok, nil
}

// ClearToken forgets the stored OAuth token.
func ClearToken() error {
	if err := keyring.Delete(keyringService, keyringUser); err != nil {
		return nil // already gone
	}

	return nil
}

// Authorize runs the loopback OAuth2 flow: bind a local port, open the consent
// URL, and wait for Google to redirect back with a code.
//
// onURL is called with the URL the user must visit. It is a callback rather
// than a print so the TUI can show it in place.
func Authorize(ctx context.Context, cfg *oauth2.Config, onURL func(string)) (*oauth2.Token, error) {
	// Port 0 lets the OS pick; Google accepts any loopback port for a desktop
	// client, which is why this flow needs no fixed redirect registration.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("bind loopback listener: %w", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	cfg.RedirectURL = fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	state, err := randomState()
	if err != nil {
		return nil, err
	}

	type result struct {
		code string
		err  error
	}

	results := make(chan result, 1)

	srv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()

			if got := q.Get("state"); got != state {
				http.Error(w, "state mismatch", http.StatusBadRequest)
				results <- result{err: fmt.Errorf("oauth state mismatch")}

				return
			}

			if e := q.Get("error"); e != "" {
				http.Error(w, e, http.StatusBadRequest)
				results <- result{err: fmt.Errorf("authorisation refused: %s", e)}

				return
			}

			code := q.Get("code")
			if code == "" {
				http.Error(w, "no code", http.StatusBadRequest)
				results <- result{err: fmt.Errorf("no authorisation code in callback")}

				return
			}

			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<!doctype html><meta charset="utf-8">
<title>frankenstein</title>
<body style="font-family:system-ui;padding:3rem">
<h1>Done.</h1><p>You can close this tab and go back to the terminal.</p>`)

			results <- result{code: code}
		}),
	}

	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	authURL := cfg.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		// Without this Google will not re-issue a refresh token to an account
		// that has already granted consent, and the tool would work until the
		// access token expired and then quietly stop.
		oauth2.SetAuthURLParam("prompt", "consent"))

	onURL(authURL)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-results:
		if res.err != nil {
			return nil, res.err
		}

		tok, err := cfg.Exchange(ctx, res.code)
		if err != nil {
			return nil, fmt.Errorf("exchange authorisation code: %w", err)
		}

		return tok, nil
	case <-time.After(5 * time.Minute):
		return nil, fmt.Errorf("timed out waiting for authorisation")
	}
}

func randomState() (string, error) {
	b := make([]byte, 16)

	if _, err := readRandom(b); err != nil {
		return "", err
	}

	return url.QueryEscape(fmt.Sprintf("%x", b)), nil
}

// New builds a provider from a stored token, refreshing it as needed and
// persisting any new refresh token.
func New(ctx context.Context, cfg *oauth2.Config, tok *oauth2.Token) (*Provider, error) {
	src := cfg.TokenSource(ctx, tok)

	// Persist a refreshed token so the next run does not have to.
	src = oauth2.ReuseTokenSource(nil, &savingSource{src: src, last: tok})

	svc, err := calendar.NewService(ctx, option.WithTokenSource(src))
	if err != nil {
		return nil, fmt.Errorf("google calendar: %w", err)
	}

	return &Provider{svc: svc}, nil
}

// savingSource writes a token back to the keyring whenever it changes.
type savingSource struct {
	src  oauth2.TokenSource
	last *oauth2.Token
}

func (s *savingSource) Token() (*oauth2.Token, error) {
	tok, err := s.src.Token()
	if err != nil {
		return nil, err
	}

	if s.last == nil || tok.AccessToken != s.last.AccessToken {
		_ = SaveToken(tok)
		s.last = tok
	}

	return tok, nil
}

func (p *Provider) Calendars(ctx context.Context) ([]fcal.Calendar, error) {
	list, err := p.svc.CalendarList.List().Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("list calendars: %w", err)
	}

	out := make([]fcal.Calendar, 0, len(list.Items))

	for _, c := range list.Items {
		out = append(out, fcal.Calendar{
			ID:       c.Id,
			Name:     c.Summary,
			Primary:  c.Primary,
			TimeZone: c.TimeZone,
			Color:    c.BackgroundColor,
		})
	}

	return out, nil
}

func (p *Provider) Events(ctx context.Context, calendarID string, from, to time.Time) ([]fcal.Event, error) {
	if calendarID == "" {
		calendarID = "primary"
	}

	res, err := p.svc.Events.List(calendarID).
		TimeMin(from.Format(time.RFC3339)).
		TimeMax(to.Format(time.RFC3339)).
		SingleEvents(true).
		OrderBy("startTime").
		MaxResults(250).
		Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}

	out := make([]fcal.Event, 0, len(res.Items))

	for _, e := range res.Items {
		ev, err := toEvent(e)
		if err != nil {
			continue
		}

		out = append(out, ev)
	}

	return out, nil
}

func toEvent(e *calendar.Event) (fcal.Event, error) {
	ev := fcal.Event{
		ID:       e.Id,
		Title:    e.Summary,
		Location: e.Location,
		Notes:    e.Description,
		Status:   e.Status,
		Link:     e.HtmlLink,
	}

	for _, a := range e.Attendees {
		ev.Attendees = append(ev.Attendees, a.Email)
	}

	start, allDay, err := parseWhen(e.Start)
	if err != nil {
		return ev, err
	}

	end, _, err := parseWhen(e.End)
	if err != nil {
		return ev, err
	}

	ev.Start, ev.End, ev.AllDay = start, end, allDay

	return ev, nil
}

// parseWhen handles Google's two shapes: an instant for timed events, a bare
// date for all-day ones. Parsing a date as an instant would shift the event
// across a timezone boundary, which is why they are kept apart.
func parseWhen(t *calendar.EventDateTime) (time.Time, bool, error) {
	if t == nil {
		return time.Time{}, false, fmt.Errorf("event has no time")
	}

	if t.DateTime != "" {
		parsed, err := time.Parse(time.RFC3339, t.DateTime)

		return parsed, false, err
	}

	parsed, err := time.ParseInLocation("2006-01-02", t.Date, time.Local)

	return parsed, true, err
}

// EventsFrom reads several calendars and merges them in time order.
//
// The calendars are read in parallel: a person with six of them would
// otherwise wait for six round trips in a row every time the week moved. One
// calendar failing does not lose the others -- a shared calendar that has been
// revoked should not empty the whole view -- but the error is kept so the
// caller can say so.
func (p *Provider) EventsFrom(ctx context.Context, calendarIDs []string, from, to time.Time) ([]fcal.Event, error) {
	if len(calendarIDs) == 0 {
		return p.Events(ctx, "", from, to)
	}

	colours := map[string]string{}

	if cals, err := p.Calendars(ctx); err == nil {
		for _, c := range cals {
			colours[c.ID] = c.Color
		}
	}

	type result struct {
		events []fcal.Event
		err    error
	}

	results := make([]result, len(calendarIDs))

	var wg sync.WaitGroup

	for i, id := range calendarIDs {
		wg.Add(1)

		go func() {
			defer wg.Done()

			events, err := p.Events(ctx, id, from, to)
			for j := range events {
				events[j].CalendarID = id
				events[j].Color = colours[id]
			}

			results[i] = result{events: events, err: err}
		}()
	}

	wg.Wait()

	var (
		all      []fcal.Event
		firstErr error
	)

	for _, r := range results {
		if r.err != nil && firstErr == nil {
			firstErr = r.err
		}

		all = append(all, r.events...)
	}

	sort.Slice(all, func(i, j int) bool { return all[i].Start.Before(all[j].Start) })

	// Only a total failure is worth reporting: some events beat none.
	if len(all) == 0 && firstErr != nil {
		return nil, firstErr
	}

	return all, nil
}

func (p *Provider) CreateEvent(ctx context.Context, calendarID string, d fcal.EventDraft) (fcal.Event, error) {
	if calendarID == "" {
		calendarID = "primary"
	}

	created, err := p.svc.Events.Insert(calendarID, toGoogleEvent(d)).Context(ctx).Do()
	if err != nil {
		return fcal.Event{}, fmt.Errorf("create event: %w", err)
	}

	return toEvent(created)
}

func (p *Provider) UpdateEvent(ctx context.Context, calendarID string, d fcal.EventDraft) (fcal.Event, error) {
	if calendarID == "" {
		calendarID = "primary"
	}

	if d.ID == "" {
		return fcal.Event{}, fmt.Errorf("update needs an event ID")
	}

	updated, err := p.svc.Events.Update(calendarID, d.ID, toGoogleEvent(d)).Context(ctx).Do()
	if err != nil {
		return fcal.Event{}, fmt.Errorf("update event: %w", err)
	}

	return toEvent(updated)
}

func (p *Provider) DeleteEvent(ctx context.Context, calendarID, eventID string) error {
	if calendarID == "" {
		calendarID = "primary"
	}

	if err := p.svc.Events.Delete(calendarID, eventID).Context(ctx).Do(); err != nil {
		return fmt.Errorf("delete event: %w", err)
	}

	return nil
}

func toGoogleEvent(d fcal.EventDraft) *calendar.Event {
	e := &calendar.Event{
		Summary:     d.Title,
		Location:    d.Location,
		Description: d.Notes,
	}

	if d.AllDay {
		e.Start = &calendar.EventDateTime{Date: d.Start.Format("2006-01-02")}
		// Google's end date for an all-day event is exclusive, so a one-day
		// event ends on the following day.
		end := d.End
		if !end.After(d.Start) {
			end = d.Start.AddDate(0, 0, 1)
		}

		e.End = &calendar.EventDateTime{Date: end.Format("2006-01-02")}
	} else {
		e.Start = &calendar.EventDateTime{DateTime: d.Start.Format(time.RFC3339)}
		e.End = &calendar.EventDateTime{DateTime: d.End.Format(time.RFC3339)}
	}

	for _, a := range d.Attendees {
		e.Attendees = append(e.Attendees, &calendar.EventAttendee{Email: a})
	}

	return e
}

var _ fcal.Provider = (*Provider)(nil)

// readRandom is crypto/rand.Read, wrapped so the import stays local to the one
// place that needs it.
func readRandom(b []byte) (int, error) { return cryptorand.Read(b) }
