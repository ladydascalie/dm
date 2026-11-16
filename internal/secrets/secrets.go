// Package secrets fetches values by running registered tools, keeps them in
// memory, and writes them to files. A value stays out of argv, out of the
// child environment unless the tool requires it by explicit configuration,
// out of logs, and out of error strings.
package secrets

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Tool describes an external command that prints a secret on stdout.
type Tool struct {
	Name string   // registry key, e.g. "op"
	Bin  string   // executable path or name
	Args []string // argument template; a secret never goes here
	Env  []string // env entries the tool requires, as KEY=VALUE
}

// Fetch runs the tool and returns its stdout, minus trailing newlines.
// The child environment is built explicitly: it holds exactly t.Env and
// nothing else. Nothing is inherited from the parent process. The value
// never appears in argv. On failure the error carries the exit code, not
// stdout or stderr, so the value cannot leak through it.
func Fetch(t Tool) (string, error) {
	if t.Bin == "" {
		return "", fmt.Errorf("secrets: tool %q has no binary", t.Name)
	}
	cmd := exec.Command(t.Bin, t.Args...)
	cmd.Env = append([]string(nil), t.Env...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("secrets: %s exited %d", t.Name, ee.ExitCode())
		}
		return "", fmt.Errorf("secrets: run %s: %v", t.Name, err)
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

// Registry holds fetched values in memory, keyed by secret name.
type Registry struct {
	mu   sync.Mutex
	vals map[string]string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{vals: make(map[string]string)}
}

// Put stores value under name. An existing value is replaced. Rotation is
// Put again, optionally after Drop.
func (r *Registry) Put(name, value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.vals == nil {
		r.vals = make(map[string]string)
	}
	r.vals[name] = value
}

// Get returns the value held for name.
func (r *Registry) Get(name string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.vals[name]
	return v, ok
}

// Drop removes name and reports whether a value was held.
func (r *Registry) Drop(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.vals[name]
	delete(r.vals, name)
	return ok
}

// Hydrate writes value to path with restrictive permissions, creating parent
// directories as needed. New parents get 0700. The file is truncated and
// forced to 0600, even when it already exists with laxer permissions.
func Hydrate(path, value string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("secrets: create %s: %w", dir, err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("secrets: open %s: %w", path, err)
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return fmt.Errorf("secrets: chmod %s: %w", path, err)
	}
	if _, err := f.WriteString(value); err != nil {
		f.Close()
		return fmt.Errorf("secrets: write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("secrets: close %s: %w", path, err)
	}
	return nil
}
