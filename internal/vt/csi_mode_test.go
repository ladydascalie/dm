package vt

import "testing"

// modeTerm returns a terminal of w by h with s already written and flushed.
func modeTerm(t *testing.T, w, h int, s string) *Term {
	t.Helper()
	term := NewTerm(w, h)
	writeTerm(term, s)
	return term
}

// writeTerm writes s and flushes the pending cluster, so a test can look at the
// grid as soon as the call returns.
func writeTerm(term *Term, s string) {
	term.Write([]byte(s))
	term.Flush()
}

func TestSMInsertMode(t *testing.T) {
	term := writeAll(t, "\x1b[4h")
	if !term.grid.Modes().Insert {
		t.Fatal("SM 4 did not set insert mode")
	}
	writeTerm(term, "\x1b[4l")
	if term.grid.Modes().Insert {
		t.Fatal("RM 4 did not clear insert mode")
	}
}

func TestSMLineFeedMode(t *testing.T) {
	term := writeAll(t, "\x1b[20h")
	if !term.grid.Modes().LineFeedNewLine {
		t.Fatal("SM 20 did not set line feed/new line mode")
	}
	writeTerm(term, "\x1b[20l")
	if term.grid.Modes().LineFeedNewLine {
		t.Fatal("RM 20 did not clear line feed/new line mode")
	}
}

func TestSMUnknownModeIsIgnored(t *testing.T) {
	term := writeAll(t, "\x1b[19h\x1b[19l")
	if got := term.grid.Modes(); got != defaultModes() {
		t.Fatalf("Modes() = %+v, want the defaults untouched", got)
	}
}

func TestDECSETOrigin(t *testing.T) {
	term := NewTerm(10, 6)
	term.grid.SetScrollRegion(ScrollRegion{Top: 2, Bottom: 4, Left: 3, Right: 8})
	writeTerm(term, "\x1b[?6h")
	if !term.grid.Modes().Origin {
		t.Fatal("DECSET 6 did not set origin mode")
	}
	if c := term.grid.Cursor(); c.X != 3 || c.Y != 2 {
		t.Fatalf("Cursor() = %+v, want the region's origin 3,2", c)
	}
	writeTerm(term, "\x1b[?6l")
	if term.grid.Modes().Origin {
		t.Fatal("DECRST 6 did not clear origin mode")
	}
	if c := term.grid.Cursor(); c.X != 0 || c.Y != 0 {
		t.Fatalf("Cursor() = %+v, want the absolute origin 0,0", c)
	}
}

func TestDECSETAutoWrap(t *testing.T) {
	term := modeTerm(t, 5, 2, "\x1b[?7l")
	if term.grid.Modes().Wrap {
		t.Fatal("DECRST 7 did not clear autowrap")
	}
	writeTerm(term, "abcdef")
	if got := term.grid.Text(1); got != "" {
		t.Fatalf("with autowrap off, Text(1) = %q, want empty", got)
	}
	if got := term.grid.Text(0); got != "abcdf" {
		t.Fatalf("Text(0) = %q, want the last column overwritten", got)
	}

	writeTerm(term, "\x1b[?7h\r\x1b[2Kabcdef")
	if !term.grid.Modes().Wrap {
		t.Fatal("DECSET 7 did not set autowrap")
	}
	if got := term.grid.Text(1); got != "f" {
		t.Fatalf("with autowrap on, Text(1) = %q, want f", got)
	}
}

func TestDECSETReverseVideo(t *testing.T) {
	term := writeAll(t, "\x1b[?5h")
	if !term.grid.Modes().ReverseVideo {
		t.Fatal("DECSET 5 did not set reverse video")
	}
	writeTerm(term, "\x1b[?5l")
	if term.grid.Modes().ReverseVideo {
		t.Fatal("DECRST 5 did not clear reverse video")
	}
}

func TestDECSETCursorVisible(t *testing.T) {
	term := writeAll(t, "\x1b[?25l")
	if term.grid.Cursor().Visible {
		t.Fatal("DECRST 25 did not hide the cursor")
	}
	writeTerm(term, "\x1b[?25h")
	if !term.grid.Cursor().Visible {
		t.Fatal("DECSET 25 did not show the cursor")
	}
}

func TestDECSETMouse(t *testing.T) {
	tests := []struct {
		name string
		seq  string
		want MouseMode
	}{
		{"x10", "\x1b[?9h", MouseX10},
		{"button", "\x1b[?1000h", MouseButton},
		{"motion", "\x1b[?1002h", MouseMotion},
		{"many", "\x1b[?1003h", MouseMany},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			term := writeAll(t, tc.seq)
			if got := term.grid.Modes().Mouse; got != tc.want {
				t.Fatalf("Mouse = %v, want %v", got, tc.want)
			}
			writeTerm(term, tc.seq[:len(tc.seq)-1]+"l")
			if got := term.grid.Modes().Mouse; got != MouseOff {
				t.Fatalf("after reset, Mouse = %v, want off", got)
			}
		})
	}
}

