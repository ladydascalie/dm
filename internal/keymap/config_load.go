package keymap

import (
	"dm/internal/theme"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultKDL returns the built-in default config text, the text a first run
// writes to config.kdl. Callers that need the defaults with one node changed
// patch this text and parse it.
func DefaultKDL() string {
	return defaultConfigKDL
}

// LoadDefault returns the built-in default keymap without touching disk. It
// panics when the built-in text is malformed, which is a programming error.
func LoadDefault() *Config {
	cfg, err := ParseConfig(defaultConfigKDL)
	if err != nil {
		panic("dm: default keymap is malformed: " + err.Error())
	}
	return cfg
}

// defaultConfigPath returns the default keymap path. It honours XDG_CONFIG_HOME
// and falls back to $HOME/.config.
func defaultConfigPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "dm", "config.kdl"), nil
	}
	home := os.Getenv("HOME")
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home directory not found: %w", err)
		}
	}
	return filepath.Join(home, ".config", "dm", "config.kdl"), nil
}

// LoadDefaultPath loads the default path. A missing file returns the built-in
// defaults. When write is true and the file is missing, it writes the default
// file first. A write or parse failure returns the defaults and the error, so
// the caller can toast it and keep running. It resolves the path from
// XDG_CONFIG_HOME and $HOME and delegates to LoadDefaultFrom.
func LoadDefaultPath(write bool) (*Config, error) {
	path, err := defaultConfigPath()
	if err != nil {
		return LoadDefault(), err
	}
	return LoadDefaultFrom(path, write)
}

// goodSuffix marks the copy of the config file that last parsed: dm keeps it
// beside the file and falls back to it when a later edit breaks the parse.
const goodSuffix = ".good"

// loadKeymap parses one config text, whitelists the action names and merges
// the defaults.
func loadKeymap(src string) (*Config, error) {
	cfg, err := ParseConfig(src)
	if err != nil {
		return nil, err
	}
	if err := cfg.checkActions(); err != nil {
		return nil, err
	}
	mergeDefaults(cfg)
	return cfg, nil
}

// applyTheme loads the theme cfg names from dir. A failure leaves the built-in
// theme in place and returns the error.
func applyTheme(cfg *Config, dir string) error {
	th, err := theme.LoadThemeFile(dir, cfg.themeName)
	if err != nil {
		return err
	}
	cfg.theme = th
	return nil
}

// saveGoodConfig keeps the last text that parsed beside the config file, so a
// later broken edit falls back to the last good config instead of the built-in
// defaults. Best effort: a directory that cannot be written loses the
// fallback, and a later broken edit runs the defaults.
func saveGoodConfig(path string, data []byte) {
	tmp := path + goodSuffix + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path+goodSuffix)
}

// lastGoodConfig loads the copy of the config file that last parsed.
func lastGoodConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path + goodSuffix)
	if err != nil {
		return nil, err
	}
	return loadKeymap(string(data))
}

// LoadDefaultFrom loads the keymap at an explicit path. A missing file returns
// the built-in defaults. When write is true and the file is missing, it writes
// the default file first. A parse failure falls back to the last good copy of
// the file, kept as <path>.good, so a broken edit never runs the session on
// the built-in defaults after one transient toast; the returned error names
// the broken line and the fallback. Without a good copy it returns the
// built-in defaults and the error, so the caller can toast it and keep
// running.
func LoadDefaultFrom(path string, write bool) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return LoadDefault(), err
		}
		cfg := LoadDefault()
		if !write {
			return cfg, nil
		}
		if err := writeDefaultConfig(path); err != nil {
			return cfg, err
		}
		return cfg, nil
	}
	cfg, err := loadKeymap(string(data))
	if err == nil {
		saveGoodConfig(path, data)
		return cfg, applyTheme(cfg, filepath.Dir(path))
	}
	// The file is broken. The last copy that parsed is still on disk; run it
	// rather than the built-in defaults, so a broken edit costs the operator
	// at most the edits since the last good parse.
	if gcfg, gerr := lastGoodConfig(path); gerr == nil {
		if terr := applyTheme(gcfg, filepath.Dir(path)); terr != nil {
			return gcfg, fmt.Errorf("%v; running the last good config from %s; theme: %v", err, path+goodSuffix, terr)
		}
		return gcfg, fmt.Errorf("%v; running the last good config from %s", err, path+goodSuffix)
	}
	return LoadDefault(), err
}

// LoadPath loads an explicit path. It never writes. A missing file returns the
// built-in defaults. A parse failure returns the last good config, or the
// built-in defaults without one, and the error. A theme read or parse failure
// returns the parsed keymap with the built-in theme and the error.
func LoadPath(path string) (*Config, error) {
	return LoadDefaultFrom(path, false)
}

// writeDefaultConfig writes the default keymap text. It creates the directory
// with 0700 and the file with 0600.
func writeDefaultConfig(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(defaultConfigKDL), 0o600)
}
