package version

import "testing"

func TestIsRelease(t *testing.T) {
	tests := []struct {
		v    string
		want bool
	}{
		{"0.1.0", true},
		{"1.2.3", true},
		{"1.2.3-rc.1", true},
		{"1.2.3+build.7", true},
		{"1.2.3-rc.1+build.7", true},
		{"10.20.30-pre", true},
		{"v1.2.3", false},
		{"dev", false},
		{"none", false},
		{"", false},
		{"1.2", false},
		{"1.2.3.4", false},
		{"1.2.x", false},
		{"1..3", false},
		{"1.2.3-", false},
		{"1.2.3+build!", false},
		{"1.2.3-rc..1", false},
		{"0d5166c", false},
		{"1.2.3 ", false},
	}
	for _, tc := range tests {
		if got := IsRelease(tc.v); got != tc.want {
			t.Errorf("IsRelease(%q) = %v, want %v", tc.v, got, tc.want)
		}
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		// Numeric fields, not strings: "0.10.0" is above "0.2.0".
		{"0.2.0", "0.10.0", -1},
		{"0.10.0", "0.2.0", 1},
		{"0.2.0", "0.2.0", 0},
		{"1.0.0", "0.9.9", 1},
		{"1.2.3", "1.2.10", -1},
		// A prerelease sits below the release it belongs to.
		{"1.0.0-rc.1", "1.0.0", -1},
		{"1.0.0", "1.0.0-rc.1", 1},
		{"1.0.0-alpha", "1.0.0-beta", -1},
		{"1.0.0-alpha.1", "1.0.0-alpha.2", -1},
		{"1.0.0-alpha.2", "1.0.0-alpha.10", -1},
		{"1.0.0-alpha", "1.0.0-alpha.1", -1},
		{"1.0.0-rc.1", "1.0.0-rc.1", 0},
		// Build metadata is ignored.
		{"1.2.3+build.1", "1.2.3+build.2", 0},
		{"1.2.3", "1.2.3+abc", 0},
		{"1.2.3-rc.1+a", "1.2.3-rc.1+b", 0},
		// Unparsable input falls back to string inequality.
		{"dev", "dev", 0},
		{"dev", "1.0.0", 1},
		{"1.0.0", "dev", -1},
		{"v1.0.0", "1.0.0", 1},
		{"garbage", "junk", -1},
		{"", "", 0},
	}
	for _, tc := range tests {
		if got := Compare(tc.a, tc.b); got != tc.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if got := Compare(tc.b, tc.a); got != -tc.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.b, tc.a, got, -tc.want)
		}
	}
}

func TestString(t *testing.T) {
	defer func(v, c string) { Version, Commit = v, c }(Version, Commit)
	tests := []struct {
		version, commit, want string
	}{
		{"dev", "none", "dev"},
		{"dev", "", "dev"},
		{"1.2.3", "none", "1.2.3"},
		{"1.2.3", "", "1.2.3"},
		{"1.2.3", "abc1234", "1.2.3+abc1234"},
	}
	for _, tc := range tests {
		Version, Commit = tc.version, tc.commit
		if got := String(); got != tc.want {
			t.Errorf("String() with %q/%q = %q, want %q", tc.version, tc.commit, got, tc.want)
		}
	}
}

func TestDefaults(t *testing.T) {
	if DevVersion != "dev" || DevCommit != "none" {
		t.Fatalf("the defaults changed: %q/%q", DevVersion, DevCommit)
	}
}
