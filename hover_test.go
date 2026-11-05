package main

import (
	"bytes"
	"strings"
	"testing"

	"dm/internal/layout"

	"github.com/hinshun/vt10x"
)

// TestURLMatchAtRow checks the cell range the hover underline uses: the range
// covers the URL and leaves its trailing punctuation outside.
func TestURLMatchAtRow(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(20, 2))
	if _, err := vt.Write([]byte("see https://x.y/a.")); err != nil {
		t.Fatal(err)
	}
	url, rng := urlMatchAtRow(vt, 20, 0, 5)
	if url != "https://x.y/a" {
		t.Fatalf("url = %q, want https://x.y/a", url)
	}
	if rng == nil || rng.lo != 4 || rng.hi != 16 {
		t.Fatalf("range = %+v, want lo 4 hi 16", rng)
	}
	if u, r := urlMatchAtRow(vt, 20, 0, 17); u != "" || r != nil {
		t.Fatalf("trailing punctuation matched %q %+v", u, r)
	}
}

// TestHoverUnderline checks that a hover range underlines exactly the cells it
// covers. The hover paint carries SGR 4 on the URL run; the plain paint does
// not, and a nil hover paints like the plain one.
func TestHoverUnderline(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(20, 2))
	if _, err := vt.Write([]byte("see https://x.y/a.")); err != nil {
		t.Fatal(err)
	}
	r := layout.Rect{X: 0, Y: 0, W: 20, H: 2}
	var plain, hovered bytes.Buffer
	drawPaneFrom(&plain, vt, r, 0, nil, nil)
	drawPaneFrom(&hovered, vt, r, 0, nil, &selRange{lo: 4, hi: 16})
	if strings.Contains(plain.String(), ";4;") {
		t.Fatalf("plain paint underlines a cell: %q", plain.String())
	}
	if !strings.Contains(hovered.String(), ";4;") {
		t.Fatalf("hover paint has no underline: %q", hovered.String())
	}
	if hovered.String() == plain.String() {
		t.Fatalf("hover paint matches plain paint")
	}
}
