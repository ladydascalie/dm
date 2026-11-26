package push

import "path/filepath"

// StorePath is the subscription store inside a session directory. dm web writes
// it and the daemon reads it, so both take the path from here and cannot drift.
func StorePath(sessionDir string) string {
	return filepath.Join(sessionDir, "webpush.json")
}

// KeysPath is the VAPID key pair inside a session directory. The daemon reads
// the pair dm web generated, so the two must name one file.
func KeysPath(sessionDir string) string {
	return filepath.Join(sessionDir, "webpush.key")
}
