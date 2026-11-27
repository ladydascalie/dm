package client

// The web URL float: the link the session daemon serves the page on, with its
// QR code under it, or the reason there is none. It is a temporary float, so it
// lives in the client, owns no PTY and dies with the client. The answer comes
// from the daemon's web-info reply, never from a local config read: the daemon
// owns the listener, and a client that guessed could print a URL nothing
// serves.
//
// The box carries no mode. It binds three close keys, esc and q by hand and the
// web-url action through the keymap, and every other byte is the pane's, so a
// reader can keep typing in the pane the box stands over.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"dm/internal/cells"
	"dm/internal/hintbar"
	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/panel"
	"dm/internal/segment"
	"dm/internal/theme"
	"dm/internal/webui"
	"dm/internal/wire"
)

// webFloatID is the float's id in the client's float stack.
const webFloatID = "web"

const (
	// webMinCols and webMaxCols bound the box's interior width. The box asks
	// for what the text and the QR need, between them, so a short link does not
	// open a box the size of the screen.
	webMinCols = 24
	webMaxCols = 96
	// webChromeRows is what the box needs around its content: the two borders
	// and the status bar row it must leave alone.
	webChromeRows = 3
	// webMargin keeps the box clear of the screen edges.
	webMargin = 2
	// webWrapCols is the width the explanation rows wrap at. It is fixed, so
	// the box's height is settled before its width and a resize never changes
	// how many rows the reason takes.
	webWrapCols = 48
)

// webReply is the daemon's answer to the web-info action: whether it holds a
// listener right now, the link it bound and the bind failure when it tried and
// failed. The flag lives in the daemon's config, not here: a reload can flip it
// without starting or stopping the listener.
type webReply struct {
	Auto  bool   `json:"auto"`
	URL   string `json:"url"`
	Addr  string `json:"addr"`
	Error string `json:"error"`
}

// webFloat is the open URL box: the request id its reply must carry, the
// answer once it lands, and the panel that draws it.
type webFloat struct {
	id int
	// info is the daemon's answer. answered tells a zero answer from no answer
	// yet, so the box can say it is asking.
	info     webReply
	answered bool
	// content is the answer as it paints: the text rows, the QR art and the
	// cell size the art needs. setAnswer builds it when the answer changes, so
	// the encoder runs once per reply rather than once per frame.
	content webContent
	// qrOn is the refit's verdict on the code: true only when every row of it
	// fits the box the screen allows. webRect sets it and paintWebFloat obeys
	// it, so the code is never painted past the box's bottom.
	qrOn bool
	// hintKey is the action key the border currently names, so a reload that
	// rebinds web-url renames the border without reopening the box.
	hintKey string
	float   *panel.FloatingPane
}

// openWebFloat opens the URL box, or closes it when it is already up. It sends
// one web-info request and paints the reply when it lands.
func (t *tui) openWebFloat() {
	if t.web != nil {
		t.closeWebFloat()
		return
	}
	if t.snap == nil || t.conn == nil {
		t.notify("no session")
		t.disarm()
		return
	}
	v := &webFloat{}
	v.setAnswer()
	v.float = &panel.FloatingPane{
		ID:     webFloatID,
		Active: true,
	}
	v.float.Paint = func(b *bytes.Buffer, r layout.Rect) { t.paintWebFloat(b, r) }
	t.web = v
	t.webID++
	v.id = t.webID
	t.AddFloatingPane(v.float)
	t.syncWeb()
	t.dirty = true
	_ = t.conn.Send(wire.Msg{Type: "req", Action: "web-info", ID: v.id})
	t.disarm()
}

// closeWebFloat takes the box down and gives the keys back to the pane. It
// leaves the input buffer alone: the close keys that swallow a byte drop it
// themselves, and a close from the desk or from a snapshot must not throw away
// keys the operator already typed.
func (t *tui) closeWebFloat() {
	if t.web == nil {
		return
	}
	t.RemoveFloatingPane(webFloatID)
	t.web = nil
	t.dirty = true
}

// webOpen reports whether the URL box is up, so the key handlers can say whose
// esc and q these are.
func (t *tui) webOpen() bool { return t.web != nil }

