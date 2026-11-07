package client

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"

	"dm/internal/keymap"
	"dm/internal/rail"
	"dm/internal/wire"
)

// menu is one open context menu: where it sits, its title, its items and the
// selected row. pane is the pane the right click landed on.
type menu struct {
	x, y  int
	w     int
	title string
	pane  string
	items []menuItem
	sel   int
}

type menuItem struct {
	label  string
	key    string
	action string
	run    func()
}

// paneMenuItems is the item list for a pane menu: the actions that act on one
// pane. withFocus puts Focus first, as a sidebar pane row does.
func (t *tui) paneMenuItems(p *wire.PaneInfo, withFocus bool) []menuItem {
	items := make([]menuItem, 0, 8)
	if withFocus {
		items = append(items, menuItem{label: "Focus", action: "focus", run: func() {
			t.actionOn(p.ID, "focus", nil)
		}})
	}
	for _, split := range []struct{ label, action, dir string }{
		{"Split right", "split-right", "right"},
		{"Split down", "split-down", "down"},
		{"Split left", "split-left", "left"},
		{"Split up", "split-up", "up"},
	} {
		split := split
		items = append(items, menuItem{label: split.label, action: split.action, run: func() {
			t.actionOn(p.ID, "split", func(m *wire.Msg) { m.Dir = split.dir })
		}})
	}
	return append(items,
		menuItem{label: "Zoom", action: "zoom-pane", run: func() {
			t.actionOn(p.ID, "zoom-pane", nil)
		}},
		menuItem{label: "Close pane", action: "close-pane", run: func() {
			t.actionOn(p.ID, "close-pane", nil)
		}},
		menuItem{label: "Scrollback", action: "scrollback", run: func() { t.startScroll(p) }},
	)
}

// windowMenuItems is the item list for a window row or its note row: the
// actions that act on one window. Focus goes to the window's first available
// pane, the same pane a left press on the row focuses.
func (t *tui) windowMenuItems(it rail.RailItem) []menuItem {
	return []menuItem{
		{label: "Rename window", action: "rename-window", run: func() {
			t.closeMenu()
			t.openRenamePromptFor("window", it.Window, it.WindowName)
		}},
		{label: "Close window", action: "close-window", run: func() {
			t.actionOn(it.Window, "close-window", nil)
		}},
		{label: "Fold/unfold", action: "toggle-fold", run: func() {
			t.actionOn(it.Window, "toggle-fold", nil)
		}},
		{label: "Focus", action: "focus", run: func() {
			if id := t.firstPaneInWindow(it.Window); id != "" {
				t.actionOn(id, "focus", nil)
			}
		}},
	}
}

// workspaceMenuItems is the item list for a workspace row: the actions that act
// on one workspace.
func (t *tui) workspaceMenuItems(it rail.RailItem) []menuItem {
	return []menuItem{
		{label: "New window", action: "new-window", run: func() {
			t.actionOn(it.Workspace, "new-window", nil)
		}},
		{label: "Rename workspace", action: "rename-workspace", run: func() {
			t.closeMenu()
			t.openRenamePromptFor("workspace", it.Workspace, it.Text)
		}},
		{label: "Close workspace", action: "close-workspace", run: func() {
			t.actionOn(it.Workspace, "close-workspace", nil)
		}},
	}
}

// sessionMenuItems is the item list for the rail's control row: the actions
// that act on the whole session.
func (t *tui) sessionMenuItems() []menuItem {
	return []menuItem{
		{label: "New workspace", action: "new-workspace", run: func() {
			t.action("new-workspace", nil)
		}},
		{label: "Switch session", action: "picker", run: func() { t.openPicker() }},
		{label: "Detach", action: "detach", run: func() { t.quit = true }},
	}
}

// hintedItems fills each row's key label from the configured main keymap, so
// the hint loop resolves the key that dispatches it. A row whose action is
// unbound keeps an empty hint.
func (t *tui) hintedItems(items []menuItem) []menuItem {
	km := t.km()
	for i := range items {
		if items[i].action != "" {
			items[i].key = km.Key("main", items[i].action)
		}
	}
	return items
}

