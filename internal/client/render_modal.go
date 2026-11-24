package client

import (
	"bytes"
	"fmt"
	"strings"

	"dm/internal/cells"
	"dm/internal/hintbar"
	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/panel"
	"dm/internal/theme"
	"dm/internal/transport"
	"dm/internal/ui"
	"dm/internal/wire"
)

// drawSessions paints the centred session picker. The panel owns the border,
// the title row, the interior clip and the key hint on the bottom border; the
// picker owns its rows and the selection highlight. The title row is a fill
// row: dim, and its trailing cells are spaces rather than the border rune, so
// the row is one write and its cells are the picker's. The hint rides the
// border, so the picker spends no interior row on it.
func drawSessions(buf *bytes.Buffer, th theme.Theme, cols, rows int, sessions []transport.SessionInfo, sel int, current string, km *keymap.Config) {
	g := th.Glyphs
	w := 46
	if w > cols-2 {
		w = cols - 2
	}
	h := len(sessions) + 3
	x := (cols - w) / 2
	y := (rows - h) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	inner := layout.Rect{X: x + 1, Y: y + 1, W: w - 2, H: len(sessions) + 1}
	panel.PaintPanel(buf, th, inner, panel.PanelTitle{
		Text:  g.BorderHorizontal + " switch session ",
		Style: "\x1b[0m\x1b[90m",
		Fill:  " ",
	}, sessionsHint(km), false, func(b *bytes.Buffer, r layout.Rect) {
		for i, s := range sessions {
			mark := " "
			if i == sel {
				mark = ">"
			}
			state := "dead"
			if s.Live {
				state = "live"
			}
			now := ""
			if s.Name == current {
				now = "now"
			}
			colour := "\x1b[0m\x1b[90m"
			if i == sel {
				colour = "\x1b[0m\x1b[97m"
			}
			line := cells.Pad(mark+" "+cells.Pad(s.Name, 18)+" "+cells.Pad(state, 4)+" "+cells.Pad(now, 3), r.W)
			fmt.Fprintf(b, "\x1b[%d;%dH%s%s", r.Y+i+1, r.X+1, colour, line)
		}
	})
}

// sessionsHint is the picker's bottom border, named from the picker keymap so a
// rebind moves it.
func sessionsHint(km *keymap.Config) *hintbar.Bar {
	key := func(action string) string {
		if km == nil {
			return ""
		}
		return km.Key("picker", action)
	}
	return &hintbar.Bar{Hints: []hintbar.Hint{
		{Keys: []string{key("down"), key("up")}, Label: "move"},
		{Keys: []string{key("pick")}, Label: "switch"},
		{Keys: []string{key("close-esc")}, Label: "cancel"},
	}}
}

// renamePromptWidth is the inner width the rename float asks for. A narrower
// pane area wins, so the box grows only as far as the cells allow.
const renamePromptWidth = 40

// renameBoxGeometry places the rename prompt's float over the pane area. The
// interior is one input row; a border row sits above and below it. The box is
// centred over the pane area and clamped clear of the rail, the toast row and
// the status bar, so both borders own a row and no band under it is covered.
// It reports false when the screen cannot hold the box.
func renameBoxGeometry(cols, rows, rail int) (layout.Rect, bool) {
	if cols < 1 || rows < 5 {
		return layout.Rect{}, false
	}
	rail = layout.EffectiveRail(cols, rail)
	avail := cols - rail - 2
	if avail < 3 {
		return layout.Rect{}, false
	}
	w := renamePromptWidth
	if w > avail {
		w = avail
	}
	x := rail + (cols-rail-(w+2))/2
	if x < rail {
		x = rail
	}
	if x+w+2 > cols {
		x = cols - w - 2
	}
	// The box is three rows: a top border, the input row and a bottom border.
	// The toast row holds rows-2 and the bar rows-1, so the bottom border stops
	// at rows-3.
	top := (rows - 4) / 2
	if top < 0 {
		top = 0
	}
	if top > rows-5 {
		top = rows - 5
	}
	return layout.Rect{X: x + 1, Y: top + 1, W: w, H: 1}, true
}