// takeWebInfo is the reply handler. A reply that lands after the box closed is
// dropped, and a refusal paints the daemon's error rather than a stale URL.
func (t *tui) takeWebInfo(m wire.Msg) {
	v := t.web
	if v == nil || m.ID != v.id {
		return
	}
	v.answered = true
	if !m.OK {
		v.info = webReply{Error: m.Error}
	} else if err := json.Unmarshal(m.Res, &v.info); err != nil {
		v.info = webReply{Error: "web-info reply: " + err.Error()}
	}
	v.setAnswer()
	t.syncWeb()
	t.dirty = true
}

// syncWeb refits the box to the snapshot and the answer it holds. It runs on
// the frame path, so a resize refits it and a narrow terminal drops the QR
// instead of painting half of one.
func (t *tui) syncWeb() {
	v := t.web
	if v == nil {
		return
	}
	if t.snap == nil {
		t.closeWebFloat()
		return
	}
	th := t.theme()
	if title := " " + th.Glyphs.GitMark + " web "; v.float.Title != title {
		v.float.Title = title
		t.dirty = true
	}
	// The third close key is the rebindable action, so the border names what
	// the keymap binds now and a reload renames it in place.
	cfg := t.km()
	if key := cfg.Key("main", "web-url"); v.hintKey != key {
		v.hintKey = key
		v.float.Hint = webHint(cfg)
		t.dirty = true
	}
	if rect := t.webRect(t.snap, v); v.float.Rect != rect {
		v.float.Rect = rect
		t.dirty = true
	}
}

// webLine is one text row of the box, in the box's dim or its text colour.
type webLine struct {
	text string
	dim  bool
}

// webContent is what the answer paints: the rows, then the QR art and the cell
// size it needs. The size does not depend on the box, so the box can shrink
// around it and drop the code when the room runs out.
type webContent struct {
	rows []webLine
	qr   string
	qrW  int
	qrH  int
}

// setAnswer rebuilds the cached content from the answer the box holds. It is
// the one place the QR encoder runs: once for the asking placeholder and once
// for each reply, never on the frame path.
func (v *webFloat) setAnswer() { v.content = webContentFor(v) }

// webContentFor is the box's content for the answer it holds.
func webContentFor(v *webFloat) webContent {
	if !v.answered {
		return webContent{rows: []webLine{{text: "asking the session daemon...", dim: true}}}
	}
	info := v.info
	switch {
	case info.Error != "":
		// A bind failure names an address and a reason, so it wraps instead of
		// losing its second half to the elision mark.
		rows := []webLine{{text: "the web listener could not start", dim: true}}
		for _, line := range wrapText(info.Error, webWrapCols) {
			rows = append(rows, webLine{text: line})
		}
		return webContent{rows: append(rows, webLine{text: "fix web-addr, or run dm web by hand", dim: true})}
	case !info.Auto:
		// The daemon holds no listener: the key is off, or a reload turned it
		// on after startWeb ran and nothing binds it. The reply cannot tell the
		// two apart, so the rows stay true for both and name the restart, since
		// a reload alone never starts one.
		return webContent{rows: []webLine{
			{text: "the session daemon holds no web listener", dim: true},
			{text: "start it with web-auto true, or run dm web by hand"},
		}}
	case info.URL == "":
		return webContent{rows: []webLine{
			{text: "the daemon started no web listener", dim: true},
			{text: "run dm web by hand", dim: true},
		}}
	}
	out := webContent{rows: []webLine{{text: info.URL}}}
	// The art is rendered once per answer by setAnswer, not per frame: it is
	// the same code for the same link.
	art, err := webui.QRArt(info.URL)
	if err != nil {
		return out
	}
	qrW, qrH := webui.QRSize(info.URL)
	if qrH <= 0 || qrW <= 0 {
		return out
	}
	out.qr, out.qrW, out.qrH = art, qrW, qrH
	return out
}

