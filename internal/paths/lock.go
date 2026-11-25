package paths

import "errors"

// ErrSessionLocked reports that another process holds a session's lock. A held
// lock means a live session owns the directory, whatever its pid file says.
//
// The invariant every path that removes a session directory keeps: no code path
// removes a session directory while another process holds that session's lock.
// The server takes server.lock for its whole life; reset, purge and the
// server's own endSession take it before they unlink anything, so a directory
// removal can never land under a live server. A pid file is a hint that names
// what to signal; the lock is the truth about what is live.
var ErrSessionLocked = errors.New("session lock is held by a live process")
