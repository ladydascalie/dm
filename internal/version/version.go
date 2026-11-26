// Package version carries the build stamp of the binary and orders two
// release tags.
//
// The linker writes Version and Commit at build time:
//
//	go build -ldflags "-X dm/internal/version.Version=1.2.3 \
//	                    -X dm/internal/version.Commit=abc1234"
//
// An unstamped build, the one "go build ." makes, reports the two defaults.
// The package imports nothing outside the standard library: the release
// workflow stamps it into every binary, so it stays a leaf.
package version

import (
	"strconv"
	"strings"
)

// Version is the release tag without its leading "v". The linker stamps it.
var Version = "dev"

// Commit is the short sha of the build. The linker stamps it.
var Commit = "none"

// DevVersion is the Version of an unstamped build.
const DevVersion = "dev"

// DevCommit is the Commit of an unstamped build.
const DevCommit = "none"

// String returns the version as the operator reads it: the stamped version,
// with "+<commit>" appended when a commit was stamped too.
//
//	1.2.3+abc1234
//	1.2.3
//	dev
func String() string {
	if Commit == "" || Commit == DevCommit {
		return Version
	}
	return Version + "+" + Commit
}

// IsRelease reports whether v is a bare semantic version: three dot-separated
// integers, then an optional "-prerelease" and an optional "+build". A leading
// "v" is not part of the version; a stamp drops it.
func IsRelease(v string) bool {
	_, ok := parse(v)
	return ok
}

// Compare orders two versions and returns -1, 0 or 1.
//
// A prerelease sorts below the release it belongs to, build metadata is
// ignored, and each numeric field is compared as a number rather than a
// string, so 0.2.0 is below 0.10.0. When either side does not parse, Compare
// falls back to string inequality rather than guess: equal strings compare
// equal, otherwise the result is the sign of the string comparison.
func Compare(a, b string) int {
	x, xok := parse(a)
	y, yok := parse(b)
	if !xok || !yok {
		return sign(strings.Compare(a, b))
	}
	return x.compare(y)
}

// parsed is one version taken apart.
type parsed struct {
	// numbers holds major, minor and patch.
	numbers [3]int
	// pre holds the prerelease identifiers, empty for a release.
	pre []string
}

// parse takes a bare semantic version apart. It refuses a leading "v", a
// missing or extra numeric field, and any stray character.
func parse(v string) (parsed, bool) {
	var p parsed
	if v == "" {
		return p, false
	}
	var build string
	// The build metadata is not part of the order, so split it off first and
	// check it only for shape.
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v, build = v[:i], v[i+1:]
		if !identifiers(build) {
			return p, false
		}
	}
	var pre string
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, pre = v[:i], v[i+1:]
		if pre == "" {
			return p, false
		}
	}
	fields := strings.Split(v, ".")
	if len(fields) != len(p.numbers) {
		return p, false
	}
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil || n < 0 {
			return p, false
		}
		p.numbers[i] = n
	}
	if pre != "" {
		p.pre = strings.Split(pre, ".")
		if !identifiers(pre) {
			return p, false
		}
	}
	return p, true
}

// identifiers reports whether s is a dot-separated list of legal prerelease or
// build identifiers.
func identifiers(s string) bool {
	for _, id := range strings.Split(s, ".") {
		if !identifier(id) {
			return false
		}
	}
	return true
}

// identifier reports whether id is a legal prerelease identifier: dot
// separated, so non-empty, and made of ASCII letters, digits and hyphens.
func identifier(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '-':
		default:
			return false
		}
	}
	return true
}

// compare orders the receiver against other.
func (p parsed) compare(other parsed) int {
	for i := range p.numbers {
		if c := compareInt(p.numbers[i], other.numbers[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(p.pre) == 0 && len(other.pre) == 0:
		return 0
	case len(p.pre) == 0:
		return 1
	case len(other.pre) == 0:
		return -1
	}
	for i := 0; i < len(p.pre) && i < len(other.pre); i++ {
		if c := compareIdentifier(p.pre[i], other.pre[i]); c != 0 {
			return c
		}
	}
	return compareInt(len(p.pre), len(other.pre))
}

// compareIdentifier orders two prerelease identifiers: numeric identifiers
// compare as numbers, an alphanumeric one sorts above a numeric one, and two
// alphanumeric ones compare as strings.
func compareIdentifier(a, b string) int {
	an, aNum := atoi(a)
	bn, bNum := atoi(b)
	switch {
	case aNum && bNum:
		return compareInt(an, bn)
	case aNum:
		return -1
	case bNum:
		return 1
	}
	return sign(strings.Compare(a, b))
}

// atoi reports the value of a numeric identifier. A leading zero is not a
// number here: semver forbids it, and "01" sorts as text.
func atoi(s string) (int, bool) {
	if s == "" || s[0] == '0' {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}
