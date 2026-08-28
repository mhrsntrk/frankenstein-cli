package cli

import (
	"testing"
	"time"
)

// The date typed at --end is the last day an all-day event covers, but the
// draft hands Google an exclusive end date. Passing the typed date through
// untouched cut the last day off every multi-day all-day event.
func TestDraftEndMakesTheTypedAllDayEndInclusive(t *testing.T) {
	end := time.Date(2026, 9, 3, 0, 0, 0, 0, time.Local)

	got := draftEnd(end, true)
	if want := end.AddDate(0, 0, 1); !got.Equal(want) {
		t.Errorf("draftEnd(all-day) = %v, want %v", got, want)
	}

	// A timed event's end is an instant, not a date, and stays as typed.
	if got := draftEnd(end, false); !got.Equal(end) {
		t.Errorf("draftEnd(timed) = %v, want %v", got, end)
	}
}
