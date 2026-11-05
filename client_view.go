package main

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"

	"dm/internal/layout"
	"dm/internal/panel"
	"dm/internal/wire"

	"github.com/hinshun/vt10x"
)

// frameOpen and frameClose bracket a frame so the terminal swaps it in whole.
// A terminal that does not know the sequence ignores it.
const (
	frameOpen  = "\x1b[?2026h"
	frameClose = "\x1b[?2026l"
)

// draw writes one frame. A frame that changes one cell writes one row: the
// rows of every pane are fingerprinted, and each band of the picture (the
// boxes, the grips, the divider, the rail, the bar, the toasts and the
// overlays) is compared with its own bytes from the previous frame. A steady
// frame writes nothing. A frame never clears the screen unless the picture
// itself is not stable.
func (t *tui) draw() {
	win := t.snap
	if win == nil || win.Cols < 1 || win.Rows < 1 {
		return
	}

	// The bands that own a whole buffer of bytes. Each one is rendered every
	// frame and written only when its bytes differ from the last frame's.
	th := t.theme()
	var box, grip, div, rail, bar, toasts, pal, help, sess, menu, rename bytes.Buffer
	for _, p := range visiblePanes(win) {
		panel.DrawPaneBox(&box, th, p.Rect, p.ID == win.Focus)
	}
	drawGrips(&grip, th, win)
	// The rail grip paints the same heavy line as a split divider, over the
	// strip its grab zone covers.
	drawDivider(&div, th, t.padZone, t.padAxis)
	drawRail(&rail, th, win)
	drawBar(&bar, th, win, t.armed, t.barInfo())
	drawToasts(&toasts, th, win, t.toasts)
	if t.sw != nil {
		t.swX, t.swY = drawSwitcher(&pal, th, t.sw, win, t.vts)
		t.askPreview()
	}
	if t.armed {
		drawHelp(&help, th, win.Cols, win.Rows, t.km().HelpEntries())
	}
	if t.mode == "sessions" {
		drawSessions(&sess, th, win.Cols, win.Rows, t.sessions, t.sel, t.session)
	}
	if t.mode == "menu" && t.menu != nil {
		drawMenu(&menu, th, t.menu)
	}
	if t.mode == "rename" {
		drawRename(&rename, th, win, t.renameID, t.renameDraft, t.renameKind)
	}

	// A picture that is not stable repaints whole and may clear: a size, a
	// layout, a pane set, a scroll or a screen-owning mode changed.
	sig := t.layoutSig(win)
	full := t.laysig != sig
	t.laysig = sig

	// The divider paints over the borders and grips between two panes, so a
	// divider that moves leaves cells the boxes must paint again.
	padFreed := !full && !bytes.Equal(t.last.div.buf, div.Bytes())
	var blank bytes.Buffer
	if padFreed {
		blankRect(&blank, t.last.divRect)
	}
	t.last.divRect = layout.Rect{}
	if div.Len() > 0 {
		t.last.divRect = t.padZone
	}

	var floatBufs []bytes.Buffer
	for _, f := range t.floats {
		var fb bytes.Buffer
		f.Draw(&fb, th, t.hoverFor(f.ID, f.Rect.W))
		floatBufs = append(floatBufs, fb)
	}
	dirty := map[int]bool{}
	type band struct {
		p       *paint
		buf     *bytes.Buffer
		overlay bool
		force   bool
		write   bool
	}
	bands := []band{
		{p: &t.last.box, buf: &box, force: padFreed},
		{p: &t.last.grip, buf: &grip, force: padFreed},
		{p: &t.last.rail, buf: &rail},
		{p: &t.last.div, buf: &div},
		{p: &t.last.bar, buf: &bar},
		{p: &t.last.toasts, buf: &toasts, overlay: true},
		{p: &t.last.pal, buf: &pal, overlay: true},
		{p: &t.last.help, buf: &help, overlay: true},
		{p: &t.last.sess, buf: &sess, overlay: true},
		{p: &t.last.menu, buf: &menu, overlay: true},
		{p: &t.last.rename, buf: &rename, overlay: true},
	}
	for i := range t.floats {
		bands = append(bands, band{p: &t.floats[i].last, buf: &floatBufs[i], overlay: true})
	}
	// An overlay paints on top of everything, so the rows it leaves need the
	// whole picture back: the panes under it and the bands around it. Without a
	// clear the rows would keep the overlay's glyphs.
	overlayChanged := false
	for i := range bands {
		b := &bands[i]
		b.write = full || b.force || !bytes.Equal(b.p.buf, b.buf.Bytes())
		if !b.write {
			continue
		}
		old := b.p.rows
		b.p.buf = append(b.p.buf[:0], b.buf.Bytes()...)
		// Every band's rows matter, not only an overlay's: the rows it writes
		// go under a standing overlay, and the rows an overlay takes and leaves
		// belong to the panes again.
		b.p.rows = bandRows(b.p.buf)
		if !b.overlay {
			continue
		}
		overlayChanged = true
		if !full {
			for _, y := range old {
				dirty[y] = true
			}
			for _, y := range b.p.rows {
				dirty[y] = true
			}
		}
	}

	// Pane rows: fingerprint each content row and repaint only the rows that
	// moved. The fingerprint covers the cells, the selection on them and the
	// scrollback row they come from.
	paints := t.panePaints(win)
	sigs := make(map[string][]uint64, len(paints))
	for _, pp := range paints {
		sigs[pp.id] = paneRowSigs(pp)
	}
	if !full {
		for _, pp := range paints {
			old := t.rowsig[pp.id]
			for row, s := range sigs[pp.id] {
				if row >= len(old) || old[row] != s {
					dirty[pp.rect.Y+row] = true
				}
			}
		}
	}
	t.rowsig = sigs

	var body bytes.Buffer
	for y := 0; y < win.Rows; y++ {
		if !full && !dirty[y] {
			continue
		}
		for _, pp := range paints {
			if y < pp.rect.Y || y >= pp.rect.Y+pp.rect.H {
				continue
			}
			drawPaneRow(&body, pp.vt, pp.rect, y-pp.rect.Y, pp.row0, pp.sel, pp.hover)
		}
	}
	if padFreed {
		body.Write(blank.Bytes())
	}
	// The rows already written this frame, by any layer. An overlay is painted
	// last, so one that is older in the paint order goes back on top of every
	// row that a pane row, a freed divider or a band wrote this frame.
	written := map[int]bool{}
	for y := range dirty {
		written[y] = true
	}
	for _, y := range bandRows(blank.Bytes()) {
		written[y] = true
	}
	for i := range bands {
		b := &bands[i]
		// A band that moved an overlay comes back whole, and an overlay goes
		// back on top whenever a row under it was written.
		if !b.write {
			b.write = overlayChanged || (b.overlay && rowsHit(b.p.rows, written))
		}
		if !b.write {
			continue
		}
		body.Write(b.p.buf)
		for _, y := range b.p.rows {
			written[y] = true
		}
	}

	cur := t.cursorBlock()
	if body.Len() == 0 && !full && cur == t.last.cur {
		return
	}
	var out bytes.Buffer
	out.WriteString(frameOpen)
	out.WriteString("\x1b[?7l")
	if full {
		out.WriteString("\x1b[0m\x1b[2J\x1b[H")
	}
	out.Write(body.Bytes())
	out.WriteString(cur)
	out.WriteString("\x1b[?7h")
	out.WriteString(frameClose)
	t.writeFrame(out.Bytes())
	t.last.cur = cur
}

