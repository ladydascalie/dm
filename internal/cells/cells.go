// Package cells measures and clips text by terminal cell width.
package cells

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Ellipsis marks a cut string. Every caller ends an elided run with it.
const Ellipsis = "…"

// Cells is the number of terminal cells s paints. It counts grapheme
// clusters, so a wide rune is two cells, a combining mark rides its base, and
// a join such as a family emoji is one glyph of the width its cluster has.
func Cells(s string) int {
	return ansi.StringWidth(s)
}

// Clip keeps the longest prefix of s that fits budget cells. It never splits a
// grapheme cluster and never returns more cells than the budget.
func Clip(s string, budget int) string {
	return ansi.Truncate(s, budget, "")
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
	return ansi.Truncate(s, budget, Ellipsis)
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
	for i := 0; i < len(s); {
		text, w := ansi.FirstGraphemeCluster(s[i:], ansi.GraphemeWidth)
		clusters = append(clusters, cluster{at: i, width: w})
		i += len(text)
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