// drawRename paints the rename prompt as a floating pane over the pane area.
// The title names the action, the kind and the id: a project is set, not
// renamed, so that title reads "set project <id>", and the ask float's answer
// prompt reads "answer". The hint names the keys the
// rename keymap binds to submit and cancel, and the one input row holds the
// label, the draft and the caret. The row is the ui.Input component: the draft
// takes what is left and loses its head first, so a long name still shows its
// end and the caret stays on screen. The kind is optional: a call without it
// renames a workspace, as the frozen prompt test expects. A screen too small to
// hold the box paints nothing.
func drawRename(buf *bytes.Buffer, th theme.Theme, snap *wire.Snapshot, id string, draft ui.Input, shadow bool, km *keymap.Config, kind ...string) {
	if snap == nil {
		return
	}
	k := "workspace"
	if len(kind) > 0 && kind[0] != "" {
		k = kind[0]
	}
	inner, ok := renameBoxGeometry(snap.Cols, snap.Rows, snap.Rail)
	if !ok {
		return
	}
	title := "rename " + k + " " + id
	switch k {
	case "project":
		title = "set project " + id
	case askPromptKind:
		// The answer prompt is not a rename: it names the question it
		// answers, and the ask float under it stays on screen.
		title = "answer"
	}
	f := &panel.FloatingPane{
		ID:    "rename",
		Rect:  inner,
		Title: title,
		Hint:  renameHint(km),
		Paint: func(b *bytes.Buffer, r layout.Rect) {
			renameInputRow(b, th, r, k, draft)
		},
	}
	// The shadow clips above the toast row and the bar: no float owns those
	// rows, so its shadow does not paint on them either.
	if shadow {
		panel.PaintShadow(buf, th, inner, snap.Cols, snap.Rows-2)
	}
	f.Draw(buf, th)
}

// renameHint is the prompt's bottom border: the keys the rename keymap binds to
// submit and cancel, so a rebind moves the hint with the keys the prompt
// dispatches on.
func renameHint(km *keymap.Config) *hintbar.Bar {
	key := func(action string) string {
		if km == nil {
			return ""
		}
		return km.Key("rename", action)
	}
	return &hintbar.Bar{Hints: []hintbar.Hint{
		{Keys: []string{key("submit")}},
		{Keys: []string{key("cancel")}},
	}}
}

// renameInputRow paints the rename float's one input row: the dim label, the
// draft and the caret. The label names the kind: a project prompt reads
// "project: ", an answer prompt "answer: ", every other kind " name: ".
func renameInputRow(buf *bytes.Buffer, th theme.Theme, r layout.Rect, kind string, draft ui.Input) {
	label := " name: "
	switch kind {
	case "project":
		label = "project: "
	case askPromptKind:
		label = "answer: "
	}
	ui.DrawInput(buf, th, ui.InputProps{
		Rect:   r,
		Prompt: label,
		Ground: th.Palette.RailBG,
		Dim:    th.Palette.RailDim,
		Text:   th.Palette.RailText,
		Caret:  th.Palette.BarAccent,
		Active: true,
	}, draft)
}

// drawSearchPrompt paints the scrollback query row, on the row above the status
// bar. It reads like the rename prompt's input row: the dim label, the draft
// and the caret, on the rail ground across the whole row.
func drawSearchPrompt(buf *bytes.Buffer, th theme.Theme, snap *wire.Snapshot, draft ui.Input) {
	if snap == nil || snap.Rows < 3 || snap.Cols < 1 {
		return
	}
	ui.DrawInput(buf, th, ui.InputProps{
		Rect:   layout.Rect{X: 0, Y: snap.Rows - 2, W: snap.Cols, H: 1},
		Prompt: " search: ",
		Ground: th.Palette.RailBG,
		Dim:    th.Palette.RailDim,
		Text:   th.Palette.RailText,
		Caret:  th.Palette.BarAccent,
		Active: true,
	}, draft)
}