// writeFrame sends one frame to the frame writer.
func (t *tui) writeFrame(b []byte) {
	w := t.out
	if w == nil {
		w = os.Stdout
	}
	_, _ = w.Write(b)
}

// layoutSig is the shape of the picture. When it changes the cells cannot be
// diffed against the last frame: the size, the layout, a pane, the scrollback,
// the mode or the palette moved, so the next frame repaints whole.
func (t *tui) layoutSig(win *wire.Snapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%dx%d r%d z=%s", win.Cols, win.Rows, win.Rail, win.Zoom)
	for _, p := range win.Panes {
		fmt.Fprintf(&b, " %s:%d,%d,%d,%d", p.ID, p.Rect.X, p.Rect.Y, p.Rect.W, p.Rect.H)
	}
	for _, f := range t.floats {
		fmt.Fprintf(&b, " F%s:%d,%d,%d,%d", f.ID, f.Rect.X, f.Rect.Y, f.Rect.W, f.Rect.H)
	}
	fmt.Fprintf(&b, " f=%s m=%s s=%s", win.Focus, t.mode, t.scroll)
	if t.sw != nil {
		fmt.Fprintf(&b, " p=%+v", t.sw.rect)
	}
	return b.String()
}

// panePaints is the content each pane shows this frame.
func (t *tui) panePaints(win *wire.Snapshot) []panePaint {
	panes := visiblePanes(win)
	out := make([]panePaint, 0, len(panes))
	for _, p := range panes {
		pp := panePaint{id: p.ID, rect: p.Rect}
		pp.hover = t.hoverFor(p.ID, p.Rect.W)
		if p.ID == t.scroll && t.scrollVT != nil {
			pp.vt = t.scrollVT
			pp.row0, _ = scrollStart(t.scrollVT, p.Rect.H, t.offset)
		} else {
			pp.vt = t.vts[p.ID]
			if t.selText != nil && t.selText.pane == p.ID {
				pp.sel = t.selText.rangeIn(p.Rect.W)
			}
		}
		if pp.vt != nil {
			out = append(out, pp)
		}
	}
	return out
}

