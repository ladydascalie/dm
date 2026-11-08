package vt

import (
	"strings"
	"testing"
)

// report writes in to a fresh terminal and returns everything the terminal
// sent back.
func report(t *testing.T, term *Term, in string) string {
	t.Helper()
	var buf strings.Builder
	ReportWriter = &buf
	t.Cleanup(func() { ReportWriter = nil })
	term.Write([]byte(in))
	term.Flush()
	return buf.String()
}

func TestDA1(t *testing.T) {
	got := report(t, NewTerm(10, 5), "\x1b[c")
	want := "\x1b[?65;1;2;6;9;15;16;17;18;21;22;28;29c"
	if got != want {
		t.Fatalf("DA1 = %q, want %q", got, want)
	}
}

func TestDA2(t *testing.T) {
	got := report(t, NewTerm(10, 5), "\x1b[>c")
	if want := "\x1b[>64;411;0c"; got != want {
		t.Fatalf("DA2 = %q, want %q", got, want)
	}
}

func TestDECID(t *testing.T) {
	got := report(t, NewTerm(10, 5), "\x1bZ")
	want := "\x1b[?65;1;2;6;9;15;16;17;18;21;22;28;29c"
	if got != want {
		t.Fatalf("DECID = %q, want %q", got, want)
	}
}

func TestDSRText(t *testing.T) {
	got := report(t, NewTerm(10, 5), "\x1b[5n")
	if want := "\x1b[0n"; got != want {
		t.Fatalf("DSR 5 = %q, want %q", got, want)
	}
}

func TestDSRCursor(t *testing.T) {
	term := NewTerm(10, 5)
	if got, want := report(t, term, "\x1b[3;4H\x1b[6n"), "\x1b[3;4R"; got != want {
		t.Fatalf("DSR 6 = %q, want %q", got, want)
	}
}

func TestDECDSRCursor(t *testing.T) {
	term := NewTerm(10, 5)
	if got, want := report(t, term, "\x1b[2;5H\x1b[?6n"), "\x1b[?2;5;1R"; got != want {
		t.Fatalf("DECXCPR = %q, want %q", got, want)
	}
}

func TestDECDSRIgnoresUnknown(t *testing.T) {
	if got := report(t, NewTerm(10, 5), "\x1b[?25n"); got != "" {
		t.Fatalf("DECDSR 25 replied %q, want nothing", got)
	}
}

