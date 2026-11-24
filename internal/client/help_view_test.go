package client

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"dm/internal/cells"
	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/theme"
)

// helpBoxDraw paints the help box for a screen and returns its bytes. The
// notes come from the same config as the rows, so a test cannot pass a short
// label and forget the caveat the overlay paints under it.
func helpBoxDraw(t *testing.T, cols, rows int, cfg *keymap.Config) string {
	t.Helper()
	var buf bytes.Buffer
	drawHelp(&buf, theme.DefaultTheme(), cols, rows, false, helpBoxHint(cfg), cfg.HelpEntries(), cfg.HelpNotes())
	return buf.String()
}

// helpBoxWidths is the widest key and action label in cells.
func helpBoxWidths(keys []keymap.HelpEntry) (keyW, actionW int) {
	for _, e := range keys {
		keyW = layout.MaxInt(keyW, cells.Cells(e.Key))
		actionW = layout.MaxInt(actionW, cells.Cells(e.Action))
	}
	return keyW, actionW
}

// helpBoxPainted is a band replayed from its absolute moves: every cell and
// what was written into it, plus the furthest cell addressed.
type helpBoxPainted struct {
	writes map[[2]int][]rune
	maxRow int
	maxCol int
}

// helpBoxParse replays the band: a move sets the cursor, an SGR changes
// nothing, and a rune takes the cells cells gives it. It fails
// on anything else, so a relative move or an unknown escape cannot slip by.
func helpBoxParse(t *testing.T, s string) helpBoxPainted {
	t.Helper()
	p := helpBoxPainted{writes: map[[2]int][]rune{}}
	row, col := 0, 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			if i+1 >= len(s) || s[i+1] != '[' {
				t.Fatalf("escape %q at %d", s[i:], i)
			}
			j := i + 2
			for j < len(s) && (s[j] == ';' || (s[j] >= '0' && s[j] <= '9')) {
				j++
			}
			if j >= len(s) {
				t.Fatalf("truncated escape at %d: %q", i, s)
			}
			params, final := s[i+2:j], s[j]
			i = j + 1
			switch final {
			case 'm':
				// Colour only: it moves no cursor and paints no cell.
				continue
			case 'H':
				var r, c int
				_, _ = fmt.Sscanf(params, "%d;%d", &r, &c)
				row, col = r-1, c-1
				continue
			default:
				t.Fatalf("band carries %q, want an absolute move", s[i-2:j+1])
			}
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		cell := [2]int{row, col}
		p.writes[cell] = append(p.writes[cell], r)
		if row > p.maxRow {
			p.maxRow = row
		}
		if col+1 > p.maxCol {
			p.maxCol = col + 1
		}
		col += cells.Cells(string(r))
	}
	return p
}

// helpBoxBounds is the smallest rectangle that holds every painted cell.
func helpBoxBounds(t *testing.T, p helpBoxPainted) (x0, y0, x1, y1 int) {
	t.Helper()
	first := true
	for cell := range p.writes {
		if first {
			x0, y0, x1, y1 = cell[1], cell[0], cell[1], cell[0]
			first = false
			continue
		}
		x0 = min(x0, cell[1])
		y0 = min(y0, cell[0])
		x1 = layout.MaxInt(x1, cell[1])
		y1 = layout.MaxInt(y1, cell[0])
	}
	if first {
		t.Fatal("the band painted nothing")
	}
	return x0, y0, x1, y1
}

