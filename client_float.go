package main

// AddFloatingPane appends a floating pane to the client's render stack.
func (t *tui) AddFloatingPane(f *FloatingPane) {
	t.floats = append(t.floats, f)
	t.dirty = true
}

// RemoveFloatingPane removes a floating pane by ID.
func (t *tui) RemoveFloatingPane(id string) {
	for i, f := range t.floats {
		if f.ID == id {
			t.floats = append(t.floats[:i], t.floats[i+1:]...)
			t.dirty = true
			return
		}
	}
}
