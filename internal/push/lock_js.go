//go:build js

package push

// fileLock is the exclusive claim on one data file. The wasm build runs in one
// browser tab, has no flock, and holds the only writer, so the claim is a
// no-op that keeps the shared callers compiling.
type fileLock struct{}

// lockFile returns at once: a tab never contends with another process.
func lockFile(string) (*fileLock, error) { return &fileLock{}, nil }

// release has nothing to drop.
func (l *fileLock) release() error { return nil }