// drawHelp paints the bottom-right action preview from the configured keymap
// rows. The box sizes to its widest row and is painted by the floating pane
// primitive, so the top border carries the word prefix and the bottom border
// names the key that closes it. A note paints as its own row under the
// columns. The box stays clear of the rail, the toast row and the status bar:
// it gives up columns, then its trailing rows, before it paints over any of
// them.
func drawHelp(buf *bytes.Buffer, th theme.Theme, cols, rows int, shadow bool, hint *hintbar.Bar, keys []keymap.HelpEntry, notes []string) {
	g, ok := helpBoxGeometry(cols, rows, keys, notes)
	if !ok {
		return
	}
	f := &panel.FloatingPane{
		Rect:  layout.Rect{X: g.x, Y: g.y, W: g.inner, H: g.h},
		Title: "prefix",
		Hint:  hint,
		Paint: func(b *bytes.Buffer, inner layout.Rect) {
			helpBoxRows(b, inner, g)
		},
	}
	// The shadow clips above the toast row and the bar: no float owns those
	// rows, so its shadow does not paint on them either.
	if shadow {
		panel.PaintShadow(buf, th, f.Rect, cols, rows-2)
	}
	f.Draw(buf, th)
}

// helpBoxHint is the armed help box's bottom border: the key that closes it,
// then the shelf's keys. The armed box owns esc, and the keymap binds no action
// for it, so it stays literal.
//
// The shelf keeps no row of its own in the table: the whole default table has
// to fit a 100x40 screen, and one more row pushes the last section off it. The
// footer carries the three keys instead, so the overlay still names them.
func helpBoxHint(cfg *keymap.Config) *hintbar.Bar {
	hints := []hintbar.Hint{{Keys: []string{"esc"}}}
	var shelf []string
	for _, action := range []string{"shelve-pane", "toggle-shelf", "pop-pane"} {
		if k := cfg.Key("main", action); k != "" {
			shelf = append(shelf, k)
		}
	}
	if len(shelf) > 0 {
		hints = append(hints, hintbar.Hint{Keys: shelf, Label: "shelf"})
	}
	// The ask toggle rides here too: one more table row pushes the last
	// section off a 100x40 screen.
	if k := cfg.Key("main", "toggle-ask"); k != "" {
		hints = append(hints, hintbar.Hint{Keys: []string{k}, Label: "ask"})
	}
	return &hintbar.Bar{Hints: hints}
}

// displayRow is one row of the help box: a category header or a key-action pair.
type displayRow struct {
	header string
	key    string
	action string
	color  string
}

// categorizeHelp groups the entries into sections, each with its own header and
// rows. A row's section is its tag when it has one, and the heuristic category
// otherwise. Sections appear in first-appearance order, so a row tagged into an
// earlier section joins it instead of opening a second header. A row keeps the
// table order inside its section.
func categorizeHelp(keys []keymap.HelpEntry) [][]displayRow {
	var categories [][]displayRow
	index := make(map[string]int, len(keys))
	for _, e := range keys {
		section := e.Section
		if section == "" {
			section = helpCategory(e.Action)
		}
		i, ok := index[section]
		if !ok {
			i = len(categories)
			index[section] = i
			categories = append(categories, []displayRow{{header: section, color: helpSectionColor(section)}})
		}
		categories[i] = append(categories[i], displayRow{key: e.Key, action: e.Action, color: helpSectionColor(section)})
	}
	return categories
}

// categorizeHelpRows flattens the categories into a single row list.
func categorizeHelpRows(keys []keymap.HelpEntry) []displayRow {
	cats := categorizeHelp(keys)
	var rows []displayRow
	for _, cat := range cats {
		rows = append(rows, cat...)
	}
	return rows
}

