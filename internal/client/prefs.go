package client

import (
	"encoding/json"
	"os"
	"path/filepath"

	"dm/internal/switcher"
)

// prefs is what the client keeps between runs: the choices that belong to the
// operator rather than to the session. The zero value is every default, so a
// missing file opens dm the way a first run does.
type prefs struct {
	// DashboardSort is the dashboard's ordering, saved as the mode name the
	// switcher's footer shows: "recent", "project" or "status". An empty or
	// unknown name opens the dashboard on recent.
	DashboardSort string `json:"dashboardSort"`
	// GitBoxCols and GitBoxRows are the git facts box's size: the outside
	// width and the interior row count a drag left. One size serves every
	// window. Zero in either means automatic, which is what a first run and a
	// wiped prefs file get, so the box hugs its facts until the operator drags
	// it.
	GitBoxCols int `json:"gitBoxCols"`
	GitBoxRows int `json:"gitBoxRows"`
}

// loadPrefs reads the prefs file at path. A missing, unreadable or malformed
// file is the zero prefs: a preference is a convenience, never a reason to
// fail a start or to toast at one.
func loadPrefs(path string) prefs {
	if path == "" {
		return prefs{}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return prefs{}
	}
	var p prefs
	if err := json.Unmarshal(b, &p); err != nil {
		return prefs{}
	}
	return p
}

// savePrefs writes p to path so a reader sees either the old file or the new
// one and never a half write: the payload lands in a temp file beside the
// target and is renamed over it. The directory is 0700 and the file 0600,
// because the state tree belongs to the operator alone.
func savePrefs(path string, p prefs) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "prefs-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// saveDashboardSort remembers the dashboard's ordering for the next run: the
// sort key writes the mode it landed on. A client with no prefs file, such as
// a browser page, and a read-only client write nothing. A failing write is
// dropped without a toast: the ordering is a convenience, and a dashboard key
// that shouted at the operator about a disk would be worse than a forgotten
// mode.
func (t *tui) saveDashboardSort(mode switcher.SortMode) {
	if t.prefsPath == "" || t.readOnly {
		return
	}
	t.prefs.DashboardSort = mode.String()
	_ = savePrefs(t.prefsPath, t.prefs)
}
