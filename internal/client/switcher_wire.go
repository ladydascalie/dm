package client

import (
	"bytes"
	"strings"

	"dm/internal/cells"
	"dm/internal/layout"
	"dm/internal/switcher"
	"dm/internal/vt"
	"dm/internal/wire"
)

// paintPaneFrom paints a pane's rows for the switcher preview, with no
// selection or hover.
func paintPaneFrom(buf *bytes.Buffer, term *vt.Term, r layout.Rect, row0 int) {
	drawPaneFrom(buf, term, r, row0, nil, nil)
}

// openSwitcher raises the dashboard, full screen or as a centred palette. A
// second call while it is up closes it and picks nothing.
func openSwitcher(t *tui) {
	if t.sw != nil {
		closeSwitcher(t)
		return
	}
	if t.snap == nil || t.snap.Cols < 1 || t.snap.Rows < 1 {
		return
	}
	t.sw = switcher.NewSwitcher(layout.Rect{W: t.snap.Cols, H: t.snap.Rows}, t.snap)
	// The config picks the shape and the share the band opens at, and zero
	// opens the table alone. A reload does not move an open dashboard: both are
	// read when it opens, the way the ordering is.
	full := true
	if km := t.km(); km != nil {
		full = km.DashboardFullScreen()
		t.sw.SetPreviewShare(km.DashboardPreview())
	}
	t.sw.SetFullScreen(full)
	if full {
		// Full screen, the dashboard claims the screen the way the other
		// screen-owning modes do: it takes its own mode, so the frame repaints
		// whole, the tab bar takes no clicks and no hover shows through the
		// table. A palette leaves the panes live under it, so it claims nothing.
		t.mode = "dashboard"
	}
	t.disarm()
	t.dirty = true
}

// closeSwitcher drops the dashboard without acting on a choice. Its buffered
// input goes with it, so no key pressed over it reaches a pane afterwards.
func closeSwitcher(t *tui) {
	if t.sw == nil {
		return
	}
	t.sw = nil
	// The preview terminals belong to the dashboard: closed, they are dead
	// weight, and the cadence that refreshed them stops with it.
	t.swVT = nil
	if t.mode == "dashboard" {
		t.mode = ""
	}
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