// paneRowSigs fingerprints one pane's content rows. Every visible change moves
// the row's hash: the glyph, its colours, its mode, the selection over it and
// the scrollback row it comes from.
func paneRowSigs(pp panePaint) []uint64 {
	cols, rows := pp.vt.Size()
	out := make([]uint64, pp.rect.H)
	pp.vt.Lock()
	defer pp.vt.Unlock()
	for row := 0; row < pp.rect.H; row++ {
		h := uint64(14695981039346656037)
		for x := 0; x < pp.rect.W; x++ {
			var g vt10x.Glyph
			if x < cols && pp.row0+row < rows {
				g = normCell(pp.vt.Cell(x, pp.row0+row))
			} else {
				g = normCell(g)
			}
			h = mix(h, uint64(g.Char))
			h = mix(h, uint64(g.FG))
			h = mix(h, uint64(g.BG))
			h = mix(h, uint64(g.Mode))
			if pp.sel.has(row*pp.rect.W+x) || pp.hover.has(row*pp.rect.W+x) {
				h = mix(h, 1)
				if pp.hover.has(row*pp.rect.W + x) {
					h = mix(h, 2)
				}
			}
		}
		out[row] = h
	}
	return out
}

// mix folds one value into a row fingerprint (FNV-1a).
func mix(h, v uint64) uint64 {
	return (h ^ v) * 1099511628211
}

// bandRows returns the rows a band's bytes address, in screen order. A band
// writes with absolute cursor moves, so its own bytes say where it lands.
// It reads every cursor position form: ESC [ row ; col H, ESC [ row H and the
// bare ESC [ H (both finals, H and f, like a terminal does). A band that moves
// the cursor another way addresses no row here, so keep bands on absolute
// moves: TestBandsUseAbsoluteCursorMoves fails if one does.
func bandRows(buf []byte) []int {
	var rows []int
	seen := map[int]bool{}
	for i := 0; i+2 < len(buf); i++ {
		if buf[i] != 0x1b || buf[i+1] != '[' {
			continue
		}
		j := i + 2
		n := 0
		for j < len(buf) && (buf[j] >= '0' && buf[j] <= '9') {
			n = n*10 + int(buf[j]-'0')
			j++
		}
		// The rest of the parameter bytes, up to the final byte.
		for j < len(buf) && (buf[j] == ';' || (buf[j] >= '0' && buf[j] <= '9')) {
			j++
		}
		if j >= len(buf) || (buf[j] != 'H' && buf[j] != 'f') {
			continue
		}
		// A zero or one row, or a bare move, is the top row.
		y := 0
		if n > 1 {
			y = n - 1
		}
		if !seen[y] {
			seen[y] = true
			rows = append(rows, y)
		}
	}
	sort.Ints(rows)
	return rows
}

// rowsHit reports whether any of rows is in the dirty set.
func rowsHit(rows []int, dirty map[int]bool) bool {
	for _, y := range rows {
		if dirty[y] {
			return true
		}
	}
	return false
}

// cursorBlock is the tail of a frame that places the cursor, or hides it. It
// is empty when the frame leaves the cursor alone.
func (t *tui) cursorBlock() string {
	if t.sw != nil {
		// The float owns the cursor while it is up: the query cell, shown.
		return fmt.Sprintf("\x1b[%d;%dH\x1b[?25h", t.swY+1, t.swX+1)
	}
	win := t.snap
	if win == nil || t.mode != "" {
		return ""
	}
	p := t.paneByID(win.Focus)
	if p == nil {
		return ""
	}
	vt := t.vts[p.ID]
	if vt == nil {
		return ""
	}
	vt.Lock()
	cur := vt.Cursor()
	visible := vt.CursorVisible()
	vt.Unlock()
	if visible && cur.X < p.Rect.W && cur.Y < p.Rect.H {
		return fmt.Sprintf("\x1b[%d;%dH\x1b[?25h", p.Rect.Y+cur.Y+1, p.Rect.X+cur.X+1)
	}
	return "\x1b[?25l"
}