// helpBoxRowText is row between two columns as the terminal ends up: the last
// write to a cell wins and an unwritten cell is a space.
func helpBoxRowText(p helpBoxPainted, row, from, to int) string {
	var b strings.Builder
	for col := from; col <= to; col++ {
		runes := p.writes[[2]int{row, col}]
		if len(runes) == 0 {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(runes[len(runes)-1])
	}
	return b.String()
}

// TestHelpBoxSizesToWidestRowAt100x40 proves the box holds the default layout
// whole, that both border rows own a row, and that the frame carries its title
// and hint. The default table grew one toggle-secrets row, so the proof runs
// on a 40-row screen. The shelf keys ride the footer rather than a row, so the
// table still fits it.
func TestHelpBoxSizesToWidestRowAt100x40(t *testing.T) {
	t.Parallel()
	cfg := keymap.LoadDefault()
	keys := cfg.HelpEntries()
	g, ok := helpBoxGeometry(100, 40, keys, cfg.HelpNotes())
	if !ok {
		t.Fatal("helpBoxGeometry returned false")
	}
	p := helpBoxParse(t, helpBoxDraw(t, 100, 40, cfg))
	x0, y0, x1, y1 := helpBoxBounds(t, p)

	if want := g.inner + 2; x1-x0+1 != want {
		t.Fatalf("box width = %d, want %d", x1-x0+1, want)
	}
	if x0 < 0 || y0 < 0 || x1 > 99 || y1 > 39 {
		t.Fatalf("box %d,%d..%d,%d is not inside 100x40", x0, y0, x1, y1)
	}
	if want := g.h + 2; y1-y0+1 != want {
		t.Fatalf("box height = %d, want %d", y1-y0+1, want)
	}

	var all strings.Builder
	for row := y0 + 1; row <= y1-1; row++ {
		all.WriteString(helpBoxRowText(p, row, x0+1, x1-1))
		all.WriteString("\n")
	}
	for _, cat := range categorizeHelp(keys) {
		for _, r := range cat {
			if r.header != "" {
				if !strings.Contains(all.String(), r.header) {
					t.Errorf("missing header %q", r.header)
				}
				continue
			}
			if !strings.Contains(all.String(), r.key) {
				t.Errorf("missing key %q", r.key)
			}
			if !strings.Contains(all.String(), r.action) {
				t.Errorf("missing action %q", r.action)
			}
		}
	}

	if got := helpBoxRowText(p, y0, x0+2, x1-1); !strings.HasPrefix(got, "prefix") {
		t.Fatalf("title row = %q, want it to read prefix", got)
	}
	if got := helpBoxRowText(p, y1, x0+2, x1-1); !strings.HasPrefix(got, " esc ") {
		t.Fatalf("hint row = %q, want it to read esc", got)
	}
}

// TestHelpBoxPaintsToggleRowAndCaveat proves the overlay lists ctrl+g and its
// caveat at both sizes the docs promise. It reads the painted cells, not the
// label string, so a reword that widens the action column cannot clip the
// Session rows again without failing here.
func TestHelpBoxPaintsToggleRowAndCaveat(t *testing.T) {
	t.Parallel()
	cfg := keymap.LoadDefault()
	notes := cfg.HelpNotes()
	if len(notes) == 0 {
		t.Fatal("HelpNotes() is empty")
	}
	for _, size := range []struct{ cols, rows int }{{120, 30}, {100, 30}} {
		p := helpBoxParse(t, helpBoxDraw(t, size.cols, size.rows, cfg))
		x0, y0, x1, y1 := helpBoxBounds(t, p)
		var rows []string
		for row := y0 + 1; row <= y1-1; row++ {
			rows = append(rows, helpBoxRowText(p, row, x0+1, x1-1))
		}
		all := strings.Join(rows, "\n")
		if !strings.Contains(all, "Session") {
			t.Errorf("%dx%d: the box paints no Session header", size.cols, size.rows)
		}
		toggle := ""
		for _, row := range rows {
			if strings.Contains(row, "ctrl+g") {
				toggle = row
				break
			}
		}
		if toggle == "" {
			t.Fatalf("%dx%d: the box paints no ctrl+g row", size.cols, size.rows)
		}
		// A narrow box may elide the action label, so the proof is the
		// painted prefix of the label, not the whole string.
		if !strings.Contains(toggle, "toggle secret redact") {
			t.Errorf("%dx%d: ctrl+g row = %q, want it to name the action", size.cols, size.rows, toggle)
		}
		for _, note := range notes {
			if !strings.Contains(all, note) {
				t.Errorf("%dx%d: the box paints no caveat %q", size.cols, size.rows, note)
			}
		}
	}
}

// TestHelpBoxShadow proves the float shadow switch paints marks beside and
// under the help box, stops above the toast row and the bar, and that the
// switch off paints none. The box needs a row to spare under its bottom
// border, so the proof runs at the 120x30 the help scene uses.
func TestHelpBoxShadow(t *testing.T) {
	t.Parallel()
	cfg := keymap.LoadDefault()
	keys := cfg.HelpEntries()
	cols, rows := 120, 30
	g, ok := helpBoxGeometry(cols, rows, keys, cfg.HelpNotes())
	if !ok {
		t.Fatal("helpBoxGeometry returned false")
	}
	var on bytes.Buffer
	drawHelp(&on, theme.DefaultTheme(), cols, rows, true, helpBoxHint(cfg), keys, cfg.HelpNotes())
	p := helpBoxParse(t, on.String())
	x1, y1 := g.x+g.inner, g.y+g.h
	if got := p.writes[[2]int{y1, x1 + 1}]; len(got) == 0 || got[0] != '░' {
		t.Fatalf("shadow beside the right border = %v, want ░", got)
	}
	if got := p.writes[[2]int{y1 + 1, x1}]; len(got) == 0 || got[0] != '░' {
		t.Fatalf("shadow under the box = %v, want ░", got)
	}
	for row := rows - 2; row < rows; row++ {
		for col := 0; col < cols; col++ {
			if got := p.writes[[2]int{row, col}]; len(got) > 0 && got[0] == '░' {
				t.Fatalf("shadow painted the toast row or the bar at %d,%d", row, col)
			}
		}
	}
	var off bytes.Buffer
	drawHelp(&off, theme.DefaultTheme(), cols, rows, false, helpBoxHint(cfg), keys, cfg.HelpNotes())
	p = helpBoxParse(t, off.String())
	for rc, got := range p.writes {
		if len(got) > 0 && got[0] == '░' {
			t.Fatalf("shadow off painted ░ at %d,%d", rc[0], rc[1])
		}
	}
}

// TestHelpBoxStaysInBoundsOnNarrowScreens proves a small screen keeps the box
// off the rail, the toast row and the bar, and inside the screen.
func TestHelpBoxStaysInBoundsOnNarrowScreens(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ cols, rows int }{{40, 15}, {26, 12}} {
		cfg := keymap.LoadDefault()
		keys := cfg.HelpEntries()
		rows := categorizeHelpRows(keys)
		p := helpBoxParse(t, helpBoxDraw(t, c.cols, c.rows, cfg))
		x0, y0, x1, y1 := helpBoxBounds(t, p)
		if x0 < 0 || y0 < 0 || x1 > c.cols-1 || y1 > c.rows-1 {
			t.Fatalf("%dx%d: box %d,%d..%d,%d is past the screen", c.cols, c.rows, x0, y0, x1, y1)
		}
		if p.maxCol > c.cols-1 || p.maxRow > c.rows-1 {
			t.Fatalf("%dx%d: band addresses %d,%d, past the screen", c.cols, c.rows, p.maxRow+1, p.maxCol+1)
		}
		// effectiveRail never exceeds maxRail(cols), so a left border at or
		// right of it clears every rail width.
		if left := layout.MaxRail(c.cols); x0 < left {
			t.Fatalf("%dx%d: box starts at %d, want its border at or right of the rail %d", c.cols, c.rows, x0, left)
		}
		if y1 > c.rows-3 {
			t.Fatalf("%dx%d: box bottom %d touches the toast row or the bar", c.cols, c.rows, y1)
		}
		for cell := range p.writes {
			if cell[0] >= c.rows-2 {
				t.Fatalf("%dx%d: box paints row %d, which belongs to the toast row or the bar", c.cols, c.rows, cell[0])
			}
		}
		if y1-y0+1 >= len(rows)+2 {
			t.Fatalf("%dx%d: box height %d, want trailing rows dropped", c.cols, c.rows, y1-y0+1)
		}
	}
}