func TestDECRQM(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*Grid)
		in    string
		want  string
	}{
		{
			name: "DECAWM on",
			in:   "\x1b[?7$p",
			want: "\x1b[?7;1$y",
		},
		{
			name:  "DECAWM off",
			setup: func(g *Grid) { m := g.Modes(); m.Wrap = false; g.SetModes(m) },
			in:    "\x1b[?7$p",
			want:  "\x1b[?7;2$y",
		},
		{
			name:  "DECOM on",
			setup: func(g *Grid) { m := g.Modes(); m.Origin = true; g.SetModes(m) },
			in:    "\x1b[?6$p",
			want:  "\x1b[?6;1$y",
		},
		{
			name: "DECSCNM reset",
			in:   "\x1b[?5$p",
			want: "\x1b[?5;2$y",
		},
		{
			name: "DECTCEM visible",
			in:   "\x1b[?25$p",
			want: "\x1b[?25;1$y",
		},
		{
			name:  "DECTCEM hidden",
			setup: func(g *Grid) { g.SetCursorVisible(false) },
			in:    "\x1b[?25$p",
			want:  "\x1b[?25;2$y",
		},
		{
			name: "IRM reset",
			in:   "\x1b[4$p",
			want: "\x1b[4;2$y",
		},
		{
			name: "IRM set",
			setup: func(g *Grid) {
				m := g.Modes()
				m.Insert = true
				g.SetModes(m)
			},
			in:   "\x1b[4$p",
			want: "\x1b[4;1$y",
		},
		{
			name: "DECHCCM permanently reset",
			in:   "\x1b[?60$p",
			want: "\x1b[?60;4$y",
		},
		{
			name: "GATM permanently reset",
			in:   "\x1b[1$p",
			want: "\x1b[1;4$y",
		},
		{
			name: "unknown DEC mode",
			in:   "\x1b[?9999$p",
			want: "\x1b[?9999;0$y",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term := NewTerm(10, 5)
			if tc.setup != nil {
				tc.setup(term.grid)
			}
			if got := report(t, term, tc.in); got != tc.want {
				t.Fatalf("DECRQM %q = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDECRQSSScrollRegion(t *testing.T) {
	term := NewTerm(10, 8)
	term.grid.SetScrollRegion(ScrollRegion{Top: 1, Bottom: 6, Left: 0, Right: 9})
	if got, want := term.DECRQSSReply("r"), "\x1bP1$r2;7r\x1b\\"; got != want {
		t.Fatalf("DECRQSS r = %q, want %q", got, want)
	}
}

func TestDECRQSSMargins(t *testing.T) {
	term := NewTerm(10, 8)
	term.grid.SetScrollRegion(ScrollRegion{Top: 0, Bottom: 7, Left: 2, Right: 7})
	if got, want := term.DECRQSSReply("s"), "\x1bP1$r3;8s\x1b\\"; got != want {
		t.Fatalf("DECRQSS s = %q, want %q", got, want)
	}
}

func TestDECRQSSInvalid(t *testing.T) {
	term := NewTerm(10, 5)
	if got, want := term.DECRQSSReply("zz"), "\x1bP0$rzz\x1b\\"; got != want {
		t.Fatalf("DECRQSS zz = %q, want %q", got, want)
	}
}

// TestDECRQSSSettings pins a report for each setting the terminal keeps for
// DECRQSS, including the ones the grid does not otherwise model.
func TestDECRQSSSettings(t *testing.T) {
	tests := []struct {
		in   string
		pt   string
		want string
	}{
		{"\x1b[0+q", "+q", "\x1bP1$r0+q\x1b\\"},
		{"\x1b[0*} ", "*}", "\x1bP1$r0*}\x1b\\"},
		{"\x1b[0$}", "$}", "\x1bP1$r0$}\x1b\\"},
		{"\x1b[0*x", "*x", "\x1bP1$r0*x\x1b\\"},
		{"\x1b[1\"q", "\"q", "\x1bP1$r1\"q\x1b\\"},
		{"\x1b[65;1\"p", "\"p", "\x1bP1$r65;1\"p\x1b\\"},
		{"\x1b[4 q", " q", "\x1bP1$r4 q\x1b\\"},
		{"\x1b[24*|", "*|", "\x1bP1$r24*|\x1b\\"},
		{"\x1b[0$~", "$~", "\x1bP1$r0$~\x1b\\"},
		{"\x1b[0+r", "+r", "\x1bP1$r0+r\x1b\\"},
		{"\x1b[27t", "t", "\x1bP1$r27t\x1b\\"},
	}
	for _, tt := range tests {
		term := NewTerm(10, 5)
		writeTerm(term, tt.in)
		if got := term.DECRQSSReply(tt.pt); got != tt.want {
			t.Errorf("%q DECRQSS %q = %q, want %q", tt.in, tt.pt, got, tt.want)
		}
	}
}

func TestDECRQSSSGR(t *testing.T) {
	term := NewTerm(10, 5)
	writeTerm(term, "\x1b[1;4:3;38;5;200;48;2;1;2;3;58;5;9m")
	want := "\x1bP1$r0;1;4:3;38;5;200;48;2;1;2;3;58;5;9m\x1b\\"
	if got := term.DECRQSSReply("m"); got != want {
		t.Fatalf("DECRQSS m = %q, want %q", got, want)
	}
	// A reset reports the default rendition.
	writeTerm(term, "\x1b[0m")
	if got, want := term.DECRQSSReply("m"), "\x1bP1$r0m\x1b\\"; got != want {
		t.Fatalf("DECRQSS m after reset = %q, want %q", got, want)
	}
}

// TestDCSDispatch pins that the parser delivers DECRQSS and XTGETTCAP to the
// terminal, so the harness can hand the sequence over unchanged.
func TestDCSDispatch(t *testing.T) {
	term := NewTerm(10, 5)
	if got, want := report(t, term, "\x1bP$qr\x1b\\"), "\x1bP1$r1;5r\x1b\\"; got != want {
		t.Fatalf("DECRQSS DCS = %q, want %q", got, want)
	}
	if got, want := report(t, term, "\x1bP+q436F\x1b\\"), "\x1bP1+r436F=323536\x1b\\"; got != want {
		t.Fatalf("XTGETTCAP DCS = %q, want %q", got, want)
	}
}

func TestXTGETTCAP(t *testing.T) {
	if got, want := XTGETTCAPReply("436F"), "\x1bP1+r436F=323536\x1b\\"; got != want {
		t.Fatalf("XTGETTCAP Co = %q, want %q", got, want)
	}
	if got, want := XTGETTCAPReply("544E"), "\x1bP0+r544E\x1b\\"; got != want {
		t.Fatalf("XTGETTCAP TN = %q, want %q", got, want)
	}
}

// TestCursorReportRelativeInOriginMode pins that the report is relative to the
// margins in origin mode while the cursor is inside them, and absolute on an
// axis where a move that ignores origin mode put it outside.
func TestCursorReportRelativeInOriginMode(t *testing.T) {
	term := NewTerm(20, 12)
	writeTerm(term, "\x1b[6;11r\x1b[?69h\x1b[5;10s\x1b[?6h")
	if got, want := report(t, term, "\x1b[1;1H\x1b[6n"), "\x1b[1;1R"; got != want {
		t.Fatalf("CUP home in origin mode: report = %q, want %q", got, want)
	}
	if got, want := report(t, term, "\x1b[?6n"), "\x1b[?1;1;1R"; got != want {
		t.Fatalf("DECXCPR in origin mode: report = %q, want %q", got, want)
	}
	// VPA is absolute, so row 2 is outside the vertical margin and that axis
	// stays absolute; the column is still inside its margin and is relative.
	if got, want := report(t, term, "\x1b[2d\x1b[6n"), "\x1b[2;1R"; got != want {
		t.Fatalf("VPA outside the vertical margin: report = %q, want %q", got, want)
	}
}
