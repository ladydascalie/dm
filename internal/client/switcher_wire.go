package client

import (
	"bytes"
	"fmt"
	"strings"

	"dm/internal/cells"
	"dm/internal/layout"
	"dm/internal/switcher"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"
)

// paintPaneFrom paints a pane's rows for the switcher preview, with no
// selection or hover.
func paintPaneFrom(buf *bytes.Buffer, term *vt.Term, r layout.Rect, row0 int) {
	drawPaneFrom(buf, term, r, row0, nil, nil)
}

// openSwitcher raises the dashboard over the whole screen. A second call while
// it is up closes it and picks nothing.
func openSwitcher(t *tui) {
	if t.sw != nil {
		closeSwitcher(t)
		return
	}
	if t.snap == nil || t.snap.Cols < 1 || t.snap.Rows < 1 {
		return
	}
	// The dashboard claims the screen the way the other screen-owning modes do:
	// it takes its own mode, so the frame repaints whole, the tab bar takes no
	// clicks and no hover shows through the table.
	t.sw = switcher.NewSwitcher(layout.Rect{W: t.snap.Cols, H: t.snap.Rows}, t.snap)
	t.mode = "dashboard"
	t.swInput = false
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
	t.swInput = false
	if t.mode == "dashboard" {
		t.mode = ""
	}
	t.inbuf = nil
	t.pending = false
	t.dirty = true
}

// switcherInputLine is the dashboard's footer while input mode is on: it names
// the pane that takes the keys and the key that leaves.
func switcherInputLine(t *tui) string {
	if t.sw == nil {
		return ""
	}
	pane := t.sw.Target()
	if pane == "" {
		return " input  no pane selected "
	}
	return " input  keys go to " + pane + "  esc leaves "
}

// drawSwitcherMode repaints the dashboard's footer row for a mode that changes
// what the keys do, so the hints beneath the table never lie.
func drawSwitcherMode(buf *bytes.Buffer, th theme.Theme, r layout.Rect, line string) {
	if r.W < 4 || r.H < 8 || line == "" {
		return
	}
	x, y, w := r.X+1, r.Y+r.H-2, r.W-2
	if x < 0 || y < 0 || w <= 0 {
		return
	}
	text := cells.Clip(line, w)
	if text == "" {
		return
	}
	fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m%s%s%s", y+1, x+1,
		th.Palette.RailFocusBG.BG(), th.Palette.RailText.FG(), text)
	if n := w - cells.Cells(text); n > 0 {
		buf.WriteString(strings.Repeat(" ", n))
	}
	buf.WriteString("\x1b[0m")
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
