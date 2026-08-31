package render

import "testing"

// TestKeyIsUniquePerEvent guards the bug where Event.recording() set neither
// ID nor OccurrenceID, so key() returned "" for every event and the grid
// highlighted none of them.
func TestKeyIsUniquePerEvent(t *testing.T) {
	a := Event{ID: "abc123", Title: "Standup"}
	b := Event{ID: "def456", Title: "Standup"}

	if Key(a) == "" {
		t.Fatal("Key returned empty; selection.has() ignores an empty key")
	}

	if Key(a) == Key(b) {
		t.Fatalf("two events share key %q", Key(a))
	}
}
