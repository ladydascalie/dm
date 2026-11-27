package layoutdoc

import (
	"os"
	"path/filepath"
	"testing"
)

func readFile(t *testing.T, path string) ([]byte, error) {
	t.Helper()
	return os.ReadFile(path)
}

func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o600)
}

// TestDirHonoursXDG pins the env precedence: XDG_CONFIG_HOME first, then
// $HOME/.config. These cases set process env, so they stay serial.
func TestDirHonoursXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got, err := Dir(); err != nil || got != "/xdg/dm/layouts" {
		t.Fatalf("Dir() = %q, %v, want /xdg/dm/layouts", got, err)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/you")
	if got, err := Dir(); err != nil || got != "/home/you/.config/dm/layouts" {
		t.Fatalf("Dir() = %q, %v, want /home/you/.config/dm/layouts", got, err)
	}
}

func TestValidName(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"go-backend": true,
		"dev_2":      true,
		"a":          true,
		"":           false,
		"-nope":      false,
		"has space":  false,
		"has/slash":  false,
		"has.dot":    false,
		"../escape":  false,
		"new\nline":  false,
		"ünïcode":    false,
	}
	for name, want := range cases {
		if got := ValidName(name); got != want {
			t.Errorf("ValidName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestPathRefusesBadName(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := Path("-evil"); err == nil {
		t.Fatal("Path accepted a leading hyphen")
	}
	got, err := Path("go-backend")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	want := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "dm", "layouts", "go-backend.json")
	if got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
}

// TestSaveThenRemove proves a save lands at the path the list reads and that
// Remove deletes it.
func TestSaveThenRemove(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := Path("go-backend")
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(path, sample()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := Remove("go-backend"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Remove left the file: %v", err)
	}
	if err := Remove("go-backend"); err == nil {
		t.Fatal("Remove accepted a missing layout")
	}
}

// TestListReportsOneBrokenFile proves the list keeps the good rows when one
// file does not parse, and that a missing directory is an empty list.
func TestListReportsOneBrokenFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if got, err := List(); err != nil || len(got) != 0 {
		t.Fatalf("List on a missing dir = %v, %v, want empty", got, err)
	}
	good, err := Path("go-backend")
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(good, sample()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(dir, "broken.json"), "{not json"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(dir, "notes.txt"), "ignored"); err != nil {
		t.Fatal(err)
	}
	got, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List returned %d rows, want 2: %+v", len(got), got)
	}
	if got[0].Name != "broken" || got[0].Err == nil {
		t.Errorf("row 0 = %+v, want broken with an error", got[0])
	}
	if got[1].Name != "go-backend" || got[1].Err != nil {
		t.Fatalf("row 1 = %+v, want go-backend with no error", got[1])
	}
	if got[1].Workspaces != 1 || got[1].Windows != 1 || got[1].Panes != 2 {
		t.Errorf("counts = %d/%d/%d, want 1/1/2", got[1].Workspaces, got[1].Windows, got[1].Panes)
	}
	if got[1].Path != good {
		t.Errorf("path = %q, want %q", got[1].Path, good)
	}
	if got[1].ModTime.IsZero() {
		t.Error("row 1 has no mtime")
	}
}

// TestSaveIsPrivate proves the layout file is 0600 and the directory it lands
// in is created when missing.
func TestSaveIsPrivate(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "dm", "layouts", "private.json")
	if err := Save(path, sample()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %o, want 600", perm)
	}
	files, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("Save left %d files, want 1", len(files))
	}
}
