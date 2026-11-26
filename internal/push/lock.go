package push

// lockPath is the lock file beside path: <path>.lock.
//
// dm web and the daemon can hold the same session directory at once, and each
// writer keeps its list in memory, so an unlocked read-modify-write would drop
// the other writer's entry. The subscription store and the VAPID key pair are
// the two files this package rewrites in place; both take this lock across the
// whole read-modify-write. A reader that never writes takes no lock.
func lockPath(path string) string { return path + ".lock" }