// helpCategory returns the category name for an action label.
func helpCategory(action string) string {
	switch {
	case strings.Contains(action, "split"):
		return "Layout"
	case strings.Contains(action, "pane") || strings.Contains(action, "scrollback"):
		return "Pane"
	case strings.Contains(action, "window") || strings.Contains(action, "win"):
		return "Window"
	case strings.Contains(action, "workspace") || strings.Contains(action, "session"):
		return "Session"
	default:
		return "Utility"
	}
}

var helpCategoryColors = map[string]string{
	"Layout":  "\x1b[96m", // cyan
	"Pane":    "\x1b[92m", // green
	"Window":  "\x1b[95m", // magenta
	"Session": "\x1b[93m", // yellow
	"Utility": "\x1b[94m", // blue
}

// helpSectionColor returns the colour a section paints. The five built-in names
// keep their own colour; any other name takes the Utility colour.
func helpSectionColor(section string) string {
	if color, ok := helpCategoryColors[section]; ok {
		return color
	}
	return helpCategoryColors["Utility"]
}

// helpColumn is one column of the help box: the key and action budgets and the
// rows it lists.
type helpColumn struct {
	keyW    int
	actionW int
	rows    []displayRow
}

// newHelpColumn sizes a column to its categories, flattened into rows. A
// positive actionCap cuts the action budget: the rows keep their full text and
// helpBoxRows elides what does not fit, so the column can take part in a split
// the whole labels would not fit.
func newHelpColumn(categories [][]displayRow, actionCap int) helpColumn {
	var rows []displayRow
	for _, cat := range categories {
		rows = append(rows, cat...)
	}
	keyW, actionW := 0, 0
	for _, r := range rows {
		if r.header == "" {
			keyW = layout.MaxInt(keyW, cells.Cells(r.key))
			actionW = layout.MaxInt(actionW, cells.Cells(r.action))
		}
	}
	if keyW < 1 {
		keyW = 1
	}
	if actionCap > 0 && actionW > actionCap {
		actionW = actionCap
	}
	return helpColumn{keyW: keyW, actionW: actionW, rows: rows}
}

// width is the cells the column paints.
func (c helpColumn) width() int {
	if c.actionW > 0 {
		return c.keyW + 2 + c.actionW
	}
	return c.keyW
}

// helpBoxLayout is the box drawHelp paints: the content origin and width, the
// number of rows that fit, the columns it draws and the notes it paints under
// them. Each note owns one of the h rows, so the columns get h-len(notes).
type helpBoxLayout struct {
	x, y    int
	inner   int
	h       int
	columns []helpColumn
	notes   []string
}

// layoutHelpColumns picks the number of columns and the split of categories
// into them. It prefers more columns when they fit, and balances the heights.
//
// A split that fits the width but not maxRows loses its tail sections, and the
// Session rows sit at the tail, so it then retries with a cut action column:
// the widest cap that still fits both the width and the rows wins, and more
// columns are preferred at that cap. Whole labels always win over cut ones, so
// the cap only reaches a screen too small to hold the table whole.
func layoutHelpColumns(categories [][]displayRow, avail, maxRows int) []helpColumn {
	n := len(categories)
	if n == 0 {
		return nil
	}
	maxCols := 4
	if n < maxCols {
		maxCols = n
	}
	var fallback []helpColumn
	for nCols := maxCols; nCols >= 1; nCols-- {
		cols := bestColumnSplit(categories, nCols, avail, 0)
		if cols == nil {
			continue
		}
		if helpColumnsHeight(cols) <= maxRows {
			return cols
		}
		if fallback == nil {
			fallback = cols
		}
	}
	for cap := widestAction(categories) - 1; cap > 0; cap-- {
		for nCols := maxCols; nCols >= 1; nCols-- {
			if cols := bestColumnSplit(categories, nCols, avail, cap); cols != nil && helpColumnsHeight(cols) <= maxRows {
				return cols
			}
		}
	}
	// Nothing fits the rows: keep the whole labels and let the tail clip.
	return fallback
}

