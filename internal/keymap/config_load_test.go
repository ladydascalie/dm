package keymap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadDefaultPathRecordsTheError proves a failed load leaves the whole line
// beside the config and a clean load removes it, because a toast elides the
// text and dies in three seconds.
func TestLoadDefaultPathRecordsTheError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "dm"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := writeConfigFile(t, filepath.Join(dir, "dm"), `plugin "top" {
    exec "/bin/true"
    where "split"
}`)

	if _, err := LoadDefaultPath(false); err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("LoadDefaultPath on a broken file = %v, want the collision error", err)
	}
	data, err := os.ReadFile(path + ErrorSuffix)
	if err != nil {
		t.Fatalf("no %s beside the config: %v", ErrorSuffix, err)
	}
	if got := strings.TrimSpace(string(data)); got == "" || !strings.Contains(got, "collides") {
		t.Fatalf("%s holds %q, want the whole error line", ErrorSuffix, got)
	}

	if err := os.WriteFile(path, []byte(`prefix "ctrl+b"`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDefaultPath(false); err != nil {
		t.Fatalf("LoadDefaultPath on a clean file: %v", err)
	}
	if _, err := os.Stat(path + ErrorSuffix); !os.IsNotExist(err) {
		t.Fatalf("%s survived a clean load: %v", ErrorSuffix, err)
	}
}

// TestLoadPathRecordsNothing proves the read-only loader writes no error file,
// so `dm config` reports without changing the tree the client owns.
func TestLoadPathRecordsNothing(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigFile(t, dir, `padding nope`)

	if _, err := LoadPath(path); err == nil {
		t.Fatal("LoadPath on a broken file returned no error")
	}
	if _, err := os.Stat(path + ErrorSuffix); !os.IsNotExist(err) {
		t.Fatalf("LoadPath wrote %s", path+ErrorSuffix)
	}
}
