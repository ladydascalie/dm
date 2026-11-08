package vt

import "testing"

func TestZWJFamilyIsOneCell(t *testing.T) {
	term := writeAll(t, "👨\u200d👩\u200d👧\u200d👦")
	c := term.grid.Cell(0, 0)
	if c.Content != "👨\u200d👩\u200d👧\u200d👦" {
		t.Errorf("Content = %q, want the whole family", c.Content)
	}
	if c.Width != WidthWide {
		t.Errorf("Width = %d, want 2", c.Width)
	}
	if term.grid.lines[0][1].Width != WidthContinuation {
		t.Error("slot 1 is not a continuation")
	}
	if cur := term.grid.Cursor(); cur.X != 2 || cur.Y != 0 {
		t.Errorf("Cursor() = %+v, want 2,0", cur)
	}
}

func TestCombiningMarkAttaches(t *testing.T) {
	cases := []struct {
		name   string
		chunks []string
		want   string
	}{
		{"one chunk", []string{"e\u0301"}, "e\u0301"},
		{"split before the mark", []string{"e", "\u0301"}, "e\u0301"},
	}
	for _, tc := range cases {
		term := writeAll(t, tc.chunks...)
		if got := term.grid.Text(0); got != tc.want {
			t.Errorf("%s: Text(0) = %q, want %q", tc.name, got, tc.want)
		}
		if c := term.grid.Cell(0, 0); c.Width != WidthNormal {
			t.Errorf("%s: Width = %d, want 1", tc.name, c.Width)
		}
		if c := term.grid.Cell(1, 0); !c.IsBlank() {
			t.Errorf("%s: slot 1 = %+v, want blank", tc.name, c)
		}
	}
}

func TestPlainText(t *testing.T) {
	term := writeAll(t, "hello")
	if got := term.grid.Text(0); got != "hello" {
		t.Errorf("Text(0) = %q, want %q", got, "hello")
	}
	if cur := term.grid.Cursor(); cur.X != 5 || cur.Y != 0 {
		t.Errorf("Cursor() = %+v, want 5,0", cur)
	}
}

func TestCarriageReturn(t *testing.T) {
	term := writeAll(t, "ab\rZ")
	if got := term.grid.Text(0); got != "Zb" {
		t.Errorf("Text(0) = %q, want %q", got, "Zb")
	}
}

func TestBackspace(t *testing.T) {
	term := writeAll(t, "ab\x08X")
	if got := term.grid.Text(0); got != "aX" {
		t.Errorf("Text(0) = %q, want %q", got, "aX")
	}
}

func TestBackspaceSkipsContinuation(t *testing.T) {
	term := writeAll(t, "世\x08x")
	if got := term.grid.Text(0); got != "x" {
		t.Errorf("Text(0) = %q, want %q", got, "x")
	}
	if c := term.grid.Cell(1, 0); !c.IsBlank() || c.Width != WidthNormal {
		t.Errorf("Cell(1,0) = %+v, want a blank normal cell", c)
	}
}

func TestTab(t *testing.T) {
	term := writeAll(t, "a\tb")
	if got := term.grid.Text(0); got != "a       b" {
		t.Errorf("Text(0) = %q, want %q", got, "a       b")
	}
	if cur := term.grid.Cursor(); cur.X != 9 {
		t.Errorf("Cursor().X = %d, want 9", cur.X)
	}
}

func TestLineFeed(t *testing.T) {
	term := writeAll(t, "a\nb")
	if got := term.grid.Text(0); got != "a" {
		t.Errorf("Text(0) = %q, want %q", got, "a")
	}
	// LF keeps the column, so b lands under column 1.
	if got := term.grid.Text(1); got != " b" {
		t.Errorf("Text(1) = %q, want %q", got, " b")
	}
}

func TestLineFeedScrollsAtBottom(t *testing.T) {
	term := NewTerm(4, 2)
	term.Write([]byte("1\r\n2\r\n3"))
	term.Flush()

	if got := term.grid.Text(0); got != "2" {
		t.Errorf("Text(0) = %q, want %q", got, "2")
	}
	if got := term.grid.Text(1); got != "3" {
		t.Errorf("Text(1) = %q, want %q", got, "3")
	}
	if cur := term.grid.Cursor(); cur.X != 1 || cur.Y != 1 {
		t.Errorf("Cursor() = %+v, want 1,1", cur)
	}
}

func TestWrapAtRightEdge(t *testing.T) {
	term := NewTerm(4, 2)
	term.Write([]byte("abcd"))
	term.Flush()
	if cur := term.grid.Cursor(); cur.X != 3 || !cur.WrapNext {
		t.Fatalf("after a full row, Cursor() = %+v, want 3 with pending wrap", cur)
	}

	term.Write([]byte("e"))
	term.Flush()
	if got := term.grid.Text(0); got != "abcd" {
		t.Errorf("Text(0) = %q, want %q", got, "abcd")
	}
	if got := term.grid.Text(1); got != "e" {
		t.Errorf("Text(1) = %q, want %q", got, "e")
	}
	if cur := term.grid.Cursor(); cur.X != 1 || cur.Y != 1 || cur.WrapNext {
		t.Errorf("Cursor() = %+v, want 1,1 with no pending wrap", cur)
	}
}

func TestWideCellWrapsAtRightEdge(t *testing.T) {
	term := NewTerm(4, 2)
	term.Write([]byte("abc世"))
	term.Flush()

	if got := term.grid.Text(0); got != "abc" {
		t.Errorf("Text(0) = %q, want %q", got, "abc")
	}
	if c := term.grid.Cell(0, 1); c.Content != "世" || c.Width != WidthWide {
		t.Errorf("Cell(0,1) = %+v, want the wide cell", c)
	}
}

func TestCharsetDesignationAndShift(t *testing.T) {
	term := NewTerm(10, 2)
	term.Write([]byte("\x1b(0"))
	if cs := term.grid.Charset(); cs.G0 != CharsetDECSpecial {
		t.Errorf("after ESC ( 0, Charset() = %+v, want G0 DEC special", cs)
	}

	term.Write([]byte("\x1b)A"))
	if cs := term.grid.Charset(); cs.G1 != CharsetUK {
		t.Errorf("after ESC ) A, Charset() = %+v, want G1 UK", cs)
	}

	term.Write([]byte("\x0e"))
	if cs := term.grid.Charset(); cs.GL != 1 {
		t.Errorf("after SO, GL = %d, want 1", cs.GL)
	}
	term.Write([]byte("\x0f"))
	if cs := term.grid.Charset(); cs.GL != 0 {
		t.Errorf("after SI, GL = %d, want 0", cs.GL)
	}
}

func TestOSCAndDCSAreNotText(t *testing.T) {
	term := writeAll(t, "a\x1b]0;title\x07b\x1bPdata\x1b\\c")
	if got := term.grid.Text(0); got != "abc" {
		t.Errorf("Text(0) = %q, want %q", got, "abc")
	}
}