// webRect is the box's interior rect: what the content asks for, centred over
// the panes and clamped inside the screen. It reads the cached content, so a
// frame never runs the encoder.
//
// The code is whole or absent: a screen too short for every row of it, or too
// narrow for every column, drops it and keeps the text rows. A code painted
// part way does not scan, and a box that shows half of one lies about it.
func (t *tui) webRect(snap *wire.Snapshot, v *webFloat) layout.Rect {
	content := v.content
	maxH := snap.Rows - 1 - webChromeRows
	// Whether the code fits is a height question, so it is answered before the
	// width: a box that drops the code is no wider than its text.
	qrOn := content.qrW > 0 && len(content.rows)+1+content.qrH <= maxH
	cols := webMinCols
	for _, line := range content.rows {
		if n := cells.Cells(line.text) + 2; n > cols {
			cols = n
		}
	}
	if qrOn && content.qrW+2 > cols {
		cols = content.qrW + 2
	}
	if cols > webMaxCols {
		cols = webMaxCols
	}
	if max := snap.Cols - 2*webMargin; cols > max {
		cols = max
	}
	if cols < 3 {
		cols = 3
	}
	// A code wider than the settled interior is a code that would wrap, so the
	// box keeps the link alone.
	if qrOn && content.qrW > cols-2 {
		qrOn = false
	}
	v.qrOn = qrOn
	rows := len(content.rows)
	if qrOn {
		rows += 1 + content.qrH
	}
	if rows > maxH {
		// A screen too short even for the text: the last rows are cut, since a
		// box with no rows paints nothing.
		rows = maxH
	}
	if rows < 1 {
		rows = 1
	}
	boxW, boxH := cols+2, rows+2
	x := (snap.Cols - boxW) / 2
	if x < 0 {
		x = 0
	}
	if max := snap.Cols - boxW; x > max {
		x = max
	}
	y := (snap.Rows - 1 - boxH) / 2
	if y < 0 {
		y = 0
	}
	if max := snap.Rows - 1 - boxH; y > max {
		y = max
	}
	return layout.Rect{X: x + 1, Y: y + 1, W: cols, H: rows}
}

// webHint is the box's bottom border. The three close keys fit a normal
// border; a narrow one drops the last, and every one of them still answers.
// The action key comes from the keymap, so a rebound web-url labels itself.
func webHint(cfg *keymap.Config) *hintbar.Bar {
	hints := []hintbar.Hint{
		{Keys: []string{"esc"}, Label: "close", Keep: true},
		{Keys: []string{"q"}, Label: "close", Keep: true},
	}
	if key := cfg.Key("main", "web-url"); key != "" {
		hints = append(hints, hintbar.Hint{Keys: []string{cfg.PrefixName() + " " + key}, Label: "close"})
	}
	return &hintbar.Bar{Hints: hints}
}

// paintWebFloat paints the URL, the QR art under it, or the reason there is
// none. The QR art is written a row at a time from the bytes webui renders, so
// the client never imports the encoder.
func (t *tui) paintWebFloat(b *bytes.Buffer, r layout.Rect) {
	v := t.web
	if v == nil {
		return
	}
	th := t.theme()
	content := v.content
	for i, line := range content.rows {
		if i >= r.H {
			return
		}
		fg := th.Palette.RailText
		if line.dim {
			fg = th.Palette.RailDim
		}
		webLineAt(b, th, layout.Rect{X: r.X, Y: r.Y + i, W: r.W, H: 1}, line.text, fg)
	}
	if !v.qrOn || content.qr == "" {
		return
	}
	// The code starts under a blank row, so the link reads on its own. The
	// refit turns the code on only when every row of it fits the box, so this
	// loop writes the whole code; the bound stays as the frame's edge.
	top := r.Y + len(content.rows) + 1
	for i, line := range strings.Split(strings.TrimSuffix(content.qr, "\n"), "\n") {
		if i >= content.qrH || top+i >= r.Y+r.H {
			return
		}
		fmt.Fprintf(b, "\x1b[%d;%dH%s", top+i+1, r.X+1, line)
	}
}

// webLineAt paints one text row: one cell in from the border, on the box's
// ground, elided with the set's mark so a long URL never wraps onto the next
// row.
func webLineAt(b *bytes.Buffer, th theme.Theme, r layout.Rect, text string, fg theme.RGB) {
	segs := []segment.Seg{{Text: text, FG: fg, BG: th.Palette.RailBG}}
	if segment.SegCells(segs) > r.W {
		segs = segment.ElideSegs(th, segs, r.W)
	}
	fmt.Fprintf(b, "\x1b[%d;%dH", r.Y+1, r.X+1)
	for _, s := range segs {
		segment.Write(b, s)
	}
}

// wrapText breaks text into rows of at most width cells, at its spaces. A word
// wider than the row keeps its own row and is elided at paint time, so a long
// address is never cut to nothing.
func wrapText(text string, width int) []string {
	if width < 1 {
		return nil
	}
	var out []string
	line := ""
	for _, word := range strings.Fields(text) {
		switch {
		case line == "":
			line = word
		case cells.Cells(line)+1+cells.Cells(word) <= width:
			line += " " + word
		default:
			out = append(out, line)
			line = word
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}
