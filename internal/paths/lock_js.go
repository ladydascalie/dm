//go:build js

package paths

import "errors"

// SessionLock is the exclusive claim on one session's directory. The wasm
// build has no session sockets and no server to lock against, so the type
// exists only to keep the shared callers compiling.
type SessionLock struct{}

// LockSession has no meaning in a browser tab: there is no second process to
// lock against. The refusal is explicit rather than a silent no-op.
func LockSession(root, name string) (*SessionLock, error) {
	return nil, errors.New("session locks are not supported on this platform")
}

// SessionLockHeld reports no holder: the wasm build never locks a session.
func SessionLockHeld(root, name string) (bool, error) { return false, nil }

// Release drops a lock the wasm build never takes.
func (l *SessionLock) Release() error { return nil }

// StillOurs reports false: the wasm build holds no lock to still own.
func (l *SessionLock) StillOurs() bool { return false }
