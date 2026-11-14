// Package blackbox tests dm from outside: it builds the binary, starts real
// sessions and talks to them the way a user or an agent would. The tests
// assert on what dm prints and on the state it writes. The only production
// imports are wire-format ones (wire, vt, cells): they decode what dm puts on
// the wire and produce no expectation of their own. Every expected number is
// pinned in the tests, so a production change cannot move the goalposts.
package blackbox
