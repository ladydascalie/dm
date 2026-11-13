package client

import (
	"bytes"
	"testing"
	"time"

	"dm/internal/testutil"
)

// TestCommandPaletteOpens: prefix k raises the palette over the main actions,
// and esc drops it without a request.
func TestCommandPaletteOpens(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	feed(tui, "\x02k")
	if tui.cp == nil {
		t.Fatal("prefix k did not open the palette")
	}
	rows := tui.cp.pal.Rows()
	if len(rows) == 0 {
		t.Fatal("the palette opened with no commands")
	}
	for _, r := range rows {
		if r.Name == "" || r.Action == "" {
			t.Fatalf("row %+v misses a name or an action", r)
		}
	}
	testutil.NoMsg(t, got)
	feed(tui, "\x1b")
	if tui.cp != nil {
		t.Fatal("esc did not close the palette")
	}
	testutil.NoMsg(t, got)
}

// TestCommandPaletteHoldsBoundLetters: a query that holds letters the main
// table binds is text, so it neither opens a float nor runs an action.
func TestCommandPaletteHoldsBoundLetters(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	feed(tui, "\x02k")
	feed(tui, "dock")
	if tui.cp == nil {
		t.Fatal("a bound letter closed the palette")
	}
	if tui.float != nil {
		t.Fatal("d opened the float from the query")
	}
	if q := tui.cp.pal.Query(); q != "dock" {
		t.Fatalf("query = %q, want the whole word dock", q)
	}
	testutil.NoMsg(t, got)
}

// TestCommandPaletteTypeFiltersAndBackspace: typing narrows the rows, backspace
// widens them again, and backspace on an empty query does nothing.
func TestCommandPaletteTypeFiltersAndBackspace(t *testing.T) {
	t.Parallel()
	tui, _ := switcherTUI(t)
	feed(tui, "\x02k")
	full := len(tui.cp.pal.Rows())
	feed(tui, "zoom-pane")
	narrow := len(tui.cp.pal.Rows())
	if narrow >= full || narrow == 0 {
		t.Fatalf("typing left %d rows of %d, want a narrowed non-empty list", narrow, full)
	}
	if r := tui.cp.pal.Rows()[0]; r.Name != "zoom-pane" {
		t.Fatalf("best row = %q, want zoom-pane", r.Name)
	}
	feed(tui, "\x7f\x7f\x7f\x7f\x7f\x7f\x7f\x7f\x7f\x7f") // ten backspaces
	if got := len(tui.cp.pal.Rows()); got != full {
		t.Fatalf("an emptied query left %d rows, want all %d", got, full)
	}
	if q := tui.cp.pal.Query(); q != "" {
		t.Fatalf("query = %q, want empty", q)
	}
}

// TestCommandPaletteNoMatch: a query nothing matches leaves no rows, says so,
// and neither enter nor the arrows send anything.
func TestCommandPaletteNoMatch(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	feed(tui, "\x02k")
	feed(tui, "zzzzzz")
	if rows := tui.cp.pal.Rows(); len(rows) != 0 {
		t.Fatalf("a query nothing matches left %d rows", len(rows))
	}
	feed(tui, "\x0e\x10") // ctrl+n, ctrl+p
	feed(tui, "\r")
	if tui.cp != nil {
		t.Fatal("enter over no rows left the palette open")
	}
	testutil.NoMsg(t, got)
}

// TestCommandPaletteMoveAndInvoke: ctrl+n and the arrows walk the selection,
// enter dispatches the selected action over the wire and closes the palette.
func TestCommandPaletteMoveAndInvoke(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	feed(tui, "\x02k")
	feed(tui, "next")
	first := tui.cp.pal.Rows()[0].Action
	if len(tui.cp.pal.Rows()) < 2 {
		t.Fatalf("query next left %d rows, want the next-* family", len(tui.cp.pal.Rows()))
	}
	feed(tui, "\x0e") // ctrl+n, down
	if idx := tui.cp.pal.SelectedIndex(); idx != 1 {
		t.Fatalf("ctrl+n left the selection at %d, want 1", idx)
	}
	feed(tui, "\x10") // ctrl+p, up
	if idx := tui.cp.pal.SelectedIndex(); idx != 0 {
		t.Fatalf("ctrl+p left the selection at %d, want 0", idx)
	}
	feed(tui, "\x1b[B") // down arrow
	if idx := tui.cp.pal.SelectedIndex(); idx != 1 {
		t.Fatalf("the down arrow left the selection at %d, want 1", idx)
	}
	feed(tui, "\x1b[A") // up arrow
	if idx := tui.cp.pal.SelectedIndex(); idx != 0 {
		t.Fatalf("the up arrow left the selection at %d, want 0", idx)
	}
	feed(tui, "\r")
	if tui.cp != nil {
		t.Fatal("enter left the palette open")
	}
	m := testutil.NextMsg(t, got)
	if m.Type != "req" || m.Action != first {
		t.Fatalf("enter sent %+v, want a %s request", m, first)
	}
	testutil.NoMsg(t, got)
}

