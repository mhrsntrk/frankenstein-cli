package tui

// Region is a clickable rectangle inside a rendered block, in the block's own
// coordinates: (0,0) is the block's top-left cell. Renderers that draw
// clickable things return the regions alongside the string, and the mouse
// handler translates a click into the block's coordinates before matching.
type Region struct {
	// ID names what was clicked: "row:3", "star:2", "send", "close",
	// "minimize", "maximize", "field:to", "field:body", ...
	ID string

	// X, Y is the top-left cell, W, H the size in cells.
	X, Y, W, H int
}

// hit returns the ID of the topmost region containing (x, y), searching from
// the end so regions drawn later win.
func hit(regions []Region, x, y int) (string, bool) {
	for i := len(regions) - 1; i >= 0; i-- {
		r := regions[i]
		if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
			return r.ID, true
		}
	}

	return "", false
}
