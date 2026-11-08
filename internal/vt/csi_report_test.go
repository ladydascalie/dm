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
			name:  "DECSCNM reset",
			in:    "\x1b[?5$p",
			want:  "\x1b[?5;2$y",
		},
		{
			name:  "DECTCEM visible",
			in:    "\x1b[?25$p",
			want:  "\x1b[?25;1$y",
		},
		{
			name:  "DECTCEM hidden",
			setup: func(g *Grid) { g.SetCursorVisible(false) },
			in:    "\x1b[?25$p",
			want:  "\x1b[?25;2$y",
		},
		{
			name:  "IRM reset",
			in:    "\x1b[4$p",
			want:  "\x1b[4;2$y",
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
			name:  "DECHCCM permanently reset",
			in:    "\x1b[?60$p",
			want:  "\x1b[?60;4$y",
		},
		{
			name:  "GATM permanently reset",
			in:    "\x1b[1$p",
			want:  "\x1b[1;4$y",
		},
		{
			name:  "unknown DEC mode",
			in:    "\x1b[?9999$p",
			want:  "\x1b[?9999;0$y",
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
	if got, want := term.DECRQSSReply("m"), "\x1bP0$rm\x1b\\"; got != want {
		t.Fatalf("DECRQSS m = %q, want %q", got, want)
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
