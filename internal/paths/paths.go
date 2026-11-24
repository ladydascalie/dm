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

// SessionDirAt is the directory that holds one session's state and
// sockets, rooted at the given state dir.
func SessionDirAt(root, name string) string {
	return filepath.Join(root, "sessions", name)
}

// SessionDir is the directory that holds one session's state and sockets.
func SessionDir(name string) string { return SessionDirAt(StateDir(), name) }

// ValidSessionName reports whether name is a safe session directory name: a
// single path component of letters, digits, underscores and hyphens. A
// session name lands in paths under the state root, so anything else is a
// filesystem traversal or a phantom ls row waiting to happen.
func ValidSessionName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// ServerSockAt is the unix socket the session's server listens on,
// rooted at the given state dir.
func ServerSockAt(root, name string) string {
	return filepath.Join(SessionDirAt(root, name), "server.sock")
}

// ServerSock is the unix socket the session's server listens on.
func ServerSock(name string) string { return ServerSockAt(StateDir(), name) }

// PaneSockAt is the unix socket one pane's mux listens on, rooted at the
// given state dir.
func PaneSockAt(root, name, id string) string {
	return filepath.Join(SessionDirAt(root, name), "panes", id+".sock")
}

// PaneSock is the unix socket one pane's mux listens on.
func PaneSock(name, id string) string { return PaneSockAt(StateDir(), name, id) }

// StatePathAt is the session's state file, rooted at the given state dir.
func StatePathAt(root, name string) string {
	return filepath.Join(SessionDirAt(root, name), "state.json")
}

// StatePath is the session's state file.
func StatePath(name string) string { return StatePathAt(StateDir(), name) }

// PrefsPath is the client's preferences file, under the state root. It holds
// what the operator chose in the TUI and wants back on the next run.
func PrefsPath() string { return filepath.Join(StateDir(), "prefs.json") }

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
