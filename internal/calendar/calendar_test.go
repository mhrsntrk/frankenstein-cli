package calendar_test

import (
	"testing"

	"github.com/mhrsntrk/frankenstein-cli/internal/calendar"
)

// "primary" is an alias the calendar list never answers with: it returns the
// account's real address. Comparing the two literally left the default
// calendar looking unselected.
func TestResolveShownExpandsThePrimaryAlias(t *testing.T) {
	cals := []calendar.Calendar{
		{ID: "en.spain#holiday@group.v.calendar.google.com", Name: "Holidays"},
		{ID: "someone@example.com", Name: "Personal", Primary: true},
	}

	got := calendar.ResolveShown([]string{"primary"}, cals)

	if !got["someone@example.com"] {
		t.Errorf("primary did not resolve to the default calendar: %v", got)
	}

	if got["primary"] {
		t.Error("the alias was kept as though it were an ID")
	}
}

func TestResolveShownKeepsRealIDs(t *testing.T) {
	cals := []calendar.Calendar{
		{ID: "a@example.com", Primary: true},
		{ID: "b@example.com"},
	}

	got := calendar.ResolveShown([]string{"b@example.com"}, cals)

	if !got["b@example.com"] || len(got) != 1 {
		t.Errorf("resolved to %v, want just b", got)
	}
}

func TestShownFallsBackForAnOlderConfig(t *testing.T) {
	// A config written before multiple calendars existed has only the one.
	c := calendar.Calendar{}
	_ = c

	for _, tc := range []struct {
		name string
		cfg  shownSource
		want []string
	}{
		{"neither set", shownSource{}, []string{"primary"}},
		{"only the old field", shownSource{one: "a@example.com"}, []string{"a@example.com"}},
		{"the new field wins", shownSource{one: "a@example.com", many: []string{"b@example.com"}}, []string{"b@example.com"}},
	} {
		got := tc.cfg.Shown()

		if len(got) != len(tc.want) || got[0] != tc.want[0] {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// shownSource mirrors config.CalendarConfig's fallback, kept here so the rule
// is tested without this package depending on config.
type shownSource struct {
	one  string
	many []string
}

func (s shownSource) Shown() []string {
	if len(s.many) > 0 {
		return s.many
	}

	if s.one != "" {
		return []string{s.one}
	}

	return []string{calendar.PrimaryAlias}
}
