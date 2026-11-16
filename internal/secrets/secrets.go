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
	// Env holds the entries the tool requires beyond the inheritance, as
	// KEY=VALUE. An entry wins over an inherited one of the same name.
	Env []string
	// Inherit names the variables copied from this process into the child,
	// when they are set: configuration and paths a tool needs, never a
	// credential. Empty means the child env holds exactly Env.
	Inherit []string
}

// childEnv builds the child's environment: the inherited names this process
// carries, then the explicit entries, which win. Nothing else passes.
func childEnv(t Tool) []string {
	if len(t.Inherit) == 0 && len(t.Env) == 0 {
		return nil
	}
	parent := make(map[string]string, 16)
	for _, kv := range os.Environ() {
		if k, _, ok := strings.Cut(kv, "="); ok {
			parent[k] = kv
		}
	}
	env := make([]string, 0, len(t.Inherit)+len(t.Env))
	for _, name := range t.Inherit {
		if kv, ok := parent[name]; ok {
			env = append(env, kv)
		}
	}
	return append(env, t.Env...)
}

// Fetch runs the tool and returns its stdout, minus trailing newlines.
// The child environment is built explicitly: it holds exactly t.Env and
// exactly what Inherit and Env name. Nothing else passes from the parent
// process. The value never appears in argv. On failure the error
// carries the exit code, not stdout or stderr, so the value cannot leak
// through it.
func Fetch(t Tool) (string, error) {
	if t.Bin == "" {
		return "", fmt.Errorf("secrets: tool %q has no binary", t.Name)
	}
	cmd := exec.Command(t.Bin, t.Args...)
	cmd.Env = childEnv(t)
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
// directories as needed. New parents get 0700. The value lands in an O_EXCL
// temp file at 0600 in the target directory, then rename moves it over the
// target. Rename replaces a planted symlink at the target instead of
// following it, and the final file is always a fresh 0600 regular file.
func Hydrate(path, value string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("secrets: create %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, ".secrets-*")
	if err != nil {
		return fmt.Errorf("secrets: temp in %s: %w", dir, err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return fmt.Errorf("secrets: chmod %s: %w", tmp, err)
	}
	if _, err := f.WriteString(value); err != nil {
		f.Close()
		return fmt.Errorf("secrets: write %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("secrets: close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("secrets: rename to %s: %w", path, err)
	}
	return nil
}