// openMenuAt places a menu with its title and items at a zero-based terminal
// point, clamped to the screen, and takes over input.
func (t *tui) openMenuAt(title string, items []menuItem, x, y int) {
	w := 34
	if w > t.snap.Cols-2 {
		w = t.snap.Cols - 2
	}
	h := len(items) + 2
	x = clampInt(x, 0, t.snap.Cols-w)
	y = clampInt(y, 0, t.snap.Rows-h)
	t.menu = &menu{x: x, y: y, w: w, title: title, items: items}
	t.mode = "menu"
	t.armed = false
	t.dirty = true
}

// openMenu builds the on-screen pane menu at a zero-based terminal point and
// takes over input.
func (t *tui) openMenu(p *wire.PaneInfo, x, y int) {
	if t.snap == nil {
		return
	}
	title := "pane " + p.ID
	if p.Name != "" {
		title += " " + p.Name
	}
	t.openMenuAt(title, t.hintedItems(t.paneMenuItems(p, false)), x, y)
	t.menu.pane = p.ID
}

// openRailMenu builds the sidebar menu for the row under a right press. Each
// kind offers only its own actions, its title names the kind, and every item
// carries the main keymap's hint for its action. A gap row opens nothing.
func (t *tui) openRailMenu(it rail.RailItem, x, y int) {
	if t.snap == nil {
		return
	}
	var title string
	var items []menuItem
	switch it.Kind {
	case rail.Workspace:
		title = "workspace " + it.Text
		items = t.workspaceMenuItems(it)
	case rail.Window, rail.WindowNote:
		title = "window " + it.WindowName
		items = t.windowMenuItems(it)
	case rail.Pane:
		p := t.paneByID(it.Pane)
		if p == nil {
			// A thin rail lists every pane in the snapshot's All set. A pane
			// row the Pane set has dropped still gets its menu.
			p = &wire.PaneInfo{ID: it.Pane}
		}
		title = "pane " + it.Text
		items = t.paneMenuItems(p, true)
		// A pane row offers its own rename, as the workspace row does. The
		// draft starts from the raw pane name the rail item carries.
		rename := menuItem{label: "Rename pane", action: "rename-pane", run: func() {
			t.closeMenu()
			t.openRenamePromptFor("pane", it.Pane, it.PaneName)
		}}
		if len(items) > 0 && items[0].action == "focus" {
			items = append(items[:1], append([]menuItem{rename}, items[1:]...)...)
		} else {
			items = append(items, rename)
		}
	default:
		return
	}
	t.openMenuAt(title, t.hintedItems(items), x, y)
	t.menuPress = true
}

// openSessionMenu builds the session menu for a right press on the rail's
// control row.
func (t *tui) openSessionMenu(x, y int) {
	if t.snap == nil {
		return
	}
	title := "session " + t.snap.Session
	t.openMenuAt(title, t.hintedItems(t.sessionMenuItems()), x, y)
	t.menuPress = true
}

