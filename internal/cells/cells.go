// Package cells measures and clips text by terminal cell width.
package cells

import (
	"strings"

	"github.com/clipperhouse/displaywidth"
	"github.com/clipperhouse/uax29/v2/graphemes"
)

// opts measures text the way the chrome does: grapheme clusters, and an escape
// sequence is one zero-width unit rather than a run of printable bytes.
var opts = displaywidth.Options{ControlSequences: true}

// Ellipsis marks a cut string. Every caller ends an elided run with it.
const Ellipsis = "…"

// Cells is the number of terminal cells s paints. It counts grapheme
// clusters, so a wide rune is two cells, a combining mark rides its base, and
// a join such as a family emoji is one glyph of the width its cluster has.
func Cells(s string) int {
	return opts.String(s)
}

// FirstCluster splits the first grapheme cluster from s and returns it with its
// width in cells.
func FirstCluster(s string) (string, int) {
	if s == "" {
		return "", 0
	}
	first := graphemes.FromString(s).First()
	return first, opts.String(first)
}

// FirstClusterBytes is FirstCluster for a byte slice, so a caller that walks a
// buffer keeps slicing without a copy.
func FirstClusterBytes(s []byte) ([]byte, int) {
	if len(s) == 0 {
		return nil, 0
	}
	first := graphemes.FromBytes(s).First()
	return first, opts.Bytes(first)
}

// Clip keeps the longest prefix of s that fits budget cells. It never splits a
// grapheme cluster and never returns more cells than the budget.
func Clip(s string, budget int) string {
	return opts.TruncateString(s, budget, "")
}

// Pad fills s on the right to budget cells. A long s is clipped first, so the
// result never paints more cells than the budget.
func Pad(s string, budget int) string {
	if budget <= 0 {
		return ""
	}
	s = Clip(s, budget)
	if n := budget - Cells(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// Center pads s on both sides to budget cells, with the odd cell going to the
// right. A long s is elided first, so the result never paints more cells than
// the budget.
func Center(s string, budget int) string {
	if budget <= 0 {
		return ""
	}
	s = Elide(s, budget)
	n := budget - Cells(s)
	if n <= 0 {
		return s
	}
	left := n / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", n-left)
}

// Elide shrinks s to budget cells and ends it with an ellipsis when it had to
// cut. A string that already fits comes back untouched.
func Elide(s string, budget int) string {
	if Cells(s) <= budget {
		return s
	}
	if budget < Cells(Ellipsis) {
		return ""
	}
	return opts.TruncateString(s, budget, Ellipsis)
}

// ClipTail keeps the last budget cells of s, whole clusters only. It exists for
// the rename draft, whose caller drops the head so the end of a long name stays
// readable.
func ClipTail(s string, budget int) string {
	if budget <= 0 {
		return ""
	}
	if Cells(s) <= budget {
		return s
	}
	type cluster struct {
		at    int
		width int
	}
	var clusters []cluster
	at := 0
	g := opts.StringGraphemes(s)
	for g.Next() {
		clusters = append(clusters, cluster{at: at, width: g.Width()})
		at += len(g.Value())
	}
	used, at := 0, len(s)
	for i := len(clusters) - 1; i >= 0; i-- {
		if used+clusters[i].width > budget {
			break
		}
		used += clusters[i].width
		at = clusters[i].at
	}
	return s[at:]
}

// AllDigits reports whether s is a non-empty run of decimal digits, the shape
// of an id without its letter.
func AllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
