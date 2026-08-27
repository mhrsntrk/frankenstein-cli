// Package calendar defines the provider-neutral calendar model.
//
// As with mail, nothing outside internal/calendar/... imports a Google type.
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
	Events(ctx context.Context, calendarID string, from, to time.Time) ([]Event, error)
	CreateEvent(ctx context.Context, calendarID string, d EventDraft) (Event, error)
	UpdateEvent(ctx context.Context, calendarID string, d EventDraft) (Event, error)
	DeleteEvent(ctx context.Context, calendarID, eventID string) error
}