func TestDECSETMouseSGR(t *testing.T) {
	term := writeAll(t, "\x1b[?1006h")
	if got := term.grid.Modes().MouseEncoding; got != MouseEncodingSGR {
		t.Fatalf("MouseEncoding = %v, want SGR", got)
	}
	writeTerm(term, "\x1b[?1006l")
	if got := term.grid.Modes().MouseEncoding; got != MouseEncodingDefault {
		t.Fatalf("after reset, MouseEncoding = %v, want default", got)
	}
}

func TestDECSETFocusReporting(t *testing.T) {
	term := writeAll(t, "\x1b[?1004h")
	if !term.grid.Modes().FocusReporting {
		t.Fatal("DECSET 1004 did not set focus reporting")
	}
	writeTerm(term, "\x1b[?1004l")
	if term.grid.Modes().FocusReporting {
		t.Fatal("DECRST 1004 did not clear focus reporting")
	}
}

func TestDECSETBracketedPaste(t *testing.T) {
	term := writeAll(t, "\x1b[?2004h")
	if !term.grid.Modes().BracketedPaste {
		t.Fatal("DECSET 2004 did not set bracketed paste")
	}
	writeTerm(term, "\x1b[?2004l")
	if term.grid.Modes().BracketedPaste {
		t.Fatal("DECRST 2004 did not clear bracketed paste")
	}
}

func TestDECSETMoreFixAndReverseWrap(t *testing.T) {
	term := writeAll(t, "\x1b[?41h\x1b[?45h")
	if m := term.grid.Modes(); !m.MoreFix || !m.ReverseWrap {
		t.Fatalf("Modes() = %+v, want MoreFix and ReverseWrap on", m)
	}
	writeTerm(term, "\x1b[?41l\x1b[?45l")
	if m := term.grid.Modes(); m.MoreFix || m.ReverseWrap {
		t.Fatalf("Modes() = %+v, want MoreFix and ReverseWrap off", m)
	}
}

func TestDECSETLeftRightMargin(t *testing.T) {
	term := writeAll(t, "\x1b[?69h")
	if !term.grid.Modes().LeftRightMargin {
		t.Fatal("DECSET 69 did not set left/right margin mode")
	}
	writeTerm(term, "\x1b[?69l")
	if term.grid.Modes().LeftRightMargin {
		t.Fatal("DECRST 69 did not clear left/right margin mode")
	}
}

func TestDECSETMultipleParameters(t *testing.T) {
	term := writeAll(t, "\x1b[?7;25l")
	if m := term.grid.Modes(); m.Wrap || term.grid.Cursor().Visible {
		t.Fatalf("after ?7;25l, Modes() = %+v visible = %v", m, term.grid.Cursor().Visible)
	}
	writeTerm(term, "\x1b[?7;25h")
	if m := term.grid.Modes(); !m.Wrap || !term.grid.Cursor().Visible {
		t.Fatalf("after ?7;25h, Modes() = %+v visible = %v", m, term.grid.Cursor().Visible)
	}
}

func TestDECSETAltScreen47(t *testing.T) {
	term := writeAll(t, "abc")
	writeTerm(term, "\x1b[?47h")
	if !term.grid.Modes().AltScreen {
		t.Fatal("DECSET 47 did not enter the alternate screen")
	}
	if got := term.grid.Text(0); got != "" {
		t.Fatalf("the alternate screen Text(0) = %q, want blank", got)
	}
	writeTerm(term, "def\x1b[?47l")
	if term.grid.Modes().AltScreen {
		t.Fatal("DECRST 47 did not leave the alternate screen")
	}
	if got := term.grid.Text(0); got != "abc" {
		t.Fatalf("the primary screen Text(0) = %q, want abc", got)
	}
	writeTerm(term, "\x1b[?47h")
	if got := term.grid.Text(0); got != "   def" {
		t.Fatalf("after re-entering, Text(0) = %q, want def kept", got)
	}
}

func TestDECSETAltScreen1047ClearsOnExit(t *testing.T) {
	term := writeAll(t, "abc\x1b[?1047hdef\x1b[?1047l")
	if got := term.grid.Text(0); got != "abc" {
		t.Fatalf("the primary screen Text(0) = %q, want abc", got)
	}
	writeTerm(term, "\x1b[?1047h")
	if got := term.grid.Text(0); got != "" {
		t.Fatalf("after re-entering, Text(0) = %q, want the alt screen cleared", got)
	}
}

