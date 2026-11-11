package keymap

import (
	"dm/internal/theme"
	"fmt"
	"os"
	"path/filepath"
)

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

// LoadDefaultFrom loads the keymap at an explicit path. A missing file returns
// the built-in defaults. When write is true and the file is missing, it writes
// the default file first. A write or parse failure returns the defaults and the
// error, so the caller can toast it and keep running.
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
	cfg, err := ParseConfig(string(data))
	if err != nil {
		return LoadDefault(), err
	}
	mergeDefaults(cfg)
	th, err := theme.LoadThemeFile(filepath.Dir(path), cfg.themeName)
	if err != nil {
		return cfg, err
	}
	cfg.theme = th
	return cfg, nil
}

// LoadPath loads an explicit path. It never writes. A missing file returns the
// built-in defaults. A parse failure returns the defaults and the error. A
// theme read or parse failure returns the parsed keymap with the built-in
// theme and the error.
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
