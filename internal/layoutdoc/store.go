package layoutdoc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"dm/internal/paths"
)

// Dir is the directory that holds the layout files: $XDG_CONFIG_HOME/dm/layouts
// when XDG_CONFIG_HOME is set, else $HOME/.config/dm/layouts. It resolves
// XDG_CONFIG_HOME before anything else, the way the keymap config loader does,
// because os.UserConfigDir does not honour the variable on every platform.
func Dir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "dm", "layouts"), nil
	}
	home := os.Getenv("HOME")
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home directory not found: %w", err)
		}
	}
	return filepath.Join(home, ".config", "dm", "layouts"), nil
}

// ValidName reports whether name is a safe layout file name: a single path
// component of letters, digits, underscores and hyphens, not empty and not
// starting with a hyphen. It reuses paths.ValidSessionName, the rule a session
// name already follows, and adds the leading hyphen check: a layout name
// reaches the command line as -L <name>, where a leading hyphen reads as a
// flag.
func ValidName(name string) bool {
	return paths.ValidSessionName(name) && !strings.HasPrefix(name, "-")
}

// Path is the file that holds the layout called name.
func Path(name string) (string, error) {
	if !ValidName(name) {
		return "", fmt.Errorf("layout name %q: want letters, digits, _ or -, not empty and no leading -", name)
	}
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".json"), nil
}

// Load reads and validates the layout file at path. The error names the file
// and the offending field, so a hand edit that breaks the shape reports where
// it broke. An unknown key is an error too: a misspelled field would otherwise
// load as its zero value.
func Load(path string) (*Doc, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	d := &Doc{}
	if err := dec.Decode(d); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("%s: trailing data after the document", path)
	}
	if err := d.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return d, nil
}

// Save writes d to path as indented JSON, atomically: a temp file in the same
// directory, synced, then renamed. The file is 0600, so a layout that names
// working directories stays private. The parent directory is created when it
// is missing.
func Save(path string, d *Doc) error {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// CreateTemp makes the file 0600, so the layout is private while written
	// and after the rename. The unique name per save lets overlapping saves
	// rename separate paths, so the last rename wins instead of one failing.
	f, err := os.CreateTemp(dir, ".layout-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
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

// Entry is one row of the layout list: the file, its counts and its mtime. Err
// carries a read or decode failure, so one broken file reports itself instead
// of hiding the rest.
type Entry struct {
	Name       string
	Path       string
	Workspaces int
	Windows    int
	Panes      int
	ModTime    time.Time
	Err        error
}

// List returns every *.json file in Dir, sorted by name. A missing directory is
// an empty list, not an error: dm has no layouts before the first save. Files
// decode leniently, without the strict field check or the shape check, so the
// list reports the counts of a file it could not apply.
func List() ([]Entry, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Entry, 0, len(files))
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, f.Name())
		e := Entry{Name: strings.TrimSuffix(f.Name(), ".json"), Path: path}
		if info, err := f.Info(); err == nil {
			e.ModTime = info.ModTime()
		}
		d := &Doc{}
		if err := decodeLenient(path, d); err != nil {
			e.Err = err
		} else {
			e.Workspaces = len(d.Workspaces)
			for _, ws := range d.Workspaces {
				e.Windows += len(ws.Windows)
			}
			e.Panes = len(d.Panes)
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// decodeLenient reads one layout file for the list: unknown keys and a wrong
// shape pass, because the row only needs the counts.
func decodeLenient(path string, d *Doc) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, d); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// Remove deletes the layout called name. A missing file is an error: the name
// was wrong or the layout was already removed, and neither is a success.
func Remove(name string) error {
	path, err := Path(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
