// Package blackbox tests dm from outside: it builds the binary, starts real
// sessions and talks to them the way a user or an agent would. Nothing here
// imports the production package; the tests assert on what dm prints and on
// the state it writes.
package blackbox
