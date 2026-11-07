package shot

import (
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
)

// TestGoldenThemes parses every theme file the golden matrix uses. It is the
// merge gate for the testdata unit: a theme that does not parse fails here.
func TestGoldenThemes(t *testing.T) {
	dir := "testdata"
	names, err := ThemeNames(dir)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skipf("no theme data at %s/themes", dir)
	}
	if err != nil {
		t.Fatalf("themes: %v", err)
	}
	if len(names) == 0 {
		t.Skipf("no theme files in %s/themes", dir)
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadTheme(dir, name); err != nil {
				t.Fatalf("theme %s: %v", name, err)
			}
		})
	}
}

// TestGoldenScenes loads the scene list. A missing file is a skip, so this
// passes before the data lands.
func TestGoldenScenes(t *testing.T) {
	path := filepath.Join("testdata", "scenes.json")
	scenes, err := LoadScenes(path)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skipf("no scene data at %s", path)
	}
	if err != nil {
		t.Fatalf("scenes: %v", err)
	}
	if len(scenes) == 0 {
		t.Fatalf("no scenes in %s", path)
	}
	for _, sc := range scenes {
		if sc.Name == "" || sc.Cols < 1 || sc.Rows < 1 {
			t.Fatalf("scene %q has no name or size", sc.Name)
		}
	}
}
