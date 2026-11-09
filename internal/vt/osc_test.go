package vt

import "testing"

// oscTerm writes a body to a fresh terminal and returns it.
func oscTerm(t *testing.T, bodies ...string) *Term {
	t.Helper()
	term := NewTerm(8, 2)
	for _, b := range bodies {
		term.Write([]byte("\x1b]" + b + "\x1b\\"))
	}
	term.Flush()
	return term
}

func TestOSCTitle(t *testing.T) {
	term := oscTerm(t, "0;both")
	if term.Title() != "both" || term.IconTitle() != "both" {
		t.Fatalf("OSC 0 title = %q icon = %q", term.Title(), term.IconTitle())
	}
	term = oscTerm(t, "1;icon", "2;window")
	if term.IconTitle() != "icon" || term.Title() != "window" {
		t.Fatalf("OSC 1/2 = %q/%q", term.IconTitle(), term.Title())
	}
	// BEL terminates an OSC too.
	term = NewTerm(8, 2)
	writeTerm(term, "\x1b]2;bell\x07")
	if term.Title() != "bell" {
		t.Fatalf("BEL title = %q, want bell", term.Title())
	}
}

func TestOSCPalette(t *testing.T) {
	term := oscTerm(t, "4;1;rgb:ffff/0000/0000;2;#00ff00;3;?")
	if c, ok := term.Color(1); !ok || c != RGBColor(255, 0, 0) {
		t.Fatalf("palette 1 = %+v, %v, want red", c, ok)
	}
	if c, ok := term.Color(2); !ok || c != RGBColor(0, 255, 0) {
		t.Fatalf("palette 2 = %+v, %v, want green", c, ok)
	}
	if _, ok := term.Color(3); ok {
		t.Fatal("a palette query stored a colour")
	}
}

func TestOSCDynamicColors(t *testing.T) {
	term := oscTerm(t, "10;#112233", "11;#445566", "12;#778899")
	for n, want := range map[int]Color{
		10: RGBColor(0x11, 0x22, 0x33),
		11: RGBColor(0x44, 0x55, 0x66),
		12: RGBColor(0x77, 0x88, 0x99),
	} {
		if c, ok := term.Color(n); !ok || c != want {
			t.Errorf("colour %d = %+v, %v, want %+v", n, c, ok, want)
		}
	}
}

func TestOSCLinkAppliesToCells(t *testing.T) {
	term := NewTerm(8, 1)
	writeTerm(term, "\x1b]8;;https://example.com\x1b\\a\x1b]8;;\x1b\\b")
	if got := term.grid.Cell(0, 0).Link; got != "https://example.com" {
		t.Fatalf("cell 0 link = %q", got)
	}
	if got := term.grid.Cell(1, 0).Link; got != "" {
		t.Fatalf("cell 1 link = %q, want cleared", got)
	}
}

func TestOSCClipboardAndStatus(t *testing.T) {
	term := oscTerm(t, "52;c;dGVzdA==", "7501;working")
	if term.Clipboard() != "dGVzdA==" {
		t.Fatalf("clipboard = %q", term.Clipboard())
	}
	if term.Status() != "working" {
		t.Fatalf("status = %q", term.Status())
	}
	// A clipboard query leaves the store alone.
	term = oscTerm(t, "52;c;data", "52;c;?")
	if term.Clipboard() != "data" {
		t.Fatalf("clipboard after query = %q", term.Clipboard())
	}
}

func TestOSCUnknownIsIgnored(t *testing.T) {
	term := oscTerm(t, "9999;x", "no-number")
	if term.Title() != "" || term.Link() != "" {
		t.Fatal("an unknown OSC changed terminal state")
	}
}

func TestRISClearsOSCState(t *testing.T) {
	term := oscTerm(t, "0;title", "4;1;#ffffff", "8;;https://example.com", "52;c;data", "7501;s")
	writeTerm(term, "\x1bc")
	if term.Title() != "" || term.IconTitle() != "" || term.Link() != "" ||
		term.Clipboard() != "" || term.Status() != "" || term.colors != nil {
		t.Fatal("RIS did not clear the OSC state")
	}
}

func TestParseOSCColor(t *testing.T) {
	tests := []struct {
		in   string
		want Color
		ok   bool
	}{
		{"rgb:f/0/0", RGBColor(255, 0, 0), true},
		{"rgb:ffff/8000/0000", RGBColor(255, 128, 0), true},
		{"#fff", RGBColor(255, 255, 255), true},
		{"#010203", RGBColor(1, 2, 3), true},
		{"?", Color{}, false},
		{"rgb:zz/0/0", Color{}, false},
		{"#12345", Color{}, false},
	}
	for _, tc := range tests {
		got, ok := parseOSCColor(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("parseOSCColor(%q) = %+v, %v; want %+v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// OSC 133 records the prompt start and the command marks as stream byte
// offsets at the start of their sequence, and ignores unknown or malformed
// bodies.
func TestOSCPromptMarks(t *testing.T) {
	term := NewTerm(20, 4)
	writeTerm(term, "one\r\ntwo\r\n")
	writeTerm(term, "\x1b]133;A\x1b\\")
	if pos, ok := term.PromptStart(); !ok || pos != 10 {
		t.Fatalf("prompt start = %d, %v, want 10, true", pos, ok)
	}
	writeTerm(term, "\x1b]133;B\x1b\\")
	writeTerm(term, "\x1b]133;C\x1b\\")
	if pos, ok := term.CommandMark(); !ok || pos != 28 {
		t.Fatalf("command start = %d, %v, want 28, true", pos, ok)
	}
	writeTerm(term, "out\r\n")
	writeTerm(term, "\x1b]133;D\x1b\\")
	if pos, ok := term.CommandMark(); !ok || pos != 42 {
		t.Fatalf("command end = %d, %v, want 42, true", pos, ok)
	}
	writeTerm(term, "\x1b]133;A\x1b\\")
	if pos, ok := term.PromptStart(); !ok || pos != 51 {
		t.Fatalf("next prompt start = %d, %v, want 51, true", pos, ok)
	}
	// Unknown and malformed bodies are ignored.
	writeTerm(term, "\x1b]133;X\x1b\\")
	writeTerm(term, "\x1b]133;\x1b\\")
	if pos, _ := term.PromptStart(); pos != 51 {
		t.Fatalf("junk moved the prompt start to %d", pos)
	}
	if pos, _ := term.CommandMark(); pos != 42 {
		t.Fatalf("junk moved the command mark to %d", pos)
	}
}
