// Calendar row types, copied from basecamp/hey-cli internal/tui/calendar.go
// (MIT). Kept so the week and day grids compile unchanged; the fields HEY
// serves and Google does not are simply left unset.
package render

import (
	"strconv"
	"time"
)

// Recording is anything HEY keeps on a calendar — an event, a todo, a habit, a time
// track — told apart by Type.
//
// Its times are time.Time and stay that way. They used to be strings rendered in UTC and
// parsed back, which is how the whole calendar came to be drawn in UTC: an event at 14:00Z
// sat on the 14:00 column wherever the reader was. Read Starts and Ends rather than these
// fields — those answer in the zone a reader thinks in.
type Recording struct {
	ID       int64
	ParentID int64
	Title    string
	AllDay   bool
	StartsAt time.Time
	EndsAt   time.Time
	Type     string
	// StartsAtZone and EndsAtZone are the IANA names of the zones the event was set in, and
	// they are empty for most events — HEY serves them only for one saved with a zone of its
	// own. They do not move the event: it is at one instant whatever zone it was set in, and
	// the calendar draws it where the reader's clock puts it. What they are for is the form,
	// which shows a zoned event on the clock it was written on.
	StartsAtZone string
	EndsAtZone   string
	CompletedAt  time.Time
	Label        string
	Icon         string
	// Color is a habit's own color. An event has none — what it wears is its
	// calendar's, which is CalendarColor, and the two are different fields in HEY too.
	Color string
	// Notes, Location, Link, Attendees and AttachedEntryID are what an event carries besides
	// when it is. They are read as well as written because HEY's update clears the lot of them
	// on any write that leaves them out — so an edit form that did not know them would wipe an
	// event's notes and location every time somebody changed its name.
	//
	// Notes arrive as plain text however they were written: HEY serves the description through
	// to_plain_text, so formatting cannot survive a round trip through any client.
	Notes           string
	Location        string
	Link            string
	Attendees       []string
	AttachedEntryID int64
	// Highlighted is what HEY calls the web app's "Circle event".
	Highlighted bool
	// Recurring and RepeatKind are how an event repeats. The kind is served but neither the
	// date it runs until nor the number of times, which is why an edit form can keep a
	// schedule or replace it but cannot show what it is bounded by.
	Recurring  bool
	RepeatKind string
	// ParentTitle is the title of the recording this one hangs off, which is the only place
	// some recordings have one: a countdown carries a label — "10 days before" — and no title
	// of its own, because what it is counting down to is the event above it.
	ParentTitle string
	// OccurrenceID names one instance of a repeating event — "153688907_2026-08-21", the
	// series and the day — and is what HEY serves instead of an id for an occurrence it has
	// not written down yet. Such a recording arrives with ID 0, which is why the arrows hold
	// on to key() rather than to the id: selecting by the id alone meant a repeating event
	// could never be picked out at all.
	OccurrenceID string
	// CalendarID is which calendar this is filed on, and CalendarColor how a reader tells
	// whose event they are looking at. HEY leaves the color empty for the personal calendar
	// and two calendars can wear the same one, so the id is what the edit form matches on and
	// the color only what it falls back to.
	CalendarID    int64
	CalendarColor string
	Days          []int32
}

// Starts and Ends are when a recording begins and ends, in the zone the reader is in.
//
// An all-day event is the exception, and it is not one HEY leaves to guesswork: its
// timestamp is a calendar date, which haystack serves as UTC midnight on purpose —
// `_recording.jbuilder` wraps it in `Time.use_zone("UTC")` so no offset creeps in. Convert
// that and a birthday moves to the day before for every reader west of UTC.
func (r Recording) Starts() time.Time { return localizedEventTime(r.StartsAt, r.AllDay) }

// Starts and Ends are when a recording begins and ends, in the zone the reader is in.
//
// An all-day event is the exception, and it is not one HEY leaves to guesswork: its
// timestamp is a calendar date, which haystack serves as UTC midnight on purpose —
// `_recording.jbuilder` wraps it in `Time.use_zone("UTC")` so no offset creeps in. Convert
// that and a birthday moves to the day before for every reader west of UTC.
func (r Recording) Ends() time.Time { return localizedEventTime(r.EndsAt, r.AllDay) }

// Done is whether a habit or a todo has been completed.
func (r Recording) Done() bool { return !r.CompletedAt.IsZero() }

// key is what the arrows hold on to, and what says two recordings are the same one. It is the
// id where there is one and the occurrence id otherwise — see OccurrenceID — and empty for a
// recording HEY has given neither, which is then not selectable because there would be nothing
// to act on.
func (r Recording) key() string {
	switch {
	case r.ID != 0:
		return strconv.FormatInt(r.ID, 10)
	case r.OccurrenceID != "":
		return r.OccurrenceID
	}
	return ""
}

func localizedEventTime(at time.Time, allDay bool) time.Time {
	if at.IsZero() || allDay {
		return at
	}
	return at.Local()
}