func TestDECSETAltScreen1049SavesCursor(t *testing.T) {
	term := modeTerm(t, 10, 6, "abc\x1b[4;6H")
	writeTerm(term, "\x1b[?1049h")
	if c := term.grid.Cursor(); c.X != 5 || c.Y != 3 {
		t.Fatalf("on entering, Cursor() = %+v, want 5,3", c)
	}
	if got := term.grid.Text(0); got != "" {
		t.Fatalf("the alt screen Text(0) = %q, want blank", got)
	}
	writeTerm(term, "def\x1b[?1049l")
	if c := term.grid.Cursor(); c.X != 5 || c.Y != 3 {
		t.Fatalf("after leaving, Cursor() = %+v, want the saved 5,3", c)
	}
	if got := term.grid.Text(0); got != "abc" {
		t.Fatalf("the primary screen Text(0) = %q, want abc", got)
	}
	writeTerm(term, "\x1b[?1049h")
	if got := term.grid.Text(0); got != "" {
		t.Fatalf("after re-entering, Text(0) = %q, want blank", got)
	}
}

func TestDECSETSaveRestoreCursor1048(t *testing.T) {
	term := modeTerm(t, 10, 6, "\x1b[4;5H\x1b[?1048h\x1b[1;1H\x1b[?1048l")
	if c := term.grid.Cursor(); c.X != 4 || c.Y != 3 {
		t.Fatalf("Cursor() = %+v, want the saved 4,3", c)
	}
}

func TestSavedCursorIsPerScreen(t *testing.T) {
	term := modeTerm(t, 10, 8, "\x1b[3;2H\x1b7")
	writeTerm(term, "\x1b[?47h\x1b[7;6H\x1b7\x1b[?47l\x1b8")
	if c := term.grid.Cursor(); c.X != 1 || c.Y != 2 {
		t.Fatalf("on the primary screen, Cursor() = %+v, want 1,2", c)
	}
	writeTerm(term, "\x1b[?47h\x1b8")
	if c := term.grid.Cursor(); c.X != 5 || c.Y != 6 {
		t.Fatalf("on the alternate screen, Cursor() = %+v, want 5,6", c)
	}
}

func TestDECSETColumnMode(t *testing.T) {
	term := modeTerm(t, 80, 5, "xyz\x1b[3;5H")
	writeTerm(term, "\x1b[?40h\x1b[?3h")
	if cols, _ := term.grid.Size(); cols != 132 {
		t.Fatalf("cols = %d, want 132", cols)
	}
	if !term.grid.Modes().ColumnMode {
		t.Fatal("DECSET 3 did not set column mode")
	}
	if got := term.grid.Text(0); got != "" {
		t.Fatalf("Text(0) = %q, want the screen cleared", got)
	}
	if c := term.grid.Cursor(); c.X != 0 || c.Y != 0 {
		t.Fatalf("Cursor() = %+v, want home", c)
	}
	if r := term.grid.ScrollRegion(); r != (ScrollRegion{Top: 0, Bottom: 4, Left: 0, Right: 131}) {
		t.Fatalf("ScrollRegion() = %+v, want the whole 132-column grid", r)
	}

	writeTerm(term, "\x1b[?3l")
	if cols, _ := term.grid.Size(); cols != 80 {
		t.Fatalf("cols = %d, want 80", cols)
	}
}

func TestDECSETColumnModeRequiresAllow(t *testing.T) {
	// DECSET 40 is on by default, so the test turns it off first.
	term := modeTerm(t, 80, 5, "\x1b[?40l\x1b[?3h")
	if cols, _ := term.grid.Size(); cols != 80 {
		t.Fatalf("cols = %d, want 80 without DECSET 40", cols)
	}
	if term.grid.Modes().ColumnMode {
		t.Fatal("column mode is on without DECSET 40")
	}
	if got := term.grid.Text(0); got != "" {
		t.Fatalf("Text(0) = %q, want the grid untouched", got)
	}
}

func TestDECSETColumnModeNoClear(t *testing.T) {
	term := modeTerm(t, 80, 5, "xyz\x1b[?40h\x1b[?95h\x1b[?3h")
	if cols, _ := term.grid.Size(); cols != 132 {
		t.Fatalf("cols = %d, want 132", cols)
	}
	if got := term.grid.Text(0); got != "xyz" {
		t.Fatalf("Text(0) = %q, want xyz kept under DECNCSM", got)
	}
}

func TestXTSaveRestoreModes(t *testing.T) {
	term := modeTerm(t, 10, 3, "\x1b[?7h\x1b[?7s\x1b[?7l")
	if term.grid.Modes().Wrap {
		t.Fatal("DECRST 7 did not clear autowrap")
	}
	writeTerm(term, "\x1b[?7r")
	if !term.grid.Modes().Wrap {
		t.Fatal("XTRESTORE 7 did not put autowrap back on")
	}
}

func TestXTRestoreUnsavedModeResets(t *testing.T) {
	term := modeTerm(t, 10, 3, "\x1b[?45h\x1b[?45r")
	if term.grid.Modes().ReverseWrap {
		t.Fatal("XTRESTORE of an unsaved mode did not reset it")
	}
}