// TestHelpBoxNarrowDropsActionColumn proves a narrow box shows the key column
// alone, with no action label bleeding in.
func TestHelpBoxNarrowDropsActionColumn(t *testing.T) {
	t.Parallel()
	cfg := keymap.LoadDefault()
	keys := cfg.HelpEntries()
	rows := categorizeHelpRows(keys)
	keyW, actionW := helpBoxWidths(keys)
	p := helpBoxParse(t, helpBoxDraw(t, 26, 12, cfg))
	x0, y0, x1, y1 := helpBoxBounds(t, p)

	if got, want := x1-x0+1, keyW+2; got != want {
		t.Fatalf("box width = %d, want %d for the key column alone", got, want)
	}
	if x1-x0+1 >= keyW+2+actionW+2 {
		t.Fatalf("box width %d still holds the action column", x1-x0+1)
	}
	for i, r := range rows[:y1-y0-1] {
		var want string
		if r.header != "" {
			want = cells.Pad(cells.Elide(r.header, x1-x0-1), x1-x0-1)
		} else {
			want = cells.Pad(cells.Elide(r.key, x1-x0-1), x1-x0-1)
		}
		if got := helpBoxRowText(p, y0+1+i, x0+1, x1-1); got != want {
			t.Fatalf("row %d = %q, want the key column alone %q", i, got, want)
		}
	}
}

