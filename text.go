package main

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// ellipsis marks a cut string. Every caller ends an elided run with it.
const ellipsis = "…"

// cells is the number of terminal cells s paints. It counts grapheme
// clusters, so a wide rune is two cells, a combining mark rides its base, and
// a join such as a family emoji is one glyph of the width its cluster has.
func cells(s string) int {
	return ansi.StringWidth(s)
}

// clip keeps the longest prefix of s that fits budget cells. It never splits a
// grapheme cluster and never returns more cells than the budget.
func clip(s string, budget int) string {
	return ansi.Truncate(s, budget, "")
}

// pad fills s on the right to budget cells. A long s is clipped first, so the
// result never paints more cells than the budget.
func pad(s string, budget int) string {
	if budget <= 0 {
		return ""
	}
	s = clip(s, budget)
	if n := budget - cells(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// elide shrinks s to budget cells and ends it with an ellipsis when it had to
// cut. A string that already fits comes back untouched.
func elide(s string, budget int) string {
	return ansi.Truncate(s, budget, ellipsis)
}

// clipTail keeps the last budget cells of s, whole clusters only. It exists for
// the rename draft, whose caller drops the head so the end of a long name stays
// readable.
func clipTail(s string, budget int) string {
	if budget <= 0 {
		return ""
	}
	if cells(s) <= budget {
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