// helpColumnsHeight is the tallest column in the split.
func helpColumnsHeight(cols []helpColumn) int {
	h := 0
	for _, c := range cols {
		h = layout.MaxInt(h, len(c.rows))
	}
	return h
}

// widestAction is the widest action label in cells.
func widestAction(categories [][]displayRow) int {
	w := 0
	for _, cat := range categories {
		for _, r := range cat {
			w = layout.MaxInt(w, cells.Cells(r.action))
		}
	}
	return w
}

// bestColumnSplit finds the split of categories into nCols columns that fits
// in avail cells and has the smallest maximum height. Ties go to the narrowest
// box.
func bestColumnSplit(categories [][]displayRow, nCols, avail, actionCap int) []helpColumn {
	n := len(categories)
	var best []helpColumn
	bestHeight := int(^uint(0) >> 1)
	bestWidth := 0

	var visit func(start, remaining int, current []helpColumn)
	visit = func(start, remaining int, current []helpColumn) {
		if remaining == 1 {
			all := make([]helpColumn, len(current)+1)
			copy(all, current)
			all[len(current)] = newHelpColumn(categories[start:], actionCap)
			w := 0
			h := 0
			for i, c := range all {
				if i > 0 {
					w += 3
				}
				w += c.width()
				h = layout.MaxInt(h, len(c.rows))
			}
			if w <= avail {
				if h < bestHeight || (h == bestHeight && w < bestWidth) {
					bestHeight = h
					bestWidth = w
					best = all
				}
			}
			return
		}
		for i := start; i <= n-remaining; i++ {
			c := newHelpColumn(categories[start:i+1], actionCap)
			visit(i+1, remaining-1, append(current, c))
		}
	}
	visit(0, nCols, nil)
	return best
}

// helpBoxGeometry sizes the box to its widest row and clamps it into the cells
// no other band owns. The fit is X-1 >= rail, X+width-1 <= cols-1, Y-1 >= 0 and
// Y+h <= rows-3, so both borders own a row and the toast row and the bar stay
// clear. It reports false when the screen cannot hold even a key column.
func helpBoxGeometry(cols, rows int, keys []keymap.HelpEntry, notes []string) (helpBoxLayout, bool) {
	var g helpBoxLayout
	if cols < 3 || rows < 5 || len(keys) == 0 {
		return g, false
	}
	categories := categorizeHelp(keys)
	left := layout.MaxRail(cols) + 1
	avail := cols - left
	if avail < 3 {
		return g, false
	}
	// A note wider than the widest box cannot paint, so it does not claim a
	// row either.
	notes = helpNotesThatFit(notes, avail-2)
	helpCols := layoutHelpColumns(categories, avail-2, rows-4-len(notes))
	if helpCols == nil {
		// A narrow screen keeps the key column alone.
		col := newHelpColumn(categories, 0)
		col.actionW = 0
		if room := avail - 2; col.keyW > room {
			col.keyW = room
		}
		if col.keyW < 1 {
			return g, false
		}
		helpCols = []helpColumn{col}
	}
	inner := 0
	h := 0
	for i, c := range helpCols {
		if i > 0 {
			inner += 3
		}
		inner += c.width()
		h = layout.MaxInt(h, len(c.rows))
	}
	if inner < 1 || inner+2 > avail {
		return g, false
	}
	if n := helpNotesWidth(notes); n > inner {
		inner = n
	}
	h += len(notes)
	width := inner + 2
	x := cols - width - 2
	if x < left {
		x = left
	}
	if h > rows-4 {
		h = rows - 4
	}
	// The notes paint last, so a clamped box keeps them and gives up the
	// trailing column rows instead. A box with no room for both drops them.
	if h <= len(notes) {
		notes = nil
	}
	if h < 1 {
		return g, false
	}
	y := rows - (h + 2) - 2
	if y < 1 {
		y = 1
	}
	if y+h > rows-3 {
		return g, false
	}
	return helpBoxLayout{x: x, y: y, inner: inner, h: h, columns: helpCols, notes: notes}, true
}

