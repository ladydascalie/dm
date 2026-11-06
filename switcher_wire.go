package main

import (
	"bytes"
	"strings"

	"dm/internal/cells"
	"dm/internal/layout"
	"dm/internal/switcher"
	"dm/internal/wire"

	"github.com/hinshun/vt10x"
)

// paintPaneFrom paints a pane's rows for the switcher preview, with no
// selection or hover.
func paintPaneFrom(buf *bytes.Buffer, vt vt10x.Terminal, r layout.Rect, row0 int) {
	drawPaneFrom(buf, vt, r, row0, nil, nil)
}

// openSwitcher raises the native switcher over the panes. A second call while
// it is up closes it and picks nothing.
func openSwitcher(t *tui) {
	if t.sw != nil {
		closeSwitcher(t)
		return
	}
	if t.snap == nil || t.snap.Cols < 1 || t.snap.Rows < 1 {
		return
	}
	t.sw = switcher.NewSwitcher(switcher.PaletteRect(t.snap.Cols, t.snap.Rows), t.snap)
	t.disarm()
	t.dirty = true
}

// closeSwitcher drops the float without acting on a choice. Its buffered input
// goes with it, so no key pressed over the float reaches a pane afterwards.
func closeSwitcher(t *tui) {
	if t.sw == nil {
		return
	}
	t.sw = nil
	t.inbuf = nil
	t.pending = false
	t.dirty = true
}

// dispatchPick turns a switcher row target into the action that goes there.
// A workspace is sN, a window is wN, a pane is wNpM.
func (t *tui) dispatchPick(target string) {
	switch {
	case strings.HasPrefix(target, "s") && cells.AllDigits(target[1:]):
		t.action("focus-workspace", func(m *wire.Msg) { m.Target = target })
	case strings.HasPrefix(target, "w") && strings.Contains(target, "p"):
		t.action("focus", func(m *wire.Msg) { m.Target = target })
	case strings.HasPrefix(target, "w") && cells.AllDigits(target[1:]):
		t.action("focus-window", func(m *wire.Msg) { m.Target = target })
	default:
		t.notify("switcher: no target " + target)
	}
}
