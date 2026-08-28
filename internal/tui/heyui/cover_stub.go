package heyui

// Cover art is a HEY feature: a picture across the top of the Imbox. Proton has
// no equivalent, so the renderer copied from hey-cli keeps its cover branches
// and they are always the "none" path.
//
// Kept rather than edited out so the copied file stays close to upstream and
// stays easy to diff against it.

type coverPreset string

const (
	coverNone coverPreset = ""

	// coverMinRows is the height below which upstream refuses to draw a cover.
	coverMinRows = 5
)

// coverColorless is set by the theme code when the terminal cannot show the
// cover's colours. Nothing reads it here; it exists so the copied theme file
// stays byte-for-byte comparable with upstream.
var coverColorless = false

// coverRenderer draws nothing here.
type coverRenderer struct{}

// view returns the empty string: there is never a cover to draw.
func (coverRenderer) view(coverPreset, int, int) string { return "" }
