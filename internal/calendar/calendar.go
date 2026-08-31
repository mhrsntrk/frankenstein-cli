// Package calendar defines the provider-neutral calendar model.
//
// As with mail, the Google types stay in internal/calendar/google. The line is
// softer here than the Proton one and is not enforced by CI: internal/cli
// constructs the Google client directly, because the OAuth setup command has
// to talk about client IDs and tokens to do its job.
package calendar

import (
	"context"
	"errors"
	"time"
)

// ErrNotConfigured means no OAuth client has been set up yet.
var ErrNotConfigured = errors.New("calendar is not configured; run `frankenstein calendar setup`")

// Calendar is one calendar the account can see.
type Calendar struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Primary  bool   `json:"primary,omitempty"`
	TimeZone string `json:"time_zone,omitempty"`

	// Color is the provider's own colour for the calendar, as a hex string.
	// Showing several calendars at once is only readable if each keeps the
	// colour its owner already recognises.
	Color string `json:"color,omitempty"`
}

// Event is a calendar entry.
type Event struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Location string `json:"location,omitempty"`
	Notes    string `json:"notes,omitempty"`

	Start time.Time `json:"start"`
	End   time.Time `json:"end"`

	// AllDay events carry a date rather than an instant, which matters for
	// display and for not shifting them across a timezone boundary.
	AllDay bool `json:"all_day,omitempty"`

	Attendees []string `json:"attendees,omitempty"`
	Status    string   `json:"status,omitempty"`
	Link      string   `json:"link,omitempty"`

	// CalendarID and Color say which calendar an event came from, so a view
	// showing several at once can tell them apart.
	CalendarID string `json:"calendar_id,omitempty"`
	Color      string `json:"color,omitempty"`
}

// Duration is how long the event runs.
func (e Event) Duration() time.Duration { return e.End.Sub(e.Start) }

// EventDraft is a new or edited event.
type EventDraft struct {
	ID       string
	Title    string
	Location string
	Notes    string

	Start time.Time
	End   time.Time

	AllDay    bool
	Attendees []string
}

// Provider is a calendar backend.
type Provider interface {
	Calendars(ctx context.Context) ([]Calendar, error)

	// Events reads one calendar. EventsFrom reads several and tags each event
	// with where it came from.
	Events(ctx context.Context, calendarID string, from, to time.Time) ([]Event, error)
	EventsFrom(ctx context.Context, calendarIDs []string, from, to time.Time) ([]Event, error)
	CreateEvent(ctx context.Context, calendarID string, d EventDraft) (Event, error)
	UpdateEvent(ctx context.Context, calendarID string, d EventDraft) (Event, error)
	DeleteEvent(ctx context.Context, calendarID, eventID string) error
}

// PrimaryAlias is the identifier Google accepts for the account's default
// calendar. The calendar list answers with the real address instead, so
// anything comparing a configured ID against a listed one has to resolve it.
const PrimaryAlias = "primary"

// ResolveShown turns configured IDs into the set that actually appears in a
// calendar list, expanding the primary alias to whichever calendar is flagged
// as the default.
func ResolveShown(ids []string, calendars []Calendar) map[string]bool {
	out := make(map[string]bool, len(ids))

	for _, id := range ids {
		if id != PrimaryAlias {
			out[id] = true

			continue
		}

		for _, c := range calendars {
			if c.Primary {
				out[c.ID] = true
			}
		}
	}

	return out
}
