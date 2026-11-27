package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dm/internal/keymap"
	"dm/internal/testutil"
)

// configAt writes src at the default config path under a temporary config home
// and returns the path. The verb reads no state tree, so only XDG_CONFIG_HOME
// moves.
func configAt(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "dm", "config.kdl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestConfigVerbReportsClean proves a file that parses prints ok and the path.
func TestConfigVerbReportsClean(t *testing.T) {
	path := configAt(t, `prefix "ctrl+b"`+"\n")
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"config"}); err != nil {
			t.Fatalf("dm config: %v", err)
		}
	})
	if !strings.Contains(out, "ok") || !strings.Contains(out, path) {
		t.Fatalf("dm config printed %q, want ok and %s", out, path)
	}
}

// TestConfigVerbReportsTheWholeError proves the verb hands back the line the
// toast has no room for, and that it writes no error file of its own.
func TestConfigVerbReportsTheWholeError(t *testing.T) {
	path := configAt(t, `plugin "top" {
    exec "/bin/true"
    where "split"
}`)
	err := Run([]string{"config"})
	if err == nil {
		t.Fatal("dm config on a broken file exited 0")
	}
	if !strings.Contains(err.Error(), "line 1") || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("dm config error = %q, want the collision line", err)
	}
	if _, statErr := os.Stat(path + keymap.ErrorSuffix); !os.IsNotExist(statErr) {
		t.Fatalf("dm config wrote %s", path+keymap.ErrorSuffix)
	}
}

// TestConfigVerbJSON proves --json carries the same facts as one object, which
// is what an agent reads.
func TestConfigVerbJSON(t *testing.T) {
	path := configAt(t, `padding nope`+"\n")
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"config", "--json"}); err == nil {
			t.Fatal("dm config --json on a broken file exited 0")
		}
	})
	var rep struct {
		Path  string `json:"path"`
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("dm config --json printed %q: %v", out, err)
	}
	if rep.Path != path || rep.OK || rep.Error == "" {
		t.Fatalf("report = %+v, want the path, ok false and the error", rep)
	}
}

// TestConfigVerbAbsent reports a missing file as ok, because a missing file is
// the built-in defaults and not a failure.
func TestConfigVerbAbsent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"config"}); err != nil {
			t.Fatalf("dm config with no file: %v", err)
		}
	})
	if !strings.Contains(out, "absent") {
		t.Fatalf("dm config printed %q, want the absent note", out)
	}
}