func clampInt(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (t *tui) closeMenu() {
	t.menu = nil
	t.mode = ""
	t.inbuf = nil
	t.dirty = true
}

// menuKey consumes one key while the menu is open. The menu's own bindings win
// first; an item's key label is the hint that dispatches it, as tmux does. It
// returns false when it needs more bytes for an escape sequence.
func (t *tui) menuKey() bool {
	if t.menu == nil {
		t.closeMenu()
		return true
	}
	if len(t.inbuf) == 0 {
		return true
	}
	action, n, state := t.km().Match("menu", t.inbuf)
	switch state {
	case keymap.Partial:
		if !t.pending {
			t.pending = true
			t.escAt = time.Now()
		}
		return false
	case keymap.Match:
		t.inbuf = append([]byte(nil), t.inbuf[n:]...)
		t.pending = false
	default:
		i, hintN, partial := t.menuHint()
		if partial {
			// The head is still arriving as part of a longer key.
			if !t.pending {
				t.pending = true
				t.escAt = time.Now()
			}
			return false
		}
		if i >= 0 {
			t.inbuf = append([]byte(nil), t.inbuf[hintN:]...)
			t.pending = false
			t.menuPick(i)
			return true
		}
		// An unbound key closes the menu, as it always has.
		t.inbuf = t.inbuf[1:]
		t.pending = false
		t.closeMenu()
		return true
	}
	switch action {
	case "down", "down-arrow":
		t.menuMove(1)
	case "up", "up-arrow":
		t.menuMove(-1)
	case "pick":
		t.menuPick(t.menu.sel)
	case "close", "close-esc":
		t.closeMenu()
	default:
		if i, ok := pickIndex(action); ok && i < len(t.menu.items) {
			t.menuPick(i)
			return true
		}
		t.closeMenu()
	}
	return true
}

// menuHint finds the first menu row whose key label matches the head of the
// input buffer. The first matching row in menu order wins. A row with no label
// or a label that names no key is skipped. A label that covers only the head of
// a longer escape sequence is not a complete key, so it does not match: the
// rest of the sequence would otherwise reach a pane. partial reports that the
// head is still arriving as part of some row's label.
func (t *tui) menuHint() (item, n int, partial bool) {
	if t.menu == nil || len(t.inbuf) == 0 {
		return -1, 0, false
	}
	prefix := t.km().PrefixBytes()
	seqs := make([][][]byte, len(t.menu.items))
	for i, it := range t.menu.items {
		if it.key == "" {
			continue
		}
		s, err := keymap.KeySequences(it.key, prefix)
		if err != nil {
			continue
		}
		seqs[i] = s
	}
	for _, s := range seqs {
		for _, seq := range s {
			if len(seq) > len(t.inbuf) && bytes.HasPrefix(seq, t.inbuf) {
				return -1, 0, true
			}
		}
	}
	run := escapeRun(t.inbuf)
	for i, s := range seqs {
		for _, seq := range s {
			if len(seq) == 0 || len(seq) < run {
				continue
			}
			if bytes.HasPrefix(t.inbuf, seq) {
				return i, len(seq), false
			}
		}
	}
	return -1, 0, false
}

// pickIndex reads a "pick-<n>" action's zero-based row. It reports false for
// any other action.
func pickIndex(action string) (int, bool) {
	rest := strings.TrimPrefix(action, "pick-")
	if rest == action || rest == "" {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 1 {
		return 0, false
	}
	return n - 1, true
}

func (t *tui) menuMove(step int) {
	if t.menu == nil || len(t.menu.items) == 0 {
		return
	}
	t.menu.sel = (t.menu.sel + step + len(t.menu.items)) % len(t.menu.items)
	t.dirty = true
}

func (t *tui) menuPick(i int) {
	if t.menu == nil || i < 0 || i >= len(t.menu.items) {
		t.closeMenu()
		return
	}
	item := t.menu.items[i]
	t.closeMenu()
	item.run()
}

// menuRow returns the item index at a zero-based point, or -1.
func (m *menu) itemAt(x, y int) int {
	if m == nil {
		return -1
	}
	if x < m.x+1 || x >= m.x+m.w-1 {
		return -1
	}
	i := y - (m.y + 1)
	if i < 0 || i >= len(m.items) {
		return -1
	}
	return i
}

// pickerKey consumes one key while the session picker is open. It returns false
// when it needs more bytes for an escape sequence.
func (t *tui) pickerKey() bool {
	if len(t.inbuf) == 0 {
		return true
	}
	action, n, state := t.km().Match("picker", t.inbuf)
	switch state {
	case keymap.Partial:
		if !t.pending {
			t.pending = true
			t.escAt = time.Now()
		}
		return false
	case keymap.Match:
		t.inbuf = append([]byte(nil), t.inbuf[n:]...)
		t.pending = false
	default:
		// An unbound key closes the picker.
		t.inbuf = t.inbuf[1:]
		t.pending = false
		t.closePicker()
		return true
	}
	switch action {
	case "down", "down-arrow":
		t.moveSel(1)
	case "up", "up-arrow":
		t.moveSel(-1)
	case "pick":
		t.pickSession()
	default:
		if i, ok := pickIndex(action); ok && i < len(t.sessions) {
			t.sel = i
			t.pickSession()
			return true
		}
		t.closePicker()
	}
	return true
}

// switcherKey consumes one key while the native switcher is open. Its own
// bindings are the switcher mode's; a printable key that matches none of them
// is text for the query. It returns false when it needs more bytes for an
// escape sequence.
func (t *tui) switcherKey() bool {
	if len(t.inbuf) == 0 {
		return true
	}
	if p := t.km().PrefixBytes(); len(p) > 0 && bytes.HasPrefix(t.inbuf, p) {
		// The prefix key pressed again closes the float.
		t.inbuf = append([]byte(nil), t.inbuf[len(p):]...)
		t.pending = false
		closeSwitcher(t)
		return true
	}
	action, n, state := t.km().Match("switcher", t.inbuf)
	if state == keymap.Match && escapeRun(t.inbuf) > n {
		// The match covered only the head of an escape sequence. The sequence
		// as a whole is unbound, and an unbound escape is not text.
		state = keymap.NoMatch
	}
	switch state {
	case keymap.Partial:
		if !t.pending {
			t.pending = true
			t.escAt = time.Now()
		}
		return false
	case keymap.Match:
		t.inbuf = append([]byte(nil), t.inbuf[n:]...)
		t.pending = false
	default:
		b := t.inbuf[0]
		n := escapeRun(t.inbuf)
		t.inbuf = append([]byte(nil), t.inbuf[n:]...)
		t.pending = false
		// An escape sequence with no binding is not text: left, right and
		// shift-tab have no meaning in a list, and neither does F1.
		if n == 1 && b >= 0x20 && b < 0x7f {
			t.sw.AppendText(rune(b))
			t.dirty = true
		}
		return true
	}
	switch action {
	case "close", "close-ctrl-c", "close-esc":
		closeSwitcher(t)
	case "pick":
		t.pickSwitcher()
	case "clear-query":
		t.sw.ClearQuery()
		t.dirty = true
	case "backspace":
		t.sw.Backspace()
		t.dirty = true
	case "down", "down-arrow", "down-ctrl-n":
		t.sw.Move(1)
		t.dirty = true
	case "up", "up-arrow", "up-ctrl-p":
		t.sw.Move(-1)
		t.dirty = true
	}
	return true
}

// pickSwitcher closes the float, then goes to its row. A workspace goes to
// focus-workspace, a window to focus-window and a pane to focus. A gap row or
// an empty selection closes silently.
func (t *tui) pickSwitcher() {
	target := ""
	if it, ok := t.sw.Selected(); ok && it.Kind != rail.Gap {
		target = t.sw.Target()
	}
	closeSwitcher(t)
	if target != "" {
		t.dispatchPick(target)
	}
}

// expireSwitcher closes a float whose lone esc never finished arriving. A bare
// esc is the close key, so a stalled one means close, the way a pending escape
// in the other modes resolves to dismiss.
func (t *tui) expireSwitcher(now time.Time) {
	if t.sw == nil || !t.pending {
		return
	}
	if now.Sub(t.escAt) <= escTimeout {
		return
	}
	t.pending = false
	t.inbuf = t.inbuf[:0]
	closeSwitcher(t)
}

// key runs one single-byte key through the main keymap. Tests call it directly;
// processInput goes through armedKey, which also handles longer sequences.
func (t *tui) key(b byte) {
	t.inbuf = append(t.inbuf, b)
	t.pending = false
	t.armedKey()
}

// reload re-reads the default config file and its named theme. On a read, parse
// or theme error it returns the error and leaves the running keymap and theme
// untouched. On success it swaps both in place, marks the frame dirty and
// toasts.
func (t *tui) reload() error {
	cfg, err := keymap.LoadDefaultPath(false)
	if err != nil {
		return err
	}
	t.keymap = cfg
	if t.snap != nil {
		t.snap.WindowLabel = cfg.WindowLabel()
	}
	th := cfg.Theme()
	t.th = &th
	t.dirty = true
	t.notify("config reloaded")
	return nil
}

// runAction runs one action from the main keymap.
func (t *tui) runAction(name string) {
	switch name {
	case "split-right":
		t.action("split", func(m *wire.Msg) { m.Dir = "right" })
	case "split-down", "split-down-alt":
		t.action("split", func(m *wire.Msg) { m.Dir = "down" })
	case "split-left":
		t.action("split", func(m *wire.Msg) { m.Dir = "left" })
	case "split-up":
		t.action("split", func(m *wire.Msg) { m.Dir = "up" })
	case "scrollback":
		t.startScrollOnFocus()
	case "focus-left", "focus-left-arrow":
		t.action("focus-dir", func(m *wire.Msg) { m.Dir = "left" })
	case "focus-down", "focus-down-arrow":
		t.action("focus-dir", func(m *wire.Msg) { m.Dir = "down" })
	case "focus-up", "focus-up-arrow":
		t.action("focus-dir", func(m *wire.Msg) { m.Dir = "up" })
	case "focus-right", "focus-right-arrow":
		t.action("focus-dir", func(m *wire.Msg) { m.Dir = "right" })
	case "next-pane":
		t.action("next-pane", nil)
	case "prev-pane":
		t.action("prev-pane", nil)
	case "close-pane":
		t.action("close-pane", nil)
	case "close-window":
		pane := ""
		if t.snap != nil {
			pane = t.snap.Focus
		}
		wid := t.windowForPane(pane)
		if wid == "" {
			t.notify("no window for " + pane)
			t.disarm()
			return
		}
		t.actionOn(wid, "close-window", nil)
	case "zoom-pane":
		t.action("zoom-pane", nil)
	case "new-window":
		t.action("new-window", nil)
	case "next-window":
		t.action("next-window", nil)
	case "prev-window":
		t.action("prev-window", nil)
	case "new-workspace":
		t.action("new-workspace", nil)
	case "next-workspace":
		t.action("next-workspace", nil)
	case "switcher":
		openSwitcher(t)
	case "picker":
		t.openPicker()
	case "rename-workspace":
		t.openRenamePrompt()
	case "toggle-rail":
		t.toggleRail()
	case "message-box":
		openBoxView(t)
	case "help":
		openKeymapView(t)
		t.disarm()
	case "detach", "detach-alt":
		t.quit = true
	case "dismiss", "dismiss-esc":
		t.disarm()
	case "send-prefix":
		if p := t.km().PrefixBytes(); len(p) > 0 && t.conn != nil {
			_ = t.conn.Send(wire.Msg{Type: "keys", Keys: append([]byte(nil), p...)})
		}
		t.disarm()
	case "reload":
		if err := t.reload(); err != nil {
			t.notify(err.Error())
		}
		t.disarm()
	default:
		if strings.HasPrefix(name, "window-") {
			if n, err := strconv.Atoi(strings.TrimPrefix(name, "window-")); err == nil &&
				n >= 1 && n <= 10 {
				i := n - 1
				t.action("select-window", func(m *wire.Msg) { m.Index = i })
				return
			}
		}
		t.notify(fmt.Sprintf("no binding for %q", name))
		t.disarm()
	}
}

// arm shows the action box. It stays up until an action runs or esc
// dismisses it; it never expires on a timer.
func (t *tui) arm() {
	t.armed = true
	// A prefix that arrived in pieces waited with pending set. The arm starts a
	// fresh sequence, so that wait is over.
	t.pending = false
	t.toasts = nil
	t.dirty = true
}

func (t *tui) disarm() {
	t.armed = false
	t.dirty = true
}

func (t *tui) action(name string, fill func(*wire.Msg)) {
	target := ""
	if t.snap != nil {
		target = t.snap.Focus
	}
	t.actionOn(target, name, fill)
}

func (t *tui) actionOn(target, name string, fill func(*wire.Msg)) {
	m := wire.Msg{Type: "req", Action: name, Target: target}
	if fill != nil {
		fill(&m)
	}
	_ = t.conn.Send(m)
	t.disarm()
}
