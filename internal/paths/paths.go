// Package paths holds the session filesystem layout: where dm keeps its
// state, its sockets, and the default window label for a directory.
package paths

import (
	"os"
	"path/filepath"
)

// StateDir is the root of dm's state tree. DM_STATE wins, then
// XDG_STATE_HOME, then ~/.local/state/dm.
func StateDir() string {
	if v := os.Getenv("DM_STATE"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return filepath.Join(v, "dm")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "dm")
}

// SessionDir is the directory that holds one session's state and sockets.
func SessionDir(name string) string { return filepath.Join(StateDir(), "sessions", name) }

// ServerSock is the unix socket the session's server listens on.
func ServerSock(name string) string { return filepath.Join(SessionDir(name), "server.sock") }

// PaneSock is the unix socket one pane's mux listens on.
func PaneSock(name, id string) string {
	return filepath.Join(SessionDir(name), "panes", id+".sock")
}

// StatePath is the session's state file.
func StatePath(name string) string { return filepath.Join(SessionDir(name), "state.json") }

// FolderName is the default window label for a directory: its last path
// element, with the filesystem root spelled out.
func FolderName(dir string) string {
	if dir == "" {
		return ""
	}
	dir = filepath.Clean(dir)
	base := filepath.Base(dir)
	if base == "/" || base == "." || base == string(filepath.Separator) {
		return "root"
	}
	return base
}
