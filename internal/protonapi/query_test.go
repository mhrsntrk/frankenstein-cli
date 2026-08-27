package protonapi

import "testing"

// Query parameters are where this client gets rejected: Proton answers an
// unexpected one with 400 Code 2001 and an empty message, which is hard to
// diagnose from the response alone. These pin what is actually sent.

func TestConversationFilterQuery(t *testing.T) {
	q := ConversationFilter{LabelID: "0", Desc: true}.query(2, 150)

	for k, want := range map[string]string{
		"LabelID":  "0",
		"Desc":     "1",
		"Page":     "2",
		"PageSize": "150",
	} {
		if got := q.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}

	// An empty subject must not be sent at all rather than sent empty.
	if _, ok := q["Subject"]; ok {
		t.Error("empty Subject was included in the query")
	}
}

func TestConversationFilterDescIsExplicit(t *testing.T) {
	// Desc has to be sent either way; omitting it lets the API pick, and the
	// cache assumes newest-first ordering.
	q := ConversationFilter{}.query(0, 50)

	if got := q.Get("Desc"); got != "0" {
		t.Errorf("Desc = %q, want 0", got)
	}
}

// NewsletterFilter must not grow a Sort field again. Every spelling of Sort
// tried against the live API returned 400 Code 2001, including the ones the
// response's own field names suggest.
func TestNewsletterFilterHasNoSort(t *testing.T) {
	active := true

	f := NewsletterFilter{Active: &active, SearchTerm: "weekly"}

	if f.Active == nil || !*f.Active {
		t.Error("Active did not round-trip")
	}

	if f.SearchTerm != "weekly" {
		t.Error("SearchTerm did not round-trip")
	}
}
