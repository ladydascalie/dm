package esctest

import (
	"io"
	"testing"

	"github.com/hinshun/vt10x"
)

func TestScan(t *testing.T) {
	tests := []struct {
		in    string
		kind  byte
		priv  byte
		inter string
		final byte
		body  string
		n     int
	}{
		{"\x1b[6n", 'c', 0, "", 'n', "", 4},
		{"\x1b[?6n", 'c', '?', "", 'n', "", 5},
		{"\x1b[>c", 'c', '>', "", 'c', "", 4},
		{"\x1b[?1;2$y", 'c', '?', "$", 'y', "", 8},
		{"\x1b[1;1;1;1;1;1*y", 'c', 0, "*", 'y', "", 15},
		{"\x1b[8;25;80t", 'c', 0, "", 't', "", 10},
		{"\x1b[!p", 'c', 0, "!", 'p', "", 4},
		{"\x1b[4 q", 'c', 0, " ", 'q', "", 5},
		{"\x1b]4;0;?\x1b\\", ']', 0, "", 0, "4;0;?", 9},
		{"\x1b]0;title\x07", ']', 0, "", 0, "0;title", 10},
		{"\x1bP$qr\x1b\\", 'P', 0, "", 0, "$qr", 7},
		{"\x1bP+q436F\x1b\\", 'P', 0, "", 0, "+q436F", 10},
		{"\x1bZ", 'Z', 0, "", 0, "", 2},
		{"a", 0, 0, "", 0, "", 1},
	}
	for _, tt := range tests {
		s, n, ok := scan([]byte(tt.in))
		if !ok {
			t.Fatalf("scan(%q) incomplete", tt.in)
		}
		if n != tt.n {
			t.Errorf("scan(%q) n = %d, want %d", tt.in, n, tt.n)
		}
		if s.kind != tt.kind || s.priv != tt.priv || s.inter != tt.inter || s.final != tt.final || s.body != tt.body {
			t.Errorf("scan(%q) = %+v", tt.in, s)
		}
	}
}

func TestScanIncomplete(t *testing.T) {
	for _, in := range []string{"\x1b", "\x1b[", "\x1b[6", "\x1b]4;0;?", "\x1bP$qr"} {
		if _, _, ok := scan([]byte(in)); ok {
			t.Errorf("scan(%q) reported complete", in)
		}
	}
}

func TestHandled(t *testing.T) {
	yes := []string{
		"\x1b[c", "\x1b[0c", "\x1b[>c", "\x1b[6n", "\x1b[?6n", "\x1b[18t",
		"\x1b[?1$p", "\x1b[1$p", "\x1b[1;1;1;1;1;1*y",
		"\x1b]4;0;?\x1b\\", "\x1b]10;?\x1b\\", "\x1b]52;;?\x1b\\",
		"\x1bP$qr\x1b\\", "\x1bP+q436F\x1b\\", "\x1bZ", "\x1b[>q",
	}
	for _, in := range yes {
		s, _, _ := scan([]byte(in))
		if !handled(s) {
			t.Errorf("handled(%q) = false", in)
		}
	}
	no := []string{
		"\x1b[?1h", "\x1b[1m", "\x1b]0;title\x1b\\",
		"\x1b]4;0;rgb:ffff/0000/0000\x1b\\", "\x1b[?1049h",
	}
	for _, in := range no {
		s, _, _ := scan([]byte(in))
		if handled(s) {
			t.Errorf("handled(%q) = true", in)
		}
	}
}

func TestTranslateC1(t *testing.T) {
	in := []byte{0x9b, '6', 'n', 0x9d, '4', 0x9c}
	got := string(translateC1(in))
	want := "\x1b[6n\x1b]4\x1b\\"
	if got != want {
		t.Errorf("translateC1 = %q, want %q", got, want)
	}
	plain := []byte("abc")
	if string(translateC1(plain)) != "abc" {
		t.Errorf("translateC1 changed plain text")
	}
}

func TestChecksum(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(80, 25), vt10x.WithWriter(io.Discard))
	if got := checksum(vt, 1, 1, 1, 1); got != ' ' {
		t.Errorf("empty cell checksum = %#x, want 0x20", got)
	}
	_, _ = vt.Write([]byte("X"))
	if got := checksum(vt, 1, 1, 1, 1); got != 'X' {
		t.Errorf("X checksum = %#x, want 0x58", got)
	}
	_, _ = vt.Write([]byte("\x1b[2;1HAB"))
	if got := checksum(vt, 2, 1, 2, 2); got != 'A'+'B' {
		t.Errorf("AB checksum = %#x, want %#x", got, 'A'+'B')
	}
}

func TestParseColor(t *testing.T) {
	tests := []struct {
		in   string
		want rgb
		ok   bool
	}{
		{"rgb:f0f0/f0f0/f0f0", rgb{0xf0f0, 0xf0f0, 0xf0f0}, true},
		{"rgb:ffff/0000/0000", rgb{0xffff, 0, 0}, true},
		{"#f0f0f0", rgb{0xf0f0, 0xf0f0, 0xf0f0}, true},
		{"#fff", rgb{0xf0f0, 0xf0f0, 0xf0f0}, true},
		{"#888", rgb{0x8080, 0x8080, 0x8080}, true},
		{"#f00f00f00", rgb{0xf0f0, 0xf0f0, 0xf0f0}, true},
		{"#f000f000f000", rgb{0xf0f0, 0xf0f0, 0xf0f0}, true},
		{"rgbi:1/1/1", rgb{0xffff, 0xffff, 0xffff}, true},
		{"rgbi:0.5/0.5/0.5", rgb{0xc1c1, 0xbbbb, 0xbbbb}, true},
		{"CIEXYZ:0.5/0.5/0.5", rgb{0xdddd, 0xb5b5, 0xa0a0}, true},
		{"CIEuvY:0.5/0.5/0.5", rgb{0xffff, 0xa3a3, 0xaeae}, true},
		{"CIExyY:0.5/0.5/0.5", rgb{0xf7f7, 0xb3b3, 0x0e0e}, true},
		{"CIELab:0.5/0.5/0.5", rgb{0x5252, 0x4f4f, 0x4f4f}, true},
		{"CIELuv:0.5/0.5/0.5", rgb{0x0e0e, 0x1313, 0x0e0e}, true},
		{"TekHVC:0.5/0.5/0.5", rgb{0x1111, 0x1313, 0x0e0e}, true},
		{"nonsense", rgb{}, false},
	}
	for _, tt := range tests {
		got, ok := parseColor(tt.in)
		if ok != tt.ok || got != tt.want {
			t.Errorf("parseColor(%q) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestFormatColor(t *testing.T) {
	if got := formatColor(rgb{0xf0f0, 0x0000, 0xffff}); got != "rgb:f0f0/0000/ffff" {
		t.Errorf("formatColor = %q", got)
	}
}

func TestOscIsQuery(t *testing.T) {
	yes := []string{"4;0;?", "4;0;?;1;?", "5;0;?", "10;?", "10;?;?", "52;;?"}
	for _, b := range yes {
		if !oscIsQuery(b) {
			t.Errorf("oscIsQuery(%q) = false", b)
		}
	}
	no := []string{"0;title", "4;0;rgb:ffff/0000/0000", "10;rgb:ffff/ffff/ffff", "52;;dGVzdA=="}
	for _, b := range no {
		if oscIsQuery(b) {
			t.Errorf("oscIsQuery(%q) = true", b)
		}
	}
}
