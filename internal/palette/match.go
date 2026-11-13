package palette

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Filter ranks the commands against the query and returns their indices into
// the commands slice, best match first. An empty query returns every index in
// input order. A command that matches nothing is left out; the function never
// errors.
//
// Ties in score keep the input order.
func Filter(commands []Command, query string) []int {
	q := []rune(strings.ToLower(query))
	if len(q) == 0 {
		all := make([]int, len(commands))
		for i := range all {
			all[i] = i
		}
		return all
	}
	type hit struct{ idx, score int }
	hits := make([]hit, 0, len(commands))
	for i, c := range commands {
		if s, ok := commandScore(c, q); ok {
			hits = append(hits, hit{i, s})
		}
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].score > hits[b].score })
	out := make([]int, len(hits))
	for i, h := range hits {
		out[i] = h.idx
	}
	return out
}

// commandScore scores one command against the query: the best of its name and
// its hint, with a hint match losing to an equal name match. A command that
// matches neither has no score.
func commandScore(c Command, q []rune) (int, bool) {
	best, found := 0, false
	if s, ok := score([]rune(c.Name), q); ok {
		best, found = s, true
	}
	if s, ok := score([]rune(c.Hint), q); ok && (!found || s-hintPenalty > best) {
		best, found = s-hintPenalty, true
	}
	return best, found
}

// hintPenalty keeps a name match ahead of a hint match of the same quality.
const hintPenalty = 64

// score matches q as a subsequence of h and scores the alignment. A match at a
// word boundary or in a run with the previous match pays; a long lead-in or a
// wide gap costs. Equal scores resolve to the earliest alignment.
//
// h keeps its original case so camelCase humps still read as word boundaries;
// the match itself folds case per rune.
func score(h, q []rune) (int, bool) {
	if len(q) == 0 {
		return 0, true
	}
	n, m := len(h), len(q)
	if n < m {
		return 0, false
	}
	// The dynamic program walks the query one rune per row. prev holds row
	// j-1: prev[i] is the best score matching q[:j-1] with its last rune at
	// i. cur holds row j.
	noScore := math.MinInt32
	prev := make([]int, n)
	cur := make([]int, n)
	for j := 0; j < m; j++ {
		// best and bestAt fold row j-1 over indices below i: the match for
		// q[j] must land after the match for q[j-1].
		best, bestAt := noScore, -1
		for i := 0; i < n; i++ {
			if j == 0 {
				cur[i] = noScore
				if unicode.ToLower(h[i]) == q[0] {
					s := -i
					if atBoundary(h, i) {
						s += 16
					}
					cur[i] = s
				}
			} else {
				cur[i] = noScore
				if bestAt >= 0 && unicode.ToLower(h[i]) == q[j] {
					s := best
					if i == bestAt+1 {
						s += 8
					} else {
						s -= min(i-bestAt-1, 8)
					}
					if atBoundary(h, i) {
						s += 16
					}
					cur[i] = s
				}
			}
			if j > 0 && prev[i] > best {
				best, bestAt = prev[i], i
			}
		}
		prev, cur = cur, prev
	}
	// The last row holds the full alignment. Strictly greater keeps the
	// earliest alignment on a tie, because the scan runs left to right.
	best := noScore
	for i := 0; i < n; i++ {
		if prev[i] > best {
			best = prev[i]
		}
	}
	if best == noScore {
		return 0, false
	}
	return best, true
}

// atBoundary reports whether the rune at i starts a word: the first rune, the
// rune after a separator, or an upper case rune after a lower case one.
func atBoundary(h []rune, i int) bool {
	if i == 0 {
		return true
	}
	switch h[i-1] {
	case ' ', '-', '_', '/', '.', ':':
		return true
	}
	return unicode.IsLower(h[i-1]) && unicode.IsUpper(h[i])
}
