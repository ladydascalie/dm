package vt

// Grid returns the terminal's visible screen. The grid is live state, not a
// copy: a caller reads cells, the cursor, the modes and the size from it, and
// Resize changes it. The esctest adapter needs this view because Term exposes
// no other way to read what it parsed.
func (t *Term) Grid() *Grid {
	if t == nil {
		return nil
	}
	return t.grid
}