// helpNotesThatFit drops the notes wider than room, so a screen too narrow for
// a note keeps the key column instead of losing the box.
func helpNotesThatFit(notes []string, room int) []string {
	var out []string
	for _, note := range notes {
		if cells.Cells(note) <= room {
			out = append(out, note)
		}
	}
	return out
}

// helpNotesWidth is the widest note in cells.
func helpNotesWidth(notes []string) int {
	w := 0
	for _, note := range notes {
		w = layout.MaxInt(w, cells.Cells(note))
	}
	return w
}

// helpBoxRows paints every content row of the box. Each row starts with an
// absolute cursor move and writes exactly its column's cells, so it neither
// keeps a glyph the frame before left nor paints past the border. A header is
// a coloured line, a key is bright and an action label is dim. Columns are
// separated by a dim vertical bar. The notes paint last, one dim row each
// across the whole interior, so they never share a row with a column.
func helpBoxRows(buf *bytes.Buffer, inner layout.Rect, g helpBoxLayout) {
	contentRows := g.h - len(g.notes)
	xOff := 0
	for ci, col := range g.columns {
		if ci > 0 {
			for i := 0; i < contentRows; i++ {
				y := inner.Y + i
				fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m\x1b[90m \u2502 \x1b[0m", y+1, inner.X+xOff+1)
			}
			xOff += 3
		}
		maxRows := contentRows
		if len(col.rows) < maxRows {
			maxRows = len(col.rows)
		}
		for i, r := range col.rows[:maxRows] {
			y := inner.Y + i
			x := inner.X + xOff
			if r.header != "" {
				color := r.color
				if color == "" {
					color = "\x1b[1m"
				}
				header := cells.Pad(cells.Elide(r.header, col.width()), col.width())
				fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m%s%s\x1b[0m", y+1, x+1, color, header)
				continue
			}
			color := r.color
			if color == "" {
				color = "\x1b[97m"
			}
			if col.actionW > 0 {
				fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, x+1)
				helpBoxKey(buf, r.key, color, col.keyW)
				buf.WriteString("  \x1b[90m")
				buf.WriteString(cells.Pad(cells.Elide(r.action, col.actionW), col.actionW))
				buf.WriteString("\x1b[0m")
				continue
			}
			fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, x+1)
			helpBoxKey(buf, r.key, color, col.width())
			buf.WriteString("\x1b[0m")
		}
		for i := maxRows; i < contentRows; i++ {
			y := inner.Y + i
			x := inner.X + xOff
			fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m%s\x1b[0m", y+1, x+1, strings.Repeat(" ", col.width()))
		}
		xOff += col.width()
	}
	for i, note := range g.notes {
		y := inner.Y + contentRows + i
		fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m\x1b[90m%s\x1b[0m", y+1, inner.X+1,
			cells.Pad(cells.Elide(note, g.inner), g.inner))
	}
}

// helpBoxKey paints a key label with each alternative in the category colour
// and the " / " separator plain, so a literal slash reads as a key and a slash
// between keys reads as a separator. It pads the result to budget cells.
func helpBoxKey(buf *bytes.Buffer, key, color string, budget int) {
	key = cells.Elide(key, budget)
	parts := strings.Split(key, " / ")
	used := 0
	for i, part := range parts {
		if i > 0 {
			buf.WriteString("\x1b[0m / ")
			used += 3
		}
		buf.WriteString(color)
		buf.WriteString("\x1b[1m")
		buf.WriteString(part)
		used += cells.Cells(part)
	}
	buf.WriteString("\x1b[0m")
	if n := budget - used; n > 0 {
		buf.WriteString(strings.Repeat(" ", n))
	}
}