// TestHelpBoxEllipsisMarksCutLabel proves a box too narrow for its key column
// cuts a label and says so.
func TestHelpBoxEllipsisMarksCutLabel(t *testing.T) {
	t.Parallel()
	cfg := keymap.LoadDefault()
	keys := cfg.HelpEntries()
	keyW, _ := helpBoxWidths(keys)
	p := helpBoxParse(t, helpBoxDraw(t, 12, 12, cfg))
	x0, y0, x1, y1 := helpBoxBounds(t, p)
	// The interior is narrower than the key column, so the action column is
	// gone as well as the label being cut.
	if x1-x0-1 >= keyW {
		t.Fatalf("interior %d cells still holds the %d cell key column", x1-x0-1, keyW)
	}
	found := false
	for row := y0 + 1; row <= y1-1; row++ {
		got := strings.TrimRight(helpBoxRowText(p, row, x0+1, x1-1), " ")
		if cells.Cells(got) > x1-x0-1 {
			t.Fatalf("row %d = %q, wider than the %d cell interior", row, got, x1-x0-1)
		}
		if strings.HasSuffix(got, "\u2026") {
			found = true
		}
	}
	if !found {
		t.Fatal("a clipped key label carries no cells.Ellipsis")
	}
}

// TestHelpBoxRowsShareColumns is the misalignment the operator saw: beyond
// the panel's ground fill, every content cell must paint once. The fill
// writes one space on the ground per interior cell, so a cell carries at
// most two writes and the second is never a bare fill.
func TestHelpBoxRowsShareColumns(t *testing.T) {
	t.Parallel()
	cfg := keymap.LoadDefault()
	p := helpBoxParse(t, helpBoxDraw(t, 100, 30, cfg))
	x0, y0, x1, y1 := helpBoxBounds(t, p)

	for row := y0 + 1; row <= y1-1; row++ {
		for col := x0 + 1; col <= x1-1; col++ {
			writes := p.writes[[2]int{row, col}]
			if len(writes) > 2 {
				t.Fatalf("content cell %d,%d painted %d times", row, col, len(writes))
			}
			if len(writes) == 2 && writes[0] != ' ' {
				t.Fatalf("content cell %d,%d's first write is %q, want the fill's space", row, col, writes[0])
			}
		}
	}
}

// helpBoxSectionRows returns the rows of the named section, header excluded.
func helpBoxSectionRows(categories [][]displayRow, section string) []displayRow {
	for _, cat := range categories {
		if len(cat) > 0 && cat[0].header == section {
			return cat[1:]
		}
	}
	return nil
}

// helpBoxSectionHeaders counts the headers that carry the named section.
func helpBoxSectionHeaders(categories [][]displayRow, section string) int {
	n := 0
	for _, cat := range categories {
		if len(cat) > 0 && cat[0].header == section {
			n++
		}
	}
	return n
}

// TestHelpBoxTagMovesRowToNamedSection proves a tagged row leaves its heuristic
// section for the named one, and that an untagged row keeps the heuristic.
func TestHelpBoxTagMovesRowToNamedSection(t *testing.T) {
	t.Parallel()
	cfg, err := keymap.ParseConfig(`prefix "ctrl+a"
keymap "main" {
	"split-right" "|"
	"scrollback" "["
	"zoom-pane" "z" help "Layout"
}`)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	categories := categorizeHelp(cfg.HelpEntries())

	layout := helpBoxSectionRows(categories, "Layout")
	if len(layout) != 2 {
		t.Fatalf("Layout rows = %#v, want split right and zoom pane", layout)
	}
	if layout[0].action != "split right" || layout[1].action != "zoom pane" {
		t.Fatalf("Layout rows = %q, %q, want table order split right then zoom pane", layout[0].action, layout[1].action)
	}
	pane := helpBoxSectionRows(categories, "Pane")
	if len(pane) != 1 || pane[0].action != "scrollback" {
		t.Fatalf("Pane rows = %#v, want the untagged scrollback row", pane)
	}
}

// TestHelpBoxTagJoinsEarlierSection proves a row tagged into a section that
// already opened joins it, with one header, instead of opening a second one.
func TestHelpBoxTagJoinsEarlierSection(t *testing.T) {
	t.Parallel()
	cfg, err := keymap.ParseConfig(`prefix "ctrl+a"
keymap "main" {
	"split-right" "|"
	"scrollback" "["
	"reload" "R" help "Layout"
}`)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	categories := categorizeHelp(cfg.HelpEntries())

	if n := helpBoxSectionHeaders(categories, "Layout"); n != 1 {
		t.Fatalf("Layout headers = %d, want 1", n)
	}
	layout := helpBoxSectionRows(categories, "Layout")
	if len(layout) != 2 || layout[0].action != "split right" || layout[1].action != "reload config" {
		t.Fatalf("Layout rows = %#v, want split right then reload config", layout)
	}
	// The tagged row must not leave a second header where it used to sit.
	if n := helpBoxSectionHeaders(categories, "Utility"); n != 0 {
		t.Fatalf("Utility headers = %d, want 0: the tagged row left its heuristic section", n)
	}
}

