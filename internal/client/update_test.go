package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"dm/internal/version"
)

// The update check's contract: an attach reads a cached answer, the cache has
// two lifetimes, the notice lands once per version per day, and every failure
// is quiet.

// setVersion stamps a version for the length of the test. The checker reads
// the package variable, the same one the linker stamps.
func setVersion(t *testing.T, v string) {
	t.Helper()
	old := version.Version
	version.Version = v
	t.Cleanup(func() { version.Version = old })
}

// clearUpdateEnv empties the two variables that switch the check off, so a
// runner that sets CI does not decide what these tests see.
func clearUpdateEnv(t *testing.T) {
	t.Helper()
	t.Setenv(updateSkipEnv, "")
	t.Setenv(ciEnv, "")
}

// seedCache writes a cache into a fresh state dir.
func seedCache(t *testing.T, c updateCache) string {
	t.Helper()
	dir := t.TempDir()
	if err := writeUpdateCache(dir, c); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	return dir
}

// TestShouldCheckTTL pins the two lifetimes: a success is fresh for a day and
// a failure for an hour, so an offline start does not retry on every attach.
func TestShouldCheckTTL(t *testing.T) {
	clearUpdateEnv(t)
	setVersion(t, "1.0.0")
	now := time.Now()

	cases := []struct {
		name  string
		cache updateCache
		want  bool
	}{
		{"no cache", updateCache{}, true},
		{"success just now", updateCache{CheckedAt: now, Latest: "1.4.0"}, false},
		{"success 23h old", updateCache{CheckedAt: now.Add(-23 * time.Hour), Latest: "1.4.0"}, false},
		{"success 25h old", updateCache{CheckedAt: now.Add(-25 * time.Hour), Latest: "1.4.0"}, true},
		{"failure 30m old", updateCache{CheckedAt: now.Add(-30 * time.Minute)}, false},
		{"failure 2h old", updateCache{CheckedAt: now.Add(-2 * time.Hour)}, true},
		{"cache without a stamp", updateCache{Latest: "1.4.0"}, true},
	}
	for _, tc := range cases {
		dir := t.TempDir()
		if tc.name != "no cache" {
			if err := writeUpdateCache(dir, tc.cache); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
		}
		if got := shouldCheck(dir, now); got != tc.want {
			t.Errorf("%s: shouldCheck = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestShouldCheckSkips pins the three reasons never to check: the operator
// turned it off, the machine is a CI runner, and the binary is not a release.
func TestShouldCheckSkips(t *testing.T) {
	stale := updateCache{CheckedAt: time.Now().Add(-48 * time.Hour), Latest: "1.4.0"}
	now := time.Now()

	t.Run("off by environment", func(t *testing.T) {
		clearUpdateEnv(t)
		setVersion(t, "1.0.0")
		t.Setenv(updateSkipEnv, "1")
		if shouldCheck(seedCache(t, stale), now) {
			t.Fatal("shouldCheck = true with DM_NO_UPDATE_CHECK set")
		}
	})
	t.Run("on CI", func(t *testing.T) {
		clearUpdateEnv(t)
		setVersion(t, "1.0.0")
		t.Setenv(ciEnv, "true")
		if shouldCheck(seedCache(t, stale), now) {
			t.Fatal("shouldCheck = true with CI set")
		}
	})
	t.Run("dev build", func(t *testing.T) {
		clearUpdateEnv(t)
		setVersion(t, version.DevVersion)
		if shouldCheck(seedCache(t, stale), now) {
			t.Fatal("shouldCheck = true on an unstamped build")
		}
	})
}

// TestUpdateNoticeText pins the one line a newer release prints.
func TestUpdateNoticeText(t *testing.T) {
	setVersion(t, "1.4.0")
	dir := seedCache(t, updateCache{CheckedAt: time.Now(), Latest: "1.5.2"})
	got := updateNotice(dir, version.Version, time.Now())
	if want := "dm 1.5.2 is out, run dm upgrade"; got != want {
		t.Fatalf("updateNotice = %q, want %q", got, want)
	}
}

// TestUpdateNoticeQuiet pins the four reasons the notice says nothing: an
// unreadable cache, a current that is not a release, a latest that is not a
// release, and a latest that is not newer.
func TestUpdateNoticeQuiet(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name    string
		current string
		latest  string
		corrupt bool
		absent  bool
	}{
		{name: "current is not a release", current: "dev", latest: "1.5.2"},
		{name: "latest is not a release", current: "1.4.0", latest: "nightly"},
		{name: "latest is empty", current: "1.4.0", latest: ""},
		{name: "same version", current: "1.4.0", latest: "1.4.0"},
		{name: "latest is older", current: "1.4.0", latest: "1.3.9"},
		{name: "corrupt cache", current: "1.4.0", latest: "1.5.2", corrupt: true},
		{name: "no cache", current: "1.4.0", absent: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			switch {
			case tc.absent:
			case tc.corrupt:
				if err := os.WriteFile(updatePath(dir), []byte("{not json"), 0o600); err != nil {
					t.Fatal(err)
				}
			default:
				if err := writeUpdateCache(dir, updateCache{CheckedAt: now, Latest: tc.latest}); err != nil {
					t.Fatal(err)
				}
			}
			if got := updateNotice(dir, tc.current, now); got != "" {
				t.Fatalf("updateNotice = %q, want no line", got)
			}
		})
	}
}

// TestUpdateNoticeOncePerVersion pins the rule the cache record exists for:
// the line lands on the first attach of the day and stays quiet for the rest,
// then returns the next day.
func TestUpdateNoticeOncePerVersion(t *testing.T) {
	setVersion(t, "1.4.0")
	now := time.Now()
	dir := seedCache(t, updateCache{CheckedAt: now, Latest: "1.5.2"})

	if got := updateNotice(dir, version.Version, now); got == "" {
		t.Fatal("first attach printed no notice")
	}
	if got := updateNotice(dir, version.Version, now.Add(time.Minute)); got != "" {
		t.Fatalf("second attach printed %q, want no line", got)
	}
	if got := updateNotice(dir, version.Version, now.Add(23*time.Hour)); got != "" {
		t.Fatalf("attach 23h later printed %q, want no line", got)
	}
	if got := updateNotice(dir, version.Version, now.Add(25*time.Hour)); got == "" {
		t.Fatal("attach 25h later printed no notice")
	}
}

// TestUpdateNoticeRecordsTheDay pins that the printed notice is what keeps the
// next attach quiet: Notified and NotifiedAt reach the cache, and nothing else
// in it moves.
func TestUpdateNoticeRecordsTheDay(t *testing.T) {
	setVersion(t, "1.4.0")
	now := time.Now()
	checked := now.Add(-2 * time.Hour)
	dir := seedCache(t, updateCache{CheckedAt: checked, Latest: "1.5.2"})

	updateNotice(dir, version.Version, now)

	c, ok := readUpdateCache(dir)
	if !ok {
		t.Fatal("notice left no readable cache")
	}
	if c.Notified != "1.5.2" || !c.NotifiedAt.Equal(now) {
		t.Fatalf("notice recorded Notified=%q NotifiedAt=%v, want 1.5.2 at %v", c.Notified, c.NotifiedAt, now)
	}
	if !c.CheckedAt.Equal(checked) || c.Latest != "1.5.2" {
		t.Fatalf("notice moved the answer: CheckedAt=%v Latest=%q", c.CheckedAt, c.Latest)
	}
}

// TestUpdateNoticeIgnoresAWriteFailure pins that an unwritable state root
// still shows the line: the notice is the product, the record is bookkeeping.
func TestUpdateNoticeIgnoresAWriteFailure(t *testing.T) {
	setVersion(t, "1.4.0")
	now := time.Now()
	dir := seedCache(t, updateCache{CheckedAt: now, Latest: "1.5.2"})
	// Readable and not writable: the cache is there, the record cannot land.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := writeUpdateCache(dir, updateCache{CheckedAt: now, Latest: "1.5.2"}); err == nil {
		t.Skip("state root is still writable, likely running as root")
	}
	if got := updateNotice(dir, version.Version, now); got == "" {
		t.Fatal("updateNotice hid the line because the record could not be written")
	}
}

// TestRunUpdateCheckWritesTheCache pins the refresh: it reads the release
// endpoint, strips the tag's leading v, stamps the read, and leaves the
// notice record alone. The failure case writes a stamped empty answer so the
// attach waits an hour instead of retrying on every start.
func TestRunUpdateCheckWritesTheCache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/latest" {
			t.Errorf("request path = %q, want /latest", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"tag_name":"v1.5.2","assets":[]}`))
	}))
	defer srv.Close()
	t.Setenv("DM_RELEASE_API", srv.URL)

	t.Run("success", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("DM_STATE", dir)
		now := time.Now()
		if err := writeUpdateCache(dir, updateCache{
			CheckedAt:  now.Add(-48 * time.Hour),
			Latest:     "1.4.0",
			Notified:   "1.5.1",
			NotifiedAt: now.Add(-48 * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}

		RunUpdateCheck()

		c, ok := readUpdateCache(dir)
		if !ok {
			t.Fatal("refresh wrote no readable cache")
		}
		if c.Latest != "1.5.2" {
			t.Fatalf("Latest = %q, want 1.5.2 (leading v stripped)", c.Latest)
		}
		if c.CheckedAt.IsZero() || time.Since(c.CheckedAt) > time.Minute {
			t.Fatalf("CheckedAt = %v, want now", c.CheckedAt)
		}
		if c.Notified != "1.5.1" || !c.NotifiedAt.Equal(now.Add(-48*time.Hour)) {
			t.Fatalf("refresh moved the notice record: %q at %v", c.Notified, c.NotifiedAt)
		}
	})

	t.Run("failure", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("DM_STATE", dir)
		// A local server that answers 500, reached through the same seam.
		bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer bad.Close()
		t.Setenv("DM_RELEASE_API", bad.URL)
		if err := writeUpdateCache(dir, updateCache{
			CheckedAt:  time.Now().Add(-48 * time.Hour),
			Latest:     "1.5.2",
			Notified:   "1.5.2",
			NotifiedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}

		RunUpdateCheck()

		c, ok := readUpdateCache(dir)
		if !ok {
			t.Fatal("failed refresh wrote no readable cache")
		}
		if c.Latest != "" {
			t.Fatalf("Latest = %q after a failure, want empty", c.Latest)
		}
		if c.CheckedAt.IsZero() || time.Since(c.CheckedAt) > time.Minute {
			t.Fatalf("CheckedAt = %v, want now", c.CheckedAt)
		}
		if c.Notified != "1.5.2" {
			t.Fatalf("failed refresh moved the notice record: Notified = %q", c.Notified)
		}
	})
}

// TestUpdateCacheRoundTrip pins the file shape: the JSON keys the cache is
// read back from, Notified omitted while it is empty.
func TestUpdateCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	checked := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := writeUpdateCache(dir, updateCache{CheckedAt: checked, Latest: "1.5.2"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(updatePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("cache is not JSON: %v", err)
	}
	if _, ok := raw["notified"]; ok {
		t.Fatalf("unannounced cache carries a notified key: %s", b)
	}
	if raw["latest"] != "1.5.2" {
		t.Fatalf("latest = %v, want 1.5.2", raw["latest"])
	}
	c, ok := readUpdateCache(dir)
	if !ok || !c.CheckedAt.Equal(checked) || c.Latest != "1.5.2" {
		t.Fatalf("round trip = %+v, %v", c, ok)
	}
}
