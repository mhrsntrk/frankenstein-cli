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