// TestHelpBoxCustomSectionTakesUtilityColour proves a section name outside the
// five built-ins paints with the Utility colour, header and rows alike.
func TestHelpBoxCustomSectionTakesUtilityColour(t *testing.T) {
	t.Parallel()
	cfg, err := keymap.ParseConfig(`prefix "ctrl+a"
keymap "main" {
	"split-right" "|"
	"zoom-pane" "z" help "Custom"
}`)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	entries := cfg.HelpEntries()
	categories := categorizeHelp(entries)
	if n := helpBoxSectionHeaders(categories, "Custom"); n != 1 {
		t.Fatalf("Custom headers = %d, want 1", n)
	}
	if got, want := helpSectionColor("Custom"), helpCategoryColors["Utility"]; got != want {
		t.Fatalf("helpSectionColor(Custom) = %q, want the Utility colour %q", got, want)
	}
	out := helpBoxDraw(t, 100, 30, cfg)
	if !strings.Contains(out, helpCategoryColors["Utility"]+"Custom") {
		t.Fatal("the Custom header does not paint with the Utility colour")
	}
	if !strings.Contains(out, helpCategoryColors["Utility"]+"\x1b[1mz") {
		t.Fatal("the Custom row does not paint with the Utility colour")
	}
}

// helpBoxDefaultSHA256 is the sha256 of the default help box at 100x30. A
// config without tags must paint these exact bytes: same cells, same colours,
// same order. Regenerate it by running the test and copying the got value. It
// moved when the footer became a hint bar, and again when the send prefix row
// made way for the question float's toggle: the table stays at 35 rows, because
// one more would push the last section off a 100x40 screen.
const helpBoxDefaultSHA256 = "e15c442e745dbc84337bbeac72bef2da873fb205502696afe51eaac6ba72403f"

// TestHelpBoxDefaultBytesFrozen proves the default config paints a stable
// help box. The five layout and session actions carry their section tags; the
// rest of the default config is untagged.
func TestHelpBoxDefaultBytesFrozen(t *testing.T) {
	t.Parallel()
	cfg := keymap.LoadDefault()
	entries := cfg.HelpEntries()
	want := map[string]string{
		"swap with next / prev pane": "Layout",
		"cycle layout":               "Layout",
		"synchronize panes":          "Session",
		"toggle secret redaction":    "Session",
	}
	for _, e := range entries {
		s, tagged := want[e.Action]
		if e.Section == "" && tagged {
			t.Fatalf("default entry %q owes section %q", e.Action, s)
		}
		if e.Section != "" && (!tagged || e.Section != s) {
			t.Fatalf("default entry %q carries section %q", e.Action, e.Section)
		}
	}
	sum := sha256.Sum256([]byte(helpBoxDraw(t, 100, 30, cfg)))
	if got := hex.EncodeToString(sum[:]); got != helpBoxDefaultSHA256 {
		t.Fatalf("default help box bytes changed: sha256 %s, want %s", got, helpBoxDefaultSHA256)
	}
}

// TestHelpBoxFooterCarriesTheShelfKeys proves the overlay names the shelf's
// three keys. They ride the footer, not a row: one more table row pushes the
// last section off a 100x40 screen, and that table must fit whole.
func TestHelpBoxFooterCarriesTheShelfKeys(t *testing.T) {
	t.Parallel()
	cfg := keymap.LoadDefault()
	p := helpBoxParse(t, helpBoxDraw(t, 100, 40, cfg))
	x0, _, x1, y1 := helpBoxBounds(t, p)
	hint := helpBoxRowText(p, y1, x0+2, x1-1)
	for _, want := range []string{"esc", "f/F/G  shelf", "A  ask"} {
		if !strings.Contains(hint, want) {
			t.Fatalf("help footer = %q, want %q in it", hint, want)
		}
	}
	// The three keys come from the keymap, so a remap moves the footer with it.
	rc, err := keymap.ParseConfig(`prefix "ctrl+a"
keymap "main" {
	"shelve-pane" "ctrl+s"
	"toggle-shelf" "ctrl+t"
	"pop-pane" "ctrl+p"
}`)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if got := barText(helpBoxHint(rc), 200); !strings.Contains(got, "ctrl+s/ctrl+t/ctrl+p  shelf") {
		t.Fatalf("remapped footer = %q, want the rebound shelf keys", got)
	}
}
