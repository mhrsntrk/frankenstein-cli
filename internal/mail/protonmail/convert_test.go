package protonmail

import (
	"testing"

	"github.com/mhrsntrk/frankenstein-cli/internal/protonapi"
)

// A conversation listed under a label arrives with Context* rollups scoped to
// that label. The cache holds one row per thread, so the conversion must keep
// the global numbers: writing the last-listed box's slice would drift unread
// counts and times on every replied thread.
func TestToConversationKeepsGlobalCounts(t *testing.T) {
	c := protonapi.Conversation{
		ID:             "c1",
		Subject:        "Hello",
		NumMessages:    4,
		NumUnread:      1,
		NumAttachments: 2,
		Size:           4096,
		Time:           1000,
		Order:          42,

		// The label-scoped view: fewer messages, nothing unread, older time.
		ContextNumMessages:    2,
		ContextNumUnread:      0,
		ContextNumAttachments: 1,
		ContextSize:           1024,
		ContextTime:           500,
	}

	got := toConversation(c)

	if got.NumMessages != 4 || got.NumUnread != 1 || got.NumAttachments != 2 {
		t.Errorf("box-scoped counts leaked into the conversion: %+v", got)
	}

	if got.Time.Unix() != 1000 || got.Size != 4096 {
		t.Errorf("box-scoped time or size leaked into the conversion: %+v", got)
	}
}

// A conversation as Proton's listing endpoint returns it: the global rollups
// are absent and everything lives in the Context* fields and Labels. Cached
// without the fallback, such a thread has no time and no box, so it sorts
// below every dated row and appears in no box at all.
func TestToConversationFallsBackToTheListingFields(t *testing.T) {
	c := protonapi.Conversation{
		ID:      "c1",
		Subject: "Tax and Price Updates",
		Order:   3400549248278,

		ContextNumMessages:    2,
		ContextNumUnread:      1,
		ContextNumAttachments: 3,
		ContextSize:           4096,
		ContextTime:           1756000000,

		Labels: []protonapi.ConversationLabel{{ID: "0"}, {ID: "5"}},
	}

	got := toConversation(c)

	if got.Time.Unix() != 1756000000 {
		t.Errorf("time = %v, want the ContextTime", got.Time)
	}

	if got.NumMessages != 2 || got.NumUnread != 1 || got.NumAttachments != 3 {
		t.Errorf("counts = %d/%d/%d, want 2/1/3",
			got.NumMessages, got.NumUnread, got.NumAttachments)
	}

	if got.Size != 4096 {
		t.Errorf("size = %d, want the ContextSize", got.Size)
	}

	if len(got.BoxIDs) != 2 || got.BoxIDs[0] != "0" || got.BoxIDs[1] != "5" {
		t.Errorf("boxes = %v, want the Labels ids", got.BoxIDs)
	}
}

// When the global rollups are there they win, so a thread in two boxes cannot
// have one box's scoped counts written into the single cached row.
func TestToConversationPrefersTheGlobalRollups(t *testing.T) {
	c := protonapi.Conversation{
		ID:          "c1",
		Time:        1756000000,
		NumMessages: 9,
		NumUnread:   4,
		Size:        999,
		LabelIDs:    []string{"0"},

		ContextTime:        1700000000,
		ContextNumMessages: 1,
		ContextNumUnread:   0,
		ContextSize:        1,
		Labels:             []protonapi.ConversationLabel{{ID: "7"}},
	}

	got := toConversation(c)

	if got.Time.Unix() != 1756000000 || got.NumMessages != 9 || got.NumUnread != 4 || got.Size != 999 {
		t.Errorf("global rollups lost: %+v", got)
	}

	if len(got.BoxIDs) != 1 || got.BoxIDs[0] != "0" {
		t.Errorf("boxes = %v, want the LabelIDs", got.BoxIDs)
	}
}