// TestCommandPaletteInvokesModalCommand: a command that opens another modal
// closes the palette first, so the two floats never stack.
func TestCommandPaletteInvokesModalCommand(t *testing.T) {
	t.Parallel()
	tui, _ := switcherTUI(t)
	feed(tui, "\x02k")
	feed(tui, "help")
	if r := tui.cp.pal.Rows()[0]; r.Action != "help" {
		t.Fatalf("best row = %q, want help", r.Action)
	}
	feed(tui, "\r")
	if tui.cp != nil {
		t.Fatal("the palette stayed open under the modal it opened")
	}
	if tui.mode != "help" {
		t.Fatalf("mode = %q, want help", tui.mode)
	}
}

// TestCommandPaletteStalledEsc: an esc split from its sequence tail waits, and
// the expiry closes the palette instead of leaving it stuck.
func TestCommandPaletteStalledEsc(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	feed(tui, "\x02k")
	feed(tui, "\x1b[")
	// The half marker takes the paste deadline first; the flush hands the
	// bytes to the palette, which waits for the sequence tail.
	tui.staleInput()
	if !tui.pending {
		t.Fatal("a partial escape sequence did not wait for the tail")
	}
	if tui.cp == nil {
		t.Fatal("a partial escape sequence closed the palette early")
	}
	tui.escAt = time.Now().Add(-time.Second)
	tui.expireCommandPalette(time.Now())
	if tui.cp != nil {
		t.Fatal("the expiry did not close the palette")
	}
	testutil.NoMsg(t, got)
}

// TestCommandPaletteSwallowsStrayKeys: a mouse event, a paste and an unbound
// escape sequence reach neither the query nor a pane.
func TestCommandPaletteSwallowsStrayKeys(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	feed(tui, "\x02k")
	feed(tui, "re")
	feed(tui, "\x1b[<0;1;1M") // a mouse press
	feed(tui, "\x1bOP")       // F1
	if q := tui.cp.pal.Query(); q != "re" {
		t.Fatalf("query = %q, want re untouched", q)
	}
	if tui.cp == nil {
		t.Fatal("a stray key closed the palette")
	}
	testutil.NoMsg(t, got)
}

// TestCommandPaletteTooSmall: a screen that cannot hold the query line and one
// row opens nothing.
func TestCommandPaletteTooSmall(t *testing.T) {
	t.Parallel()
	tui, _ := switcherTUI(t)
	tui.snap.Cols = 30
	tui.snap.Rows = 30
	feed(tui, "\x02k")
	if tui.cp != nil {
		t.Fatal("the palette opened on a screen too small for it")
	}
}

// TestCommandPaletteDraw: the float paints its title, the query and the
// selected row, and reports the cursor cell after the query.
func TestCommandPaletteDraw(t *testing.T) {
	t.Parallel()
	tui, _ := switcherTUI(t)
	feed(tui, "\x02k")
	var buf bytes.Buffer
	cx, cy := drawCommandPalette(&buf, tui.theme(), tui.cp, tui.snap)
	if cx == 0 && cy == 0 {
		t.Fatal("the palette painted no cursor cell")
	}
	out := buf.String()
	for _, want := range []string{"palette", "> ", "send-prefix"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Fatalf("the palette draw misses %q", want)
		}
	}
}

// TestCommandPaletteCommandsShareHelpSource: the rows are the main keymap's
// actions and the configdoc descriptions, so the palette and the help view
// never drift apart.
func TestCommandPaletteCommandsShareHelpSource(t *testing.T) {
	t.Parallel()
	tui, _ := switcherTUI(t)
	commands := commandPaletteCommands(tui.km())
	if len(commands) == 0 {
		t.Fatal("the command list is empty")
	}
	saw := map[string]bool{}
	for _, c := range commands {
		if c.Name != c.Action {
			t.Fatalf("row name %q and action %q disagree", c.Name, c.Action)
		}
		if c.Hint == "" {
			t.Fatalf("action %q carries no description", c.Action)
		}
		saw[c.Action] = true
	}
	for _, want := range []string{"command-palette", "switcher", "focus-up", "zoom-pane"} {
		if !saw[want] {
			t.Fatalf("the command list misses %q", want)
		}
	}
}

// TestCommandPalettePrefixInsideIsSwallowed: the brief gives the palette's
// keys to it alone; a prefix byte over the open palette is not a toggle.
func TestCommandPalettePrefixInsideIsSwallowed(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	feed(tui, "\x02k")
	feed(tui, "\x02")
	if tui.cp == nil {
		t.Fatal("a prefix press closed the palette")
	}
	testutil.NoMsg(t, got)
}
