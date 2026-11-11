package cli

// The msg clear flags: --prefix parses, and key with prefix is refused.

import (
	"strings"
	"testing"
)

// clearFlags builds the cmdFlags parse builds, so a test drives the flags a
// shell would pass.
func clearFlags(t *testing.T, args ...string) *cmdFlags {
	t.Helper()
	c := newFlags("msg")
	c.parse(args)
	return c
}

// --prefix parses on msg clear and rides alone.
func TestClearParsesPrefixFlag(t *testing.T) {
	t.Parallel()
	c := clearFlags(t, "clear", "--prefix", "notify/")
	if c.keyPrefix != "notify/" || c.key != "" {
		t.Fatalf("flags = key %q prefix %q, want the prefix alone", c.key, c.keyPrefix)
	}
}

// --key and --prefix together are refused before any request goes out.
func TestClearRefusesKeyAndPrefix(t *testing.T) {
	t.Parallel()
	c := clearFlags(t, "clear", "--key", "notify/w1p1", "--prefix", "notify/")
	err := boxClearVerb(c)
	if err == nil || !strings.Contains(err.Error(), "box: key and keyPrefix are exclusive") {
		t.Fatalf("clear with key and prefix = %v, want the exclusivity error", err)
	}
}
