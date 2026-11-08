package vt

import (
	"reflect"
	"testing"
)

// writeAll writes the chunks in order and then flushes the pending cluster.
func writeAll(t *testing.T, chunks ...string) *Term {
	t.Helper()
	term := NewTerm(20, 6)
	for _, c := range chunks {
		term.Write([]byte(c))
	}
	term.Flush()
	return term
}

// sameTerm fails when the grids or the pending parser state differ.
func sameTerm(t *testing.T, a, b *Term) {
	t.Helper()
	if !reflect.DeepEqual(a.grid, b.grid) {
		ca, ra := a.grid.Size()
		cb, rb := b.grid.Size()
		t.Fatalf("grids differ: %dx%d vs %dx%d", ca, ra, cb, rb)
	}
	if a.parser.state != b.parser.state {
		t.Errorf("parser state = %v vs %v", a.parser.state, b.parser.state)
	}
	if string(a.parser.cluster) != string(b.parser.cluster) {
		t.Errorf("pending cluster = %q vs %q", a.parser.cluster, b.parser.cluster)
	}
	if string(a.parser.pending) != string(b.parser.pending) {
		t.Errorf("pending bytes = %q vs %q", a.parser.pending, b.parser.pending)
	}
}

// splitStream writes stream in two chunks at every byte boundary and compares
// each result with a single write. The stream covers ground text, a CSI, a
// charset designation, an OSC, a DCS, a wide cluster, a ZWJ family, a combining
// mark and a flag.
const splitStream = "ab\x1b[1;31m世\x1b[0m\r\nc\td\x08e\x1b(0q\x1b)0x" +
	"\x1b]0;title\x07\x1bPdata\x1b\\z👨\u200d👩\u200d👧\u200d👦e\u0301🇯🇵\x1b[?25l"

func TestChunkSplitMatchesOneChunk(t *testing.T) {
	want := writeAll(t, splitStream)
	for i := 0; i <= len(splitStream); i++ {
		got := writeAll(t, splitStream[:i], splitStream[i:])
		if !reflect.DeepEqual(got.grid, want.grid) {
			t.Fatalf("split at %d: grid differs", i)
		}
		sameTerm(t, got, want)
	}
}

func TestChunkSplitMidRune(t *testing.T) {
	want := writeAll(t, "世界a")
	b := []byte("世界a")
	for i := 0; i <= len(b); i++ {
		got := NewTerm(20, 6)
		got.Write(b[:i])
		got.Write(b[i:])
		got.Flush()
		if !reflect.DeepEqual(got.grid, want.grid) {
			t.Fatalf("split at %d: grid differs", i)
		}
		sameTerm(t, got, want)
	}
}

func TestPartialEscapeSurvivesChunk(t *testing.T) {
	term := NewTerm(20, 2)
	term.Write([]byte("a\x1b["))
	if term.parser.state != stateCSI {
		t.Fatalf("state = %v, want CSI", term.parser.state)
	}
	term.Flush()
	if got := term.grid.Text(0); got != "a" {
		t.Fatalf("after a partial CSI, Text(0) = %q, want %q", got, "a")
	}

	term.Write([]byte("2Jb"))
	term.Flush()
	if got := term.grid.Text(0); got != "ab" {
		t.Fatalf("after completing the CSI, Text(0) = %q, want %q", got, "ab")
	}
}

func TestPartialRuneSurvivesChunk(t *testing.T) {
	term := NewTerm(20, 2)
	b := []byte("世")
	term.Write(b[:2])
	if len(term.parser.pending) != 2 {
		t.Fatalf("pending bytes = %q, want the two undecoded bytes", term.parser.pending)
	}
	term.Write(b[2:])
	term.Flush()
	if got := term.grid.Cell(0, 0); got.Content != "世" {
		t.Fatalf("Cell(0,0) = %+v, want %q", got, "世")
	}
}

func TestPartialClusterSurvivesChunk(t *testing.T) {
	whole := writeAll(t, "👨\u200d👩\u200d👧")
	split := writeAll(t, "👨", "\u200d👩\u200d👧")

	if !reflect.DeepEqual(whole.grid, split.grid) {
		t.Fatal("a ZWJ family split before the ZWJ differs from one chunk")
	}
	c := whole.grid.Cell(0, 0)
	if c.Content != "👨\u200d👩\u200d👧" {
		t.Errorf("Content = %q, want the whole family", c.Content)
	}
	if c.Width != WidthWide {
		t.Errorf("Width = %d, want 2", c.Width)
	}
	if got := whole.grid.Next(0, 0); got != 2 {
		t.Errorf("Next(0,0) = %d, want 2", got)
	}
}

func TestUnfinishedClusterStaysPending(t *testing.T) {
	term := NewTerm(20, 1)
	term.Write([]byte("abc"))
	if got := term.grid.Text(0); got != "ab" {
		t.Fatalf("before flush, Text(0) = %q, want %q", got, "ab")
	}
	if string(term.parser.cluster) != "c" {
		t.Fatalf("pending cluster = %q, want %q", term.parser.cluster, "c")
	}
	term.Flush()
	if got := term.grid.Text(0); got != "abc" {
		t.Fatalf("after flush, Text(0) = %q, want %q", got, "abc")
	}
}

func TestParseCSI(t *testing.T) {
	cases := []struct {
		body    string
		final   byte
		private byte
		params  []int
		inter   string
	}{
		{"?25", 'l', '?', []int{25}, ""},
		{"1;31", 'm', 0, []int{1, 31}, ""},
		{"", 'm', 0, nil, ""},
		{"!", 'p', 0, nil, "!"},
		{"?1;2", 'h', '?', []int{1, 2}, ""},
	}
	for _, tc := range cases {
		p := parser{seq: []byte(tc.body)}
		got := p.parseCSI(tc.final)
		if got.Private != tc.private || got.Final != tc.final {
			t.Errorf("parseCSI(%q, %q) = private %q final %q", tc.body, tc.final, got.Private, got.Final)
		}
		if !reflect.DeepEqual(got.Params, tc.params) {
			t.Errorf("parseCSI(%q).Params = %v, want %v", tc.body, got.Params, tc.params)
		}
		if string(got.Inter) != tc.inter {
			t.Errorf("parseCSI(%q).Inter = %q, want %q", tc.body, got.Inter, tc.inter)
		}
	}
}

func TestParseParams(t *testing.T) {
	cases := []struct {
		in   string
		want []int
	}{
		{"", []int{0}},
		{"5", []int{5}},
		{"1;31", []int{1, 31}},
		{";5", []int{0, 5}},
		{"38:2:255:0:0", []int{38, 0}},
	}
	for _, tc := range cases {
		if got := parseParams([]byte(tc.in)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parseParams(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
